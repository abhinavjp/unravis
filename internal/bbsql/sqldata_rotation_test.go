package bbsql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// W10-09: rotation protects the actual current writing hour by name (not by sort
// order), defers age purging on a wall/monotonic discontinuity, bounds each pass,
// and handles future-dated files explicitly.

func writeSample(t *testing.T, dir string, when time.Time, payload string) string {
	t.Helper()
	p := SamplePath(dir, when)
	if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRotateProtectsCurrentHourAgainstFutureFile: a future-dated file plus a
// nonempty current hour, with a byte cap that fits the current hour alone. The
// current hour's CONTENT must survive (not merely its pathname after a rewrite),
// and the future file is the one removed.
func TestRotateProtectsCurrentHourAgainstFutureFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir, ConnConfig{})
	r.KeepHours = 24
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	current := writeSample(t, dir, now, strings.Repeat("C", 100)+"\n")
	future := writeSample(t, dir, now.Add(5*time.Hour), strings.Repeat("F", 100)+"\n")
	r.MaxBytes = 120 // fits the current hour alone, not both

	if err := r.rotate(now); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("current hour was deleted: %v", err)
	}
	if !strings.HasPrefix(string(data), "CCC") {
		t.Fatalf("current hour content lost: %q", data)
	}
	if _, err := os.Stat(future); !os.IsNotExist(err) {
		t.Fatalf("future-dated file survived the byte cap while current history was at risk: %v", err)
	}
}

// TestRotateDefersAgePurgeOnClockJump: a forward wall-clock jump with a steady
// clock that does not move must defer age purging; once the clocks agree again,
// age purging resumes.
func TestRotateDefersAgePurgeOnClockJump(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir, ConnConfig{})
	r.KeepHours = 2
	var mono time.Duration
	r.Mono = func() time.Duration { return mono }
	t0 := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	p := writeSample(t, dir, t0.Add(-time.Hour), "x\n") // fresh at t0, expired after a jump

	if err := r.rotate(t0); err != nil { // records the clock; file within window
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file removed on the first pass: %v", err)
	}
	// Wall jumps forward 100 h; steady clock stays put -> age purge deferred.
	jumped := t0.Add(100 * time.Hour)
	if err := r.rotate(jumped); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("age purge ran despite a forward clock jump: %v", err)
	}
	// Both clocks advance together by 1 h -> no jump -> age purge resumes.
	mono = time.Hour
	if err := r.rotate(jumped.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("age purge did not resume once the clocks agreed: %v", err)
	}
}

// TestRotateBoundsAgePurgePerPass: more expired hours than the per-pass bound are
// released in steps, not all at once.
func TestRotateBoundsAgePurgePerPass(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir, ConnConfig{})
	r.KeepHours = 1
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < maxAgeRemovalsPerPass+4; i++ {
		writeSample(t, dir, now.Add(-time.Duration(i+3)*time.Hour), "x\n")
	}
	if err := r.rotate(now); err != nil {
		t.Fatal(err)
	}
	left, err := filepath.Glob(filepath.Join(dir, "bb-sql-*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 4 {
		t.Fatalf("after one bounded pass %d files remain, want 4 (removed %d)", len(left), maxAgeRemovalsPerPass)
	}
}

// TestLowDiskRecoversByDeletingSQLHistory: with the free-space reading derived
// from the retained SQL bytes, a low volume must recover by deleting eligible SQL
// history alone — no external change to the fake disk (W10-03).
func TestLowDiskRecoversByDeletingSQLHistory(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	r.KeepHours = 1
	r.MinFreePercent = 10
	// Free percent falls as retained SQL bytes grow; old history pushes it low.
	r.FreePercent = func(path string) (float64, error) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return 0, err
		}
		var total int64
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "bb-sql-") {
				if info, err := e.Info(); err == nil {
					total += info.Size()
				}
			}
		}
		return 100 - float64(total)/1000, nil
	}
	// Seed expired history large enough to drop free space below the floor.
	for i := 0; i < 5; i++ {
		writeSample(t, r.Dir, now.Add(-time.Duration(i+3)*time.Hour), strings.Repeat("x", 20000)+"\n")
	}
	if free, _ := r.FreePercent(r.Dir); free >= 10 {
		t.Fatalf("setup: free=%v, want below the floor before recovery", free)
	}
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick should recover by deleting eligible history: %v", err)
	}
	if sampleCount(t, r.Dir, "waits") != 1 {
		t.Error("sampling did not resume after SQL history restored the floor")
	}
	if free, _ := r.FreePercent(r.Dir); free < 10 {
		t.Errorf("free=%v after recovery, want at/above the floor", free)
	}
}

// TestAutoOffStillHousekeeps: an auto-off tick runs bounded cleanup (removable
// history is reclaimed) without sending any query or writing any sample (W10-03).
func TestAutoOffStillHousekeeps(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	r.KeepHours = 1
	r.state = State{Interval: BaseInterval, AutoOff: true, State: StateAutoOff}
	old := writeSample(t, r.Dir, now.Add(-3*time.Hour), "x\n")

	if err := r.Tick(now); err != nil {
		t.Fatalf("auto-off tick: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("auto-off tick did not reclaim expired history: %v", err)
	}
	if len(q.texts()) != 0 {
		t.Errorf("auto-off tick sent %d statements, want 0", len(q.texts()))
	}
	if len(readSampleLines(t, r.Dir)) != 0 {
		t.Error("auto-off tick wrote samples")
	}
}
