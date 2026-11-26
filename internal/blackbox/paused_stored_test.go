package blackbox

import (
	"os"
	"path/filepath"
	"strconv"
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

// A recorder restarted while the drive is still full stays paused, and a restart
// after the drive was freed clears the stored pause (it must not stay stuck).
func TestRestartKeepsAndThenClearsTheStoredPause(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	g.disk.set(5)
	g.tick(60 * time.Second)
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
	mono := time.Duration(0)
	r2 := NewRecorder(RecorderOptions{Config: cfg, Store: s2, Collector: &fakeCollector{}, Disk: g.disk, RunID: "run2",
		Now: c2.now, Mono: func() time.Duration { return mono }})
	r2.Tick(c2.t) // drive still full: stays paused, writes nothing
	if s2.Manifest().SampleCount() != 1 || !s2.PausedDiskLow() {
		t.Fatalf("not paused after restart: samples %d", s2.Manifest().SampleCount())
	}
	g.disk.set(50)
	c2.t, mono = c2.t.Add(60*time.Second), mono+60*time.Second
	r2.Tick(c2.t)
	if s2.PausedDiskLow() || !hasEvent(s2.Manifest(), EventResumed) {
		t.Fatal("stored pause stuck after the drive was freed")
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
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(doc), 0o644); err != nil {
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
