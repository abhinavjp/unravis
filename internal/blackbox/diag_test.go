package blackbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagLogStaysUnderItsCap(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	defer d.Close()
	for i := 0; i < 5000; i++ {
		c.t = c.t.Add(time.Second)
		d.Line("round", "seq", i, "groups", "a:1:ok", "err", "")
	}
	var total int64
	for _, n := range []string{DiagFileName, diagOldName} {
		st, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if st.Size() > diagMaxBytes {
			t.Fatalf("%s is %d bytes, cap is %d", n, st.Size(), diagMaxBytes)
		}
		total += st.Size()
	}
	if total > 2*diagMaxBytes {
		t.Fatalf("log holds %d bytes", total)
	}
	tail := string(d.Tail(DiagTailBytes))
	if !strings.Contains(tail, "seq=4999") {
		t.Fatal("the newest line must survive rotation")
	}
	if !strings.HasPrefix(tail, "2026-") {
		t.Fatalf("tail must start at a whole line, got %.30q", tail)
	}
}

func TestDiagLogValuesAreSafe(t *testing.T) {
	dir := t.TempDir()
	d := OpenDiagLog(dir, (&clock{t: t0}).now)
	d.Line("event", "detail", "a b\nc\td", "empty", "", "long", strings.Repeat("x", 500))
	d.Close()
	b, _ := os.ReadFile(filepath.Join(dir, DiagFileName))
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("one call must be one line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "detail=a_b_c_d") || !strings.Contains(lines[0], "empty=-") {
		t.Fatalf("line = %q", lines[0])
	}
	if len(lines[0]) > 300 {
		t.Fatalf("a value was not cut: %d bytes", len(lines[0]))
	}
}

func TestNilAndBrokenDiagLogAreSilent(t *testing.T) {
	var d *DiagLog
	d.Line("x") // must not panic
	d.Close()
	if d.Tail(10) != nil {
		t.Fatal("nil log has no tail")
	}
	bad := OpenDiagLog(filepath.Join(t.TempDir(), "missing", "dir"), nil)
	bad.Line("x", "a", 1) // folder missing: silent
}

func diagRig(t *testing.T) (*rig, *DiagLog) {
	t.Helper()
	g := newRig(t, func(c *Config) { c.IntervalSeconds = 15 })
	d := OpenDiagLog(g.s.Dir(), g.clk.now)
	t.Cleanup(d.Close)
	g.r.o.Diag = d
	return g, d
}

func readDiag(g *rig) string {
	return string(ReadDiagTail(g.s.Dir(), DiagTailBytes))
}

func TestRecorderLogsAFailedRoundWithItsCode(t *testing.T) {
	g, _ := diagRig(t)
	g.r.Tick(g.clk.t)
	g.col.mu.Lock()
	g.col.err = errHostSlow
	g.col.mu.Unlock()
	g.tick(15 * time.Second)
	out := readDiag(g)
	if !strings.Contains(out, " round ") || !strings.Contains(out, "err=host-slow") {
		t.Fatalf("a round with no sample must name why:\n%s", out)
	}
	if strings.Contains(out, "host collection over budget") {
		t.Fatal("error text must never be logged")
	}
}

func TestRecorderLogsNothingForQuietRoundsButABeat(t *testing.T) {
	g, _ := diagRig(t)
	g.r.Tick(g.clk.t)
	for i := 0; i < 25; i++ { // 25 x 15 s = 6 minutes
		g.tick(15 * time.Second)
	}
	out := readDiag(g)
	if strings.Contains(out, " round ") {
		t.Fatalf("quiet rounds must not be written one by one:\n%s", out)
	}
	if !strings.Contains(out, " beat ") || !strings.Contains(out, "rounds=") {
		t.Fatalf("a heartbeat must be written every five minutes:\n%s", out)
	}
}

func TestRecorderLogsSkippedDeadlines(t *testing.T) {
	g, _ := diagRig(t)
	g.r.Tick(g.clk.t)
	g.r.sched = schedInfo{lag: 3 * time.Second, missed: 2}
	g.tick(15 * time.Second)
	out := readDiag(g)
	if !strings.Contains(out, "missed=2") || !strings.Contains(out, "lag=3000") {
		t.Fatalf("lag and missed rounds must be logged:\n%s", out)
	}
}

func TestRecorderRateLimitsRoundLines(t *testing.T) {
	g, _ := diagRig(t)
	g.r.Tick(g.clk.t)
	for i := 0; i < 60; i++ {
		g.r.sched = schedInfo{missed: 1}
		g.clk.t = g.clk.t.Add(time.Second)
		g.mono += time.Second
		g.r.Tick(g.clk.t)
	}
	n := strings.Count(readDiag(g), " round ")
	if n > 2*diagLinesPerMin {
		t.Fatalf("%d round lines in a minute, cap is %d", n, diagLinesPerMin)
	}
	if g.r.agg.suppressed == 0 {
		t.Fatal("rounds past the cap must be counted")
	}
}

func TestRecorderLogsStartAndStop(t *testing.T) {
	g, _ := diagRig(t)
	g.r.diagStart()
	if err := g.r.stop("stopped"); err != nil {
		t.Fatal(err)
	}
	out := readDiag(g)
	if !strings.Contains(out, " start ") || !strings.Contains(out, " stop ") {
		t.Fatalf("start and stop must be logged:\n%s", out)
	}
}

