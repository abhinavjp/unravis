package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

// stubTraceCase serves fixed imports and events to the trace; every other Case
// method panics through the nil embedded interface, which these tests never call.
type stubTraceCase struct {
	casestore.Case
	imports []casestore.ImportRecord
	events  func(q casestore.EventQuery) []evidence.Event
	queries []casestore.EventQuery
}

func (s *stubTraceCase) Imports(context.Context) ([]casestore.ImportRecord, error) {
	return s.imports, nil
}

func (s *stubTraceCase) Events(_ context.Context, q casestore.EventQuery) ([]evidence.Event, error) {
	s.queries = append(s.queries, q)
	if s.events == nil {
		return nil, nil
	}
	return s.events(q), nil
}

func stubImport(name string, first, last time.Time) casestore.ImportRecord {
	rec := casestore.ImportRecord{Importer: name, Status: casestore.StatusOK, TZRule: "utc-field"}
	if !first.IsZero() {
		rec.Quality = evidence.SourceQuality{CoverageFirst: &first, CoverageLast: &last}
	}
	return rec
}

func tracePlans(t *testing.T, ids ...string) []tracePlan {
	t.Helper()
	repo := newTraceRepo()
	var plans []tracePlan
	for _, id := range ids {
		call, ok := calltrace.CallFromRecord(repo.recs[id].RequestRecord)
		if !ok {
			t.Fatalf("call %s not usable", id)
		}
		plans = append(plans, tracePlan{call: call, win: callWindow(call, 2*time.Minute, 2*time.Minute)})
	}
	return plans
}

func runStub(t *testing.T, ctx context.Context, plans []tracePlan, c casestore.Case) (*httptest.ResponseRecorder, traceView) {
	t.Helper()
	w := httptest.NewRecorder()
	(&handler{}).runCallTrace(ctx, w, plans, c)
	var v traceView
	if w.Code == http.StatusOK {
		decodeBody(t, w, &v)
	}
	return w, v
}

