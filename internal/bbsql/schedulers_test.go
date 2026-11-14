package bbsql

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Fixtures are keyed by the SQL column names the allowlisted statements
// SELECT (current_tasks, runnable_tasks, internal_pages, user_pages,
// resource_semaphore_id, grantee_count, waiter_count, forced_grant_count,
// timeout_count) because that is what decodeRows maps through columnKeys. A
// fixture keyed by the camelCase JSON keys would be rejected as an unknown
// column and would prove nothing about the samplers.

func schedulersRow(current, runnable any) Row {
	return Row{"current_tasks": current, "runnable_tasks": runnable}
}

func tempdbRow(internal, user any) Row {
	return Row{"internal_pages": internal, "user_pages": user}
}

func semaphoreRow(id, grantees, waiters, forced, timeouts any) Row {
	return Row{
		"resource_semaphore_id": id, "grantee_count": grantees, "waiter_count": waiters,
		"forced_grant_count": forced, "timeout_count": timeouts,
	}
}

// semFixture is one semaphore sighting in the units the DMV reports.
type semFixture struct {
	id, grantees, waiters, forced, timeouts int32
}

// i32p is a pointer to an int32 delta, for the nullable emitted Forced/Timeouts
// fields (a measured delta is present; an unreadable one is nil/omitted).
func i32p(v int32) *int32 { return &v }

// semKey is the baseline key of a fixture row that carried no pool id: the
// noPoolID sentinel beside the semaphore id (W11-02). The legacy fixtures below
// project no pool_id, so they exercise the unreadable-identity fallback path.
func semKey(id int32) semaphoreKey { return semaphoreKey{poolID: noPoolID, semID: id} }

// semDelta reads an emitted nullable delta, failing the test if it is missing
// when a value was expected.
func semDelta(t *testing.T, p *int32) int32 {
	t.Helper()
	if p == nil {
		t.Fatalf("delta is missing (nil), want a value")
	}
	return *p
}

func semRows(rows ...semFixture) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, semaphoreRow(int64(r.id), int64(r.grantees), int64(r.waiters),
			int64(r.forced), int64(r.timeouts)))
	}
	return out
}

func semPayload(t *testing.T, payload any) []Semaphores {
	t.Helper()
	got, ok := payload.([]Semaphores)
	if !ok {
		t.Fatalf("semaphores payload = %T %#v, want []Semaphores", payload, payload)
	}
	return got
}

