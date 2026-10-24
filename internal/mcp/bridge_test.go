package mcp

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/sessionproof"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadOnlyToolsAndCache(t *testing.T) {
	calls := 0
	q := audit.Query{SourceIDs: []string{"s1"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC), BaselineStart: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Audit-Token") != "private-test-token" {
			t.Error("missing private auth")
		}
		switch r.URL.Path {
		case "/api/sources":
			json.NewEncoder(w).Encode([]audit.Source{{ID: "s1", Database: "db", Collection: "Audit"}})
		case "/api/analyze":
			calls++
			json.NewEncoder(w).Encode(audit.Analysis{Query: q, APIs: []audit.Comparison{{Key: "k", SourceID: "s1", Controller: "Payroll", Action: "Run", State: "compared"}}, Timeline: []audit.Series{{Key: "k", Points: []audit.Point{{At: q.Start, Count: 30}}}}})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	b := newBridge(fixedToken(upstream.URL, "private-test-token"), upstream.Client())
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"query": q})
	result, err := b.call(context.Background(), "audit_compare", raw)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "private-test-token") {
		t.Fatal("credential leaked")
	}
	handle := result.(map[string]any)["resultId"].(string)
	args, _ := json.Marshal(map[string]any{"resultId": handle, "apiKey": "k"})
	if _, err = b.call(context.Background(), "audit_get_timeline", args); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("timeline re-queried production: calls=%d", calls)
	}
	if _, err = b.call(context.Background(), "audit_delete_source", json.RawMessage(`{}`)); err == nil {
		t.Fatal("mutation tool accepted")
	}
	if _, err = b.call(context.Background(), "audit_get_timeline", json.RawMessage(`{"resultId":"expired","apiKey":"k"}`)); err == nil {
		t.Fatal("stale result accepted")
	}
}

func TestInvalidQueryDoesNotContactProduction(t *testing.T) {
	b := newBridge(fixed("http://127.0.0.1:1"), &http.Client{})
	if _, err := b.call(context.Background(), "audit_compare", json.RawMessage(`{"query":{}}`)); err == nil {
		t.Fatal("invalid query accepted")
	}
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{"url":"http://elsewhere"}`)); err == nil {
		t.Fatal("unknown argument accepted")
	}
}

var auditAnalysisForTest = audit.Analysis{APIs: []audit.Comparison{{Key: "a"}, {Key: "b"}}}

func TestRequestMetadataProjectionExcludesSensitiveFields(t *testing.T) {
	token := "test-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/requests" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(audit.Page{Rows: []audit.RequestRecord{{ID: "private-record-id", URL: "https://private.example/?password=secret", Status: "OK", DurationMs: func() *float64 { v := 42.0; return &v }()}}, Sources: []audit.SourceResult{{SourceID: "s1", OK: true}}})
	}))
	defer upstream.Close()
	b := newBridge(fixed(upstream.URL), upstream.Client())
	b.token = "test-token"
	b.resultID = "current"
	b.created = time.Now()
	b.result = &audit.Analysis{Query: audit.Query{}, APIs: []audit.Comparison{{Key: "k", SourceID: "s1"}}}
	result, err := b.call(context.Background(), "audit_get_requests", json.RawMessage(`{"resultId":"current","apiKey":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	for _, private := range []string{"password", "private.example", token} {
		if strings.Contains(string(data), private) {
			t.Fatalf("projection exposed %s", private)
		}
	}
	// Deliberately changed from "no record id": a finding must cite the record id
	// (see TestMCPRequestsExposeIDNotPayloadOrIP for the full allow-list).
	if !strings.Contains(string(data), `"id":"private-record-id"`) {
		t.Fatal("record id not returned")
	}
	if !strings.Contains(string(data), `"durationMs":42`) {
		t.Fatal("timing lost")
	}
}

// testSessionToken is a token of the shape the dashboard accepts.
const testSessionToken = "test-session-token-0123456789abcdef"

// fixed locates a fake dashboard at base with the token "test-token".
func fixed(base string) locator { return fixedToken(base, "test-token") }

func fixedToken(base, token string) locator {
	return func() (string, string, error) { return base, token, nil }
}

func TestRedirectAndProxyPolicy(t *testing.T) {
	b, err := New(18765, WithToken(testSessionToken))
	if err != nil {
		t.Fatal(err)
	}
	if b.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("local requests must not use environment proxy")
	}
	if b.client.CheckRedirect == nil {
		t.Fatal("redirects not disabled")
	}
	if _, err = New(0); err == nil {
		t.Fatal("bridge without a session file or token accepted")
	}
	if _, err = New(18765); err == nil {
		t.Fatal("bridge with a port but no token source accepted")
	}
	if _, err = New(18765, WithToken("short")); err == nil {
		t.Fatal("malformed AUDIT_SESSION_TOKEN accepted")
	}
	if _, err = New(70000, WithToken(testSessionToken)); err == nil {
		t.Fatal("out-of-range port accepted")
	}
	if err = newBridge(fixed("http://127.0.0.1:1"), &http.Client{}).request(context.Background(), "/api/payload", nil, nil); err == nil {
		t.Fatal("payload route accepted")
	}
}

