package blackbox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// countingCollector wraps the rig's collector and counts every Collect call that
// returns. The recorder loop is sequential, so this is a processed-tick signal
// the rig can wait on: once a tick's Collect has returned, its append is next.
type countingCollector struct {
	inner Collector
	done  atomic.Int64 // Collect calls returned
}

func (c *countingCollector) Collect(now time.Time) (Sample, error) {
	s, err := c.inner.Collect(now)
	c.done.Add(1)
	return s, err
}
func (c *countingCollector) Close() error { return c.inner.Close() }

type hostRig struct {
	cfg    Config
	key    string
	keyP   string
	ln     net.Listener
	ticks  chan time.Time
	col    *fakeCollector
	ccount *countingCollector
	ready  chan string
	cancel context.CancelFunc
	done   chan error
	logs   chan string

	// sent counts the ticks the test has pushed through tick. The recorder also
	// takes one sample at once when Run starts, so a running rig holds 1+sent
	// samples once every sent tick has been processed.
	sent atomic.Int64

	stopped bool
	stopErr error
}

func newHostRig(t *testing.T) *hostRig {
	t.Helper()
	g := &hostRig{ticks: make(chan time.Time), col: &fakeCollector{}, ready: make(chan string, 1), done: make(chan error, 1), logs: make(chan string, 64)}
	g.ccount = &countingCollector{inner: g.col}
	g.cfg = Default()
	g.cfg.Name = "box"
	g.cfg.Folder = filepath.Join(t.TempDir(), "rec")
	g.cfg.Record.LogsCatalogue = false
	g.key, _ = NewReadKey()
	g.keyP = filepath.Join(t.TempDir(), "readkey.sha256")
	if err := SaveKeyHash(g.keyP, g.key); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g.ln = ln
	t.Cleanup(func() { ln.Close() })
	return g
}

func (g *hostRig) opts() HostOptions {
	return HostOptions{Config: g.cfg, KeyPath: g.keyP, Version: "test", Collector: g.ccount, Disk: &pctDisk{pct: 80},
		Listener: g.ln, Ticks: g.ticks, Ready: func(a string) { g.ready <- a },
		Log: func(f string, a ...any) {
			select {
			case g.logs <- f:
			default:
			}
		}}
}

// tick sends one tick and remembers it, so waitProcessed knows how many ticks
// the recorder still owes. Sending on the unbuffered channel only proves the
// loop RECEIVED the tick; processing (collect + append) finishes later, which is
// why a test must waitProcessed before reading state it depends on.
func (g *hostRig) tick(t *testing.T, now time.Time) {
	t.Helper()
	g.sent.Add(1)
	g.ticks <- now
}