// TestSchedulersSummed covers the packet's TestSchedulersSummed: the single
// summed row decodes field by field into one object payload, on the 15 s
// cadence.
func TestSchedulersSummed(t *testing.T) {
	var s schedulersSampler
	if s.ID() != "bb:schedulers" || s.Kind() != "schedulers" || s.Interval() != BaseInterval {
		t.Fatalf("shape = %s/%s/%v, want bb:schedulers/schedulers/%v",
			s.ID(), s.Kind(), s.Interval(), BaseInterval)
	}
	payload, truncated, err := s.Decode([]Row{schedulersRow(int64(96), int64(3))})
	if err != nil || truncated {
		t.Fatalf("decode: truncated=%v err=%v, want false/nil", truncated, err)
	}
	got, ok := payload.(Schedulers)
	if !ok {
		t.Fatalf("payload = %T %#v, want a Schedulers value (one object, not an array)", payload, payload)
	}
	if got.CurrentTasks != 96 || got.RunnableTasks != 3 {
		t.Errorf("decoded = %+v, want {CurrentTasks:96 RunnableTasks:3}", got)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasPrefix(strings.TrimLeft(string(data), " \t\n"), "{") {
		t.Errorf("schedulers payload JSON = %s, want an object", data)
	}
}

// TestSchedulersMissingNeverZero pins the SUM/NULL decision: bb:schedulers has
// no GROUP BY, so an instance with no VISIBLE ONLINE scheduler rows answers
// with ONE row of NULLs, not with zero rows. That row is reported as missing
// (nil payload, which the runner writes as "rows": null), never as a 0/0
// sample. Real zeros stay real zeros.
func TestSchedulersMissingNeverZero(t *testing.T) {
	cases := map[string][]Row{
		"nil rows":            nil,
		"no rows":             {},
		"both totals NULL":    {schedulersRow(nil, nil)},
		"current NULL":        {schedulersRow(nil, int64(2))},
		"runnable NULL":       {schedulersRow(int64(2), nil)},
		"row without columns": {{}},
	}
	for name, rows := range cases {
		var s schedulersSampler
		payload, truncated, err := s.Decode(rows)
		if err != nil {
			t.Errorf("%s: err = %v, want nil", name, err)
		}
		if payload != nil {
			t.Errorf("%s: payload = %#v, want nil (missing is never 0)", name, payload)
		}
		if len(rows) == 0 && truncated {
			t.Errorf("%s: truncated = true with no rows", name)
		}
	}
	var s schedulersSampler
	if payload, _, err := s.Decode([]Row{schedulersRow(int64(0), int64(0))}); err != nil ||
		payload != (Schedulers{CurrentTasks: 0, RunnableTasks: 0}) {
		t.Errorf("real zeros: payload=%#v err=%v, want the decoded Schedulers{0,0}", payload, err)
	}
}

func TestSchedulersCapOneTruncated(t *testing.T) {
	if CapSchedulers != 1 {
		t.Fatalf("CapSchedulers = %d, want 1 (a summed statement returns one row)", CapSchedulers)
	}
	var s schedulersSampler
	payload, truncated, err := s.Decode([]Row{schedulersRow(int64(8), int64(1))})
	if err != nil || truncated {
		t.Fatalf("exactly cap rows: truncated=%v err=%v, want false/nil", truncated, err)
	}
	if payload != (Schedulers{CurrentTasks: 8, RunnableTasks: 1}) {
		t.Fatalf("payload = %#v, want the single summed row", payload)
	}

	// cap+1 rows: the reader keeps the first one and marks the sample
	// truncated.
	payload, truncated, err = s.Decode([]Row{
		schedulersRow(int64(8), int64(1)),
		schedulersRow(int64(999), int64(999)),
	})
	if err != nil || !truncated {
		t.Fatalf("cap+1 rows: truncated=%v err=%v, want true/nil", truncated, err)
	}
	if payload != (Schedulers{CurrentTasks: 8, RunnableTasks: 1}) {
		t.Errorf("cap+1 payload = %#v, want the first kept row only", payload)
	}

	// A NULL in the row the reader already dropped is not this sample's
	// business: the kept row still decodes.
	payload, truncated, err = s.Decode([]Row{
		schedulersRow(int64(8), int64(1)),
		schedulersRow(nil, nil),
	})
	if err != nil || !truncated || payload == nil {
		t.Errorf("NULL past the cap: payload=%#v truncated=%v err=%v, want the kept row, true, nil",
			payload, truncated, err)
	}
}

// TestTempdbTotals covers the packet's TestTempdbTotals: the summed page
// totals decode, the payload is one object, the cadence is 60 s, and the
// totals are emitted as read rather than as a delta.
func TestTempdbTotals(t *testing.T) {
	var s tempdbSampler
	if s.ID() != "bb:tempdb" || s.Kind() != "tempdb" {
		t.Fatalf("shape = %s/%s, want bb:tempdb/tempdb", s.ID(), s.Kind())
	}
	if s.Interval() != DemotedInterval {
		t.Fatalf("tempdb interval = %v, want %v (BL-118: page totals every 60 s)",
			s.Interval(), DemotedInterval)
	}
	payload, truncated, err := s.Decode([]Row{tempdbRow(int64(131072), int64(4096))})
	if err != nil || truncated {
		t.Fatalf("decode: truncated=%v err=%v, want false/nil", truncated, err)
	}
	got, ok := payload.(Tempdb)
	if !ok {
		t.Fatalf("payload = %T %#v, want a Tempdb value", payload, payload)
	}
	if got.InternalPages != 131072 || got.UserPages != 4096 {
		t.Errorf("decoded = %+v, want {InternalPages:131072 UserPages:4096}", got)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasPrefix(strings.TrimLeft(string(data), " \t\n"), "{") {
		t.Errorf("tempdb payload JSON = %s, want an object not an array", data)
	}

	// Totals as read, not differenced: a repeat of the same sighting reports
	// the same pages, and a total that fell is still reported.
	for _, rows := range [][]Row{{tempdbRow(int64(131072), int64(4096))}, {tempdbRow(int64(9), int64(1))}} {
		if payload, _, err = s.Decode(rows); err != nil || payload == nil {
			t.Fatalf("cycle %v: payload=%#v err=%v, want the totals again (no delta state)", rows, payload, err)
		}
	}
}

func TestTempdbMissingNeverZero(t *testing.T) {
	cases := map[string][]Row{
		"nil rows":            nil,
		"no rows":             {},
		"both totals NULL":    {tempdbRow(nil, nil)},
		"internal NULL":       {tempdbRow(nil, int64(8))},
		"user NULL":           {tempdbRow(int64(8), nil)},
		"row without columns": {{}},
	}
	for name, rows := range cases {
		var s tempdbSampler
		payload, _, err := s.Decode(rows)
		if err != nil || payload != nil {
			t.Errorf("%s: payload=%#v err=%v, want nil,nil", name, payload, err)
		}
	}
	if CapTempdb != 1 {
		t.Fatalf("CapTempdb = %d, want 1", CapTempdb)
	}
	var s tempdbSampler
	payload, truncated, err := s.Decode([]Row{tempdbRow(int64(16), int64(2)), tempdbRow(nil, nil)})
	if err != nil || !truncated || payload != (Tempdb{InternalPages: 16, UserPages: 2}) {
		t.Errorf("cap+1 with a NULL tail: payload=%#v truncated=%v err=%v", payload, truncated, err)
	}
}

// TestSemaphoresFirstCycleIsBaselineOnly covers the packet's
// "first sample nil": the first sighting records the baseline and emits
// nothing, because a delta with nothing to subtract from is a fabricated
// number.
func TestSemaphoresFirstCycleIsBaselineOnly(t *testing.T) {
	s := &semaphoresSampler{}
	rows := semRows(semFixture{id: 0, grantees: 2, waiters: 1, forced: 40, timeouts: 7})
	payload, truncated, err := s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}
	if len(s.prev) != 1 {
		t.Fatalf("baseline not recorded: len(prev) = %d, want 1", len(s.prev))
	}
	if got := s.prev[semKey(0)]; got.forced != 40 || got.timeouts != 7 || got.grantees != 2 || got.waiters != 1 {
		t.Errorf("baseline = %+v, want the full sighting {2 1 40 7}", got)
	}
}

// TestSemaphoresDelta is the heart of BL-118 and the packet's
// TestSemaphoresDelta: the two cumulative counters are emitted as
// differences, the two instantaneous counts as read. A falling
// grantee_count is a grant finishing, not a counter reset.
func TestSemaphoresDelta(t *testing.T) {
	var shape semaphoresSampler
	if shape.ID() != "bb:semaphores" || shape.Kind() != "semaphores" || shape.Interval() != BaseInterval {
		t.Fatalf("shape = %s/%s/%v, want bb:semaphores/semaphores/%v",
			shape.ID(), shape.Kind(), shape.Interval(), BaseInterval)
	}
	if CapSemaphores != 16 {
		t.Fatalf("CapSemaphores = %d, want 16", CapSemaphores)
	}

	s := &semaphoresSampler{}
	base := semFixture{id: 0, grantees: 5, waiters: 0, forced: 100, timeouts: 20}
	if payload, _, err := s.Decode(semRows(base)); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}

	cur := semFixture{id: 0, grantees: 2, waiters: 4, forced: 105, timeouts: 22}
	payload, truncated, err := s.Decode(semRows(cur))
	if err != nil || truncated {
		t.Fatalf("delta cycle: truncated=%v err=%v", truncated, err)
	}
	got := semPayload(t, payload)
	if len(got) != 1 {
		t.Fatalf("payload = %+v, want exactly 1 row", got)
	}
	if got[0].ResourceSemaphoreID != 0 || got[0].Grantees != 2 || got[0].Waiters != 4 ||
		semDelta(t, got[0].Forced) != 5 || semDelta(t, got[0].Timeouts) != 2 {
		t.Fatalf("delta row = %+v, want gauges {2 4} and deltas {forced:5 timeouts:2}", got[0])
	}

	// An unchanged sighting still emits one row: the gauges describe the
	// grants in flight right now, and a zero forced delta is a measured zero
	// (a present pointer to 0), not a missing value.
	payload, _, err = s.Decode(semRows(cur))
	if err != nil {
		t.Fatalf("steady cycle: %v", err)
	}
	got = semPayload(t, payload)
	if len(got) != 1 || got[0].Grantees != 2 || got[0].Waiters != 4 ||
		semDelta(t, got[0].Forced) != 0 || semDelta(t, got[0].Timeouts) != 0 {
		t.Fatalf("steady cycle = %+v, want gauges {2 4} and present zero deltas", got)
	}
}

