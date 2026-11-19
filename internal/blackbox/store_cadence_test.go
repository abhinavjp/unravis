package blackbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- restart gap and the restart event ---

func hasRestartEvent(m Manifest, detail string) bool {
	for _, e := range m.Events {
		if e.Kind == EventRestart && e.Detail == detail {
			return true
		}
	}
	return false
}

// A restart that takes 36 s after a run that ticked every 60 s is not a gap
// (the limit is 1.5 x 60 = 90 s), but it is still visible as a restart event.
func TestStoreShortRestartAfterWidenedRunIsNotAGapButIsAnEvent(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	s.SetInterval(60)
	if err := s.Append(smp("r1", 1, t0), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}

	c.t = t0.Add(36 * time.Second)
	s2 := openT(t, dir, "r2", c) // base 15, as the owner configured it
	if err := s2.Append(smp("r2", 1, c.t), ""); err != nil {
		t.Fatal(err)
	}
	m := s2.Manifest()
	if len(m.Gaps) != 0 {
		t.Fatalf("a 36 s restart after a 60 s cadence is not a gap: %+v", m.Gaps)
	}
	if !hasRestartEvent(m, RestartAfterStop) {
		t.Fatalf("restart event missing: %+v", m.Events)
	}
}

// At a 15 s cadence a 36 s restart is a gap: more than 1.5 intervals.
func TestStoreRestartAtBaseCadenceIsAGap(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	c.t = t0.Add(36 * time.Second)
	s2 := openT(t, dir, "r2", c)
	_ = s2.Append(smp("r2", 1, c.t), "")
	m := s2.Manifest()
	if len(m.Gaps) != 1 || m.Gaps[0].Cause != CauseRestart || m.Gaps[0].Seconds != 36 {
		t.Fatalf("gaps %+v, want one restart gap of 36 s", m.Gaps)
	}
	// 20 s is inside 1.5 x 15 = 22.5 s.
	dir2 := t.TempDir()
	c2 := &clock{t: t0}
	a := openT(t, dir2, "r1", c2)
	_ = a.Append(smp("r1", 1, t0), "")
	_ = a.Close("stopped")
	c2.t = t0.Add(20 * time.Second)
	b := openT(t, dir2, "r2", c2)
	_ = b.Append(smp("r2", 1, c2.t), "")
	if g := b.Manifest().Gaps; len(g) != 0 {
		t.Fatalf("a 20 s restart at 15 s is not a gap: %+v", g)
	}
}

func TestStoreRestartAfterCrashIsRecorded(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	// no Close: the process dies here.
	c.t = t0.Add(10 * time.Second)
	s2 := openT(t, dir, "r2", c)
	if !hasRestartEvent(s2.Manifest(), RestartAfterCrash) {
		t.Fatalf("after-crash restart event missing: %+v", s2.Manifest().Events)
	}
	// The very first start has no restart event.
	s0 := openT(t, t.TempDir(), "r1", &clock{t: t0})
	for _, e := range s0.Manifest().Events {
		if e.Kind == EventRestart {
			t.Fatalf("first start recorded a restart: %+v", e)
		}
	}
}

func TestRestartGapLimit(t *testing.T) {
	cases := []struct {
		prior int
		want  time.Duration
	}{
		{15, 22500 * time.Millisecond},
		{60, 90 * time.Second},
		{0, time.Duration(1.5 * float64(DefaultIntervalSecond) * float64(time.Second))},
	}
	for _, tc := range cases {
		if got := RestartGapLimit(tc.prior); got != tc.want {
			t.Errorf("RestartGapLimit(%d) = %v, want %v", tc.prior, got, tc.want)
		}
	}
}

// --- [] not null ---

func TestManifestSlicesAreNeverNull(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"runs", "files", "gaps", "events"} {
		v := strings.TrimSpace(string(generic[k]))
		if v == "null" || !strings.HasPrefix(v, "[") {
			t.Fatalf("%s is %s in the file, want an array", k, v)
		}
	}
	m := s.Manifest()
	if m.Runs == nil || m.Files == nil || m.Gaps == nil || m.Events == nil {
		t.Fatalf("Manifest() has a nil slice: %+v", m)
	}
	// The copy is JSON-encoded the same way by the status server.
	out, _ := json.Marshal(m)
	if strings.Contains(string(out), `"gaps":null`) || strings.Contains(string(out), `"files":null`) {
		t.Fatalf("copy encodes null: %s", out)
	}
}