func laneOf(t *testing.T, lanes []calltrace.Lane, key string) calltrace.Lane {
	t.Helper()
	for _, l := range lanes {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no lane %s", key)
	return calltrace.Lane{}
}

func checkOf(t *testing.T, checks []calltrace.Check, id string) calltrace.Check {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %s", id)
	return calltrace.Check{}
}

func TestRunCallTraceReadsEachCallOnItsOwnWindowAndOnlyValidatedSources(t *testing.T) {
	cover := func(name string) casestore.ImportRecord {
		return stubImport(name, traceT0.Add(-time.Hour), traceT0.Add(time.Hour))
	}
	unchecked := cover("iis-w3c")
	unchecked.TZRule = ""
	stub := &stubTraceCase{imports: []casestore.ImportRecord{cover("sql-toy"), unchecked}}
	plans := tracePlans(t, "a", "far")
	w, _ := runStub(t, context.Background(), plans, stub)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	if len(stub.queries) != 2 {
		t.Fatalf("reads = %d, want one per call", len(stub.queries))
	}
	for i, q := range stub.queries {
		if len(q.Sources) != 1 || q.Sources[0] != "sql-toy" {
			t.Errorf("read %d sources = %v, want only the validated importer", i, q.Sources)
		}
		if q.Window.To.After(plans[i].win.To) {
			t.Errorf("read %d reaches past its own window: %v", i, q.Window.To)
		}
		if q.Window.From.Before(plans[i].win.From.Add(-callTraceLookback)) {
			t.Errorf("read %d starts before the lookback: %v", i, q.Window.From)
		}
	}
	if !stub.queries[1].Window.From.After(plans[0].win.To) {
		t.Errorf("second read overlaps the first call's window: %v", stub.queries[1].Window)
	}
}

func TestRunCallTraceNoValidatedSourceReadsNothing(t *testing.T) {
	rec := stubImport("sql-toy", traceT0.Add(-time.Hour), traceT0.Add(time.Hour))
	rec.TZRule = ""
	stub := &stubTraceCase{imports: []casestore.ImportRecord{rec}}
	w, v := runStub(t, context.Background(), tracePlans(t, "a"), stub)
	if w.Code != http.StatusOK || len(stub.queries) != 0 {
		t.Fatalf("status %d, reads %d; want 200 and no read", w.Code, len(stub.queries))
	}
	if got := laneOf(t, v.Traces[0].Lanes, calltrace.LaneSQL).State; got != calltrace.StateClockUnchecked {
		t.Errorf("sql lane = %s, want clock-unchecked", got)
	}
}

func TestRunCallTraceCappedReadIsIncompleteNotContradicted(t *testing.T) {
	dense := func(q casestore.EventQuery) []evidence.Event {
		if q.Window.From.After(traceT0.Add(time.Hour)) {
			return nil // the second call: nothing in its window
		}
		evs := make([]evidence.Event, casestore.MaxEventQuery)
		for i := range evs {
			at := q.Window.From.Add(time.Duration(i) * time.Millisecond)
			evs[i] = evidence.Event{EvidenceID: fmt.Sprintf("e%d", i), Source: "sql-toy", Kind: "wait", Start: at, End: at}
		}
		return evs
	}
	stub := &stubTraceCase{
		imports: []casestore.ImportRecord{stubImport("sql-toy", traceT0.Add(-time.Hour), traceT0.Add(6*time.Hour))},
		events:  dense,
	}
	// The first call's read is full of events from its lookback that end before
	// the call, so its lane holds no event although the read stopped early.
	w, v := runStub(t, context.Background(), tracePlans(t, "a", "far"), stub)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	if !v.Truncated {
		t.Errorf("truncated = false, want true when a read hit its cap")
	}
	far := laneOf(t, v.Traces[1].Lanes, calltrace.LaneSQL)
	if far.State != calltrace.StateNoneSeen {
		t.Errorf("uncapped call sql lane = %s, want none-seen", far.State)
	}
	first := laneOf(t, v.Traces[0].Lanes, calltrace.LaneSQL)
	if first.State != calltrace.StateIncomplete {
		t.Errorf("capped call sql lane = %s, want incomplete", first.State)
	}
	for _, l := range v.Traces[0].Lanes {
		if l.State == calltrace.StateNoneSeen {
			t.Errorf("capped call lane %s says none-seen", l.Key)
		}
	}
}

func TestRunCallTraceCappedEmptyLaneSaysIncomplete(t *testing.T) {
	// The cap is hit by events of the sql importer that do not reach the window
	// of the call (they start in the lookback and end at once), so the sql lane
	// holds no event, and a lane with no event must not read as "none seen".
	full := func(q casestore.EventQuery) []evidence.Event {
		evs := make([]evidence.Event, casestore.MaxEventQuery)
		at := q.Window.From
		for i := range evs {
			evs[i] = evidence.Event{EvidenceID: fmt.Sprintf("e%d", i), Source: "sql-toy", Kind: "wait", Start: at, End: at}
		}
		return evs
	}
	stub := &stubTraceCase{
		imports: []casestore.ImportRecord{
			stubImport("sql-toy", traceT0.Add(-time.Hour), traceT0.Add(time.Hour)),
			stubImport("iis-w3c", traceT0.Add(-time.Hour), traceT0.Add(time.Hour)),
		},
		events: full,
	}
	plans := tracePlans(t, "a", "b")
	w, v := runStub(t, context.Background(), plans, stub)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	if !v.Truncated {
		t.Fatalf("truncated = false")
	}
	for i, tr := range v.Traces {
		if got := laneOf(t, tr.Lanes, calltrace.LaneIIS).State; got != calltrace.StateIncomplete {
			t.Errorf("trace %d iis lane = %s, want incomplete", i, got)
		}
		ck := checkOf(t, tr.Checks, "iis-during-call")
		if ck.Status != calltrace.StatusNotEnoughData {
			t.Errorf("trace %d iis check = %s, want not-enough-data", i, ck.Status)
		}
	}
	cmp := checkOf(t, v.Compare, "iis-in-every-call")
	if cmp.Status != calltrace.StatusNotEnoughData {
		t.Errorf("compare iis = %s, want not-enough-data", cmp.Status)
	}
}

func TestRunCallTraceFilesOutsideTheWindowAreOutOfRange(t *testing.T) {
	long := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	stub := &stubTraceCase{imports: []casestore.ImportRecord{
		stubImport("sql-toy", long, long.Add(time.Hour)),
		stubImport("iis-w3c", traceT0.Add(-time.Hour), traceT0.Add(time.Hour)),
		stubImport("mongo-log", time.Time{}, time.Time{}), // no stated span
	}}
	w, v := runStub(t, context.Background(), tracePlans(t, "a"), stub)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	lanes := v.Traces[0].Lanes
	if got := laneOf(t, lanes, calltrace.LaneSQL).State; got != calltrace.StateOutOfRange {
		t.Errorf("sql lane = %s, want out-of-range", got)
	}
	if got := laneOf(t, lanes, calltrace.LaneIIS).State; got != calltrace.StateNoneSeen {
		t.Errorf("iis lane = %s, want none-seen", got)
	}
	if got := laneOf(t, lanes, calltrace.LaneMongo).State; got != calltrace.StateNoneSeen {
		t.Errorf("mongo lane without a stated span = %s, want none-seen", got)
	}
	ck := checkOf(t, v.Traces[0].Checks, "sql-during-call")
	if ck.Status != calltrace.StatusNotEnoughData {
		t.Errorf("sql check = %s, want not-enough-data", ck.Status)
	}
}

func TestCallTraceOddAuditNumbersNeverAnswer500(t *testing.T) {
	odd := func(mut func(r *audit.RequestRecord)) audit.RecordDetail {
		rec := traceRecord("odd", traceT0, time.Second)
		mut(&rec.RequestRecord)
		return rec
	}
	big := 1e300
	inf := math.Inf(1)
	y10000 := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	y9999 := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	y0 := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
	variants := map[string]audit.RecordDetail{
		"inf duration": odd(func(r *audit.RequestRecord) { r.DurationMs = &inf }),
		"huge audit time": odd(func(r *audit.RequestRecord) {
			r.AuditCreateTimeMs = &big
			r.ApiTimeMs = &big
			r.PayloadRecordTimeMs = &big
		}),
		"year 10000":      odd(func(r *audit.RequestRecord) { r.InitiatedAt = &y10000; r.CompletedAt = &y10000 }),
		"year 9999 start": odd(func(r *audit.RequestRecord) { r.InitiatedAt = &y9999; r.CompletedAt = &y9999 }),
		"year 0 start":    odd(func(r *audit.RequestRecord) { r.InitiatedAt = &y0; r.CompletedAt = &y0 }),
		"end year 10000":  odd(func(r *audit.RequestRecord) { r.CompletedAt = &y10000 }),
		"nine hour call":  odd(func(r *audit.RequestRecord) { e := traceT0.Add(9 * time.Hour); r.CompletedAt = &e }),
	}
	repo := newTraceRepo()
	for name, rec := range variants {
		repo.recs[name] = rec
		rec.ID = name
		repo.recs[name] = rec
	}
	traceRoutes(t, repo, func(t *testing.T, post tracePost, _ bool) {
		for name := range variants {
			res := post(callsBody(name) + `}`)
			if res.Code != http.StatusOK && res.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s = %d %s, want 200 or 422", name, res.Code, res.Body)
			}
		}
		if res := post(callsBody("year 10000") + `}`); res.Code != http.StatusUnprocessableEntity {
			t.Errorf("year 10000 start = %d, want 422", res.Code)
		}
	})
}

