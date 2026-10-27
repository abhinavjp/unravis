package blackbox

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Clumsy-user cases end to end: the real host (recorder, store, status server)
// driven over real HTTP on a loopback port, with a fake collector and disk.

func (g *hostRig) do(t *testing.T, method, path string, hdr map[string]string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+g.ln.Addr().String()+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, resp.Header, sb.String()
}

func (g *hostRig) startAndWait(t *testing.T, o HostOptions) {
	t.Helper()
	g.start(t, o)
	// Wait for the host to finish before the temp folder is removed.
	t.Cleanup(func() { _ = g.stop(t) })
	select {
	case <-g.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server never listened")
	}
}

func TestClumsyWrongReadKeyIsRefusedEveryWay(t *testing.T) {
	g := newHostRig(t)
	g.startAndWait(t, g.opts())
	g.ticks <- time.Now()
	g.ticks <- time.Now()

	// The right key works first, so the later refusals are about the key.
	if code, _, body := g.do(t, "GET", "/status", map[string]string{KeyHeader: g.key}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	// Missing, wrong, the key in the address instead of the header, a key with
	// a stray space, and the key as a bearer token: all refused, none leaks it.
	cases := []struct {
		name, path string
		hdr        map[string]string
	}{
		{"no key", "/status", nil},
		{"wrong key", "/status", map[string]string{KeyHeader: "ubk-" + strings.Repeat("0", 32)}},
		{"key in query string", "/status?key=" + g.key, nil},
		{"key with a trailing space", "/status", map[string]string{KeyHeader: g.key + " x"}},
		{"bearer token", "/status", map[string]string{"Authorization": "Bearer " + g.key}},
	}
	for _, c := range cases {
		code, _, body := g.do(t, "GET", c.path, c.hdr)
		if code != http.StatusUnauthorized {
			t.Fatalf("%s: %d %s", c.name, code, body)
		}
		if strings.Contains(body, g.key) {
			t.Fatalf("%s: the key came back in the answer", c.name)
		}
	}
}

func TestClumsyEveryRouteIsGuardedAndRepeatedWrongKeysAreTurnedAway(t *testing.T) {
	g := newHostRig(t)
	g.startAndWait(t, g.opts())
	g.ticks <- time.Now()
	g.ticks <- time.Now()
	// Every route is guarded, not only /status.
	for _, p := range []string{"/manifest", "/files", "/files/bb-20260101-00.ndjson"} {
		if code, _, _ := g.do(t, "GET", p, nil); code != http.StatusUnauthorized {
			t.Fatalf("%s without a key: %d", p, code)
		}
	}
	// Asking again and again with a wrong key gets this address turned away,
	// right key or not, until the minute is over.
	if code, _, _ := g.do(t, "GET", "/status", map[string]string{KeyHeader: g.key}); code == http.StatusTooManyRequests {
		t.Fatal("three misses already blocked a reader who has the right key")
	}
	var last int
	for i := 0; i < 20 && last != http.StatusTooManyRequests; i++ {
		last, _, _ = g.do(t, "GET", "/status", map[string]string{KeyHeader: "ubk-wrong"})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("never throttled, last %d", last)
	}
	if code, hdr, _ := g.do(t, "GET", "/status", map[string]string{KeyHeader: g.key}); code != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" {
		t.Fatalf("throttled address with the right key: %d", code)
	}
}