// An older manifest (null slices, no budget or self block) is still read, and a
// store opened on it writes arrays back.
func TestStoreReadsOldManifestWithNulls(t *testing.T) {
	dir := t.TempDir()
	old := `{"format":1,"name":"box","version":"old","host":"H","intervalSeconds":15,` +
		`"created":"2026-10-27T09:00:00Z","updated":"2026-10-27T09:30:00Z","state":"stopped",` +
		`"runs":null,"files":null,"gaps":null,"events":null,"probe":{"enabled":false}}`
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Runs == nil || m.Files == nil || m.Gaps == nil || m.Events == nil {
		t.Fatal("ReadManifest left a nil slice")
	}
	if m.Budget != nil {
		t.Fatalf("an old manifest has no budget: %+v", m.Budget)
	}
	s := openT(t, dir, "r1", &clock{t: t0})
	raw, _ := os.ReadFile(filepath.Join(dir, ManifestName))
	if strings.Contains(string(raw), "null") {
		t.Fatalf("the rewritten manifest still has a null: %s", raw)
	}
	_ = s
}

// --- manifest write cadence ---

// The manifest file is not rewritten for every sample: it follows the first
// sample of a file, events, and a five-minute beat. The in-memory copy is always
// current.
func TestStoreManifestWriteCadence(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	fileLast := func() time.Time {
		m, err := ReadManifest(dir)
		if err != nil || m.LastSample == nil {
			return time.Time{}
		}
		return *m.LastSample
	}

	if err := s.Append(smp("r1", 1, t0), ""); err != nil {
		t.Fatal(err)
	}
	if !fileLast().Equal(t0) {
		t.Fatal("the first sample of a file must be written to the manifest at once")
	}
	// Samples inside the five minutes: the file stays, memory moves.
	for i := 1; i <= 10; i++ {
		c.t = t0.Add(time.Duration(i) * 15 * time.Second)
		if err := s.Append(smp("r1", uint64(i+1), c.t), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := fileLast(); !got.Equal(t0) {
		t.Fatalf("manifest file rewritten for plain samples: last = %v", got)
	}
	if got := s.Manifest().LastSample; got == nil || !got.Equal(t0.Add(150*time.Second)) {
		t.Fatalf("in-memory manifest not current: %v", got)
	}
	if n := s.Manifest().SampleCount(); n != 11 {
		t.Fatalf("in-memory sample count = %d, want 11", n)
	}
	// Past five minutes the next sample is written.
	c.t = t0.Add(manifestWriteEvery + 15*time.Second)
	if err := s.Append(smp("r1", 12, c.t), ""); err != nil {
		t.Fatal(err)
	}
	if got := fileLast(); !got.Equal(c.t) {
		t.Fatalf("manifest not written after five minutes: last = %v", got)
	}
	// An event writes at once.
	c.t = c.t.Add(15 * time.Second)
	_ = s.Append(smp("r1", 13, c.t), "")
	s.Event(EventCollectFailed, "x")
	m, _ := ReadManifest(dir)
	found := false
	for _, e := range m.Events {
		found = found || e.Kind == EventCollectFailed
	}
	if !found {
		t.Fatal("an event must reach the manifest file at once")
	}
	if got := m.LastSample; got == nil || !got.Equal(c.t) {
		t.Fatalf("an event write must carry the newest sample: %v", got)
	}
	// A gap writes at once.
	gapsBefore := len(s.Manifest().Gaps)
	c.t = c.t.Add(10 * time.Minute)
	_ = s.Append(smp("r1", 14, c.t), "")
	m, _ = ReadManifest(dir)
	if len(m.Gaps) != gapsBefore+1 {
		t.Fatalf("a gap must reach the manifest file at once: %+v", m.Gaps)
	}
	// Close writes the final state.
	c.t = c.t.Add(15 * time.Second)
	_ = s.Append(smp("r1", 15, c.t), "")
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	m, _ = ReadManifest(dir)
	if m.State != ManifestStopped || m.LastSample == nil || !m.LastSample.Equal(c.t) {
		t.Fatalf("the stop must write the final manifest: %+v", m)
	}
}

// The compact file is smaller than the indented one and still reads back.
func TestStoreManifestIsCompactJSON(t *testing.T) {
	dir := t.TempDir()
	openT(t, dir, "r1", &clock{t: t0})
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\n  ") {
		t.Fatalf("manifest is indented: %s", raw)
	}
	if _, err := ReadManifest(dir); err != nil {
		t.Fatal(err)
	}
}

// --- fsync batching ---

type syncCounter struct {
	*os.File
	n *int
}

func (f syncCounter) Sync() error { *f.n++; return f.File.Sync() }

func appendN(t *testing.T, s *Store, c *clock, n int, step time.Duration) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Append(smp("r1", uint64(i+1), c.t), ""); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(step)
	}
}

func TestStoreSyncBatchingBelow30Seconds(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c) // base 15
	syncs := 0
	s.wrapFile = func(f *os.File) recFile { return syncCounter{File: f, n: &syncs} }
	appendN(t, s, c, 8, 15*time.Second)
	if syncs >= 8 {
		t.Fatalf("%d syncs for 8 samples at 15 s: the fsync must be batched", syncs)
	}
	if syncs < 2 {
		t.Fatalf("%d syncs for 8 samples at 15 s: want at least the first and the 60 s one", syncs)
	}
	// An hour roll always syncs the file it leaves.
	before := syncs
	c.t = t0.Add(time.Hour + time.Second)
	if err := s.Append(smp("r1", 99, c.t), ""); err != nil {
		t.Fatal(err)
	}
	if syncs <= before {
		t.Fatal("the hour roll did not sync the old file")
	}
}

