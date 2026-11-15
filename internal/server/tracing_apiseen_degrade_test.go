package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
)

// DATA-05: a failing api-seen fallback read must DEGRADE the Timeline answer, not
// fail it. The API (audit) and IIS lanes already built stay in the answer, the SQL
// lane keeps its unchanged fallback state, and a plain-word warning says the read
// failed. These tests drive the whole handler (caseTracing) with a failing
// SQLBusy reader, reusing the api-seen harness (apiSeenFakeRepo / apiSeenEnv /
// tracingPost / tracingLaneOf) read-only. The failure is injected through
// apiSeenFakeRepo.err, never by calling apiSeenBuckets directly.

// apiSeenDegradeWarn is the plain-word warning the handler adds when the api-seen
// read fails. It carries no host, value or error text. Kept in sync with the
// literal in caseTracing's api-seen error branch.
const apiSeenDegradeWarn = "The database time the API saw could not be read for this window, so the database lane may be empty."

// apiSeenDegradeRepo pairs the api-seen fake (SQLBusy) with a working rollup fake
// so one request produces all three lanes: a live API (audit) lane with counts, a
// stored IIS lane, and the empty SQL lane the api-seen fallback tries to fill. The
// rollup fake is a named field (not embedded) so its Sources does not clash with
// the api-seen fake's; only Rollup is delegated.
type apiSeenDegradeRepo struct {
	*apiSeenFakeRepo
	rollup *auditFake
}

func (r *apiSeenDegradeRepo) Rollup(ctx context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	return r.rollup.Rollup(ctx, plan)
}

// apiSeenDegradeEnv is a case with a stored IIS import (five 503 requests in the
// window) and one connected synthetic audit source whose rollup saw two API
// requests. The SQL lane has no saved data, so the api-seen fallback runs on it.
func apiSeenDegradeEnv(t *testing.T, f *apiSeenFakeRepo) (*caseEnv, string) {
	t.Helper()
	f.sources = []audit.Source{{ID: "src-a", Label: "src-a", Database: "db", Collection: "src-a"}}
	repo := &apiSeenDegradeRepo{apiSeenFakeRepo: f, rollup: &auditFake{rows: []auditRow{
		{at: tracingLaneT0.Add(5 * time.Minute)},
		{at: tracingLaneT0.Add(35 * time.Minute)},
	}}}
	e, id := apiSeenEnv(t, repo)
	seedTracingImport(t, e.store, id, "iis-w3c", "web.log",
		tracingKindEvents("request", tracingLaneT0.Add(20*time.Minute), 5, time.Minute, map[string]string{"status": "503"}))
	return e, id
}

