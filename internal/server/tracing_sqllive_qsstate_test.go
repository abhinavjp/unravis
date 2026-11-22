package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
)

// DATA-09: the Query Store state saved with a SQL day reaches the held SQL lane's reason, so "switched off",
// "read-only", "denied" and "data starts later" are not all shown as "no activity". All inputs are synthetic.

var qsStateWindowStart = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

// heldSQLQSCase is a case holding one saved SQL day with the given points and Query Store block (nil = none recorded).
func heldSQLQSCase(t *testing.T, series []dbhealth.LaneSeriesPoint, qs *dbhealth.LaneQueryStore) *healthyCase {
	t.Helper()
	inc := dbhealth.Window{Start: qsStateWindowStart, End: qsStateWindowStart.Add(time.Hour)}
	run := laneReportJSON(t, "sql", "AuditDb", inc, nil, nil)
	var rep dbhealth.Report
	if err := json.Unmarshal(run.Report, &rep); err != nil {
		t.Fatal(err)
	}
	rep.Lane.Series = series
	rep.Lane.QueryStore = qs
	enc, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	run.Report = enc
	run.At = qsStateWindowStart.Add(2 * time.Hour)
	return &healthyCase{stubTraceCase: &stubTraceCase{}, runs: []casestore.HealthRun{run}}
}

func heldSQLQSLane(t *testing.T, series []dbhealth.LaneSeriesPoint, qs *dbhealth.LaneQueryStore) (tracingLane, bool) {
	t.Helper()
	return heldSQLQSLaneSpan(t, series, qs, time.Hour)
}

// heldSQLQSLaneSpan is heldSQLQSLane for a Timeline window of the given length. The saved day read exactly the first hour (its incident window), so a
// longer window is not one it read.
func heldSQLQSLaneSpan(t *testing.T, series []dbhealth.LaneSeriesPoint, qs *dbhealth.LaneQueryStore, span time.Duration) (tracingLane, bool) {
	t.Helper()
	axis := newTracingAxis(qsStateWindowStart, qsStateWindowStart.Add(span))
	return tracingHeldSQLLane(context.Background(), heldSQLQSCase(t, series, qs), axis, time.Now())
}

func requireQSLane(t *testing.T, qs *dbhealth.LaneQueryStore, code, msgPart string) tracingLane {
	t.Helper()
	lane, ok := heldSQLQSLane(t, nil, qs)
	if !ok {
		t.Fatalf("state %q drew no lane; the reason was lost", qs.State)
	}
	if lane.Key != calltrace.LaneSQL || lane.State != calltrace.StateNoneSeen {
		t.Fatalf("lane %s %s, want sql none-seen", lane.Key, lane.State)
	}
	if lane.ReasonCode != code {
		t.Fatalf("reason code %q, want %q", lane.ReasonCode, code)
	}
	if !strings.Contains(lane.Reason, msgPart) {
		t.Fatalf("reason %q does not mention %q", lane.Reason, msgPart)
	}
	if lane.Scanned != 0 || len(lane.Events) != 0 {
		t.Fatalf("scanned %d events %d, want none", lane.Scanned, len(lane.Events))
	}
	if !lane.Complete || lane.Origin != tracingOriginCase || lane.Consistency != tracingConsStored {
		t.Fatalf("lane provenance %+v", lane)
	}
	if len(lane.Sources) != 1 || lane.Sources[0].SourceID != "sql-held" || !strings.HasSuffix(lane.Sources[0].Label, " · Query Store") {
		t.Fatalf("sources %+v", lane.Sources)
	}
	if !lane.AsOf.Equal(qsStateWindowStart.Add(2 * time.Hour)) {
		t.Fatalf("asOf %v", lane.AsOf)
	}
	if len(lane.Buckets) == 0 {
		t.Fatal("no buckets on the axis")
	}
	for _, b := range lane.Buckets {
		if b.Count != 0 || b.BusyMs != nil || b.AvgMs != nil {
			t.Fatalf("bucket carries figures: %+v", b)
		}
	}
	return lane
}

func TestHeldSQLLaneQSOffSaysOff(t *testing.T) {
	requireQSLane(t, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff}, tracingReasonQSOff, "switched off")
}

func TestHeldSQLLaneQSReadOnlySaysReadOnly(t *testing.T) {
	requireQSLane(t, &dbhealth.LaneQueryStore{State: dbhealth.QSStateReadOnly}, tracingReasonQSReadOnly, "read-only")
}