func TestStoreSyncEverySampleFrom30Seconds(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	s.SetInterval(30)
	syncs := 0
	s.wrapFile = func(f *os.File) recFile { return syncCounter{File: f, n: &syncs} }
	appendN(t, s, c, 8, 30*time.Second)
	if syncs != 8 {
		t.Fatalf("%d syncs for 8 samples at 30 s, want one per sample", syncs)
	}
}

// --- budget state and self block in the manifest ---

func TestStoreSetBudgetPersistsAndRestores(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	until := t0.Add(10 * time.Minute)
	s.SetBudget(BudgetState{Base: 15, Effective: 30, Probation: []GroupProbation{{Group: "tcp", Until: until}}})
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Budget == nil || m.Budget.Base != 15 || m.Budget.Effective != 30 ||
		len(m.Budget.Probation) != 1 || m.Budget.Probation[0].Group != "tcp" || !m.Budget.Probation[0].Until.Equal(until) {
		t.Fatalf("budget not written when the probation set changed: %+v", m.Budget)
	}
	// Same set, new interval: memory only (SetInterval writes the interval change).
	s.SetBudget(BudgetState{Base: 15, Effective: 60, Probation: []GroupProbation{{Group: "tcp", Until: until.Add(time.Minute)}}})
	if got := s.Manifest().Budget; got == nil || got.Effective != 60 {
		t.Fatalf("in-memory budget not current: %+v", got)
	}
	m, _ = ReadManifest(dir)
	if m.Budget.Effective != 30 {
		t.Fatalf("budget file rewritten without a change in probation: %+v", m.Budget)
	}
	// The copy is independent of the store's own state.
	cp := s.Manifest()
	cp.Budget.Probation[0].Group = "changed"
	if s.Manifest().Budget.Probation[0].Group != "tcp" {
		t.Fatal("Manifest() shares the probation slice with the store")
	}
	// Clean stop, then a new run sees the state.
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	c.t = t0.Add(time.Minute)
	s2 := openT(t, dir, "r2", c)
	if b := s2.Manifest().Budget; b == nil || b.Effective != 60 || b.Base != 15 {
		t.Fatalf("a new run must see the budget of the old one: %+v", b)
	}
}

func TestStoreSetSelfIsInMemoryUntilTheNextWrite(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	st := SelfStats{HeapMB: 12.3, PrivateMB: 40, Handles: 210, TickMs: 8.5, PeakHeapMB: 14, Updated: t0}
	s.SetSelf(st)
	runs := s.Manifest().Runs
	if got := runs[len(runs)-1].Self; got == nil || got.HeapMB != 12.3 || got.Handles != 210 {
		t.Fatalf("self block not in the in-memory run record: %+v", got)
	}
	m, _ := ReadManifest(dir)
	if m.Runs[len(m.Runs)-1].Self != nil {
		t.Fatal("SetSelf must not write the manifest file by itself")
	}
	// Close writes it.
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	m, _ = ReadManifest(dir)
	got := m.Runs[len(m.Runs)-1].Self
	if got == nil || got.HeapMB != 12.3 || got.PrivateMB != 40 || got.Handles != 210 || got.TickMs != 8.5 || got.PeakHeapMB != 14 {
		t.Fatalf("self block lost at the stop: %+v", got)
	}
	// The copy does not share the pointer.
	cp := s.Manifest()
	cp.Runs[len(cp.Runs)-1].Self.HeapMB = 999
	if s.Manifest().Runs[len(cp.Runs)-1].Self.HeapMB != 12.3 {
		t.Fatal("Manifest() shares the self block with the store")
	}
}

// --- housekeeping works on one snapshot of the file list ---

func TestHousekeepManyFilesLeavesManifestMatchingDisk(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 12, from, 1000)
	now := from.Add(11 * time.Hour)
	res, err := s.Housekeep(Limits{MaxBytes: 3500}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 9 {
		t.Fatalf("removed %d, want 9: %v", len(res.Removed), res.Removed)
	}
	if res.Bytes > 3500 {
		t.Fatalf("bytes after the pass = %d", res.Bytes)
	}
	// The manifest file lists exactly what is on disk.
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	onDisk := listRecordingFiles(dir)
	if len(m.Files) != len(onDisk) || len(onDisk) != 3 {
		t.Fatalf("manifest lists %d files, disk has %d", len(m.Files), len(onDisk))
	}
	for i, f := range m.Files {
		if f.Name != onDisk[i] {
			t.Fatalf("manifest file %d = %s, disk has %s", i, f.Name, onDisk[i])
		}
	}
	// One housekeeping event for the whole pass.
	n := 0
	for _, e := range m.Events {
		if e.Kind == EventHousekeeping {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d housekeeping events for one pass, want 1", n)
	}
}
