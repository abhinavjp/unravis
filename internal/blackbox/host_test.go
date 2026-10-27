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
	"testing"
	"time"
)

type hostRig struct {
	cfg    Config
	key    string
	keyP   string
	ln     net.Listener
	ticks  chan time.Time
	col    *fakeCollector
	ready  chan string
	cancel context.CancelFunc
	done   chan error
	logs   chan string
}

func newHostRig(t *testing.T) *hostRig {
	t.Helper()
	g := &hostRig{ticks: make(chan time.Time), col: &fakeCollector{}, ready: make(chan string, 1), done: make(chan error, 1), logs: make(chan string, 64)}
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
	return HostOptions{Config: g.cfg, KeyPath: g.keyP, Version: "test", Collector: g.col, Disk: &pctDisk{pct: 80},
		Listener: g.ln, Ticks: g.ticks, Ready: func(a string) { g.ready <- a },
		Log: func(f string, a ...any) {
			select {
			case g.logs <- f:
			default:
			}
		}}
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
	g.cancel()
	select {
	case err := <-g.done:
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
	g.ticks <- time.Now()
	g.ticks <- time.Now() // a second send proves the first tick was taken
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
	g.ticks <- time.Now()
	g.ticks <- time.Now()
	select {
	case <-g.logs:
	case <-time.After(5 * time.Second):
		t.Fatal("the busy port was not reported")
	}
	// Recording went on although the status page could not open.
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
	g.ticks <- time.Now()
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
}