func TestSemaphoresUnseenIDSkipped(t *testing.T) {
	s := &semaphoresSampler{}
	known := semFixture{id: 0, grantees: 1, waiters: 0, forced: 10, timeouts: 1}
	if payload, _, err := s.Decode(semRows(known)); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}

	// id 1 is sighted for the first time: only id 0, which already has a
	// baseline, is emitted; id 1 becomes a baseline for the next cycle.
	payload, _, err := s.Decode(semRows(
		semFixture{id: 0, grantees: 1, waiters: 0, forced: 12, timeouts: 1},
		semFixture{id: 1, grantees: 0, waiters: 3, forced: 400, timeouts: 9},
	))
	if err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	got := semPayload(t, payload)
	if len(got) != 1 || got[0].ResourceSemaphoreID != 0 || semDelta(t, got[0].Forced) != 2 {
		t.Fatalf("cycle 2 = %+v, want only id 0 with forced delta 2", got)
	}
	if len(s.prev) != 2 {
		t.Fatalf("len(prev) = %d, want 2 baselines (the new id included)", len(s.prev))
	}

	payload, _, err = s.Decode(semRows(
		semFixture{id: 0, grantees: 1, waiters: 0, forced: 13, timeouts: 2},
		semFixture{id: 1, grantees: 0, waiters: 3, forced: 405, timeouts: 9},
	))
	if err != nil {
		t.Fatalf("cycle 3: %v", err)
	}
	if got := semPayload(t, payload); len(got) != 2 {
		t.Fatalf("cycle 3 = %+v, want both ids now baselined", got)
	}
}

// TestSemaphoresCounterReset covers the packet's "reset guarded": a cumulative
// counter going backwards replaces the baseline and emits nothing for that id,
// and the next cycle is measured from the new baseline.
func TestSemaphoresCounterReset(t *testing.T) {
	s := &semaphoresSampler{}
	base := semFixture{id: 0, grantees: 1, waiters: 1, forced: 500, timeouts: 40}
	if payload, _, err := s.Decode(semRows(base)); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}

	reset := semFixture{id: 0, grantees: 1, waiters: 1, forced: 3, timeouts: 0}
	payload, _, err := s.Decode(semRows(reset))
	if err != nil || payload != nil {
		t.Fatalf("reset cycle: payload=%v err=%v, want nil,nil (never a negative delta)", payload, err)
	}
	if got := s.prev[semKey(0)]; got.forced != 3 || got.timeouts != 0 {
		t.Fatalf("baseline after reset = %+v, want the new reading {forced:3 timeouts:0}", got)
	}

	after := semFixture{id: 0, grantees: 2, waiters: 0, forced: 8, timeouts: 1}
	payload, _, err = s.Decode(semRows(after))
	if err != nil {
		t.Fatalf("after reset: %v", err)
	}
	got := semPayload(t, payload)
	if len(got) != 1 || got[0].ResourceSemaphoreID != 0 || got[0].Grantees != 2 || got[0].Waiters != 0 ||
		semDelta(t, got[0].Forced) != 5 || semDelta(t, got[0].Timeouts) != 1 {
		t.Fatalf("after reset = %+v, want gauges {2 0} and deltas {forced:5 timeouts:1} from the new baseline", got)
	}

	// Either counter rolling back alone is enough: a fall in timeout_count by
	// itself is a reset too.
	t2 := &semaphoresSampler{}
	if payload, _, err := t2.Decode(semRows(semFixture{id: 0, forced: 5, timeouts: 9})); err != nil || payload != nil {
		t.Fatalf("second baseline: payload=%v err=%v", payload, err)
	}
	if payload, _, err := t2.Decode(semRows(semFixture{id: 0, forced: 6, timeouts: 1})); err != nil || payload != nil {
		t.Fatalf("timeout-only rollback: payload=%v err=%v, want nil,nil", payload, err)
	}
}