func TestBudgetDecisionsAreLogged(t *testing.T) {
	g, _ := diagRig(t)
	b := NewBudget(15)
	g.r.o.Budget = b
	now := t0
	hot := SelfReading{CPUPct: 5, WallSeconds: 15, HeapInUse: 1 << 20, Private: 1 << 20}
	for i := 0; i < 40 && b.EffectiveInterval() == 15; i++ {
		now = now.Add(15 * time.Second)
		b.Cycle(now, hot, true)
		g.r.diagBudget(now)
	}
	if b.EffectiveInterval() == 15 {
		t.Fatal("test setup: budget did not widen")
	}
	out := readDiag(g)
	if !strings.Contains(out, "action=widen") || !strings.Contains(out, "reason=cpu") {
		t.Fatalf("a widen must be logged with its reason:\n%s", out)
	}
}

func TestTimedOutGroupIsNamedAsTheSlowOne(t *testing.T) {
	b := NewBudget(15)
	stall := make(chan struct{})
	defer close(stall)
	g := &stallGroup{name: "iis", stall: stall}
	c := NewGroupCollector(&fakeCollector{}, []Group{g}, b).(*groupCollector)
	s, err := c.Collect(t0)
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	_, name, dur := c.Timings()
	if name != "iis" || dur < budgetMaxGroup {
		t.Fatalf("slow group = %q %v, want iis >= %v", name, dur, budgetMaxGroup)
	}
	gt := c.GroupTimings()
	if len(gt) != 1 || gt[0].Outcome != "timeout" {
		t.Fatalf("group timings = %+v", gt)
	}
}

func TestGatePassesTimingsToTheRecorder(t *testing.T) {
	b := NewBudget(15)
	c := NewGroupCollector(&fakeCollector{}, nil, b)
	gate := &processChangeGate{inner: c}
	if _, ok := Collector(gate).(interface {
		Timings() (time.Duration, string, time.Duration)
	}); !ok {
		t.Fatal("the gate must expose Timings or the self block's host and slow-group stay empty")
	}
	if _, ok := Collector(gate).(interface{ GroupTimings() []groupTime }); !ok {
		t.Fatal("the gate must expose GroupTimings")
	}
}

type stallGroup struct {
	name  string
	stall chan struct{}
}

func (s *stallGroup) Name() string   { return s.name }
func (s *stallGroup) Optional() bool { return true }
func (s *stallGroup) Collect(time.Time, float64) (any, error) {
	<-s.stall
	return nil, errors.New("stalled")
}

func TestNewestSampleTime(t *testing.T) {
	tail := `{"v":1,"t":"2026-10-27T10:00:00Z"}` + "\n" +
		`{"v":1,"t":"2026-10-27T10:00:05Z","procs":[1,2]}` + "\n" +
		`{"v":1,"t":"2026-10-27T10:00:1` // being written
	got, ok := newestSampleTime([]byte(tail))
	if !ok || !got.Equal(time.Date(2026, 10, 27, 10, 0, 5, 0, time.UTC)) {
		t.Fatalf("got %v %v, want 10:00:05 (the unfinished line is ignored)", got, ok)
	}
	if _, ok := newestSampleTime([]byte(`{"v":1,"t":"2026`)); ok {
		t.Fatal("a cut line alone has no time")
	}
	if _, ok := newestSampleTime(nil); ok {
		t.Fatal("empty tail has no time")
	}
}

// A folder read must not report Running late just because manifest.json is
// rewritten only every five minutes.
func TestFolderReadUsesNewestSampleNotStaleManifest(t *testing.T) {
	g := newRig(t, func(c *Config) { c.IntervalSeconds = 5 })
	g.r.Tick(g.clk.t)
	for i := 0; i < 20; i++ { // 100 s of samples, inside the 5 minute manifest write
		g.tick(5 * time.Second)
	}
	fileLast := func() time.Time {
		b, err := os.ReadFile(filepath.Join(g.s.Dir(), ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		m, perr := parseManifest(b)
		if perr != nil || m.LastSample == nil {
			t.Fatalf("manifest on disk: %v %v", m, perr)
		}
		return *m.LastSample
	}
	if !fileLast().Before(g.clk.t.Add(-60 * time.Second)) {
		t.Skip("manifest was rewritten; the stale case does not occur in this setup")
	}
	src := NewFolderSource(Location{Kind: LocFolder, Path: g.s.Dir()})
	rd, err := ProbeSource(context.Background(), src, func() time.Time { return g.clk.t })
	if err != nil {
		t.Fatal(err)
	}
	if rd.Manifest.LastSample == nil || !rd.Manifest.LastSample.Equal(g.clk.t.Truncate(time.Millisecond)) {
		t.Fatalf("LastSample = %v, want the newest sample %v", rd.Manifest.LastSample, g.clk.t)
	}
	st := DeriveState(StateInput{Placed: true, Reading: &rd})
	if st.State == SRunningLate {
		t.Fatalf("a recorder that just sampled must not read as late: %s", st.Detail)
	}
}

func TestFolderAndHTTPDiagnostics(t *testing.T) {
	g, d := diagRig(t)
	d.Line("beat", "rounds", 5)
	src := NewFolderSource(Location{Kind: LocFolder, Path: g.s.Dir()})
	b, err := src.Diagnostics(context.Background())
	if err != nil || !strings.Contains(string(b), " beat ") {
		t.Fatalf("folder diagnostics = %q %v", b, err)
	}
	empty := NewFolderSource(Location{Kind: LocFolder, Path: t.TempDir()})
	b, err = empty.Diagnostics(context.Background())
	if err == nil && len(b) != 0 {
		t.Fatal("no log must read as empty")
	}
}
