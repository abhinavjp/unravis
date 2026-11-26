package blackbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// RV-BB-9: "paused for low disk" is a stored field, not the newest pause or
// resume event in a list that keeps only the newest maxEvents.

func pausedState(m Manifest, now time.Time) PageState {
	last := now.Add(-5 * time.Second)
	first := last.Add(-time.Hour)
	m.FirstSample, m.LastSample = &first, &last
	return DeriveState(StateInput{Placed: true, Reading: &Reading{Manifest: m, ReadAt: now}}).State
}

func TestPausedForDiskSurvivesMoreThanTheEventWindow(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	g.disk.set(5)
	g.tick(60 * time.Second) // pause
	if !hasEvent(g.s.Manifest(), EventPausedDiskLow) {
		t.Fatal("no pause event")
	}
	for i := 0; i < maxEvents+50; i++ {
		g.s.Event(EventHousekeeping, "later event "+strconv.Itoa(i))
	}
	m := g.s.Manifest()
	if hasEvent(m, EventPausedDiskLow) {
		t.Fatal("the pause event should have left the window by now")
	}
	if !m.PausedDiskLow || m.PausedSince.IsZero() {
		t.Fatalf("stored pause lost: %v %v", m.PausedDiskLow, m.PausedSince)
	}
	if got := pausedState(m, g.clk.t); got != SDiskLow {
		t.Fatalf("status = %v, want %v while recording is still paused", got, SDiskLow)
	}
	// The same through the file a reader reads.
	rm, err := ReadManifest(g.s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := pausedState(rm, g.clk.t); got != SDiskLow {
		t.Fatalf("file status = %v, want %v", got, SDiskLow)
	}
}

func TestResumeClearsTheStoredPause(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	g.disk.set(5)
	g.tick(60 * time.Second)
	g.disk.set(50)
	g.tick(60 * time.Second)
	m := g.s.Manifest()
	if m.PausedDiskLow || !m.PausedSince.IsZero() {
		t.Fatalf("pause not cleared: %v %v", m.PausedDiskLow, m.PausedSince)
	}
	if got := pausedState(m, g.clk.t); got == SDiskLow {
		t.Fatal("still says paused after resume")
	}
}

// pausedRestart pauses a recorder for low disk, stops it, and starts a second
// recorder on the same folder. disk is what the second recorder sees.
func pausedRestart(t *testing.T, disk DiskSpace) (*Recorder, *Store, *clock, *time.Duration, *pctDisk) {
	t.Helper()
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	g.disk.set(5)
	g.tick(60 * time.Second)
	if !g.s.PausedDiskLow() {
		t.Fatal("first recorder did not pause")
	}
	dir := g.s.Dir()
	if err := g.s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	c2 := &clock{t: g.clk.t.Add(time.Minute)}
	s2 := openT(t, dir, "run2", c2)
	if !s2.PausedDiskLow() {
		t.Fatal("restart lost the stored pause")
	}
	cfg := Default()
	cfg.Name, cfg.Folder = "box", dir
	cfg.Record.LogsCatalogue = false
	mono := new(time.Duration)
	if disk == nil {
		disk = g.disk
	}
	r2 := NewRecorder(RecorderOptions{Config: cfg, Store: s2, Collector: &fakeCollector{}, Disk: disk, RunID: "run2",
		Now: c2.now, Mono: func() time.Duration { return *mono }})
	return r2, s2, c2, mono, g.disk
}

func countKind(m Manifest, kind string) int {
	n := 0
	for _, e := range m.Events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// A recorder restarted while the drive is still full stays paused, and says so
// once: it does not raise a second pause event for the same stretch.
func TestRestartWhileStillFullStaysPausedWithOnePauseEvent(t *testing.T) {
	r2, s2, c2, _, _ := pausedRestart(t, nil)
	r2.Tick(c2.t)
	if s2.Manifest().SampleCount() != 1 || !s2.PausedDiskLow() {
		t.Fatalf("not paused after restart: samples %d", s2.Manifest().SampleCount())
	}
	if st := r2.Status(c2.t); st.State != StateDiskLow {
		t.Fatalf("status = %v, want %v", st.State, StateDiskLow)
	}
	if n := countKind(s2.Manifest(), EventPausedDiskLow); n != 1 {
		t.Fatalf("pause events = %d, want exactly 1", n)
	}
}

// The drive was freed while the recorder was down: the first pass after the
// restart resumes, clears the stored pause and records again.
func TestRestartAfterTheDriveWasFreedResumes(t *testing.T) {
	r2, s2, c2, mono, disk := pausedRestart(t, nil)
	disk.set(50)
	r2.Tick(c2.t)
	if s2.PausedDiskLow() || !s2.Manifest().PausedSince.IsZero() {
		t.Fatal("stored pause stuck after the drive was freed")
	}
	if n := countKind(s2.Manifest(), EventResumed); n != 1 {
		t.Fatalf("resumed events = %d, want 1", n)
	}
	if n := countKind(s2.Manifest(), EventPausedDiskLow); n != 1 {
		t.Fatalf("pause events = %d, want still 1", n)
	}
	c2.t, *mono = c2.t.Add(15*time.Second), *mono+15*time.Second
	before := s2.Manifest().SampleCount()
	r2.Tick(c2.t)
	if s2.Manifest().SampleCount() <= before {
		t.Fatal("no sample after resuming")
	}
}

// A failed housekeeping pass must not leave a restarted, paused recorder paused
// for good once the free space can be read and is enough.
func TestRestartResumesEvenWhenHousekeepingFails(t *testing.T) {
	r2, s2, c2, _, disk := pausedRestart(t, nil)
	disk.set(50)
	orig := housekeepFn
	housekeepFn = func(s *Store, l Limits, d DiskSpace, now time.Time) (Housekeeping, error) {
		return Housekeeping{}, errors.New("pass failed")
	}
	t.Cleanup(func() { housekeepFn = orig })
	r2.Tick(c2.t)
	if s2.PausedDiskLow() || countKind(s2.Manifest(), EventResumed) != 1 {
		t.Fatal("stayed paused although the free space read fine")
	}
}

// With the free space still low, or not readable at all, a failed pass changes
// nothing: the recorder stays paused.
func TestFailedPassKeepsThePauseWhenSpaceIsLowOrUnknown(t *testing.T) {
	// Both restarts are made first, with the real housekeeping pass.
	r2, s2, c2, _, _ := pausedRestart(t, nil) // disk still 5%
	r3, s3, c3, _, _ := pausedRestart(t, errDisk{})
	orig := housekeepFn
	housekeepFn = func(s *Store, l Limits, d DiskSpace, now time.Time) (Housekeeping, error) {
		return Housekeeping{}, errors.New("pass failed")
	}
	t.Cleanup(func() { housekeepFn = orig })

	r2.Tick(c2.t)
	if !s2.PausedDiskLow() || countKind(s2.Manifest(), EventResumed) != 0 {
		t.Fatal("resumed while the drive was still full")
	}
	r3.Tick(c3.t)
	if !s3.PausedDiskLow() || countKind(s3.Manifest(), EventResumed) != 0 {
		t.Fatal("resumed without a reading of the free space")
	}
}

type errDisk struct{}

func (errDisk) Free(string) (DiskInfo, error) { return DiskInfo{}, errors.New("no reading") }

// An older reader that knows nothing of the new fields still reads the manifest,
// and the new fields are left out when the drive is fine.
func TestStoredPauseJSONCompatibility(t *testing.T) {
	m := Manifest{Format: 1, Name: "box"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pausedDiskLow") || strings.Contains(string(b), "pausedSince") {
		t.Fatalf("clear manifest carries the pause fields: %s", b)
	}
	m.PausedDiskLow, m.PausedSince = true, time.Date(2026, 10, 27, 10, 30, 0, 0, time.UTC)
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	// An old reader is a struct without the two fields.
	var old struct {
		Format int    `json:"format"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(b, &old); err != nil || old.Name != "box" {
		t.Fatalf("old reader: %v %+v", err, old)
	}
	var back Manifest
	if err := json.Unmarshal(b, &back); err != nil || !back.PausedDiskLow || !back.PausedSince.Equal(m.PausedSince) {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

// A manifest an older recorder wrote has no stored pause: it is worked out from
// the events once, then kept.
func TestOldManifestWithoutTheFieldStillReadsPaused(t *testing.T) {
	write := func(t *testing.T, events string) string {
		dir := t.TempDir()
		doc := `{"format":1,"name":"box","version":"old","host":"H","intervalSeconds":15,` +
			`"created":"2026-10-27T10:00:00Z","updated":"2026-10-27T11:00:00Z","state":"running",` +
			`"runs":[],"files":[],"gaps":[],"events":` + events + `,"probe":{"enabled":false}}`
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	paused := `[{"t":"2026-10-27T10:30:00Z","kind":"paused-disk-low"},{"t":"2026-10-27T10:31:00Z","kind":"housekeeping"}]`
	resumed := `[{"t":"2026-10-27T10:30:00Z","kind":"paused-disk-low"},{"t":"2026-10-27T10:40:00Z","kind":"resumed"}]`

	m, err := ReadManifest(write(t, paused))
	if err != nil {
		t.Fatal(err)
	}
	if !m.PausedDiskLow || !m.PausedSince.Equal(time.Date(2026, 10, 27, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("old paused manifest: %v %v", m.PausedDiskLow, m.PausedSince)
	}
	if m, err = ReadManifest(write(t, resumed)); err != nil || m.PausedDiskLow {
		t.Fatalf("old resumed manifest: %v %v", m.PausedDiskLow, err)
	}

	// The remote reader parses the same way.
	pm, perr := parseManifest([]byte(`{"format":1,"events":` + paused + `}`))
	if perr != nil || !pm.PausedDiskLow {
		t.Fatalf("parseManifest: %v %v", pm.PausedDiskLow, perr)
	}

	// Opening the store stores the result, so the next write keeps it with no events.
	dir := write(t, paused)
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	if !s.PausedDiskLow() {
		t.Fatal("store did not take the pause from the old manifest")
	}
	for i := 0; i < maxEvents+10; i++ {
		s.Event(EventHousekeeping, "x")
	}
	rm, err := ReadManifest(dir)
	if err != nil || !rm.PausedDiskLow {
		t.Fatalf("stored pause not kept: %v %v", rm.PausedDiskLow, err)
	}
}