func TestSemaphoresPrevStaysBounded(t *testing.T) {
	s := &semaphoresSampler{}
	over := make([]semFixture, 0, CapSemaphores+1)
	for i := 0; i <= CapSemaphores; i++ {
		over = append(over, semFixture{id: int32(i), forced: int32(i)})
	}
	payload, truncated, err := s.Decode(semRows(over...))
	if err != nil || !truncated {
		t.Fatalf("cap+1 ids: truncated=%v err=%v, want truncated true", truncated, err)
	}
	if payload != nil {
		t.Error("first cycle must emit nothing")
	}
	if len(s.prev) != CapSemaphores {
		t.Fatalf("len(prev) = %d, want %d (rows past the cap are not baselined)", len(s.prev), CapSemaphores)
	}

	// Wholly different key sets from cycle to cycle must not grow the map.
	for cycle := 0; cycle < 4; cycle++ {
		shifted := make([]semFixture, 0, CapSemaphores)
		for i := 0; i < CapSemaphores; i++ {
			shifted = append(shifted, semFixture{id: int32(cycle*100 + i), forced: int32(i)})
		}
		if _, _, err := s.Decode(semRows(shifted...)); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(s.prev) > CapSemaphores {
			t.Fatalf("cycle %d: len(prev) = %d, want <= %d", cycle, len(s.prev), CapSemaphores)
		}
	}
}

// TestSemaphoresStaleIDDropped proves prev is REBUILT from this cycle's rows
// rather than updated in place: an id that vanished stops holding memory.
func TestSemaphoresStaleIDDropped(t *testing.T) {
	s := &semaphoresSampler{}
	if _, _, err := s.Decode(semRows(
		semFixture{id: 0, forced: 1}, semFixture{id: 1, forced: 1}, semFixture{id: 2, forced: 1},
	)); err != nil {
		t.Fatalf("baseline cycle: %v", err)
	}
	if len(s.prev) != 3 {
		t.Fatalf("len(prev) = %d, want 3", len(s.prev))
	}
	payload, _, err := s.Decode(semRows(semFixture{id: 1, forced: 2}))
	if err != nil {
		t.Fatalf("second cycle: %v", err)
	}
	if got := semPayload(t, payload); len(got) != 1 || got[0].ResourceSemaphoreID != 1 {
		t.Fatalf("second cycle = %+v, want only id 1", got)
	}
	if len(s.prev) != 1 {
		t.Fatalf("len(prev) = %d after ids 0 and 2 vanished, want 1", len(s.prev))
	}
	if _, ok := s.prev[semKey(0)]; ok {
		t.Error("stale id 0 kept a baseline: prev must be rebuilt from this cycle's rows")
	}
}

// TestSemaphoresEmptyReply proves both the empty contract and the bounded
// state on the way out: no rows means (nil, false, nil) and no leftover
// baselines.
func TestSemaphoresEmptyReply(t *testing.T) {
	s := &semaphoresSampler{}
	if _, _, err := s.Decode(semRows(semFixture{id: 0, forced: 4})); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for _, rows := range [][]Row{nil, {}} {
		payload, truncated, err := s.Decode(rows)
		if err != nil || truncated || payload != nil {
			t.Fatalf("empty rows %#v: payload=%v truncated=%v err=%v, want nil,false,nil",
				rows, payload, truncated, err)
		}
		if len(s.prev) != 0 {
			t.Fatalf("len(prev) = %d after an empty reply, want 0", len(s.prev))
		}
	}
}