func TestHeldSQLLaneQSDeniedSaysDenied(t *testing.T) {
	requireQSLane(t, &dbhealth.LaneQueryStore{State: dbhealth.QSStateDenied}, tracingReasonQSDenied, "VIEW DATABASE STATE")
}

func TestHeldSQLLaneQSLateSaysLate(t *testing.T) {
	first := qsStateWindowStart.Add(30 * time.Minute)
	lane := requireQSLane(t, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOn, FirstData: &first}, tracingReasonQSLate, first.UTC().Format(time.RFC3339))
	if !strings.Contains(lane.Reason, "unknown, not zero") {
		t.Fatalf("late reason must say the earlier time is unknown: %q", lane.Reason)
	}
}

// A first-data time at or before the window start explains nothing: the honest answer stays "no activity" (false).
func TestHeldSQLLaneQSFirstDataNotAfterWindowStartStillReturnsFalse(t *testing.T) {
	for _, first := range []time.Time{qsStateWindowStart, qsStateWindowStart.Add(-time.Hour)} {
		f := first
		if lane, ok := heldSQLQSLane(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOn, FirstData: &f}); ok {
			t.Fatalf("first data %v drew a lane: %+v", first, lane)
		}
	}
}

// Query Store on (or unknown, or not recorded) with no activity keeps the old contract: no lane is drawn.
func TestHeldSQLLaneQSOnEmptyStillReturnsFalse(t *testing.T) {
	for name, qs := range map[string]*dbhealth.LaneQueryStore{
		"on":      {State: dbhealth.QSStateOn},
		"unknown": {State: dbhealth.QSStateUnknown},
		"empty":   {},
		"nil":     nil,
	} {
		if lane, ok := heldSQLQSLane(t, nil, qs); ok {
			t.Fatalf("%s: drew a lane with no activity: %+v", name, lane)
		}
	}
}

// Activity in the window wins: the saved state is not stamped over real figures.
func TestHeldSQLLaneQSStateNeverReplacesTheSentenceWhenActivityExists(t *testing.T) {
	t0 := qsStateWindowStart.Unix()
	series := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 40, AvgMs: 10, MaxMs: 30}}
	first := qsStateWindowStart.Add(30 * time.Minute)
	for _, st := range []string{dbhealth.QSStateOff, dbhealth.QSStateReadOnly, dbhealth.QSStateDenied, dbhealth.QSStateOn} {
		lane, ok := heldSQLQSLane(t, series, &dbhealth.LaneQueryStore{State: st, FirstData: &first})
		if !ok {
			t.Fatalf("state %s: no lane drawn from real activity", st)
		}
		if lane.State != calltrace.StateEvents || lane.ReasonCode != tracingReasonHeldSQL || lane.Reason != tracingHeldSQLMsg || lane.Scanned != 40 {
			t.Fatalf("state %s: lane %s %s scanned %d, want events held-sql 40", st, lane.State, lane.ReasonCode, lane.Scanned)
		}
		// The sentence is the plain one for every state; only an off day carries a note, and the note has its own field.
		if (lane.Note != "") != (st == dbhealth.QSStateOff) {
			t.Fatalf("state %s: note %q", st, lane.Note)
		}
	}
}

