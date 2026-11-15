package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/sqlserver"
)

// apiSeenFakeRepo is an audit.Repository whose listing is empty (so the tracing
// answer has no audit lane) and an audit.SQLBusyReader that returns a canned
// result, counts its calls and records the last input. The embedded nil
// Repository panics on any method the tracing route does not use.
type apiSeenFakeRepo struct {
	audit.Repository
	result  audit.SQLBusyResult
	err     error
	calls   int
	lastIn  audit.SQLBusyInput
	onCall  func()
	sources []audit.Source // listed audit sources; nil keeps the empty listing
}

func (f *apiSeenFakeRepo) Sources(context.Context) ([]audit.Source, error) {
	if f.sources == nil {
		return []audit.Source{}, nil
	}
	return f.sources, nil
}

func (f *apiSeenFakeRepo) SQLBusy(_ context.Context, in audit.SQLBusyInput) (audit.SQLBusyResult, error) {
	f.calls++
	f.lastIn = in
	if f.onCall != nil {
		f.onCall()
	}
	if f.err != nil {
		return audit.SQLBusyResult{}, f.err
	}
	return f.result, nil
}

// apiSeenEnv is a memory case store with repo as the audit repository.
func apiSeenEnv(t *testing.T, repo audit.Repository) (*caseEnv, string) {
	t.Helper()
	store, _ := tagStores(t)["memory"]()
	e := tagEnv(t, store, repo)
	return e, e.newCase("tracing")
}

// apiSeenDemoEnv is a --demo server backed by the demo audit repository (which
// implements SQLBusyReader). Like demoLaneEnv it forbids any real SQL open/test.
func apiSeenDemoEnv(t *testing.T, now time.Time) (*caseEnv, string) {
	t.Helper()
	oldTest, oldOpen := testSQLFn, openSQLFn
	t.Cleanup(func() { testSQLFn, openSQLFn = oldTest, oldOpen })
	testSQLFn = func(context.Context, dbhealth.SQLTarget) (dbhealth.TestResult, error) {
		t.Error("the demo opened a real SQL Server test")
		return dbhealth.TestResult{}, nil
	}
	openSQLFn = func(context.Context, dbhealth.SQLTarget) (healthQueryer, error) {
		t.Error("the demo opened a real SQL Server connection")
		return nil, nil
	}
	st := casestore.NewMemory()
	e := &caseEnv{t: t, store: st, h: New(audit.NewDemo(now), testAssets(), testToken, true,
		WithCases(st, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})))}
	return e, e.newCase("demo api-seen")
}

func apiSeenBin(at time.Time, busyMs float64, calls int64) audit.SQLBusyBin {
	return audit.SQLBusyBin{Start: at, BusyMs: busyMs, Calls: calls}
}

// apiSeenSQLBusyInput is the one bounded call the axis window asks for.
func apiSeenSQLBusyInput(axis tracingAxis) audit.SQLBusyInput {
	return audit.SQLBusyInput{Start: axis.Start, End: axis.End, BucketSeconds: int(axis.WidthMs / 1000)}
}

// ---- unit: apiSeenBuckets maps the returned bins ----

func TestApiSeenBucketsMapsRecordedBins(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{
		apiSeenBin(tracingLaneT0, 1200, 3),
		apiSeenBin(tracingLaneT0.Add(5*time.Minute), 800, 2),
	}}}
	cells, status, err := apiSeenBuckets(context.Background(), f, nil, axis)
	if err != nil || status != apiSeenRecorded {
		t.Fatalf("status %v err %v", status, err)
	}
	if f.calls != 1 || !reflect.DeepEqual(f.lastIn, apiSeenSQLBusyInput(axis)) {
		t.Fatalf("calls %d input %+v, want one call with the axis window", f.calls, f.lastIn)
	}
	if len(cells) != 2 {
		t.Fatalf("cells %d, want 2", len(cells))
	}
	b0 := cells[0]
	if b0 == nil || b0.BusyMs == nil || *b0.BusyMs != 1200 || b0.Count != 3 || b0.TimedCount != 3 || !b0.Complete {
		t.Fatalf("cell 0 %+v", b0)
	}
	if !b0.Start.Equal(tracingLaneT0) || !b0.End.Equal(tracingLaneT0.Add(time.Minute)) {
		t.Fatalf("cell 0 bounds %v..%v", b0.Start, b0.End)
	}
	// No maximum or average is invented from a per-step timed total.
	if b0.MaxMs != nil || b0.AvgMs != nil || b0.P50Ms != nil || b0.P95Ms != nil {
		t.Fatalf("cell 0 invented figures: %+v", b0)
	}
	b5 := cells[5]
	if b5 == nil || b5.BusyMs == nil || *b5.BusyMs != 800 || b5.Count != 2 || b5.TimedCount != 2 {
		t.Fatalf("cell 5 %+v", b5)
	}
}