// TestApiSeenReadFailureDegradesToWarning is the DATA-05 proof: a failing api-seen
// reader leaves the API and IIS lanes intact, keeps the SQL lane in the answer
// unchanged, and adds one plain-word warning instead of aborting the request.
func TestApiSeenReadFailureDegradesToWarning(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"deadline", context.DeadlineExceeded},
		{"limit", audit.ErrLimit},
		{"busy", audit.ErrBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &apiSeenFakeRepo{err: tc.err}
			e, id := apiSeenDegradeEnv(t, f)
			before := tracingFingerprint(t, e.store, id)
			// tracingPost requires HTTP 200: the failed read did not abort the request.
			out, raw := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))
			if f.calls != 1 {
				t.Fatalf("SQLBusy called %d times, want 1", f.calls)
			}
			// The IIS lane keeps its stored events.
			iis := tracingLaneOf(t, out, calltrace.LaneIIS)
			if iis.State != calltrace.StateEvents || tracingBucketSum(iis) != 5 || iis.Scanned != 5 {
				t.Fatalf("iis lane lost its data: state %s sum %d scanned %d", iis.State, tracingBucketSum(iis), iis.Scanned)
			}
			// The API (audit) lane keeps its live read.
			api := tracingLaneOf(t, out, "audit")
			if api.State != calltrace.StateEvents || !api.Complete || api.Scanned != 2 {
				t.Fatalf("audit lane lost its read: state %s complete %v scanned %d", api.State, api.Complete, api.Scanned)
			}
			// The SQL lane is still in the answer, unchanged by the failed read.
			sql := tracingLaneOf(t, out, calltrace.LaneSQL)
			if sql.ReasonCode == tracingReasonAPISseen || sql.State != calltrace.StateNoData || tracingBucketSum(sql) != 0 {
				t.Fatalf("sql lane changed by the failed read: reason %s state %s sum %d", sql.ReasonCode, sql.State, tracingBucketSum(sql))
			}
			// Exactly one plain-word warning survives into the answer; no error text leaks.
			n := 0
			for _, w := range out.Warnings {
				if w == apiSeenDegradeWarn {
					n++
				}
			}
			if n != 1 || len(out.Warnings) != 1 {
				t.Fatalf("warnings %v, want exactly the api-seen read warning", out.Warnings)
			}
			// The failure became a warning, not an error envelope: no "error" key
			// (the abort path writes {"error":...}) and no driver error text in it.
			if strings.Contains(raw, `"error":`) {
				t.Fatalf("answer is an error envelope, not a degraded Timeline: %s", raw)
			}
			if tracingFingerprint(t, e.store, id) != before {
				t.Fatal("tracing changed the case")
			}
		})
	}
}

// TestApiSeenReadSuccessFillsLaneWithoutWarning is the control: a successful
// api-seen read still fills the SQL lane and adds no warning, with the API and IIS
// lanes intact.
func TestApiSeenReadSuccessFillsLaneWithoutWarning(t *testing.T) {
	f := &apiSeenFakeRepo{result: audit.SQLBusyResult{Recorded: true, Bins: []audit.SQLBusyBin{
		apiSeenBin(tracingLaneT0, 1200, 3),
		apiSeenBin(tracingLaneT0.Add(5*time.Minute), 800, 2),
	}}}
	e, id := apiSeenDegradeEnv(t, f)
	out, _ := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))
	if f.calls != 1 {
		t.Fatalf("SQLBusy called %d times, want 1", f.calls)
	}
	sql := tracingLaneOf(t, out, calltrace.LaneSQL)
	if sql.ReasonCode != tracingReasonAPISseen || sql.State != calltrace.StateEvents || !sql.Complete || tracingBucketSum(sql) != 5 {
		t.Fatalf("api-seen did not fill the SQL lane: reason %s state %s complete %v sum %d", sql.ReasonCode, sql.State, sql.Complete, tracingBucketSum(sql))
	}
	if iis := tracingLaneOf(t, out, calltrace.LaneIIS); tracingBucketSum(iis) != 5 {
		t.Fatalf("iis lane lost its data: sum %d", tracingBucketSum(iis))
	}
	if api := tracingLaneOf(t, out, "audit"); api.Scanned != 2 || !api.Complete {
		t.Fatalf("audit lane lost its read: scanned %d complete %v", api.Scanned, api.Complete)
	}
	if len(out.Warnings) != 0 {
		t.Fatalf("a successful read added warnings: %v", out.Warnings)
	}
}

// TestApiSeenReadFailureClientGoneWritesNothing proves the client-gone case still
// returns silently after the degrade change: a read that fails because the client
// went away writes nothing (no warning, no error body).
func TestApiSeenReadFailureClientGoneWritesNothing(t *testing.T) {
	f := &apiSeenFakeRepo{err: context.Canceled}
	e, id := apiSeenDegradeEnv(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onCall = cancel // the client goes away during the SQLBusy read
	req := httptest.NewRequest(http.MethodPost, tracingPath(id), strings.NewReader(tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))).WithContext(ctx)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	res := e.serve(req)
	if body := res.Body.String(); body != "" {
		t.Fatalf("cancelled request wrote %q, want nothing", body)
	}
}