// The token is never scraped from the page: the bridge must not request GET /.
func TestBridgeNeverRequestsThePage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			t.Errorf("bridge requested %s", r.URL.Path)
		}
		if r.Header.Get("X-Audit-Token") != testSessionToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode([]audit.Source{})
	}))
	defer upstream.Close()
	b := newBridge(fixedToken(upstream.URL, testSessionToken), upstream.Client())
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func sessionFileFor(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, err := SessionFilePath(func(name string) string {
		if name == "AUDIT_CONFIG_DIR" {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("AUDIT_CONFIG_DIR ignored: %s", path)
	}
	return path, port
}

// proving wraps a fake dashboard so it answers the session proof for whatever
// token *token holds, as the real dashboard does for its own token.
func proving(token *string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == sessionproof.Path {
			local := r.Context().Value(http.LocalAddrContextKey).(net.Addr).String()
			json.NewEncoder(w).Encode(map[string]string{"proof": sessionproof.Compute(*token, local, r.URL.Query().Get("n"))})
			return
		}
		next(w, r)
	})
}

func TestBridgeReadsPortAndTokenFromSessionFile(t *testing.T) {
	var seen []string
	current := testSessionToken
	upstream := httptest.NewServer(proving(&current, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Audit-Token"))
		json.NewEncoder(w).Encode([]audit.Source{})
	}))
	defer upstream.Close()
	path, port := sessionFileFor(t, upstream)
	b, err := New(0, WithSessionFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err == nil || err.Error() != notRunning {
		t.Fatalf("missing session file: %v", err)
	}
	if err := WriteSessionFile(path, port, testSessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// A restarted dashboard rewrites the file; the bridge follows without a restart.
	restarted := "restarted-token-0123456789abcdef0123"
	current = restarted
	if err := WriteSessionFile(path, port, restarted); err != nil {
		t.Fatal(err)
	}
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != testSessionToken || seen[1] != restarted {
		t.Fatalf("tokens sent = %q", seen)
	}
	// An explicit port must match the running dashboard.
	other, err := New(port+1, WithSessionFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "--port") {
		t.Fatalf("mismatched explicit port: %v", err)
	}
	matching, err := New(port, WithSessionFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := matching.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// AUDIT_SESSION_TOKEN overrides the file's token.
	current = testSessionToken
	override, err := New(0, WithSessionFile(path), WithToken(testSessionToken))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := override.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if seen[len(seen)-1] != testSessionToken {
		t.Fatalf("env token not used: %q", seen[len(seen)-1])
	}
}

// A cached comparison belongs to one dashboard session; a new token drops it.
func TestNewSessionTokenDropsCachedResult(t *testing.T) {
	token := "test-token"
	b := newBridge(func() (string, string, error) { return "http://127.0.0.1:1", token, nil }, nil)
	b.token, b.result, b.resultID, b.created = "test-token", &auditAnalysisForTest, "r", time.Now()
	if _, _, err := b.session(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.cached("r"); err != nil {
		t.Fatal("same session dropped the cached result")
	}
	token = "restarted-token"
	if _, _, err := b.session(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.cached("r"); err == nil {
		t.Fatal("a result from the previous dashboard session was kept")
	}
}

func TestSessionFileValidationAndOwnedRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", SessionFileName)
	if err := WriteSessionFile(path, 0, testSessionToken); err == nil {
		t.Fatal("port 0 written")
	}
	if err := WriteSessionFile(path, 1234, "short"); err == nil {
		t.Fatal("short token written")
	}
	if err := WriteSessionFile(path, 1234, testSessionToken); err != nil {
		t.Fatal(err)
	}
	port, token, err := ReadSessionFile(path)
	if err != nil || port != 1234 || token != testSessionToken {
		t.Fatalf("read back %d %q %v", port, token, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("session file mode %v", info.Mode().Perm())
		}
	}
	// A dashboard that exits leaves a newer dashboard's file alone.
	if err := RemoveSessionFile(path, "another-token-0123456789abcdef0123"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file removed by a dashboard that does not own it")
	}
	if err := RemoveSessionFile(path, testSessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned file not removed")
	}
	if err := RemoveSessionFile(path, testSessionToken); err != nil {
		t.Fatal("removing a missing file must succeed")
	}
	for _, bad := range []string{`{"port":1234}`, `{"port":0,"token":"` + testSessionToken + `"}`, `{"port":1234,"token":"` + testSessionToken + `","extra":1}`, `not json`, `{"port":1234,"token":"` + strings.Repeat("a", 20000) + `"}`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadSessionFile(path); err == nil {
			t.Errorf("invalid session file accepted: %.40s", bad)
		}
	}
}

func TestUpstreamErrorsDoNotExposeDetails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"mongodb://user:password@private"}`))
	}))
	defer upstream.Close()
	b := newBridge(fixed(upstream.URL), upstream.Client())
	_, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`))
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "private") {
		t.Fatal("upstream private failure leaked")
	}
}