func TestApiSeenBucketsSumsBinsInOneCell(t *testing.T) {
	// A 3 h window uses 5-minute cells; two steps inside one cell are added.
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(3*time.Hour))
	if axis.WidthMs != 300000 {
		t.Fatalf("axis width %d, want 300000", axis.WidthMs)
	}
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{
		apiSeenBin(tracingLaneT0, 1000, 2),
		apiSeenBin(tracingLaneT0.Add(time.Minute), 500, 1),
		apiSeenBin(tracingLaneT0.Add(5*time.Minute), 700, 3),
	}}}
	cells, status, err := apiSeenBuckets(context.Background(), f, nil, axis)
	if err != nil || status != apiSeenRecorded {
		t.Fatalf("status %v err %v", status, err)
	}
	b0 := cells[0]
	if b0 == nil || b0.BusyMs == nil || *b0.BusyMs != 1500 || b0.Count != 3 || b0.TimedCount != 3 {
		t.Fatalf("cell 0 %+v, want summed busyMs 1500 and 3 calls", b0)
	}
	b1 := cells[1]
	if b1 == nil || b1.BusyMs == nil || *b1.BusyMs != 700 || b1.Count != 3 {
		t.Fatalf("cell 1 %+v", b1)
	}
}

func TestApiSeenBucketsNotRecordedAndNilReader(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: false}}
	if cells, status, err := apiSeenBuckets(context.Background(), f, nil, axis); err != nil || status != apiSeenNotRecorded || len(cells) != 0 {
		t.Fatalf("not recorded: cells %d status %v err %v", len(cells), status, err)
	}
	// A nil reader is not-recorded, never a panic or an error.
	if cells, status, err := apiSeenBuckets(context.Background(), nil, nil, axis); err != nil || status != apiSeenNotRecorded || cells != nil {
		t.Fatalf("nil reader: cells %v status %v err %v", cells, status, err)
	}
}

func TestApiSeenBucketsSkipsWindowOneCallCannotRead(t *testing.T) {
	// A window over 24 h uses a coarser axis step outside the allowed set, so the
	// fallback reports an unsupported window (not "not recorded") and leaves the
	// lane unread rather than failing the request: no SQLBusy call, no error.
	long := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(48*time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1, 1)}}}
	cells, status, err := apiSeenBuckets(context.Background(), f, nil, long)
	if err != nil || status != apiSeenUnsupported || len(cells) != 0 || f.calls != 0 {
		t.Fatalf("long window: cells %d status %v err %v calls %d", len(cells), status, err, f.calls)
	}
}

