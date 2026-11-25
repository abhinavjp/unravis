package codemode

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpsrv"
)

// Review 4 of #267 (/mnt/project-files/reviews/pr267-review-4.md), M1: a code run carries the tier of its caller's grant. The host of a
// run is built from the same options as the MCP dispatcher's bridge (mcpsrv.BridgeOptions), so a script reads what the host's own tool
// calls read and no more: counts below the cohort floor are hidden, record ids are hidden, and the tools are the tier's.

// narrowQuery is a 15 second window on one source of the demo: every group has 2 events, below the floor of 5.
func narrowQuery() map[string]any {
	q := demoQuery()
	win := 15 * time.Second
	q["sourceIds"] = []string{"demo-west"}
	q["start"], q["end"] = demoNow.Add(-win).Format(time.RFC3339), demoNow.Format(time.RFC3339)
	q["baselineStart"], q["baselineEnd"] = demoNow.Add(-2*win).Format(time.RFC3339), demoNow.Add(-win).Format(time.RFC3339)
	return q
}

func tierRun(t *testing.T, tier string, identifiers bool, script fakeScript) (*env, RunResult) {
	t.Helper()
	opts := []envOpt{}
	if identifiers {
		opts = append(opts, withOperatorIdentifiers)
	}
	e := newEnv(t, script, opts...)
	res, err := e.runReq(RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: tier, Sources: []string{"demo-west"}, Reason: "tier test", AllowIdentifiers: identifiers})
	if err != nil {
		t.Fatal(err)
	}
	return e, res
}