// Query Store off with blocks in the window: the lane draws them, keeps the held-sql code and its plain sentence, and carries the off note in its own
// field (the page shows it above the bars).
func TestHeldSQLLaneQSOffWithSeriesDrawsBlocksAndSaysOff(t *testing.T) {
	t0 := qsStateWindowStart.Unix()
	series := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 40, AvgMs: 10, MaxMs: 30}, {T: t0 + 900, Execs: 20, AvgMs: 12, MaxMs: 40}}
	first, last := qsStateWindowStart.Add(-48*time.Hour), qsStateWindowStart.Add(45*time.Minute)
	lane, ok := heldSQLQSLane(t, series, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff, FirstData: &first, LastData: &last, IntervalMinutes: 15})
	if !ok || lane.State != calltrace.StateEvents || lane.ReasonCode != tracingReasonHeldSQL || lane.Scanned != 60 || lane.Reason != tracingHeldSQLMsg {
		t.Fatalf("ok %v lane %s %s scanned %d reason %q", ok, lane.State, lane.ReasonCode, lane.Scanned, lane.Reason)
	}
	for _, part := range []string{"Query Store was off when this saved day was read", last.Format(time.RFC3339)} {
		if !strings.Contains(lane.Note, part) {
			t.Errorf("note %q lacks %q", lane.Note, part)
		}
	}
	if strings.Contains(lane.Note, "saved no history") || strings.Contains(lane.Note, "may have changed") {
		t.Errorf("note %q", lane.Note)
	}
	drawn := 0
	for _, b := range lane.Buckets {
		if b.BusyMs != nil && b.Count > 0 {
			drawn++
		}
	}
	if drawn == 0 {
		t.Error("no bucket carries figures")
	}
	// Without a newest block time the note still says the blocks were kept, and names no time.
	lane, _ = heldSQLQSLane(t, series, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff})
	if lane.Note != tracingQSOffWithSeriesNote {
		t.Errorf("note %q", lane.Note)
	}
	// A day saved before the window ended says so, as the other state sentences do.
	c := heldSQLQSCase(t, series, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff, LastData: &last})
	saved := qsStateWindowStart.Add(30 * time.Minute)
	c.runs[0].At = saved
	lane, ok = tracingHeldSQLLane(context.Background(), c, newTracingAxis(qsStateWindowStart, qsStateWindowStart.Add(time.Hour)), time.Now())
	if !ok || !strings.Contains(lane.Note, "Query Store was off when this saved day was read") || !strings.Contains(lane.Note, saved.Format(time.RFC3339)) || !strings.Contains(lane.Note, "may have changed") {
		t.Errorf("ok %v note %q", ok, lane.Note)
	}
	// An on day, and an off day none of whose blocks fall in the window of an older source day, carry no note.
	if lane, _ := heldSQLQSLane(t, series, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOn}); lane.Note != "" {
		t.Errorf("an on day carries a note: %q", lane.Note)
	}
}

// The note is only for blocks the newest day drew: when only an older day of the source (read while Query Store was on) has blocks in the window, the
// newest off day adds nothing to say about them.
func TestHeldSQLLaneQSOffNoteOnlyWhenTheNewestDayDrewBlocks(t *testing.T) {
	inWindow := []dbhealth.LaneSeriesPoint{{T: qsStateWindowStart.Unix(), Execs: 40, AvgMs: 10, MaxMs: 30}}
	older := heldSQLQSCase(t, inWindow, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOn})
	outside := []dbhealth.LaneSeriesPoint{{T: qsStateWindowStart.Add(-30 * time.Hour).Unix(), Execs: 5, AvgMs: 1}}
	newer := heldSQLQSCase(t, outside, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff})
	n := newer.runs[0]
	n.ID = older.runs[0].ID + 1
	n.At = older.runs[0].At.Add(time.Hour)
	older.runs = append([]casestore.HealthRun{n}, older.runs...)
	lane, ok := tracingHeldSQLLane(context.Background(), older, newTracingAxis(qsStateWindowStart, qsStateWindowStart.Add(time.Hour)), time.Now())
	if !ok || lane.State != calltrace.StateEvents || lane.Scanned != 40 || lane.Note != "" {
		t.Fatalf("ok %v lane %s scanned %d note %q", ok, lane.State, lane.Scanned, lane.Note)
	}
}

