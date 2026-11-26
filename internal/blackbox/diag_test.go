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

func TestAGroupThatAlwaysErrorsDoesNotMakeEveryRoundNotable(t *testing.T) {
	g, _ := diagRig(t)
	g.r.Tick(g.clk.t)
	g.r.o.Collector = &groupsFake{inner: g.col, gt: []groupTime{{Name: "aspnet", Outcome: "error"}, {Name: "events", Dur: 150 * time.Millisecond, Outcome: "empty"}}}
	for i := 0; i < 5; i++ {
		g.tick(15 * time.Second)
	}
	if strings.Contains(readDiag(g), " round ") {
		t.Fatalf("error/empty groups alone must not write round lines:\n%s", readDiag(g))
	}
}

type groupsFake struct {
	inner Collector
	gt    []groupTime
}

func (f *groupsFake) Collect(n time.Time) (Sample, error) { return f.inner.Collect(n) }
func (f *groupsFake) Close() error                        { return nil }
func (f *groupsFake) GroupTimings() []groupTime           { return f.gt }

func TestAbsentGroupsAreNotCountedAsSlowInTheBeat(t *testing.T) {
	var a diagAgg
	for i := 0; i < 50; i++ {
		a.add(roundDiag{Groups: []groupTime{{Name: "iis", Outcome: "empty"}, {Name: "sqlHost", Outcome: "error"}, {Name: "x", Outcome: "skipped"}}})
	}
	if a.slowText() != "-" {
		t.Fatalf("absent groups are not slow: %s", a.slowText())
	}
	a.add(roundDiag{Groups: []groupTime{{Name: "tcp", Dur: 900 * time.Millisecond, Outcome: "ok"}}})
	if !strings.HasPrefix(a.slowText(), "tcp:900ms:x1") {
		t.Fatal(a.slowText())
	}
}

func TestDiagLogComesBackAfterAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	d.Line("a")
	d.mu.Lock()
	d.f.Close() // the next write fails
	d.mu.Unlock()
	d.Line("lost")
	c.t = c.t.Add(2 * time.Minute)
	d.Line("back")
	d.Close()
	b, _ := os.ReadFile(filepath.Join(dir, DiagFileName))
	if !strings.Contains(string(b), " back") {
		t.Fatalf("log must retry after a failure:\n%s", b)
	}
}