func groupsOf(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var env struct {
		Groups []map[string]any `json:"groups"`
		Error  *struct{ Code string }
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Error != nil || len(env.Groups) == 0 {
		t.Fatalf("not a comparison answer (%v): %.400s", err, raw)
	}
	return env.Groups
}

func TestAggregateRunSeesNoSmallCountsAndEvidenceRunSeesThem(t *testing.T) {
	var agg, ev json.RawMessage
	_, res := tierRun(t, mcpsrv.TierAggregate, false, func(w *fakeWorker) {
		agg, _ = w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
		w.done(1)
	})
	if res.Status != casestore.RunOK {
		t.Fatalf("aggregate run: %+v", res)
	}
	_, res = tierRun(t, mcpsrv.TierEvidence, false, func(w *fakeWorker) {
		ev, _ = w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
		w.done(1)
	})
	if res.Status != casestore.RunOK {
		t.Fatalf("evidence run: %+v", res)
	}
	for _, g := range groupsOf(t, agg) {
		if g["suppressed"] != true || g["currentEvents"] != "<5" || g["referenceEvents"] != "<5" {
			t.Errorf("an aggregate run saw a small count: %v", g)
		}
	}
	if strings.Contains(string(agg), `"count":2`) || strings.Contains(string(agg), `"histogram"`) {
		t.Errorf("a figure of a small group is in the aggregate answer: %.600s", agg)
	}
	for _, g := range groupsOf(t, ev) {
		if g["suppressed"] != nil || g["currentEvents"] != float64(2) {
			t.Errorf("an evidence run did not get the real figures: %v", g)
		}
	}
}

// What the script gets is what the host's own tool call gets: the same bridge options, the same bytes.
func TestRunAnswerEqualsTheDispatcherBridgeAnswerForTheGrant(t *testing.T) {
	for _, tier := range []string{mcpsrv.TierAggregate, mcpsrv.TierEvidence} {
		var got json.RawMessage
		e, res := tierRun(t, tier, false, func(w *fakeWorker) {
			got, _ = w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
			w.done(1)
		})
		if res.Status != casestore.RunOK {
			t.Fatalf("%s: %+v", tier, res)
		}
		direct := mcp.NewLocal(e.handler, testToken, mcpsrv.BridgeOptions(false, []string{"demo-west"}, tier)...)
		args, _ := json.Marshal(map[string]any{"query": narrowQuery(), "limit": 5})
		want, isErr := direct.CallEnvelope(t.Context(), "audit_compare", args)
		if isErr || !sameJSON(t, got, want, "resultId") {
			t.Errorf("%s: the script's answer differs from the dispatcher bridge's:\n%s", tier, firstDiff(got, want))
		}
	}
}

// sameJSON compares two documents after decoding, leaving out the named top-level keys (a result handle is new for every bridge) and
// setting every "elapsedMs" to zero (provenance holds the milliseconds the query took; the rest of provenance is compared).
func sameJSON(t *testing.T, a, b []byte, skip ...string) bool {
	t.Helper()
	var x, y map[string]any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	for _, k := range skip {
		delete(x, k)
		delete(y, k)
	}
	zeroElapsed(x)
	zeroElapsed(y)
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

func zeroElapsed(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, c := range t {
			if k == "elapsedMs" {
				t[k] = float64(0)
			} else {
				zeroElapsed(c)
			}
		}
	case []any:
		for _, c := range t {
			zeroElapsed(c)
		}
	}
}

func TestTierLimitsTheToolsOfARun(t *testing.T) {
	evidenceOnly := []string{"audit_get_requests", "audit_get_slowest", "audit_get_activity"}
	for _, tool := range evidenceOnly {
		var answer json.RawMessage
		var worker *fakeWorker
		e, res := tierRun(t, mcpsrv.TierAggregate, false, func(w *fakeWorker) {
			worker = w
			answer, _ = w.Call(tool, map[string]any{"resultId": "x"})
			w.done(1)
		})
		e.wantStatus(res, casestore.RunError, CodeBadFrame)
		if len(answer) != 0 {
			t.Errorf("%s: an aggregate run was answered: %.200s", tool, answer)
		}
		rows := e.calls(res.RunID)
		if len(rows) != 1 || rows[0].Tool != tool || rows[0].Status != casestore.CallDenied {
			t.Errorf("%s: rows %+v", tool, rows)
		}
		if got := worker.runMsg.Tools; slices.Contains(got, tool) || len(got) != len(mcpsrv.ToolsForTier(mcpsrv.TierAggregate)) {
			t.Errorf("%s: the run frame names %v", tool, got)
		}
		e.verify()
	}
	// An evidence run gets all ten, and its call goes to the host.
	var worker *fakeWorker
	var answer json.RawMessage
	e, res := tierRun(t, mcpsrv.TierEvidence, false, func(w *fakeWorker) {
		worker = w
		answer, _ = w.Call("audit_get_activity", map[string]any{"sourceIds": []string{"demo-west"}, "start": demoNow.Add(-time.Hour), "end": demoNow})
		w.done(1)
	})
	e.wantStatus(res, casestore.RunOK, "")
	if len(worker.runMsg.Tools) != 10 || len(answer) == 0 {
		t.Errorf("evidence run: tools %v answer %.200s", worker.runMsg.Tools, answer)
	}
	if rows := e.calls(res.RunID); len(rows) != 1 || rows[0].Status == casestore.CallDenied {
		t.Errorf("rows %+v", rows)
	}
}

// Without the identifiers grant no row of an evidence tool carries the audit record id; with it, it does (as for the dispatcher).
func TestRecordIDsOfARunFollowTheIdentifiersGrant(t *testing.T) {
	for _, identifiers := range []bool{false, true} {
		var rows []any
		e, res := tierRun(t, mcpsrv.TierEvidence, identifiers, func(w *fakeWorker) {
			q := demoQuery()
			q["sourceIds"] = []string{"demo-west"}
			c, _ := w.Call("audit_compare", map[string]any{"query": q, "limit": 5})
			g := groupsOf(t, c)[0]
			var env struct{ ResultID string }
			_ = json.Unmarshal(c, &env)
			var raw json.RawMessage
			raw, _ = w.Call("audit_get_requests", map[string]any{"resultId": env.ResultID, "apiKey": g["key"], "limit": 5})
			var out struct{ Rows []any }
			_ = json.Unmarshal(raw, &out)
			rows = out.Rows
			w.done(1)
		})
		e.wantStatus(res, casestore.RunOK, "")
		if len(rows) == 0 {
			t.Fatalf("identifiers %v: no rows", identifiers)
		}
		for _, row := range rows {
			if _, has := row.(map[string]any)["id"]; has != identifiers {
				t.Errorf("identifiers %v: record id present %v in %v", identifiers, has, row)
			}
		}
	}
}

func TestARunWithoutAKnownTierIsRefusedAndWritesNothing(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) { w.done(1) })
	n := len(e.runs())
	for name, req := range map[string]RunRequest{
		"scope without a tier":   {Code: "1", Surface: "mcp", Scoped: true, Sources: []string{"demo-west"}},
		"scope, unknown tier":    {Code: "1", Surface: "mcp", Scoped: true, Tier: "root", Sources: []string{"demo-west"}},
		"scope, tier in caps":    {Code: "1", Surface: "mcp", Scoped: true, Tier: "Evidence"},
		"cli scope without tier": {Code: "1", Surface: "cli", Scoped: true},
	} {
		_, err := e.runReq(req)
		if ref, ok := err.(*Refusal); !ok || ref.Code != CodeDenied {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A tier with no scope is not a request the caller can mean.
	_, err := e.runReq(RunRequest{Code: "1", Surface: "cli", Tier: mcpsrv.TierEvidence})
	if ref, ok := err.(*Refusal); !ok || ref.Code != CodeBadRequest {
		t.Errorf("tier without scope: %v", err)
	}
	if len(e.runs()) != n {
		t.Error("a refused run left a row")
	}
}

func TestTheScopeNoteCarriesTheTier(t *testing.T) {
	for _, tier := range []string{mcpsrv.TierAggregate, mcpsrv.TierEvidence} {
		e, res := tierRun(t, tier, false, func(w *fakeWorker) { w.done(1) })
		if got := e.getRun(res.RunID).Reason; !strings.HasPrefix(got, "[sources: demo-west; tier: "+tier+"] tier test") {
			t.Errorf("%s: %q", tier, got)
		}
	}
}

// A host factory other than the production one still gets the tier, so a replacement cannot forget it.
func TestHostScopeCarriesTheTier(t *testing.T) {
	var seen HostScope
	e := newEnv(t, func(w *fakeWorker) { w.done(1) }, func(e *env) {
		e.hosts = func(s HostScope) Host { seen = s; return DashboardHosts(http.NotFoundHandler(), testToken)(s) }
	})
	if _, err := e.runReq(RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierAggregate, Sources: []string{"demo-west"}}); err != nil {
		t.Fatal(err)
	}
	if seen.Tier != mcpsrv.TierAggregate || !seen.Scoped {
		t.Errorf("scope %+v", seen)
	}
}

