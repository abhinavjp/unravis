package bbsql

import (
	"encoding/json"
	"testing"
	"time"
)

// W11-05: a delta must never span an unobserved interval or a reset that overtook
// the old value during a gap. The runner hands each stateful sampler an
// Observation{Time, Epoch}; a changed epoch (reconnect, failure, pause, partial
// tick, clock jump) rebaselines so the next sighting emits no delta.

// TestSamplerEpochInvalidatesBaselines proves the contract directly on each
// stateful sampler: a same-epoch sequence emits a delta, an epoch change
// rebaselines (no delta), and the following same-epoch cycle deltas from the new
// baseline.
func TestSamplerEpochInvalidatesBaselines(t *testing.T) {
	t0 := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	t.Run("waits", func(t *testing.T) {
		s := &waitsSampler{}
		s.Observe(Observation{Time: t0, Epoch: 1})
		s.Decode(waitsRows(waitsFixture{"WRITELOG", 100, 5}))
		s.Observe(Observation{Time: t0.Add(time.Minute), Epoch: 1})
		if p, _, _ := s.Decode(waitsRows(waitsFixture{"WRITELOG", 200, 9})); p == nil {
			t.Fatal("same-epoch cycle emitted no delta, want 100")
		}
		// Epoch change (e.g. reconnect): the counter jumped during the gap.
		s.Observe(Observation{Time: t0.Add(2 * time.Minute), Epoch: 2})
		if p, _, _ := s.Decode(waitsRows(waitsFixture{"WRITELOG", 700, 20})); p != nil {
			t.Fatalf("epoch change emitted %#v, want nil (rebaseline, no delta over the gap)", p)
		}
		s.Observe(Observation{Time: t0.Add(3 * time.Minute), Epoch: 2})
		p, _, _ := s.Decode(waitsRows(waitsFixture{"WRITELOG", 750, 22}))
		got := p.([]Waits)
		if len(got) != 1 || got[0].DeltaMs != 50 {
			t.Fatalf("after rebaseline = %#v, want delta 50 from the new baseline", got)
		}
	})
	t.Run("filestats", func(t *testing.T) {
		s := &filestatsSampler{}
		base := FileStats{DatabaseID: 5, FileID: 2, ReadStallMs: 100, Reads: 10}
		s.Observe(Observation{Time: t0, Epoch: 1})
		s.Decode(fileStatsRows(base))
		s.Observe(Observation{Time: t0.Add(time.Minute), Epoch: 1})
		cur := base
		cur.ReadStallMs = 200
		cur.Reads = 20
		if p, _, _ := s.Decode(fileStatsRows(cur)); p == nil {
			t.Fatal("same-epoch cycle emitted no delta")
		}
		s.Observe(Observation{Time: t0.Add(2 * time.Minute), Epoch: 2})
		gap := base
		gap.ReadStallMs = 900
		gap.Reads = 90
		if p, _, _ := s.Decode(fileStatsRows(gap)); p != nil {
			t.Fatalf("epoch change emitted %#v, want nil (rebaseline)", p)
		}
	})
	t.Run("semaphores", func(t *testing.T) {
		s := &semaphoresSampler{}
		s.Observe(Observation{Time: t0, Epoch: 1})
		s.Decode(semRows(semFixture{id: 0, forced: 100, timeouts: 10}))
		s.Observe(Observation{Time: t0.Add(15 * time.Second), Epoch: 1})
		if p, _, _ := s.Decode(semRows(semFixture{id: 0, forced: 110, timeouts: 12})); p == nil {
			t.Fatal("same-epoch cycle emitted no delta")
		}
		// Epoch change rebaselines: the first cycle of a new epoch emits nothing.
		s.Observe(Observation{Time: t0.Add(30 * time.Second), Epoch: 2})
		if p, _, _ := s.Decode(semRows(semFixture{id: 0, grantees: 1, forced: 500, timeouts: 50})); p != nil {
			t.Fatalf("epoch change emitted %#v, want nil (rebaseline)", p)
		}
		// The next same-epoch cycle deltas from the new baseline.
		s.Observe(Observation{Time: t0.Add(45 * time.Second), Epoch: 2})
		p, _, _ := s.Decode(semRows(semFixture{id: 0, forced: 505, timeouts: 53}))
		got := p.([]Semaphores)
		if len(got) != 1 || semDelta(t, got[0].Forced) != 5 || semDelta(t, got[0].Timeouts) != 3 {
			t.Fatalf("after rebaseline = %#v, want deltas {5 3} from the new baseline", got)
		}
	})
}