// TestApiSeenUnsupportedWindowStampsBroadScan proves an unsupported window is no
// longer a silent empty lane: apiSeenBuckets reports the unsupported status with
// no SQLBusy call and no error, and the lane stamps the EXISTING actionable
// broad-scan reason, incomplete — mirroring the audit lane — without claiming a
// live read or filling any busy data.
func TestApiSeenUnsupportedWindowStampsBroadScan(t *testing.T) {
	// A 48 h window uses an hourly axis step outside the frozen SQLBusy contract
	// (60/300/600/900 s), so it cannot be read in one bounded call.
	long := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(48*time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	cells, status, err := apiSeenBuckets(context.Background(), f, nil, long)
	if err != nil || status != apiSeenUnsupported || len(cells) != 0 || f.calls != 0 {
		t.Fatalf("unsupported window: cells %d status %v err %v calls %d", len(cells), status, err, f.calls)
	}
	empty := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true, Buckets: []tracingBucket{}, Events: []tracingEvent{}}
	got, err := tracingAPISseenLane(context.Background(), f, nil, long, empty, auditClock(tracingLaneT0))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReasonCode != auditCodeBroadScan || got.Complete || got.State != calltrace.StateIncomplete {
		t.Fatalf("lane %s complete %v state %s, want broad-scan incomplete", got.ReasonCode, got.Complete, got.State)
	}
	if got.ReasonCode == tracingReasonAPISseen {
		t.Fatal("unsupported window claimed an api-seen read")
	}
	if got.Reason != tracingAuditBroadMsg {
		t.Fatalf("reason %q, want the broad-scan message", got.Reason)
	}
	// No live read is claimed: origin/consistency/asOf stay as the empty lane had.
	if got.Origin != empty.Origin || got.Consistency != empty.Consistency || !got.AsOf.Equal(empty.AsOf) {
		t.Fatalf("unsupported window stamped a live read: origin %q consistency %q asOf %v", got.Origin, got.Consistency, got.AsOf)
	}
	// The empty axis grid stays; no busy data is invented.
	if tracingBucketSum(got) != 0 {
		t.Fatalf("unsupported window filled counts: %d", tracingBucketSum(got))
	}
	for _, b := range got.Buckets {
		if b.BusyMs != nil {
			t.Fatalf("unsupported window invented busyMs %v", *b.BusyMs)
		}
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times for an unsupported window, want 0", f.calls)
	}
}

// ---- unit: the lane guard and stamping ----

func TestApiSeenLaneGuardsAndStampsLiveRead(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	clk := tracingLaneT0.Add(2 * time.Hour)
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	empty := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true, Buckets: []tracingBucket{}, Events: []tracingEvent{}}
	got, err := tracingAPISseenLane(context.Background(), f, nil, axis, empty, auditClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReasonCode != tracingReasonAPISseen || got.State != calltrace.StateEvents || !got.Complete || got.Truncated {
		t.Fatalf("lane %s %s complete %v truncated %v", got.ReasonCode, got.State, got.Complete, got.Truncated)
	}
	if got.Origin != tracingOriginLive || got.Consistency != tracingConsLive || !got.AsOf.Equal(clk) {
		t.Fatalf("origin %s consistency %s asOf %v, want a live read at %v", got.Origin, got.Consistency, got.AsOf, clk)
	}
	if got.BlockSeconds != 0 || got.Scanned != 3 || got.Reason != "" {
		t.Fatalf("blockSeconds %d scanned %d reason %q", got.BlockSeconds, got.Scanned, got.Reason)
	}
	b := heldBucketAt(t, got, tracingLaneT0.Unix())
	if b.BusyMs == nil || *b.BusyMs != 1200 || b.Count != 3 || b.TimedCount != 3 || !b.Complete {
		t.Fatalf("bucket %+v", b)
	}
	// The screen's sentence comes from dbState; the lane invents no copy.
	if got.Reason != "" {
		t.Fatalf("lane wrote copy %q", got.Reason)
	}
}

func TestApiSeenLaneRecordedNoCallsLeavesLaneEmpty(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{}}}
	empty := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true}
	got, err := tracingAPISseenLane(context.Background(), f, nil, axis, empty, auditClock(tracingLaneT0))
	if err != nil || !reflect.DeepEqual(got, empty) {
		t.Fatalf("recorded with no calls changed the lane: %+v err %v", got, err)
	}
	if f.calls != 1 {
		t.Fatalf("calls %d, want 1", f.calls)
	}
}