func TestMultipleTimelinesKeepOwnBaselinesAndMissingData(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 5, 0, time.UTC)
	q := audit.Query{Start: start, End: start.Add(15 * time.Second), BucketSeconds: 10}
	quantile := func(v float64) *audit.Quantile { return &audit.Quantile{ValueMs: &v, LowerMs: v - 1} }
	a := audit.Analysis{Query: q, APIs: []audit.Comparison{{Key: "a", State: "compared", Reference: audit.Stats{P95: quantile(100)}}, {Key: "b", State: "compared", Reference: audit.Stats{P95: quantile(200)}}}, Timeline: []audit.Series{{Key: "a", Points: []audit.Point{{At: start.Truncate(10 * time.Second), Count: 30, P95: quantile(150)}}}, {Key: "b", Points: []audit.Point{{At: start.Truncate(10 * time.Second), Count: 3, P95: quantile(150)}}}}}
	raw, err := compareTimelines(&a, "test", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(map[string]any)
	slots := result["slots"].([]map[string]any)
	first := slots[0]["groups"].([]map[string]any)
	if first[0]["increaseMs"] != float64(50) || first[1]["increaseMs"] != float64(-50) {
		t.Fatal("APIs did not retain separate baselines")
	}
	if first[0]["increasePercent"] != float64(50) || first[1]["increasePercent"] != float64(-25) {
		t.Fatal("wrong percentage change")
	}
	if first[1]["sampleStatus"] != "few_samples" {
		t.Fatal("sparse evidence marked reliable")
	}
	if slots[0]["start"].(time.Time) != q.Start {
		t.Fatal("incident boundary not clipped")
	}
	missing := slots[1]["groups"].([]map[string]any)[0]
	if missing["currentP95Ms"] != nil || missing["sampleStatus"] != "missing" {
		t.Fatal("missing observations became zero")
	}
	if _, err := compareTimelines(&a, "test", []string{"a", "a"}); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestMultiTimelineUsesUnixEpochForNonstandardIntervals(t *testing.T) {
	start := time.Date(2026, 10, 22, 7, 54, 51, 296000000, time.UTC)
	a := audit.Analysis{Query: audit.Query{Start: start, End: start.Add(time.Hour), BucketSeconds: 2443}, APIs: []audit.Comparison{{Key: "a"}}}
	raw, err := compareTimelines(&a, "test", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	slots := raw.(map[string]any)["slots"].([]map[string]any)
	if slots[0]["at"].(time.Time).Unix()%2443 != 0 || slots[0]["start"].(time.Time) != start {
		t.Fatal("time slots do not align to backend epoch buckets")
	}
}

func TestActivityToolPreservesSourceIdentityWithoutURLOrID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/activity" {
			t.Error("unexpected route")
		}
		json.NewEncoder(w).Encode(audit.Page{Rows: []audit.RequestRecord{{ID: "private-id", URL: "private-url", SourceID: "source2", Controller: "Payroll", Action: "Run", Kind: "hangfire"}}})
	}))
	defer server.Close()
	b := newBridge(fixed(server.URL), server.Client())
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	raw, _ := json.Marshal(audit.ActivityInput{SourceIDs: []string{"source2"}, Start: at, End: at.Add(time.Minute), Limit: 20})
	data, err := b.call(context.Background(), "audit_get_activity", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(data)
	if strings.Contains(string(encoded), "private-") || !strings.Contains(string(encoded), "source2") || !strings.Contains(string(encoded), "hangfire") {
		t.Fatal("activity projection lost identity or exposed private fields")
	}
}