// Query Store off with a kept range but no block in the window: the lane names the range. "None of it is for this window" is said only when the
// window lies outside the range. Inside the range: a window the saved day read cleanly says it was read and holds no block with activity; a window
// it did not read says the case holds no block for it; a failed read says that is not known.
func TestHeldSQLLaneQSOffOutsideNamesRange(t *testing.T) {
	win := func(first, last time.Time, interval int, unread bool, span time.Duration) string {
		lane, ok := heldSQLQSLaneSpan(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff, FirstData: &first, LastData: &last, IntervalMinutes: interval, HistoryUnread: unread}, span)
		if !ok || lane.ReasonCode != tracingReasonQSOff || lane.State != calltrace.StateNoneSeen {
			t.Fatalf("ok %v lane %s %s", ok, lane.State, lane.ReasonCode)
		}
		for _, part := range []string{first.Format(time.RFC3339), last.Format(time.RFC3339)} {
			if !strings.Contains(lane.Reason, part) {
				t.Errorf("reason %q lacks %q", lane.Reason, part)
			}
		}
		if strings.Contains(lane.Reason, "saved no history") {
			t.Errorf("a kept range was called no history: %q", lane.Reason)
		}
		return lane.Reason
	}
	const outside, notRead, readEmpty, failed = "none of it is for this window", "holds no saved block for it", "This window was read, and no block with activity falls in it", "reading its blocks for this day failed"
	h := func(n int) time.Time { return qsStateWindowStart.Add(time.Duration(n) * time.Hour) }
	hour, wide := time.Hour, 2*time.Hour                                          // the saved day read the first hour only
	if r := win(h(-72), h(-48), 15, false, hour); !strings.Contains(r, outside) { // the newest block ended long before the window
		t.Errorf("after the range: %q", r)
	}
	if r := win(h(2), h(5), 15, false, wide); !strings.Contains(r, outside) { // the oldest block starts after the window ended
		t.Errorf("before the range: %q", r)
	}
	// The range covers a window the day read: it was read, and nothing with activity is in it. Reading again would give the same answer.
	if r := win(h(-48), h(5), 15, false, hour); !strings.Contains(r, readEmpty) || strings.Contains(r, outside) || strings.Contains(r, "Read the database") {
		t.Errorf("read and empty: %q", r)
	}
	// The same range over a window the day did not read: the case holds no saved block for it.
	if r := win(h(-48), h(5), 15, false, wide); !strings.Contains(r, notRead) || strings.Contains(r, readEmpty) {
		t.Errorf("not read: %q", r)
	}
	// The newest block, 15 minutes long, still runs into the window: not outside. Exactly at its end is outside.
	if r := win(h(-48), qsStateWindowStart.Add(-14*time.Minute), 15, false, wide); !strings.Contains(r, notRead) {
		t.Errorf("block runs into the window: %q", r)
	}
	if r := win(h(-48), qsStateWindowStart.Add(-15*time.Minute), 15, false, hour); !strings.Contains(r, outside) {
		t.Errorf("block ends where the window starts: %q", r)
	}
	// Block length unknown: the end of the range cannot be placed, so a later window is not outside.
	if r := win(h(-72), h(-48), 0, false, wide); !strings.Contains(r, notRead) || strings.Contains(r, outside) {
		t.Errorf("unknown block length: %q", r)
	}
	// A later read failed after the range was read: the range is named and the words say whether blocks exist for the window is not known.
	// It is still outside when the window lies outside the range.
	if r := win(h(-48), h(5), 15, true, hour); !strings.Contains(r, failed) || strings.Contains(r, "could not read the history it may have kept") || strings.Contains(r, readEmpty) {
		t.Errorf("failed read with a range: %q", r)
	}
	if r := win(h(-72), h(-48), 15, true, hour); !strings.Contains(r, outside) {
		t.Errorf("failed read, window outside the range: %q", r)
	}
}

// A failed or refused read of the history with no range is not "saved no history": the lane says it could not read, so whether it saved any is not known.
func TestHeldSQLLaneQSOffUnreadHistoryNeverSaysNoHistory(t *testing.T) {
	lane, ok := heldSQLQSLane(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff, HistoryUnread: true})
	if !ok || lane.ReasonCode != tracingReasonQSOff || lane.State != calltrace.StateNoneSeen {
		t.Fatalf("ok %v lane %s %s", ok, lane.State, lane.ReasonCode)
	}
	if !strings.Contains(lane.Reason, "could not read the history") || !strings.Contains(lane.Reason, "not known") {
		t.Errorf("reason %q", lane.Reason)
	}
	for _, bad := range []string{"saved no history", "none of it is for this window"} {
		if strings.Contains(lane.Reason, bad) {
			t.Errorf("reason %q says %q", lane.Reason, bad)
		}
	}
	// The confirmed "none kept" case keeps today's sentence.
	if lane, _ := heldSQLQSLane(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff}); !strings.Contains(lane.Reason, "saved no history") {
		t.Errorf("reason %q", lane.Reason)
	}
}

// A saved day whose figures all fall outside the window carries its state too: nothing in the window, and the state says why.
func TestHeldSQLLaneQSStateWithSeriesOutsideWindow(t *testing.T) {
	out := []dbhealth.LaneSeriesPoint{{T: qsStateWindowStart.Add(-48 * time.Hour).Unix(), Execs: 9, AvgMs: 1}}
	lane, ok := heldSQLQSLane(t, out, &dbhealth.LaneQueryStore{State: dbhealth.QSStateReadOnly})
	if !ok || lane.ReasonCode != tracingReasonQSReadOnly || lane.State != calltrace.StateNoneSeen {
		t.Fatalf("ok %v lane %s %s", ok, lane.State, lane.ReasonCode)
	}
}