// waitsDeltasFromLines collects every emitted waits delta across the NDJSON lines.
func waitsDeltasFromLines(t *testing.T, dir string) []Waits {
	t.Helper()
	var out []Waits
	for _, doc := range readSampleLines(t, dir) {
		if doc.Kind != "waits" || doc.Rows == nil {
			continue
		}
		data, err := json.Marshal(doc.Rows)
		if err != nil {
			t.Fatal(err)
		}
		var rows []Waits
		if err := json.Unmarshal(data, &rows); err != nil {
			t.Fatal(err)
		}
		out = append(out, rows...)
	}
	return out
}

// TestRunnerFailureGapRebaselines drives the REAL waits sampler through the
// runner: a query failure between two sightings must rebaseline, so the recovered
// sighting emits no delta spanning the failure gap.
func TestRunnerFailureGapRebaselines(t *testing.T) {
	now := time.Date(2026, 3, 4, 6, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: waitsRows(waitsFixture{"WRITELOG", 100, 5})}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{&waitsSampler{}}})

	if err := r.Tick(now); err != nil { // baseline
		t.Fatalf("tick1: %v", err)
	}
	q.replies[Statements["bb:waits"]] = waitsRows(waitsFixture{"WRITELOG", 200, 9})
	if err := r.Tick(now.Add(DemotedInterval)); err != nil { // delta 100
		t.Fatalf("tick2: %v", err)
	}
	// Failure: the connection drops and continuity breaks.
	q.errFor = func(text string) error {
		if text == Statements["bb:waits"] {
			return &Error{CodeNetwork, "unreachable"}
		}
		return nil
	}
	if err := r.Tick(now.Add(2 * DemotedInterval)); err == nil {
		t.Fatal("tick3: want the injected failure")
	}
	// Recovered: reconnect rebaselines; the counter jumped to 700 during the gap.
	q.errFor = nil
	q.replies[Statements["bb:waits"]] = waitsRows(waitsFixture{"WRITELOG", 700, 20})
	if err := r.Tick(now.Add(3 * DemotedInterval)); err != nil {
		t.Fatalf("tick4: %v", err)
	}
	q.replies[Statements["bb:waits"]] = waitsRows(waitsFixture{"WRITELOG", 750, 22})
	if err := r.Tick(now.Add(4 * DemotedInterval)); err != nil {
		t.Fatalf("tick5: %v", err)
	}

	deltas := waitsDeltasFromLines(t, r.Dir)
	for _, d := range deltas {
		if d.DeltaMs == 500 {
			t.Fatalf("a delta of 500 (700-200) spans the failure gap: %#v", deltas)
		}
	}
	if len(deltas) != 2 || deltas[0].DeltaMs != 100 || deltas[1].DeltaMs != 50 {
		t.Fatalf("deltas = %#v, want {100, 50} (rebaseline after the gap)", deltas)
	}
}

// TestRunnerReconnectRebaselines proves the hourly connection recycle (a new
// source epoch) rebaselines delta samplers even without a failure.
func TestRunnerReconnectRebaselines(t *testing.T) {
	now := time.Date(2026, 3, 4, 7, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: waitsRows(waitsFixture{"WRITELOG", 100, 5})}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{&waitsSampler{}}})
	clock := now
	r.Now = func() time.Time { return clock }
	tick := func(at time.Time) {
		clock = at
		if err := r.Tick(at); err != nil {
			t.Fatalf("tick at %v: %v", at, err)
		}
	}

	tick(now) // baseline 100
	q.replies[Statements["bb:waits"]] = waitsRows(waitsFixture{"WRITELOG", 200, 9})
	tick(now.Add(DemotedInterval)) // delta 100
	// Past the connection lifetime: the runner reconnects (new epoch) and the
	// counter has jumped to 700 over the unobserved recycle.
	q.replies[Statements["bb:waits"]] = waitsRows(waitsFixture{"WRITELOG", 700, 20})
	later := now.Add(ConnMaxLifetime + DemotedInterval)
	tick(later) // reconnect -> rebaseline 700, no delta
	q.replies[Statements["bb:waits"]] = waitsRows(waitsFixture{"WRITELOG", 750, 22})
	tick(later.Add(DemotedInterval)) // delta 50 from the new baseline

	deltas := waitsDeltasFromLines(t, r.Dir)
	for _, d := range deltas {
		if d.DeltaMs == 500 {
			t.Fatalf("a delta of 500 spans the reconnect epoch: %#v", deltas)
		}
	}
	if len(deltas) != 2 || deltas[0].DeltaMs != 100 || deltas[1].DeltaMs != 50 {
		t.Fatalf("deltas = %#v, want {100, 50} (rebaseline at the new epoch)", deltas)
	}
}