// TestSemaphoresNULLCounterKeepsGauges is the schema-specific NULL decision
// (W11-04): a NULL cumulative counter is unreadable for THAT counter only. The
// readable gauges beside it are still baselined and emitted, and the missing
// counter is a nil (omitted) delta — never a faked zero and never a blanked row.
func TestSemaphoresNULLCounterKeepsGauges(t *testing.T) {
	s := &semaphoresSampler{}
	rows := []Row{
		semaphoreRow(int64(0), int64(1), int64(0), int64(10), int64(2)),
		semaphoreRow(int64(1), int64(0), int64(0), nil, int64(0)), // forced NULL
	}
	payload, truncated, err := s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}
	if len(s.prev) != 2 {
		t.Fatalf("len(prev) = %d, want 2: the gauges of the NULL-counter row are still baselined", len(s.prev))
	}
	if s.prev[semKey(1)].forcedOK {
		t.Error("the NULL forced counter was baselined as a readable zero")
	}
	if !s.prev[semKey(1)].timeoutsOK {
		t.Error("the readable timeouts counter beside the NULL forced counter was dropped")
	}

	// Next cycle: id0 deltas normally; id1's forced counter is now readable but
	// has no prior baseline (it was NULL), so forced is baseline-only (nil) while
	// its gauges and timeouts delta are emitted.
	payload, _, err = s.Decode([]Row{
		semaphoreRow(int64(0), int64(1), int64(0), int64(11), int64(2)),
		semaphoreRow(int64(1), int64(2), int64(1), int64(3), int64(0)),
	})
	if err != nil {
		t.Fatalf("recovered cycle: %v", err)
	}
	got := semPayload(t, payload)
	if len(got) != 2 {
		t.Fatalf("recovered payload = %+v, want both ids (gauges beside a missing delta)", got)
	}
	byID := map[int32]Semaphores{}
	for _, r := range got {
		byID[r.ResourceSemaphoreID] = r
	}
	if semDelta(t, byID[0].Forced) != 1 {
		t.Errorf("id0 forced delta = %v, want 1", byID[0].Forced)
	}
	if byID[1].Forced != nil {
		t.Errorf("id1 forced delta = %v, want nil (counter was NULL last cycle, baseline-only now)", *byID[1].Forced)
	}
	if byID[1].Grantees != 2 || byID[1].Waiters != 1 {
		t.Errorf("id1 gauges = {%d %d}, want {2 1} emitted beside the missing delta", byID[1].Grantees, byID[1].Waiters)
	}
	if semDelta(t, byID[1].Timeouts) != 0 {
		t.Errorf("id1 timeouts delta = %v, want a present measured 0", byID[1].Timeouts)
	}
}

// TestSemaphoresNoWrappedDelta proves a difference that cannot fit the fixed
// int32 field skips the row, never wrapped into a negative number. A negative
// cumulative counter is unreadable (missing), never a baseline.
func TestSemaphoresNoWrappedDelta(t *testing.T) {
	s := &semaphoresSampler{}
	// Baseline at 0; a jump to 2147483648 is a delta of 2147483648 (> MaxInt32).
	if payload, _, err := s.Decode(semRows(semFixture{id: 0, grantees: 1, waiters: 0, forced: 0, timeouts: 0})); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}
	payload, _, err := s.Decode([]Row{semaphoreRow(int64(0), int64(1), int64(0), int64(2147483648), int64(0))})
	if err != nil {
		t.Fatalf("jump cycle: %v", err)
	}
	if payload != nil {
		t.Fatalf("payload = %#v, want nil instead of a delta that cannot fit int32", payload)
	}
	// A negative cumulative counter is unreadable: no baseline, no delta.
	neg := &semaphoresSampler{}
	if _, _, err := neg.Decode([]Row{semaphoreRow(int64(0), int64(0), int64(0), int64(-1), int64(0))}); err != nil {
		t.Fatalf("negative baseline: %v", err)
	}
	if neg.prev[semKey(0)].forcedOK {
		t.Error("a negative cumulative counter was accepted as a readable baseline")
	}
}

// TestSemaphoresWideCumulativeDelta replaces the old narrow-decode failure test
// (W11-01): a long-lived instance whose cumulative counter exceeds MaxInt32 must
// still be sampled. The wide nullable int64 raw type reads the lifetime value,
// the difference is computed in the wide domain, and a small delta is emitted
// with the fixed int32 field — no decode error, no wrapped number, no fabricated
// zero. Covers the MaxInt32 crossing, a true reset, NULL and the maximum
// supported bigint value.
func TestSemaphoresWideCumulativeDelta(t *testing.T) {
	s := &semaphoresSampler{}
	// First sighting above MaxInt32: baseline only, NO error (the old code
	// returned errDecode here and could auto-off the whole stream).
	rows := []Row{semaphoreRow(int64(0), int64(1), int64(0), int64(2147483648), int64(2147483648))}
	payload, _, err := s.Decode(rows)
	if err != nil || payload != nil {
		t.Fatalf("wide baseline: payload=%v err=%v, want nil,nil (no narrow-decode failure)", payload, err)
	}
	if len(s.prev) != 1 || !s.prev[semKey(0)].forcedOK {
		t.Fatalf("wide baseline not recorded: prev=%+v", s.prev)
	}
	// 2147483648 -> 2147483649: a delta of 1 fits int32 and is emitted.
	payload, _, err = s.Decode([]Row{semaphoreRow(int64(0), int64(1), int64(0), int64(2147483649), int64(2147483649))})
	if err != nil {
		t.Fatalf("wide delta cycle: %v", err)
	}
	got := semPayload(t, payload)
	if len(got) != 1 || semDelta(t, got[0].Forced) != 1 || semDelta(t, got[0].Timeouts) != 1 {
		t.Fatalf("wide delta = %+v, want forced/timeouts deltas of 1 above the old int32 ceiling", got)
	}

	// A true reset (counter goes backwards) rebaselines and emits no delta.
	payload, _, err = s.Decode([]Row{semaphoreRow(int64(0), int64(1), int64(0), int64(5), int64(5))})
	if err != nil || payload != nil {
		t.Fatalf("reset cycle: payload=%v err=%v, want nil,nil", payload, err)
	}
	payload, _, err = s.Decode([]Row{semaphoreRow(int64(0), int64(1), int64(0), int64(9), int64(7))})
	if err != nil {
		t.Fatalf("after reset: %v", err)
	}
	got = semPayload(t, payload)
	if len(got) != 1 || semDelta(t, got[0].Forced) != 4 || semDelta(t, got[0].Timeouts) != 2 {
		t.Fatalf("after reset = %+v, want deltas measured from the reset baseline {4 2}", got)
	}

	// Maximum supported bigint value, then a small increase: still a small delta.
	maxS := &semaphoresSampler{}
	const maxI64 = int64(9223372036854775807)
	if _, _, err := maxS.Decode([]Row{semaphoreRow(int64(0), int64(0), int64(0), maxI64, maxI64)}); err != nil {
		t.Fatalf("max bigint baseline: %v", err)
	}
	// Same max value: a measured zero delta, present (not missing).
	payload, _, err = maxS.Decode([]Row{semaphoreRow(int64(0), int64(0), int64(0), maxI64, maxI64)})
	if err != nil {
		t.Fatalf("max bigint steady: %v", err)
	}
	got = semPayload(t, payload)
	if len(got) != 1 || semDelta(t, got[0].Forced) != 0 || semDelta(t, got[0].Timeouts) != 0 {
		t.Fatalf("max bigint steady = %+v, want present zero deltas", got)
	}
}