func TestApiSeenLaneNilReaderLeavesLaneUntouched(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	empty := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true}
	got, err := tracingAPISseenLane(context.Background(), nil, nil, axis, empty, auditClock(tracingLaneT0))
	if err != nil || !reflect.DeepEqual(got, empty) {
		t.Fatalf("nil reader changed the lane: %+v err %v", got, err)
	}
}

func TestApiSeenReaderErrorLeavesLaneUnchanged(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	orig := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true}
	f := &apiSeenFakeRepo{err: audit.ErrBusy}
	got, err := tracingAPISseenLane(context.Background(), f, nil, axis, orig, auditClock(tracingLaneT0))
	if !errors.Is(err, audit.ErrBusy) {
		t.Fatalf("err %v, want ErrBusy", err)
	}
	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("lane changed on error: %+v", got)
	}
}

// TestApiSeenNotImportedReasonPreserved proves a lane whose files were listed but
// never imported keeps its not-imported reason: api-seen does not run on it, so
// the reader is not called even with a recorded result ready.
func TestApiSeenNotImportedReasonPreserved(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	orig := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true,
		ReasonCode: tracingReasonNotImported, Reason: tracingSQLNotImportedMsg, Buckets: []tracingBucket{}, Events: []tracingEvent{}}
	got, err := tracingAPISseenLane(context.Background(), f, nil, axis, orig, auditClock(tracingLaneT0))
	if err != nil || !reflect.DeepEqual(got, orig) {
		t.Fatalf("not-imported lane changed: %+v err %v", got, err)
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times on a not-imported lane, want 0", f.calls)
	}
}

// TestApiSeenSkippedReasonPreserved proves a lane whose imports were set aside
// keeps its import-skipped reason: api-seen does not run on it either.
func TestApiSeenSkippedReasonPreserved(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	orig := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true,
		ReasonCode: tracingReasonSkipped, Reason: tracingSkippedMsg, Buckets: []tracingBucket{}, Events: []tracingEvent{}}
	got, err := tracingAPISseenLane(context.Background(), f, nil, axis, orig, auditClock(tracingLaneT0))
	if err != nil || !reflect.DeepEqual(got, orig) {
		t.Fatalf("import-skipped lane changed: %+v err %v", got, err)
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times on an import-skipped lane, want 0", f.calls)
	}
}

// ---- handler: the empty SQL lane is filled ----

func TestApiSeenEmptySQLLaneRecordedFillsBuckets(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{
		apiSeenBin(tracingLaneT0, 1200, 3),
		apiSeenBin(tracingLaneT0.Add(5*time.Minute), 800, 2),
	}}}
	e, id := apiSeenEnv(t, f)
	before := tracingFingerprint(t, e.store, id)
	out, raw := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonAPISseen || lane.State != calltrace.StateEvents || !lane.Complete || lane.Truncated {
		t.Fatalf("lane %s %s complete %v truncated %v", lane.ReasonCode, lane.State, lane.Complete, lane.Truncated)
	}
	if lane.Origin != tracingOriginLive || lane.Consistency != tracingConsLive || lane.BlockSeconds != 0 {
		t.Fatalf("origin %s consistency %s blockSeconds %d", lane.Origin, lane.Consistency, lane.BlockSeconds)
	}
	if lane.Scanned != 5 || tracingBucketSum(lane) != 5 {
		t.Fatalf("scanned %d sum %d, want 5", lane.Scanned, tracingBucketSum(lane))
	}
	b0 := heldBucketAt(t, lane, tracingLaneT0.Unix())
	if b0.BusyMs == nil || *b0.BusyMs != 1200 || b0.Count != 3 || b0.TimedCount != 3 || b0.MaxMs != nil || b0.AvgMs != nil {
		t.Fatalf("bucket 0 %+v", b0)
	}
	b5 := heldBucketAt(t, lane, tracingLaneT0.Add(5*time.Minute).Unix())
	if b5.BusyMs == nil || *b5.BusyMs != 800 || b5.Count != 2 {
		t.Fatalf("bucket 5 %+v", b5)
	}
	// A step with no recorded call keeps busyMs null, never an invented zero.
	b1 := heldBucketAt(t, lane, tracingLaneT0.Add(time.Minute).Unix())
	if b1.BusyMs != nil {
		t.Fatalf("unrecorded step invented busyMs %v", *b1.BusyMs)
	}
	if !strings.Contains(raw, `"reasonCode":"api-seen"`) || !strings.Contains(raw, `"busyMs":1200`) {
		t.Fatal("wire output lacks api-seen figures")
	}
	if f.calls != 1 {
		t.Fatalf("calls %d, want one SQLBusy call", f.calls)
	}
	if tracingFingerprint(t, e.store, id) != before {
		t.Fatal("tracing changed the case")
	}
}