// impostor is a process on the dashboard's port that does not hold the token.
// With relay set it forwards the proof request to the real dashboard and
// returns its answer. It records every X-Audit-Token it is sent.
func impostor(t *testing.T, relay string) (*httptest.Server, *[]string) {
	t.Helper()
	var got []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if token := r.Header.Get("X-Audit-Token"); token != "" {
			got = append(got, token)
		}
		mu.Unlock()
		if r.URL.Path == sessionproof.Path && relay != "" {
			res, err := http.Get(relay + r.URL.RequestURI())
			if err == nil {
				defer res.Body.Close()
				io.Copy(w, res.Body)
			}
			return
		}
		if r.URL.Path == sessionproof.Path {
			json.NewEncoder(w).Encode(map[string]string{"proof": strings.Repeat("0", 64)})
			return
		}
		json.NewEncoder(w).Encode([]audit.Source{})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// TestBridgeNeverSendsTokenToUnprovenPeer is the M-A regression: a process on a
// stale port, with an env token or a session file, and even one relaying the
// proof to the real dashboard, never receives X-Audit-Token.
func TestBridgeNeverSendsTokenToUnprovenPeer(t *testing.T) {
	real := httptest.NewServer(server.New(audit.NewDemo(time.Now()), nil, testSessionToken, true))
	defer real.Close()
	for _, tc := range []struct{ name, relay string }{{"forged proof", ""}, {"relayed proof", real.URL}} {
		t.Run(tc.name, func(t *testing.T) {
			fake, got := impostor(t, tc.relay)
			path, port := sessionFileFor(t, fake)
			if err := writeSessionRecord(path, sessionRecord{Port: port, Token: testSessionToken}); err != nil {
				t.Fatal(err)
			}
			viaFile, _ := New(0, WithSessionFile(path))
			viaEnv, _ := New(port, WithToken(testSessionToken))
			for name, b := range map[string]*Bridge{"session file": viaFile, "env token": viaEnv} {
				if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err == nil || err.Error() != unproven {
					t.Errorf("%s: err=%v", name, err)
				}
			}
			if len(*got) != 0 {
				t.Fatalf("impostor received tokens: %q", *got)
			}
		})
	}
	// The real dashboard passes the same check.
	u, _ := url.Parse(real.URL)
	port, _ := strconv.Atoi(u.Port())
	b, _ := New(port, WithToken(testSessionToken))
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("real dashboard refused: %v", err)
	}
}

// TestStaleSessionFileOfDeadProcessIsRemoved: a session file whose process has
// exited is reported as not running and deleted, without dialing its port.
func TestStaleSessionFileOfDeadProcessIsRemoved(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	fake, got := impostor(t, "")
	path, port := sessionFileFor(t, fake)
	if err := writeSessionRecord(path, sessionRecord{Port: port, Token: testSessionToken, Pid: cmd.Process.Pid}); err != nil {
		t.Fatal(err)
	}
	b, _ := New(0, WithSessionFile(path))
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err == nil || err.Error() != notRunning {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale session file kept: %v", err)
	}
	if len(*got) != 0 {
		t.Fatalf("tokens sent: %q", *got)
	}
}

// TestSessionFileRefusedWhenAnotherProcessOwnsThePort: a live pid that does not
// own the listening port is refused (Windows reads the TCP owner table).
func TestSessionFileRefusedWhenAnotherProcessOwnsThePort(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("port owner check is Windows-only")
	}
	current := testSessionToken
	upstream := httptest.NewServer(proving(&current, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]audit.Source{})
	}))
	defer upstream.Close()
	path, port := sessionFileFor(t, upstream)
	if owners, ok := listenerOwners(port); !ok || len(owners) != 1 || owners[0] != os.Getpid() {
		t.Fatalf("listener owners = %v, %v; want [%d]", owners, ok, os.Getpid())
	}
	if !processAlive(os.Getppid()) {
		t.Skip("parent process not visible")
	}
	if err := writeSessionRecord(path, sessionRecord{Port: port, Token: testSessionToken, Pid: os.Getppid()}); err != nil {
		t.Fatal(err)
	}
	b, _ := New(0, WithSessionFile(path))
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err == nil || err.Error() != notOwner {
		t.Fatalf("err=%v", err)
	}
	if err := WriteSessionFile(path, port, testSessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("own pid refused: %v", err)
	}
}