// gateProbeRepo checks, while the audit record is read, that no read share of
// the case is held: an erase that waits for readers must get through at once.
type gateProbeRepo struct {
	*traceRepo
	probe func() error
	err   error
}

func (r *gateProbeRepo) Record(ctx context.Context, in audit.RecordInput) (audit.RecordDetail, error) {
	if r.probe != nil {
		r.err = r.probe()
	}
	return r.traceRepo.Record(ctx, in)
}

func TestCaseCallTraceReadsAuditBeforeTakingTheCaseGate(t *testing.T) {
	requireRealTrace(t)
	repo := &gateProbeRepo{traceRepo: newTraceRepo()}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("trace")
	gates := &e.handler().gates
	probed := false
	repo.probe = func() error {
		probed = true
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		release, err := gates.acquireErase(ctx, casestore.CaseID(id))
		if err != nil {
			return err
		}
		release()
		return nil
	}
	res := e.json(http.MethodPost, "/api/cases/"+id+"/call-trace", callsBody("a", "b")+`}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d %s", res.Code, res.Body)
	}
	if !probed {
		t.Fatal("the audit record was never read")
	}
	if repo.err != nil {
		t.Errorf("the case read gate was held while the audit record was read: %v", repo.err)
	}
}

func TestCallTraceCanceledRequestIs400NotServerError(t *testing.T) {
	h := New(&fakeRepository{recordErr: context.Canceled}, testAssets(), testToken, false)
	res := request(h, http.MethodPost, "/api/call-trace", callsBody("a")+`}`, true)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("canceled audit read = %d %s, want 400", res.Code, res.Body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w, _ := runStub(t, ctx, tracePlans(t, "a"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("canceled run = %d %s, want 400", w.Code, w.Body)
	}
}