func TestApiSeenRecordedFalseLeavesLaneUnchanged(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: false}}
	e, id := apiSeenEnv(t, f)
	out, _ := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode == tracingReasonAPISseen || lane.State != calltrace.StateNoData {
		t.Fatalf("not-recorded lane was filled: %s %s", lane.ReasonCode, lane.State)
	}
	if tracingBucketSum(lane) != 0 {
		t.Fatalf("not-recorded lane has counts: %d", tracingBucketSum(lane))
	}
	if f.calls != 1 {
		t.Fatalf("calls %d, want 1", f.calls)
	}
}

// TestApiSeenUnsupportedWindowHandlerStampsBroadScan proves a >24 h window through
// the route answers 200 with a SQL lane carrying the actionable broad-scan reason
// (not a request error, not a silent empty lane), no SQLBusy call, and the other
// saved lane unaffected. The empty-listing harness has no audit lane, so the iis
// lane proves the stamp is SQL-only.
func TestApiSeenUnsupportedWindowHandlerStampsBroadScan(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	e, id := apiSeenEnv(t, f)
	// 48 h is within the route's 186-day cap but uses an hourly axis step the
	// SQLBusy contract cannot answer in one bounded call.
	out, raw := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-12T10:00:00Z"))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != auditCodeBroadScan || lane.Complete || lane.State != calltrace.StateIncomplete {
		t.Fatalf("sql lane %s complete %v state %s, want broad-scan incomplete", lane.ReasonCode, lane.Complete, lane.State)
	}
	if lane.ReasonCode == tracingReasonAPISseen || tracingBucketSum(lane) != 0 {
		t.Fatalf("unsupported window filled the sql lane: %s sum %d", lane.ReasonCode, tracingBucketSum(lane))
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times for an unsupported window, want 0", f.calls)
	}
	if !strings.Contains(raw, `"reasonCode":"broad-scan"`) {
		t.Fatal("wire output lacks the broad-scan reason")
	}
	// The stamp is SQL-only: the other saved lane is untouched.
	iis := tracingLaneOf(t, out, calltrace.LaneIIS)
	if iis.ReasonCode == auditCodeBroadScan || iis.ReasonCode == tracingReasonAPISseen {
		t.Fatalf("iis lane affected by the sql stamp: %s", iis.ReasonCode)
	}
}

// ---- handler: the three reader-NOT-called guards ----

func TestApiSeenHeldDayReaderNotCalled(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(at, 999, 9)}}}
	e, id := apiSeenEnv(t, f)
	c := heldSQLCase(t, []dbhealth.LaneSeriesPoint{{T: at.Unix(), Execs: 7, AvgMs: 3, MaxMs: 9}}, 15)
	handle, err := e.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	hs, ok := handle.(casestore.HealthStore)
	if !ok {
		handle.Close()
		t.Fatal("test store cannot save a live day")
	}
	if _, err := hs.AddHealthRun(context.Background(), c.runs[0]); err != nil {
		handle.Close()
		t.Fatal(err)
	}
	handle.Close()
	out, _ := tracingPost(t, e, id, tracingWindowBodyFor(at.Format(time.RFC3339), at.Add(time.Hour).Format(time.RFC3339)))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonHeldSQL {
		t.Fatalf("held day was displaced: %s", lane.ReasonCode)
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times with a held day present, want 0", f.calls)
	}
}