// semPoolRow is one semaphore sighting carrying the numeric pool_id (W11-02).
func semPoolRow(pool, id, grantees, waiters, forced, timeouts any) Row {
	return Row{
		"pool_id": pool, "resource_semaphore_id": id, "grantee_count": grantees,
		"waiter_count": waiters, "forced_grant_count": forced, "timeout_count": timeouts,
	}
}

// TestSemaphoresCompositePoolIdentity is the W11-02 fix: resource_semaphore_id
// is NONUNIQUE and repeats in every resource pool, so two pools both expose
// semaphore id 0. A single-id baseline key let one pool's row overwrite the
// other's, producing a wrong delta. The composite (pool_id, resource_semaphore_id)
// key keeps them independent, and the numeric pool id is emitted (it is an id,
// not a pool name — C9).
func TestSemaphoresCompositePoolIdentity(t *testing.T) {
	s := &semaphoresSampler{}
	base := []Row{
		semPoolRow(int64(2), int64(0), int64(1), int64(0), int64(100), int64(10)),
		semPoolRow(int64(3), int64(0), int64(2), int64(1), int64(500), int64(50)),
	}
	if p, _, err := s.Decode(base); err != nil || p != nil {
		t.Fatalf("baseline: p=%v err=%v, want nil,nil", p, err)
	}
	if len(s.prev) != 2 {
		t.Fatalf("len(prev)=%d, want 2 distinct composite baselines (same sem id, different pools)", len(s.prev))
	}
	// Each pool's counters advance by a DIFFERENT amount; neither baseline may
	// overwrite the other.
	cur := []Row{
		semPoolRow(int64(2), int64(0), int64(1), int64(0), int64(105), int64(12)),
		semPoolRow(int64(3), int64(0), int64(2), int64(1), int64(530), int64(57)),
	}
	p, _, err := s.Decode(cur)
	if err != nil {
		t.Fatalf("delta cycle: %v", err)
	}
	got := semPayload(t, p)
	if len(got) != 2 {
		t.Fatalf("payload=%+v, want both pools emitted", got)
	}
	byPool := map[int32]Semaphores{}
	for _, r := range got {
		if r.PoolID == nil {
			t.Fatalf("row %+v: poolId missing, want the numeric pool id", r)
		}
		byPool[*r.PoolID] = r
	}
	if d := semDelta(t, byPool[2].Forced); d != 5 {
		t.Errorf("pool 2 forced delta=%d, want 5", d)
	}
	if d := semDelta(t, byPool[2].Timeouts); d != 2 {
		t.Errorf("pool 2 timeouts delta=%d, want 2", d)
	}
	if d := semDelta(t, byPool[3].Forced); d != 30 {
		t.Errorf("pool 3 forced delta=%d, want 30 (not overwritten by pool 2)", d)
	}
	if d := semDelta(t, byPool[3].Timeouts); d != 7 {
		t.Errorf("pool 3 timeouts delta=%d, want 7", d)
	}
}

// TestSemaphoresPoolIDMissingNeverZero: a reply with no pool_id (a shape the
// fixed statement never produces) still decodes and deltas under the internal
// sentinel key, and poolId is OMITTED — never a fabricated 0.
func TestSemaphoresPoolIDMissingNeverZero(t *testing.T) {
	s := &semaphoresSampler{}
	if p, _, err := s.Decode(semRows(semFixture{id: 0, grantees: 1, forced: 10, timeouts: 2})); err != nil || p != nil {
		t.Fatalf("baseline: p=%v err=%v", p, err)
	}
	p, _, err := s.Decode(semRows(semFixture{id: 0, grantees: 1, forced: 13, timeouts: 3}))
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	got := semPayload(t, p)
	if len(got) != 1 || got[0].PoolID != nil {
		t.Fatalf("payload=%+v, want one row with poolId omitted (missing, never 0)", got)
	}
	if semDelta(t, got[0].Forced) != 3 || semDelta(t, got[0].Timeouts) != 1 {
		t.Fatalf("deltas=%+v, want {3 1}", got[0])
	}
	if _, ok := s.prev[semKey(0)]; !ok {
		t.Fatal("missing-pool row was not baselined under the sentinel key")
	}
}

