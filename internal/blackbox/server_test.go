package blackbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("log text"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readSamples reads every sample of every recording file in dir, oldest first.
func readSamples(t *testing.T, dir string) []Sample {
	t.Helper()
	names := listRecordingFiles(dir)
	var out []Sample
	for _, n := range sortedNames(names) {
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<16), 8<<20)
		for sc.Scan() {
			var s Sample
			if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			out = append(out, s)
		}
		f.Close()
	}
	return out
}

func sortedNames(n []string) []string {
	out := append([]string(nil), n...)
	sort.Strings(out)
	return out
}

type srvRig struct {
	*rig
	srv  *Server
	ts   *httptest.Server
	key  string
	hash string
}

func newSrvRig(t *testing.T) *srvRig {
	t.Helper()
	g := newRig(t, nil)
	key, _ := NewReadKey()
	hash, _ := HashKey(key)
	kc, _ := NewKeyCheck(hash)
	srv := NewServer(ServerOptions{Store: g.s, Recorder: g.r, Config: g.r.o.Config, Keys: kc, Now: g.clk.now})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	g.r.Tick(g.clk.t)
	g.tick(15 * time.Second)
	return &srvRig{rig: g, srv: srv, ts: ts, key: key, hash: hash}
}

func (g *srvRig) get(t *testing.T, path, key string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", g.ts.URL+path, nil)
	if key != "" {
		req.Header.Set(KeyHeader, key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestStatusAnswersWithKeyAndClock(t *testing.T) {
	g := newSrvRig(t)
	resp, body := g.get(t, "/status", g.key)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var d StatusDoc
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if d.State != StateRunning || d.Samples != 2 || d.Files != 1 || d.Name == "" {
		t.Fatalf("%+v", d)
	}
	if !d.Time.Equal(g.clk.t) {
		t.Fatalf("clock %v vs %v", d.Time, g.clk.t)
	}
	if h := resp.Header.Get(TimeHeader); h == "" {
		t.Fatal("no time header")
	} else if ht, err := time.Parse(time.RFC3339Nano, h); err != nil || !ht.Equal(g.clk.t) {
		t.Fatalf("header %q", h)
	}
	if d.FreePercent == nil || *d.FreePercent != 80 || d.Probe.Enabled || !d.ManifestOK || !d.WriteOK {
		t.Fatalf("%+v", d)
	}
	if d.IntervalSeconds != 15 || d.KeepDays != 7 || d.MinFreePercent != 10 {
		t.Fatalf("%+v", d)
	}
}

func TestStatusReportsLateAndDiskLow(t *testing.T) {
	g := newSrvRig(t)
	g.clk.t = g.clk.t.Add(2 * time.Minute) // no tick: the recorder is late
	_, body := g.get(t, "/status", g.key)
	var d StatusDoc
	_ = json.Unmarshal(body, &d)
	if d.State != StateRunningLate {
		t.Fatalf("state %s", d.State)
	}
}

func TestWrongOrMissingKeyIsRefusedEverywhere(t *testing.T) {
	g := newSrvRig(t)
	other, _ := NewReadKey()
	for _, p := range []string{"/status", "/manifest", "/files", "/files/" + FileNameFor(t0)} {
		for _, k := range []string{"", other, "ubk-nonsense"} {
			g.srv.mu.Lock()
			g.srv.fails = map[string][]time.Time{} // the throttle has its own test
			g.srv.mu.Unlock()
			resp, body := g.get(t, p, k)
			if resp.StatusCode != 401 {
				t.Fatalf("%s key %q: %d", p, k, resp.StatusCode)
			}
			if resp.Header.Get(TimeHeader) == "" {
				t.Fatal("refusals carry the clock too")
			}
			if strings.Contains(string(body), "samples") {
				t.Fatal("data in a refusal")
			}
		}
	}
}

func TestKeyInUrlIsNotAccepted(t *testing.T) {
	g := newSrvRig(t)
	resp, _ := g.get(t, "/status?key="+g.key+"&"+KeyHeader+"="+g.key, "")
	if resp.StatusCode != 401 {
		t.Fatalf("%d", resp.StatusCode)
	}
}

func TestRepeatedWrongKeysAreThrottled(t *testing.T) {
	g := newSrvRig(t)
	for i := 0; i < maxFailures; i++ {
		if resp, _ := g.get(t, "/status", "wrong"); resp.StatusCode != 401 {
			t.Fatalf("attempt %d: %d", i, resp.StatusCode)
		}
	}
	resp, _ := g.get(t, "/status", g.key)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("%d", resp.StatusCode)
	}
	// After the window the address may try again.
	g.clk.t = g.clk.t.Add(failWindow + time.Second)
	if resp, _ := g.get(t, "/status", g.key); resp.StatusCode != 200 {
		t.Fatalf("after the window: %d", resp.StatusCode)
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	g := newSrvRig(t)
	req, _ := http.NewRequest("POST", g.ts.URL+"/status", strings.NewReader("x"))
	req.Header.Set(KeyHeader, g.key)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("%d", resp.StatusCode)
	}
	req, _ = http.NewRequest("DELETE", g.ts.URL+"/files/"+FileNameFor(t0), nil)
	req.Header.Set(KeyHeader, g.key)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("%d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(g.s.Dir(), FileNameFor(t0))); err != nil {
		t.Fatal("file was removed through the server")
	}
}

func TestManifestAndFilesAnswers(t *testing.T) {
	g := newSrvRig(t)
	_, body := g.get(t, "/manifest", g.key)
	var m struct {
		Time     time.Time `json:"time"`
		Manifest Manifest  `json:"manifest"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Time.IsZero() || m.Manifest.SampleCount() != 2 || m.Manifest.State != ManifestRunning {
		t.Fatalf("%+v", m)
	}
	_, body = g.get(t, "/files", g.key)
	var f struct {
		Time    time.Time   `json:"time"`
		Files   []FileEntry `json:"files"`
		Current string      `json:"current"`
	}
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if f.Time.IsZero() || len(f.Files) != 1 || f.Current != f.Files[0].Name {
		t.Fatalf("%+v", f)
	}
}

func TestFileDownloadAndRange(t *testing.T) {
	g := newSrvRig(t)
	name := g.s.CurrentFile()
	want, _ := os.ReadFile(filepath.Join(g.s.Dir(), name))
	resp, body := g.get(t, "/files/"+name, g.key)
	if resp.StatusCode != 200 || string(body) != string(want) {
		t.Fatalf("%d", resp.StatusCode)
	}
	if resp.Header.Get(TimeHeader) == "" {
		t.Fatal("download carries the clock in a header")
	}
	req, _ := http.NewRequest("GET", g.ts.URL+"/files/"+name, nil)
	req.Header.Set(KeyHeader, g.key)
	req.Header.Set("Range", "bytes=5-14")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 206 || string(b2) != string(want[5:15]) {
		t.Fatalf("range: %d %q", r2.StatusCode, b2)
	}
}

func TestOnlyRecordingFilesAreServed(t *testing.T) {
	g := newSrvRig(t)
	_ = os.WriteFile(filepath.Join(g.s.Dir(), "readkey.sha256"), []byte(g.hash), 0o644)
	for _, p := range []string{
		"/files/manifest.json", "/files/readkey.sha256", "/files/../manifest.json", "/files/..%2fmanifest.json",
		"/files/bb-20990101-00.ndjson", "/files/bb-20261027-10.ndjson/..", "/files/", "/files/snapshots",
		"/files/%2e%2e%5cmanifest.json", "/files/bb-20261027-10.ndjson%00.txt",
	} {
		resp, body := g.get(t, p, g.key)
		if resp.StatusCode == 200 && !strings.HasPrefix(p, "/files/bb-20261027-10.ndjson/..") {
			t.Fatalf("%s served: %s", p, body)
		}
		if strings.Contains(string(body), g.hash) {
			t.Fatalf("%s leaked the key hash", p)
		}
	}
}

func TestNothingReachableLeaksKeyOrHash(t *testing.T) {
	g := newSrvRig(t)
	for _, p := range []string{"/status", "/manifest", "/files"} {
		_, body := g.get(t, p, g.key)
		if strings.Contains(string(body), g.hash) || strings.Contains(string(body), g.key) {
			t.Fatalf("%s leaks", p)
		}
	}
}

func TestRootIsLoopbackOnly(t *testing.T) {
	g := newSrvRig(t)
	resp, body := g.get(t, "/", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "running") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "samples") {
		t.Fatal("root gives no data")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:5555"
	g.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("remote root: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/other", nil)
	req.RemoteAddr = "127.0.0.1:5"
	rec = httptest.NewRecorder()
	g.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("other path: %d", rec.Code)
	}
}

func TestConcurrencyCap(t *testing.T) {
	g := newSrvRig(t)
	for i := 0; i < maxConcurrent; i++ {
		g.srv.sem <- struct{}{}
	}
	resp, _ := g.get(t, "/status", g.key)
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("%d", resp.StatusCode)
	}
	<-g.srv.sem
	if resp, _ := g.get(t, "/status", g.key); resp.StatusCode != 200 {
		t.Fatalf("%d", resp.StatusCode)
	}
}

func TestListenReportsPortBusy(t *testing.T) {
	ln, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if _, err := Listen("127.0.0.1", port); err == nil {
		t.Fatal("a busy port must be an error at start")
	}
}

func TestStatusServedOverRealListener(t *testing.T) {
	g := newRig(t, nil)
	key, _ := NewReadKey()
	hash, _ := HashKey(key)
	kc, _ := NewKeyCheck(hash)
	srv := NewServer(ServerOptions{Store: g.s, Recorder: g.r, Config: g.r.o.Config, Keys: kc, Now: g.clk.now})
	ln, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/status", nil)
	req.Header.Set(KeyHeader, key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v", err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A stalled client must not hold a recording file open for ever: an open file
// cannot be removed by housekeeping on Windows (review F4).
func TestServerHasWriteAndHeaderTimeouts(t *testing.T) {
	g := newSrvRig(t)
	if g.srv.srv.WriteTimeout <= 0 || g.srv.srv.ReadHeaderTimeout <= 0 || g.srv.srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts %v %v %v", g.srv.srv.WriteTimeout, g.srv.srv.ReadHeaderTimeout, g.srv.srv.IdleTimeout)
	}
}

// Review F6.3: a spray of source addresses must not clear the lockout of an
// address that is locked out.
func TestThrottleTableEvictsOneOffAddressesNotLockedOutOnes(t *testing.T) {
	g := newSrvRig(t)
	now := g.clk.t
	for i := 0; i < maxFailures; i++ {
		g.srv.fail("203.0.113.9", now)
	}
	if !g.srv.throttled("203.0.113.9", now) {
		t.Fatal("not locked out")
	}
	for i := 0; i < 3*maxTrackedIPs; i++ {
		g.srv.fail(fmt.Sprintf("10.%d.%d.%d", i/65536, i/256%256, i%256), now.Add(time.Duration(i)*time.Millisecond))
	}
	if !g.srv.throttled("203.0.113.9", now.Add(time.Second)) {
		t.Fatal("lockout cleared by a spray of addresses")
	}
	g.srv.mu.Lock()
	n := len(g.srv.fails)
	g.srv.mu.Unlock()
	if n > maxTrackedIPs {
		t.Fatalf("table grew to %d", n)
	}
	// Once the window passes, expired addresses make room first.
	later := now.Add(failWindow + time.Hour)
	g.srv.fail("198.51.100.1", later)
	g.srv.mu.Lock()
	n = len(g.srv.fails)
	g.srv.mu.Unlock()
	if n > maxTrackedIPs {
		t.Fatalf("table grew to %d", n)
	}
}

// BBX-5-04 (C11): /status carries only the four-field SQL status object; no SQL
// sample data ever crosses the plain-HTTP endpoint.

// writeSQLStatusFile puts text into <folder>\sql\status.json, as the SQL runner does.
func writeSQLStatusFile(t *testing.T, folder, text string) {
	t.Helper()
	dir := SQLDataDir(folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sqlObject answers /status and returns the raw sql object plus the whole body.
func sqlObject(t *testing.T, g *srvRig) (map[string]json.RawMessage, string) {
	t.Helper()
	resp, body := g.get(t, "/status", g.key)
	if resp.StatusCode != 200 {
		t.Fatalf("/status: %d %s", resp.StatusCode, body)
	}
	var doc struct {
		SQL map[string]json.RawMessage `json:"sql"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.SQL, string(body)
}

func TestStatusSQLShape(t *testing.T) {
	g := newSrvRig(t)
	writeSQLStatusFile(t, g.s.Dir(), `{"enabled":true,"state":"backing-off","lastOK":"2026-10-27T09:59:00Z","failures":3}`)
	sql, body := sqlObject(t, g)
	if len(sql) != 4 {
		t.Fatalf("sql must hold exactly four fields: %v", sql)
	}
	for _, k := range []string{"enabled", "state", "lastOK", "failures"} {
		if _, ok := sql[k]; !ok {
			t.Fatalf("sql is missing %q: %v", k, sql)
		}
	}
	var d StatusDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 27, 9, 59, 0, 0, time.UTC)
	if !d.SQL.Enabled || d.SQL.State != "backing-off" || d.SQL.Failures != 3 || d.SQL.LastOK == nil || !d.SQL.LastOK.Equal(want) {
		t.Fatalf("%+v", d.SQL)
	}
	// A missing lastOK is omitted, never a zero time.
	writeSQLStatusFile(t, g.s.Dir(), `{"enabled":false,"state":"off","failures":0}`)
	sql, body = sqlObject(t, g)
	if _, ok := sql["lastOK"]; ok {
		t.Fatalf("lastOK must be omitted when missing: %v", sql)
	}
	if strings.Contains(body, "0001-01-01") {
		t.Fatal("a zero time leaked into /status")
	}
}

func TestStatusSQLNoInstanceOrError(t *testing.T) {
	g := newSrvRig(t)
	writeSQLStatusFile(t, g.s.Dir(), `{"enabled":true,"state":"on","failures":0,"instance":"MSSQL$SECRET","version":"16.0.1000","error":"Login failed for user sa on SECRET"}`)
	sql, body := sqlObject(t, g)
	for k := range sql {
		switch k {
		case "enabled", "state", "lastOK", "failures":
		default:
			t.Fatalf("extra field %q passed through: %v", k, sql)
		}
	}
	for _, secret := range []string{"SECRET", "16.0.1000", "Login failed", `"instance"`, `"error"`} {
		if strings.Contains(body, secret) {
			t.Fatalf("/status leaked %q: %s", secret, body)
		}
	}
	var d StatusDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	if d.SQL.Enabled || d.SQL.State != "off" {
		t.Fatalf("a file with extra fields must read as off: %+v", d.SQL)
	}
}

func TestStatusSQLMissingOff(t *testing.T) {
	g := newSrvRig(t)
	resp, body := g.get(t, "/status", g.key)
	if resp.StatusCode != 200 {
		t.Fatalf("missing status.json must not fail /status: %d %s", resp.StatusCode, body)
	}
	var d StatusDoc
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if d.SQL.Enabled || d.SQL.State != "off" || d.SQL.Failures != 0 || d.SQL.LastOK != nil {
		t.Fatalf("%+v", d.SQL)
	}
	// Unreadable content is off too, never a 500.
	writeSQLStatusFile(t, g.s.Dir(), `{"enabled":true,"state":`)
	if _, body := sqlObject(t, g); strings.Contains(body, `"enabled":true`) {
		t.Fatalf("broken status.json must read as off: %s", body)
	}
}

func TestStatusSQLBadState(t *testing.T) {
	g := newSrvRig(t)
	for _, state := range []string{"ON", "running", "on;drop", "", "unknown"} {
		writeSQLStatusFile(t, g.s.Dir(), `{"enabled":true,"state":"`+state+`","failures":1}`)
		_, body := sqlObject(t, g)
		var d StatusDoc
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		if d.SQL.State != "off" {
			t.Fatalf("state %q must normalize to off: %+v", state, d.SQL)
		}
	}
	for _, state := range []string{"on", "backing-off", "auto-off", "denied", "off"} {
		writeSQLStatusFile(t, g.s.Dir(), `{"enabled":true,"state":"`+state+`","failures":0}`)
		_, body := sqlObject(t, g)
		var d StatusDoc
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		if d.SQL.State != state {
			t.Fatalf("whitelisted state %q lost: %+v", state, d.SQL)
		}
	}
}

func TestFilesDoesNotServeSQL(t *testing.T) {
	g := newSrvRig(t)
	writeSQLStatusFile(t, g.s.Dir(), `{"enabled":true,"state":"on","failures":0}`)
	sample := `{"secret":"sql sample data"}`
	if err := os.WriteFile(filepath.Join(SQLDataDir(g.s.Dir()), "bb-20261027-09.ndjson"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/files/sql/status.json",
		"/files/sql/bb-20261027-09.ndjson", // a valid recording name, but in the sql folder
		"/files/sql%2fstatus.json",
		"/files/sql%5cstatus.json",
		"/files/status.json",
	} {
		resp, body := g.get(t, p, g.key)
		if resp.StatusCode != 404 {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, body)
		}
		if strings.Contains(string(body), "sql sample data") {
			t.Fatalf("%s served SQL sample data", p)
		}
	}
}

func TestProbeStillRefused(t *testing.T) {
	cfg := Default()
	cfg.Name = "box"
	cfg.Probe.Enabled = true
	if err := cfg.Validate(); err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("probe.enabled true must stay refused: %v", err)
	}
	g := newSrvRig(t)
	_, body := sqlObject(t, g)
	var d StatusDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	if d.Probe.Enabled || !strings.Contains(body, `"probe":{"enabled":false}`) {
		t.Fatalf("the probe gate must stay hardcoded false: %s", body)
	}
}