func TestApiSeenQSFileReaderNotCalled(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 999, 9)}}}
	e, id := apiSeenEnv(t, f)
	imp := seedTracingImport(t, e.store, id, sqlserver.QSName, "export.csv", []evidence.Event{qsFileTestEvent(tracingLaneT0, "100", 20, 90)})
	out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonQSFile {
		t.Fatalf("qs-file was displaced: %s", lane.ReasonCode)
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times with a qs-file present, want 0", f.calls)
	}
}

func TestApiSeenExistingBucketsReaderNotCalled(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 999, 9)}}}
	e, id := apiSeenEnv(t, f)
	imp := seedTracingImport(t, e.store, id, "sql-xel", "slow.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(10*time.Minute), 3, time.Minute, nil))
	out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode == tracingReasonAPISseen {
		t.Fatal("api-seen displaced imported bucket data")
	}
	if tracingBucketSum(lane) != 3 {
		t.Fatalf("imported counts lost: %d", tracingBucketSum(lane))
	}
	if f.calls != 0 {
		t.Fatalf("SQLBusy called %d times with bucket data present, want 0", f.calls)
	}
}

// ---- handler: a read error is fail-closed and mapped ----

// DATA-05: a failing api-seen read degrades to a warning instead of aborting the
// request. The SQL lane keeps its unchanged fallback state and the other lanes
// survive; the multi-lane survival proof is in tracing_apiseen_degrade_test.go.
func TestApiSeenReaderErrorDegradesToWarning(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"busy", audit.ErrBusy},
		{"limit", audit.ErrLimit},
		{"deadline", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &apiSeenFakeRepo{err: tc.err}
			e, id := apiSeenEnv(t, f)
			res := e.json(http.MethodPost, tracingPath(id), tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))
			if res.Code != http.StatusOK {
				t.Fatalf("%s = %d %s, want 200 (degrade, not abort)", tc.name, res.Code, res.Body)
			}
			if f.calls != 1 {
				t.Fatalf("calls %d, want 1", f.calls)
			}
			if !strings.Contains(res.Body.String(), "could not be read for this window") {
				t.Fatalf("%s: missing degrade warning: %s", tc.name, res.Body)
			}
		})
	}
}

func TestApiSeenCancelledRequestWritesNothing(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	e, id := apiSeenEnv(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onCall = cancel // the client goes away during the SQLBusy read
	f.err = context.Canceled
	req := httptest.NewRequest(http.MethodPost, tracingPath(id), strings.NewReader(tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))).WithContext(ctx)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	res := e.serve(req)
	if res.Code >= 500 {
		t.Fatalf("cancelled request = %d %s", res.Code, res.Body)
	}
	if body := res.Body.String(); body != "" {
		t.Fatalf("cancelled request wrote %q, want nothing", body)
	}
}

// ---- demo: api-seen only when the demo SQL day does not cover the window ----

func TestApiSeenDemoOnlyWhenSQLDayDoesNotCoverWindow(t *testing.T) {
	now := time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC)
	e, id := apiSeenDemoEnv(t, now)
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", demoSQLBody("AuditDb")); res.Code != http.StatusOK {
		t.Fatalf("demo SQL day run = %d %s", res.Code, res.Body)
	}
	// The demo SQL day covers 2026-10-20T10:00-11:00: that window stays held-sql.
	covered, _ := tracingPost(t, e, id, tracingWindowBodyFor("2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z"))
	if lane := tracingLaneOf(t, covered, calltrace.LaneSQL); lane.ReasonCode != tracingReasonHeldSQL {
		t.Fatalf("covered window reason %s, want held-sql", lane.ReasonCode)
	}
	// A window the demo SQL day does not cover falls back to the demo audit source.
	uncovered, raw := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:30:00Z", "2026-03-10T11:00:00Z"))
	lane := tracingLaneOf(t, uncovered, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonAPISseen || lane.State != calltrace.StateEvents || !lane.Complete {
		t.Fatalf("uncovered window lane %s %s complete %v", lane.ReasonCode, lane.State, lane.Complete)
	}
	if lane.Origin != tracingOriginLive || lane.Consistency != tracingConsLive || lane.BlockSeconds != 0 {
		t.Fatalf("uncovered lane origin %s consistency %s blockSeconds %d", lane.Origin, lane.Consistency, lane.BlockSeconds)
	}
	busy := false
	for _, b := range lane.Buckets {
		if b.BusyMs != nil && *b.BusyMs > 0 {
			busy = true
			if b.TimedCount != b.Count || b.MaxMs != nil || b.AvgMs != nil {
				t.Fatalf("demo bucket %+v", b)
			}
		}
	}
	if !busy || !strings.Contains(raw, `"reasonCode":"api-seen"`) || !strings.Contains(raw, `"busyMs"`) {
		t.Fatal("demo api-seen lane carries no busy time")
	}
}