// TestSemaphoresPoolIDUnreadableIsMissing: a NULL, negative or out-of-int32
// pool id is an unreadable identity. The gauges and deltas are still emitted
// (keyed by the sentinel), but poolId is omitted — never 0, never a wrapped id.
func TestSemaphoresPoolIDUnreadableIsMissing(t *testing.T) {
	for _, pool := range []any{nil, int64(-1), int64(2147483648)} {
		s := &semaphoresSampler{}
		if p, _, err := s.Decode([]Row{semPoolRow(pool, int64(0), int64(1), int64(0), int64(10), int64(2))}); err != nil || p != nil {
			t.Fatalf("pool=%v baseline: p=%v err=%v", pool, p, err)
		}
		p, _, err := s.Decode([]Row{semPoolRow(pool, int64(0), int64(1), int64(0), int64(12), int64(3))})
		if err != nil {
			t.Fatalf("pool=%v delta: %v", pool, err)
		}
		got := semPayload(t, p)
		if len(got) != 1 || got[0].PoolID != nil {
			t.Errorf("pool=%v: payload=%+v, want poolId omitted (unreadable identity is missing, never 0)", pool, got)
		}
		if semDelta(t, got[0].Forced) != 2 {
			t.Errorf("pool=%v: forced delta=%v, want 2 beside the missing identity", pool, got[0].Forced)
		}
	}
}

func TestSemaphoresRejectForbiddenColumns(t *testing.T) {
	// C9 banned INPUT columns are rejected as unknown; columns columnKeys maps
	// onto some other struct's field are rejected by the second gate
	// (DisallowUnknownFields) as a decode failure. Both fail closed and leave
	// the payload nil.
	cases := map[string]error{
		"name":          errUnknownColumn,
		"instance_name": errUnknownColumn,
		"text":          errUnknownColumn,
		"session_id":    errUnknownColumn,
		"login_name":    errUnknownColumn,
		"host_name":     errUnknownColumn,
		"program_name":  errUnknownColumn,
		"description":   errUnknownColumn,
		"scheduler_id":  errUnknownColumn,
		"database_id":   errDecode,
		"wait_type":     errDecode,
	}
	for col, want := range cases {
		s := &semaphoresSampler{}
		rows := []Row{semaphoreRow(int64(0), int64(1), int64(0), int64(1), int64(0))}
		rows[0][col] = "forbidden"
		payload, _, err := s.Decode(rows)
		if err != want || payload != nil {
			t.Errorf("semaphores + column %q: payload=%#v err=%v, want nil,%v", col, payload, err, want)
		}
		if len(s.prev) != 0 {
			t.Errorf("semaphores + column %q: rejected row left %d baselines behind", col, len(s.prev))
		}
	}
}

// TestSchedulersAndTempdbRejectForbiddenColumns is the packet's
// TestSchedNoForbiddenFields for the two summed statements.
func TestSchedulersAndTempdbRejectForbiddenColumns(t *testing.T) {
	cases := map[string]error{
		"name":           errUnknownColumn,
		"instance_name":  errUnknownColumn,
		"text":           errUnknownColumn,
		"session_id":     errUnknownColumn,
		"login_name":     errUnknownColumn,
		"host_name":      errUnknownColumn,
		"program_name":   errUnknownColumn,
		"description":    errUnknownColumn,
		"scheduler_id":   errUnknownColumn,
		"parent_node_id": errUnknownColumn,
		"status":         errUnknownColumn,
		"physical_name":  errUnknownColumn,
		"database_id":    errDecode,
		"file_id":        errDecode,
	}
	for col, want := range cases {
		var sched schedulersSampler
		srows := []Row{schedulersRow(int64(1), int64(0))}
		srows[0][col] = "forbidden"
		if payload, _, err := sched.Decode(srows); err != want || payload != nil {
			t.Errorf("schedulers + column %q: payload=%#v err=%v, want nil,%v", col, payload, err, want)
		}

		var tempdb tempdbSampler
		trows := []Row{tempdbRow(int64(1), int64(0))}
		trows[0][col] = "forbidden"
		if payload, _, err := tempdb.Decode(trows); err != want || payload != nil {
			t.Errorf("tempdb + column %q: payload=%#v err=%v, want nil,%v", col, payload, err, want)
		}
	}
}