func TestClumsyReadersCannotChangeOrEscapeTheFolder(t *testing.T) {
	g := newHostRig(t)
	g.startAndWait(t, g.opts())
	g.ticks <- time.Now()
	g.ticks <- time.Now()
	h := map[string]string{KeyHeader: g.key}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if code, _, _ := g.do(t, m, "/files/bb-20260101-00.ndjson", h); code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: %d", m, code)
		}
	}
	for _, p := range []string{
		"/files/manifest.json",
		"/files/..%5Cmanifest.json",
		"/files/..%2Fmanifest.json",
		"/files/%2e%2e/%2e%2e/windows/win.ini",
		"/files/snapshots",
		"/files/bb-20990101-00.ndjson",
		"/files/",
	} {
		code, _, body := g.do(t, "GET", p, h)
		if code == 200 {
			t.Fatalf("%s served: %s", p, body)
		}
	}
	// The current hour file is readable, with the key, as a plain recording.
	_, _, body := g.do(t, "GET", "/files", h)
	var list struct {
		Current string `json:"current"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil || list.Current == "" {
		t.Fatalf("%v %s", err, body)
	}
	code, _, data := g.do(t, "GET", "/files/"+list.Current, h)
	if code != 200 || !strings.Contains(data, `"seq":1`) {
		t.Fatalf("%d %s", code, data)
	}
}

func TestClumsyClockMovedFiveSecondsIsInTheManifestAndOnTheStatusClock(t *testing.T) {
	g := newHostRig(t)
	var offset atomic.Int64 // nanoseconds the "wall clock" has been moved
	o := g.opts()
	o.Now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	g.startAndWait(t, o)
	g.ticks <- o.Now()
	g.ticks <- o.Now()

	offset.Store(int64(5 * time.Second)) // someone sets the server clock 5 s ahead
	g.ticks <- o.Now()
	g.ticks <- o.Now()

	code, hdr, body := g.do(t, "GET", "/manifest", map[string]string{KeyHeader: g.key})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var doc struct {
		Manifest Manifest `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range doc.Manifest.Events {
		// The jump is 5 s minus the real time between the two ticks, which on a
		// busy test machine can be a little: accept 3.5 to 5.5 s.
		var secs float64
		if e.Kind == EventClockJump && strings.HasPrefix(e.Detail, "clock moved forward by ") {
			if _, err := fmt.Sscanf(e.Detail, "clock moved forward by %f s", &secs); err == nil && secs >= 3.5 && secs <= 5.5 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no clock-jump event: %+v", doc.Manifest.Events)
	}
	if len(doc.Manifest.Gaps) != 0 {
		t.Fatalf("a 5 s jump made a gap: %+v", doc.Manifest.Gaps)
	}
	// Every answer carries the Blackbox clock, so the reader can see the 5 s.
	stamp, err := time.Parse(time.RFC3339Nano, hdr.Get(TimeHeader))
	if err != nil {
		t.Fatalf("no clock stamp: %q", hdr.Get(TimeHeader))
	}
	if d := stamp.Sub(time.Now()); d < 3*time.Second || d > 7*time.Second {
		t.Fatalf("the stamp is %v ahead, want about 5 s", d)
	}
	if code, _, body := g.do(t, "GET", "/status", map[string]string{KeyHeader: g.key}); code != 200 || !strings.Contains(body, `"state":"running"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestClumsyFullDrivePausesThenResumesAndSaysSoOnTheStatusPage(t *testing.T) {
	g := newHostRig(t)
	disk := &pctDisk{pct: 80}
	var clock atomic.Int64
	base := time.Now()
	o := g.opts()
	o.Disk = disk
	o.Now = func() time.Time { return base.Add(time.Duration(clock.Load())) }
	g.startAndWait(t, o)
	h := map[string]string{KeyHeader: g.key}
	step := func() {
		clock.Add(int64(61 * time.Second)) // past the housekeeping interval
		g.ticks <- o.Now()
		g.ticks <- o.Now()
	}
	state := func() (string, int) {
		_, _, body := g.do(t, "GET", "/status", h)
		var st StatusDoc
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatalf("%v %s", err, body)
		}
		return st.State, st.Samples
	}
	g.ticks <- o.Now()
	g.ticks <- o.Now()
	s0, n0 := state()
	if s0 != "running" || n0 < 1 {
		t.Fatalf("%s %d", s0, n0)
	}
	disk.set(2) // the drive fills up and there is nothing older to remove
	step()
	step()
	s, n := state()
	if s != StateDiskLow || n != n0 {
		t.Fatalf("a full drive: %s with %d samples, want %d", s, n, n0)
	}
	disk.set(60) // someone frees space
	step()
	if s, n := state(); s != "running" || n <= n0 {
		t.Fatalf("after freeing space: %s with %d samples, had %d", s, n, n0)
	}
	_ = g.stop(t)
	m, err := ReadManifest(g.cfg.Folder)
	if err != nil || len(m.Gaps) != 1 || m.Gaps[0].Cause != CauseDiskLow {
		t.Fatalf("%v %+v", err, m.Gaps)
	}
}

func TestClumsyStopInTheMiddleOfWritingLeavesWholeLinesAndACleanManifest(t *testing.T) {
	g := newHostRig(t)
	g.startAndWait(t, g.opts())
	var wg sync.WaitGroup
	quit := make(chan struct{})
	wg.Add(1)
	go func() { // ticks arrive as fast as the recorder takes them
		defer wg.Done()
		for {
			select {
			case g.ticks <- time.Now():
			case <-quit:
				return
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	err := g.stop(t) // the service is stopped mid-stream
	close(quit)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	m, merr := ReadManifest(g.cfg.Folder)
	if merr != nil || m.State != ManifestStopped || m.SampleCount() < 1 {
		t.Fatalf("%v %+v", merr, m)
	}
	ents, _ := os.ReadDir(g.cfg.Folder)
	total := 0
	for _, e := range ents {
		name := e.Name()
		switch {
		case ValidFileName(name):
			f, err := os.Open(filepath.Join(g.cfg.Folder, name))
			if err != nil {
				t.Fatal(err)
			}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 8<<20)
			for sc.Scan() {
				var s Sample
				if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
					f.Close()
					t.Fatalf("a torn line in %s: %v", name, err)
				}
				total++
			}
			f.Close()
		case name == "manifest.json":
		default:
			t.Fatalf("a leftover file after stop: %s", name)
		}
	}
	if total != m.SampleCount() {
		t.Fatalf("the manifest says %d samples, the files hold %d", m.SampleCount(), total)
	}
	// Starting again on the same folder carries on without losing anything.
	g2 := newHostRig(t)
	g2.cfg.Folder = g.cfg.Folder
	g2.startAndWait(t, g2.opts())
	g2.ticks <- time.Now()
	g2.ticks <- time.Now()
	_ = g2.stop(t)
	m2, err := ReadManifest(g.cfg.Folder)
	if err != nil || m2.SampleCount() <= m.SampleCount() {
		t.Fatalf("%v %d after restart, %d before", err, m2.SampleCount(), m.SampleCount())
	}
}