func TestHostBusyRoundCarriesNoStaleTimings(t *testing.T) {
	stall := make(chan struct{})
	defer close(stall)
	c := NewGroupCollector(&diagStuckHost{stall: stall}, nil, NewBudget(15)).(*groupCollector)
	c.gt = []groupTime{{Name: "old", Outcome: "timeout"}}
	c.hostDur = time.Hour
	if _, err := c.Collect(t0); !errors.Is(err, errHostSlow) {
		t.Fatalf("err = %v", err)
	}
	c.gt = []groupTime{{Name: "old", Outcome: "timeout"}}
	c.hostDur = time.Hour
	if _, err := c.Collect(t0); !errors.Is(err, errHostBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(c.GroupTimings()) != 0 || c.hostDur != 0 {
		t.Fatalf("stale: %v %v", c.gt, c.hostDur)
	}
}

type diagStuckHost struct{ stall chan struct{} }

func (b *diagStuckHost) Collect(time.Time) (Sample, error) { <-b.stall; return Sample{}, nil }
func (b *diagStuckHost) Close() error                      { return nil }

// failingDiagFS is the real file system with switches that make a step fail.
// failRename refuses to move the current file (anything else may move);
// noReplace makes a rename refuse a target that exists, as some systems do.
type failingDiagFS struct {
	osDiagFS
	failRename, failTruncate, noReplace bool
	removed                             []string
}

func (f *failingDiagFS) Remove(name string) error {
	f.removed = append(f.removed, filepath.Base(name))
	return f.osDiagFS.Remove(name)
}

func (f *failingDiagFS) Rename(a, b string) error {
	if f.failRename && filepath.Base(a) == DiagFileName {
		return errors.New("rename refused")
	}
	if _, err := os.Stat(b); f.noReplace && err == nil {
		return errors.New("target exists")
	}
	return f.osDiagFS.Rename(a, b)
}

func (f *failingDiagFS) Truncate(name string, n int64) error {
	if f.failTruncate {
		return errors.New("truncate refused")
	}
	return f.osDiagFS.Truncate(name, n)
}

func diagFileSize(t *testing.T, dir string) int64 {
	t.Helper()
	st, err := os.Stat(filepath.Join(dir, DiagFileName))
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// fillDiagNearCap writes lines until the file is just under its cap.
func fillDiagNearCap(t *testing.T, d *DiagLog, c *clock, dir string) {
	t.Helper()
	for diagFileSize(t, dir) < diagMaxBytes-400 {
		c.t = c.t.Add(time.Millisecond)
		d.Line("round", "pad", strings.Repeat("x", 150))
	}
}

// RV-BB-12: when the rename fails the file is emptied in place and the cap holds.
func TestDiagLogRenameFailsSoTheFileIsEmptied(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	defer d.Close()
	d.fsys = &failingDiagFS{failRename: true}
	stuck := 0
	d.SetOnStuck(func() { stuck++ })
	fillDiagNearCap(t, d, c, dir)
	for i := 0; i < 20; i++ { // crosses the cap
		c.t = c.t.Add(time.Millisecond)
		d.Line("round", "pad", strings.Repeat("x", 150), "seq", i)
	}
	if n := diagFileSize(t, dir); n > diagMaxBytes {
		t.Fatalf("file is %d bytes, cap is %d: it was not emptied", n, diagMaxBytes)
	}
	if !strings.Contains(string(d.Tail(DiagTailBytes)), "seq=19") {
		t.Fatal("the newest line must be written after the file was emptied")
	}
	if stuck != 0 {
		t.Fatalf("an emptied file is not stuck, event raised %d times", stuck)
	}
}

// RV-BB-12: when neither the rename nor the emptying works, writing stops (the
// file cannot grow), one event is raised, and writing starts again once a
// rotation works.
func TestDiagLogStopsWritingWhenRotationAndEmptyingFail(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	defer d.Close()
	fs := &failingDiagFS{failRename: true, failTruncate: true}
	d.fsys = fs
	stuck, resumed := 0, 0
	d.SetOnStuck(func() { stuck++ })
	d.SetOnResumed(func() { resumed++ })
	fillDiagNearCap(t, d, c, dir)
	for i := 0; i < 20; i++ {
		c.t = c.t.Add(time.Millisecond)
		d.Line("round", "pad", strings.Repeat("x", 150), "seq", i)
	}
	frozen := diagFileSize(t, dir)
	if frozen > diagMaxBytes {
		t.Fatalf("file is %d bytes, cap is %d", frozen, diagMaxBytes)
	}
	if stuck != 1 {
		t.Fatalf("event raised %d times, want once", stuck)
	}
	// Retries that fail leave the file as it is and raise nothing more.
	for i := 0; i < 50; i++ {
		c.t = c.t.Add(10 * time.Second)
		d.Line("round", "pad", strings.Repeat("y", 150))
	}
	if n := diagFileSize(t, dir); n != frozen {
		t.Fatalf("file grew from %d to %d while stuck", frozen, n)
	}
	if stuck != 1 {
		t.Fatalf("event raised %d times while stuck, want once", stuck)
	}
	if resumed != 0 {
		t.Fatalf("resumed raised %d times while still stuck", resumed)
	}
	// The way clears: the next try rotates and writing resumes.
	fs.failRename, fs.failTruncate = false, false
	c.t = c.t.Add(2 * time.Minute)
	d.Line("round", "seq", "back")
	if !strings.Contains(string(d.Tail(DiagTailBytes)), "seq=back") {
		t.Fatal("writing must resume after a rotation works")
	}
	if n := diagFileSize(t, dir); n >= frozen {
		t.Fatalf("file is %d bytes after the rotation, was %d", n, frozen)
	}
	if stuck != 1 {
		t.Fatalf("event raised %d times in all, want once", stuck)
	}
	if resumed != 1 {
		t.Fatalf("resumed raised %d times, want once", resumed)
	}
}

// RV-BB-12: the log can get stuck, recover and get stuck again; each change is
// said once, and the second stop is not hidden by the first.
func TestDiagLogRecoversThenGetsStuckAgain(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	defer d.Close()
	fs := &failingDiagFS{failRename: true, failTruncate: true}
	d.fsys = fs
	stuck, resumed := 0, 0
	d.SetOnStuck(func() { stuck++ })
	d.SetOnResumed(func() { resumed++ })
	cross := func() {
		fillDiagNearCap(t, d, c, dir)
		for i := 0; i < 5; i++ {
			c.t = c.t.Add(time.Millisecond)
			d.Line("round", "pad", strings.Repeat("x", 150))
		}
	}
	cross()
	if stuck != 1 || resumed != 0 {
		t.Fatalf("after the first stop: stuck %d resumed %d, want 1 and 0", stuck, resumed)
	}
	fs.failRename, fs.failTruncate = false, false
	c.t = c.t.Add(2 * time.Minute)
	d.Line("round", "seq", "back")
	if stuck != 1 || resumed != 1 {
		t.Fatalf("after recovery: stuck %d resumed %d, want 1 and 1", stuck, resumed)
	}
	// A line while it runs fine raises nothing.
	c.t = c.t.Add(time.Millisecond)
	d.Line("round", "seq", "again")
	if stuck != 1 || resumed != 1 {
		t.Fatalf("a normal line changed the counts: stuck %d resumed %d", stuck, resumed)
	}
	fs.failRename, fs.failTruncate = true, true
	cross()
	if stuck != 2 || resumed != 1 {
		t.Fatalf("after the second stop: stuck %d resumed %d, want 2 and 1", stuck, resumed)
	}
	fs.failRename, fs.failTruncate = false, false
	c.t = c.t.Add(2 * time.Minute)
	d.Line("round", "seq", "back2")
	if stuck != 2 || resumed != 2 {
		t.Fatalf("after the second recovery: stuck %d resumed %d, want 2 and 2", stuck, resumed)
	}
}

// RV-BB-12: a rename that fails must not cost the old ".1": it stays as it was,
// whether the emptying then works or not, and no side file is left behind.
func TestDiagLogFailedRenameKeepsTheOldFile(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failTruncate bool
	}{{"emptied instead", false}, {"stuck", true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const oldText = "2020-01-01T00:00:00.000Z round old=kept\n"
			oldPath := filepath.Join(dir, diagOldName)
			if err := os.WriteFile(oldPath, []byte(oldText), 0o600); err != nil {
				t.Fatal(err)
			}
			c := &clock{t: t0}
			d := OpenDiagLog(dir, c.now)
			defer d.Close()
			fs := &failingDiagFS{failRename: true, failTruncate: tc.failTruncate}
			d.fsys = fs
			fillDiagNearCap(t, d, c, dir)
			for i := 0; i < 5; i++ {
				c.t = c.t.Add(time.Millisecond)
				d.Line("round", "pad", strings.Repeat("x", 150))
			}
			got, err := os.ReadFile(oldPath) //nolint:gosec // G304: a test temp file
			if err != nil || string(got) != oldText {
				t.Fatalf("the old file changed: %q, %v", got, err)
			}
			if _, err := os.Stat(oldPath + ".tmp"); err == nil {
				t.Fatal("a side file was left behind")
			}
			for _, n := range fs.removed {
				if n == diagOldName {
					t.Fatal("the old file was removed before the rename worked")
				}
			}
		})
	}
}