// waitProcessed blocks until every tick sent so far has been fully processed and
// its sample appended, so a test can read state without racing an in-flight tick.
//
// Sending on the unbuffered ticks channel only proves the loop RECEIVED a tick;
// processing (collect, then append) finishes later. The processed-tick counter
// first confirms every sent tick has been collected; the status page then
// confirms the samples landed. The status page reads the store's IN-MEMORY
// manifest, which Append updates synchronously, so this never races the store's
// atomic manifest rename on Windows (reading the manifest file while the
// recorder writes it would). Call it only while the rig is running and its
// status server is listening.
func (g *hostRig) waitProcessed(t *testing.T) {
	t.Helper()
	want := 1 + g.sent.Load() // the first sample Run takes at once, plus sent ticks
	g.waitDone(t, want)       // every sent tick collected; appends follow at once
	deadline := time.Now().Add(5 * time.Second)
	for {
		var st StatusDoc
		if _, body := g.get(t, "/status", g.key); json.Unmarshal(body, &st) == nil && int64(st.Samples) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("samples never reached %d", want)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitCollected blocks until every tick sent so far has been collected. It is the
// barrier for rigs whose status server is not listening (a busy port): the
// caller reads the manifest file itself, once, after the ticks are done.
func (g *hostRig) waitCollected(t *testing.T) {
	t.Helper()
	g.waitDone(t, 1+g.sent.Load())
}

// waitDone waits until at least n Collect calls have returned. The recorder loop
// is sequential, so a returned Collect means that tick's append is next.
func (g *hostRig) waitDone(t *testing.T, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for g.ccount.done.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d ticks finished collecting, want %d", g.ccount.done.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func (g *hostRig) start(t *testing.T, o HostOptions) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	t.Cleanup(cancel)
	go func() { g.done <- RunHost(ctx, o) }()
}

func (g *hostRig) stop(t *testing.T) error {
	t.Helper()
	if g.stopped {
		return g.stopErr
	}
	g.cancel()
	select {
	case err := <-g.done:
		g.stopped, g.stopErr = true, err
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("RunHost did not stop")
		return nil
	}
}

func (g *hostRig) get(t *testing.T, path, key string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+g.ln.Addr().String()+path, nil)
	if key != "" {
		req.Header.Set(KeyHeader, key)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b []byte
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b
}

func TestRunHostRecordsServesAndStopsCleanly(t *testing.T) {
	g := newHostRig(t)
	g.start(t, g.opts())
	select {
	case <-g.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server never listened")
	}
	g.tick(t, time.Now())
	g.tick(t, time.Now()) // a second send proves the first tick was taken
	g.waitProcessed(t)
	code, body := g.get(t, "/status", g.key)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var st StatusDoc
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "box" || st.Version != "test" || st.Samples < 1 || st.State != StateRunning {
		t.Fatalf("%+v", st)
	}
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(g.cfg.Folder)
	if err != nil || m.State != ManifestStopped || !g.col.closed {
		t.Fatalf("%v %+v", err, m)
	}
	if _, err := net.DialTimeout("tcp", g.ln.Addr().String(), 500*time.Millisecond); err == nil {
		t.Fatal("the port is still open after stop")
	}
}

func TestRunHostRefusesToStartWithoutAKeyOrWithBadSettings(t *testing.T) {
	g := newHostRig(t)
	o := g.opts()
	o.KeyPath = filepath.Join(t.TempDir(), "none")
	if err := RunHost(context.Background(), o); !errors.Is(err, ErrNoReadKey) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(g.cfg.Folder); err == nil {
		t.Fatal("made the folder before it knew it could run")
	}
	o = g.opts()
	o.Config.KeepDays = 0
	if err := RunHost(context.Background(), o); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("%v", err)
	}
	// A key file that is garbage is not "no key".
	_ = os.WriteFile(g.keyP, []byte("garbage"), 0o600)
	if err := RunHost(context.Background(), g.opts()); err == nil || errors.Is(err, ErrNoReadKey) {
		t.Fatalf("%v", err)
	}
}

func TestRunHostKeepsRecordingWhenThePortIsBusyAndTakesItLater(t *testing.T) {
	g := newHostRig(t)
	// Hold a real port, then ask the host to use it.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := busy.Addr().(*net.TCPAddr).Port
	o := g.opts()
	o.Listener = nil
	o.Config.Bind = "127.0.0.1"
	o.Config.Port = port
	o.ListenRetry = 50 * time.Millisecond
	g.start(t, o)
	g.tick(t, time.Now())
	g.tick(t, time.Now())
	select {
	case <-g.logs:
	case <-time.After(5 * time.Second):
		t.Fatal("the busy port was not reported")
	}
	// Recording went on although the status page could not open.
	g.waitCollected(t)
	if m, err := ReadManifest(g.cfg.Folder); err != nil || m.SampleCount() < 1 {
		t.Fatalf("%v %+v", err, m)
	}
	busy.Close()
	select {
	case a := <-g.ready:
		if a != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
			t.Fatalf("%s", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never took the port once it was free")
	}
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
}

func TestRunHostStopWhileWaitingForAPortReturns(t *testing.T) {
	g := newHostRig(t)
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	o := g.opts()
	o.Listener = nil
	o.Config.Bind = "127.0.0.1"
	o.Config.Port = busy.Addr().(*net.TCPAddr).Port
	g.start(t, o)
	g.tick(t, time.Now())
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
}

// gateCollector reads the shared change verdict three times in one cycle,
// like three wildcard groups sharing GroupDeps.ProcessSetChanged.
type gateCollector struct {
	verdicts *[]bool
	read     func() bool
}

func (c *gateCollector) Collect(time.Time) (Sample, error) {
	for i := 0; i < 3; i++ {
		*c.verdicts = append(*c.verdicts, c.read())
	}
	return Sample{}, nil
}
func (c *gateCollector) Close() error { return nil }

// TestProcessChangeGateOneVerdictPerCycle guards the wiring: the resolver is
// snapshotted ONCE per collection cycle and every group in the cycle sees the
// same verdict; a set change reaches all groups on the next cycle, and a
// stable set never re-expands.
func TestProcessChangeGateOneVerdictPerCycle(t *testing.T) {
	procs := []ProcInfo{{PID: 1, Start: 10}}
	var snaps int
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		snaps++
		out := make([]ProcInfo, len(procs))
		copy(out, procs)
		return out, nil
	}
	gate := &processChangeGate{resolver: r}
	var verdicts []bool
	gate.inner = &gateCollector{verdicts: &verdicts, read: func() bool { return gate.changed }}

	cycle := func(want bool) {
		t.Helper()
		verdicts = nil
		if _, err := gate.Collect(time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(verdicts) != 3 {
			t.Fatalf("verdicts read = %d, want 3", len(verdicts))
		}
		for _, v := range verdicts {
			if v != want {
				t.Fatalf("mixed verdicts in one cycle: %v, want all %v", verdicts, want)
			}
		}
	}
	cycle(true)  // first cycle: no baseline yet
	cycle(false) // stable set
	procs = append(procs, ProcInfo{PID: 2, Start: 20})
	cycle(true)  // the change reaches every group
	cycle(false) // stable again
	if snaps != 4 {
		t.Fatalf("snapshots = %d, want 4 (one per cycle, not one per group)", snaps)
	}
}

// TestRunHostWithSelectedTargetsRecordsCleanly guards the resolver wiring:
// selected targets that do not exist never stop recording — machine-wide
// groups still work and the run stops cleanly.
func TestRunHostWithSelectedTargetsRecordsCleanly(t *testing.T) {
	g := newHostRig(t)
	g.cfg.Targets = []Target{{Kind: "appPool", Name: "No Such Pool"}, {Kind: "service", Name: "No Such Service"}}
	g.start(t, g.opts())
	select {
	case <-g.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server never listened")
	}
	g.tick(t, time.Now())
	g.tick(t, time.Now())
	g.waitProcessed(t)
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(g.cfg.Folder)
	if err != nil || m.State != ManifestStopped || !g.col.closed {
		t.Fatalf("%v %+v", err, m)
	}
}