// TestSchedulersSemaphoresTempdbPayloadJSONKeys asserts all three payloads
// marshal to nothing but the allowed numeric keys, and that every value is a
// number: no name, pool, instance, login or text key can appear whatever the
// rows carried.
func TestSchedulersSemaphoresTempdbPayloadJSONKeys(t *testing.T) {
	var sched schedulersSampler
	schedPayload, _, err := sched.Decode([]Row{schedulersRow(int64(4), int64(1))})
	if err != nil {
		t.Fatalf("schedulers: %v", err)
	}
	var tempdb tempdbSampler
	tempdbPayload, _, err := tempdb.Decode([]Row{tempdbRow(int64(64), int64(8))})
	if err != nil {
		t.Fatalf("tempdb: %v", err)
	}
	sem := &semaphoresSampler{}
	if _, _, err := sem.Decode(semRows(semFixture{id: 0, forced: 1})); err != nil {
		t.Fatalf("semaphores baseline: %v", err)
	}
	semsPayload, _, err := sem.Decode(semRows(semFixture{id: 0, grantees: 1, waiters: 2, forced: 3, timeouts: 4}))
	if err != nil {
		t.Fatalf("semaphores: %v", err)
	}

	// A composite-identity payload (W11-02): the numeric poolId is an allowed
	// output key because it is an id, NOT a pool name. Build it from rows that
	// carry pool_id so the key is actually emitted and checked.
	semPool := &semaphoresSampler{}
	poolRows := func(forced, timeouts int64) []Row {
		return []Row{{"pool_id": int64(2), "resource_semaphore_id": int64(0),
			"grantee_count": int64(1), "waiter_count": int64(0),
			"forced_grant_count": forced, "timeout_count": timeouts}}
	}
	if _, _, err := semPool.Decode(poolRows(1, 1)); err != nil {
		t.Fatalf("semaphores pool baseline: %v", err)
	}
	semsPoolPayload, _, err := semPool.Decode(poolRows(3, 4))
	if err != nil {
		t.Fatalf("semaphores pool: %v", err)
	}

	allowed := map[string]bool{
		"currentTasks": true, "runnableTasks": true,
		"internalPages": true, "userPages": true,
		"poolId": true, "resourceSemaphoreId": true, "grantees": true, "waiters": true,
		"forced": true, "timeouts": true,
	}
	// "pool" is deliberately NOT banned: the numeric poolId is a resource-pool
	// ID (C9 allows ids), not a pool name. A pool NAME would be an unknown key
	// and is rejected by the allowed gate above; every value must still be a
	// number, so no name/text can ride along under any key.
	banned := []string{"name", "login", "host", "program", "path", "text",
		"instance", "session", "description", "parent"}

	for _, tc := range []struct {
		label   string
		payload any
	}{
		{"schedulers", schedPayload},
		{"tempdb", tempdbPayload},
		{"semaphores", semsPayload},
		{"semaphores-pool", semsPoolPayload},
	} {
		data, err := json.Marshal(tc.payload)
		if err != nil {
			t.Fatalf("%s marshal: %v", tc.label, err)
		}
		var objs []map[string]any
		if data[0] == '[' {
			if err := json.Unmarshal(data, &objs); err != nil {
				t.Fatalf("%s unmarshal array: %v", tc.label, err)
			}
		} else {
			var one map[string]any
			if err := json.Unmarshal(data, &one); err != nil {
				t.Fatalf("%s unmarshal object: %v", tc.label, err)
			}
			objs = []map[string]any{one}
		}
		for _, obj := range objs {
			if len(obj) == 0 {
				t.Errorf("%s: payload carries no keys", tc.label)
			}
			for k, v := range obj {
				if !allowed[k] {
					t.Errorf("%s: JSON key %q is not an allowed numeric output key", tc.label, k)
				}
				if _, ok := v.(float64); !ok {
					t.Errorf("%s: key %q is %#v, want a number", tc.label, k, v)
				}
				lk := strings.ToLower(k)
				for _, b := range banned {
					if strings.Contains(lk, b) {
						t.Errorf("%s: key %q contains banned %q", tc.label, k, b)
					}
				}
			}
		}
	}
}

// TestSchedulersSemaphoresTempdbRegistered is the packet's TestSchedRegistered:
// the three kinds reach the global registry with the right ids, cadences and
// allowlisted statements, and Decode works through the Sampler interface.
func TestSchedulersSemaphoresTempdbRegistered(t *testing.T) {
	found := map[string]Sampler{}
	for _, s := range Samplers() {
		found[s.Kind()] = s
	}
	want := []struct {
		kind     string
		id       string
		interval time.Duration
	}{
		{"schedulers", "bb:schedulers", BaseInterval},
		{"semaphores", "bb:semaphores", BaseInterval},
		{"tempdb", "bb:tempdb", DemotedInterval},
	}
	for _, w := range want {
		s, ok := found[w.kind]
		if !ok {
			t.Fatalf("kind %q is not registered", w.kind)
		}
		if s.ID() != w.id {
			t.Errorf("%s ID = %q, want %q", w.kind, s.ID(), w.id)
		}
		if s.Interval() != w.interval {
			t.Errorf("%s Interval = %v, want %v", w.kind, s.Interval(), w.interval)
		}
		if text, ok := Statement(s.ID()); !ok || !IsAllowed(text) {
			t.Errorf("%s statement %q is not allowlisted", w.kind, s.ID())
		}
	}

	if payload, _, err := found["schedulers"].Decode([]Row{schedulersRow(int64(2), int64(1))}); err != nil || payload == nil {
		t.Errorf("registry schedulers: payload=%#v err=%v, want a row,nil", payload, err)
	}
	if payload, _, err := found["tempdb"].Decode([]Row{tempdbRow(int64(2), int64(1))}); err != nil || payload == nil {
		t.Errorf("registry tempdb: payload=%#v err=%v, want a row,nil", payload, err)
	}
	// The registry's semaphores singleton carries its own state, so this is
	// still its first cycle: it must emit nothing rather than a delta
	// fabricated from nothing.
	if payload, _, err := found["semaphores"].Decode(semRows(semFixture{id: 3})); err != nil || payload != nil {
		t.Errorf("registry semaphores first cycle: payload=%#v err=%v, want nil,nil", payload, err)
	}
}