// A system that will not rename over an existing file still gets the new ".1":
// the old one is moved aside, replaced, and then dropped.
func TestDiagLogReplacesTheOldFileWhereRenameCannotOverwrite(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, diagOldName)
	if err := os.WriteFile(oldPath, []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	defer d.Close()
	d.fsys = &failingDiagFS{noReplace: true}
	stuck := 0
	d.SetOnStuck(func() { stuck++ })
	fillDiagNearCap(t, d, c, dir)
	for i := 0; i < 5; i++ {
		c.t = c.t.Add(time.Millisecond)
		d.Line("round", "pad", strings.Repeat("x", 150))
	}
	got, err := os.ReadFile(oldPath) //nolint:gosec // G304: a test temp file
	if err != nil || strings.Contains(string(got), "older") || len(got) < diagMaxBytes-1000 {
		t.Fatalf("the old file was not replaced by the full one (%d bytes, %v)", len(got), err)
	}
	if _, err := os.Stat(oldPath + ".tmp"); err == nil {
		t.Fatal("a side file was left behind")
	}
	if n := diagFileSize(t, dir); n > diagMaxBytes/2 {
		t.Fatalf("the current file is %d bytes: it was not started again", n)
	}
	if stuck != 0 {
		t.Fatalf("a rotation that worked raised the stuck event %d times", stuck)
	}
}

// The callbacks run after the lock is released: they may write a line (the real
// one takes the store lock and may log), and must not deadlock the log.
func TestDiagLogCallbacksRunOutsideTheLock(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	d := OpenDiagLog(dir, c.now)
	defer d.Close()
	fs := &failingDiagFS{failRename: true, failTruncate: true}
	d.fsys = fs
	d.SetOnStuck(func() { d.Line("note", "what", "stuck-seen") })
	d.SetOnResumed(func() { d.Line("note", "what", "resumed-seen") })
	done := make(chan struct{})
	go func() {
		defer close(done)
		fillDiagNearCap(t, d, c, dir)
		for i := 0; i < 5; i++ {
			c.t = c.t.Add(time.Millisecond)
			d.Line("round", "pad", strings.Repeat("x", 150))
		}
		fs.failRename, fs.failTruncate = false, false
		c.t = c.t.Add(2 * time.Minute)
		d.Line("round", "seq", "back")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a callback that writes a line deadlocked the log")
	}
	if !strings.Contains(string(d.Tail(DiagTailBytes)), "what=resumed-seen") {
		t.Fatal("the line written by the resumed callback is missing")
	}
}