// The reasons are plain words for a first-time reader and the lane keeps working through JSON.
func TestHeldSQLLaneQSLaneEncodesReason(t *testing.T) {
	lane := requireQSLane(t, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff}, tracingReasonQSOff, "switched off")
	raw, err := json.Marshal(lane)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reasonCode":"qs-off"`) || !strings.Contains(string(raw), `"state":"none-seen"`) {
		t.Fatalf("lane JSON: %.300s", raw)
	}
}

// A held lane that only carries a Query Store state has no bars: imported slow-statement events are never layered
// onto it (the lane would turn none-seen and hide the imported events). The imported lane is kept as it is.
func TestLayerSQLLaneIgnoresQueryStoreStateLane(t *testing.T) {
	held, ok := heldSQLQSLane(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff})
	if !ok {
		t.Fatal("no state lane")
	}
	imported := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateEvents, Complete: true,
		Sources: []tracingRef{{SourceID: "f1", Label: "slow.xel"}},
		Buckets: []tracingBucket{{Count: 7}}, Events: []tracingEvent{{ID: "e1"}}}
	got, did := layerSQLLane(imported, held)
	if did || got.State != calltrace.StateEvents || len(got.Events) != 1 {
		t.Fatalf("layered onto a state lane: did %v state %s events %d", did, got.State, len(got.Events))
	}
}

// BL-039: a Query Store state reason says it comes from the saved database check, so it is not read as being about
// an imported SQL file.
func TestHeldSQLLaneQSReasonNamesTheSavedCheck(t *testing.T) {
	lane := requireQSLane(t, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff}, tracingReasonQSOff, "switched off")
	if !strings.HasPrefix(lane.Reason, tracingQSCheckLead) {
		t.Fatalf("reason %q does not say it comes from the saved database check", lane.Reason)
	}
	if strings.Contains(lane.Reason, "before this window ended") {
		t.Fatalf("a check saved after the window was called stale: %q", lane.Reason)
	}
}

// BL-039: a state saved before the window ended is older than part of the window, so the lane says when it was saved
// and that it may have changed.
func TestHeldSQLLaneQSStaleStateSaysWhenItWasSaved(t *testing.T) {
	c := heldSQLQSCase(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateReadOnly})
	saved := qsStateWindowStart.Add(30 * time.Minute)
	c.runs[0].At = saved
	axis := newTracingAxis(qsStateWindowStart, qsStateWindowStart.Add(time.Hour))
	lane, ok := tracingHeldSQLLane(context.Background(), c, axis, time.Now())
	if !ok || lane.ReasonCode != tracingReasonQSReadOnly {
		t.Fatalf("ok %v code %q", ok, lane.ReasonCode)
	}
	if !strings.Contains(lane.Reason, saved.Format(time.RFC3339)) || !strings.Contains(lane.Reason, "may have changed") {
		t.Fatalf("stale state not flagged: %q", lane.Reason)
	}
}

// BL-039: when the newest source's saved days hold no figures in the window, only that source's state explains it;
// a newer day of another server is not used.
func TestHeldSQLLaneQSStateComesFromTheSeriesSource(t *testing.T) {
	c := heldSQLQSCase(t, []dbhealth.LaneSeriesPoint{{T: qsStateWindowStart.Add(-48 * time.Hour).Unix(), Execs: 5, AvgMs: 2}}, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOn})
	other := heldSQLQSCase(t, nil, &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff})
	o := other.runs[0]
	o.Key = "sql|Other|OtherDb|x|1|2"
	o.ID = c.runs[0].ID + 1
	o.At = c.runs[0].At.Add(time.Hour)
	c.runs = append([]casestore.HealthRun{o}, c.runs...)
	axis := newTracingAxis(qsStateWindowStart, qsStateWindowStart.Add(time.Hour))
	if lane, ok := tracingHeldSQLLane(context.Background(), c, axis, time.Now()); ok {
		t.Fatalf("another server's Query Store state explained this lane: %s %q", lane.ReasonCode, lane.Reason)
	}
}