// ---- DATA-02: the selected audit source ids reach SQLBusy ----

func TestApiSeenBucketsForwardsSourceIDs(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	ids := []string{"src-a"}
	if _, _, err := apiSeenBuckets(context.Background(), f, ids, axis); err != nil {
		t.Fatal(err)
	}
	want := apiSeenSQLBusyInput(axis)
	want.SourceIDs = ids
	if f.calls != 1 || !reflect.DeepEqual(f.lastIn, want) {
		t.Fatalf("calls %d input %+v, want the axis window with SourceIDs %v", f.calls, f.lastIn, ids)
	}
}

func TestApiSeenLaneForwardsSourceIDs(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}}}
	empty := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateNoData, Complete: true}
	ids := []string{"src-a", "src-b"}
	got, err := tracingAPISseenLane(context.Background(), f, ids, axis, empty, auditClock(tracingLaneT0))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReasonCode != tracingReasonAPISseen {
		t.Fatalf("lane not filled: %s", got.ReasonCode)
	}
	if !reflect.DeepEqual(f.lastIn.SourceIDs, ids) {
		t.Fatalf("SourceIDs = %v, want %v", f.lastIn.SourceIDs, ids)
	}
}

// TestApiSeenPassesSelectedAuditSourceIDs proves caseTracing threads the selected
// AUDIT source ids into the api-seen lane: with two synthetic audit sources
// connected, selecting one forwards only that id (so SQLBusy cannot sum both),
// selecting both forwards both, and no explicit selection forwards the whole
// listing. The ids are audit ids, never SQL/IIS import ids.
func TestApiSeenPassesSelectedAuditSourceIDs(t *testing.T) {
	srcA := audit.Source{ID: "src-a", Label: "src-a", Database: "db", Collection: "src-a"}
	srcB := audit.Source{ID: "src-b", Label: "src-b", Database: "db", Collection: "src-b"}
	f := &apiSeenFakeRepo{
		sources: []audit.Source{srcA, srcB},
		result:  audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{apiSeenBin(tracingLaneT0, 1200, 3)}},
	}
	e, id := apiSeenEnv(t, f)
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"one selected", tracingLaneBody("src-a"), []string{"src-a"}},
		{"both selected", tracingLaneBody("src-a", "src-b"), []string{"src-a", "src-b"}},
		{"none selected lists all", tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"), []string{"src-a", "src-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.calls, f.lastIn = 0, audit.SQLBusyInput{}
			out, _ := tracingPost(t, e, id, tc.body)
			lane := tracingLaneOf(t, out, calltrace.LaneSQL)
			if lane.ReasonCode != tracingReasonAPISseen {
				t.Fatalf("api-seen did not fill the SQL lane: %s", lane.ReasonCode)
			}
			if f.calls != 1 {
				t.Fatalf("SQLBusy called %d times, want 1", f.calls)
			}
			if !reflect.DeepEqual(f.lastIn.SourceIDs, tc.want) {
				t.Fatalf("SourceIDs = %v, want %v", f.lastIn.SourceIDs, tc.want)
			}
		})
	}
}