// firstDiff names the first top-level keys whose values differ, for a failure message.
func firstDiff(a, b []byte) string {
	var x, y map[string]any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	var out []string
	for k, v := range x {
		xv, _ := json.Marshal(v)
		yv, _ := json.Marshal(y[k])
		if string(xv) != string(yv) {
			out = append(out, k+": "+string(xv)+" != "+string(yv))
		}
	}
	for k := range y {
		if _, ok := x[k]; !ok {
			out = append(out, "only in direct: "+k)
		}
	}
	return strings.Join(out, "\n")
}

// A scoped run is paged with sealed cursors: the dashboard's own cursor holds a position in the record list (record id, source), and a
// script must not hold it (it could return it to a host that has no identifiers grant).
func TestScopedRunGetsSealedCursorsThatPageTheRun(t *testing.T) {
	var first, second json.RawMessage
	var id string
	_, res := tierRun(t, mcpsrv.TierEvidence, false, func(w *fakeWorker) {
		cmp, _ := w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
		var m struct {
			ResultID string `json:"resultId"`
		}
		_ = json.Unmarshal(cmp, &m)
		id = m.ResultID
		first, _ = w.Call("audit_get_slowest", map[string]any{"resultId": id, "limit": 1})
		var f struct {
			NextCursor string `json:"nextCursor"`
		}
		_ = json.Unmarshal(first, &f)
		second, _ = w.Call("audit_get_slowest", map[string]any{"resultId": id, "limit": 1, "cursor": f.NextCursor})
		w.done(1)
	})
	if res.Status != casestore.RunOK || id == "" {
		t.Fatalf("run: %+v", res)
	}
	var f, s struct {
		NextCursor string `json:"nextCursor"`
		Error      *struct{ Code string }
	}
	_ = json.Unmarshal(first, &f)
	_ = json.Unmarshal(second, &s)
	if f.NextCursor == "" || s.Error != nil {
		t.Fatalf("paging a run failed: first %.300s second %.300s", first, second)
	}
	// the same call on a bridge without the codec shows the dashboard's own cursor; the run's is not it
	e := newEnv(t, func(w *fakeWorker) { w.done(1) })
	raw := mcp.NewLocal(e.handler, testToken, mcpsrv.BridgeOptions(false, []string{"demo-west"}, mcpsrv.TierEvidence)...)
	args, _ := json.Marshal(map[string]any{"query": narrowQuery(), "limit": 5})
	cmp, _ := raw.CallEnvelope(t.Context(), "audit_compare", args)
	var cm struct {
		ResultID string `json:"resultId"`
	}
	_ = json.Unmarshal(cmp, &cm)
	args, _ = json.Marshal(map[string]any{"resultId": cm.ResultID, "limit": 1})
	rawSlow, _ := raw.CallEnvelope(t.Context(), "audit_get_slowest", args)
	var r struct {
		NextCursor string `json:"nextCursor"`
	}
	_ = json.Unmarshal(rawSlow, &r)
	if r.NextCursor == "" || r.NextCursor == f.NextCursor {
		t.Errorf("the run's cursor is the dashboard's own: %q", f.NextCursor)
	}
	// a cursor made by hand does not open
	if _, ok := forgedCursor(t); ok {
		t.Error("a forged cursor opened")
	}
}

func forgedCursor(t *testing.T) (string, bool) {
	t.Helper()
	var ans json.RawMessage
	var id string
	tierRun(t, mcpsrv.TierEvidence, false, func(w *fakeWorker) {
		cmp, _ := w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
		var m struct {
			ResultID string `json:"resultId"`
		}
		_ = json.Unmarshal(cmp, &m)
		id = m.ResultID
		ans, _ = w.Call("audit_get_slowest", map[string]any{"resultId": id, "limit": 1, "cursor": "AAAA.BBBB"})
		w.done(1)
	})
	var s struct{ Error *struct{ Code string } }
	_ = json.Unmarshal(ans, &s)
	return "", s.Error == nil
}
