package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// Provenance counts the records behind the figures of a source and window. Next to a comparison that hides one group of 1 to 4 events,
// "sampleSize" minus the listed groups is the count of the hidden group (review 5 of #267, BL-348). A suppressing bridge shows no
// record counts in provenance; the rest of each entry (window, mode, completeness, timing) stays.
func provenanceUpstream() (*httptest.Server, audit.Query) {
	q := audit.Query{SourceIDs: []string{"s1"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC),
		BaselineStart: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC),
		Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}
	examined := int64(9000)
	prov := func(window string, sample, returned int64) audit.Provenance {
		return audit.Provenance{SourceID: "s1", Window: window, Start: q.Start, End: q.End, QueryMode: audit.QueryModeExact, ScanClass: "narrow",
			Completeness: "complete", DocsReturned: returned, DocsExamined: &examined, SampleSize: sample, ElapsedMs: 12, MaxTimeMs: 5000}
	}
	big := audit.Comparison{Key: "big", SourceID: "s1", Controller: "Payroll", Action: "Run", State: "compared", CurrentEvents: 200, ReferenceEvents: 190, Current: audit.Stats{Count: 200}, Reference: audit.Stats{Count: 190}}
	small := audit.Comparison{Key: "small", SourceID: "s1", Controller: "Payroll", Action: "Rare", State: "compared", CurrentEvents: 3, ReferenceEvents: 2, Current: audit.Stats{Count: 3}, Reference: audit.Stats{Count: 2}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/analyze":
			_ = json.NewEncoder(w).Encode(audit.Analysis{Query: q, APIs: []audit.Comparison{big, small}, Provenance: []audit.Provenance{prov("current", 203, 2), prov("reference", 192, 2)}})
		default:
			w.WriteHeader(404)
		}
	}))
	return srv, q
}

func provenanceOf(t *testing.T, result any) []map[string]any {
	t.Helper()
	list, _ := asJSON(t, result)["provenance"].([]any)
	if len(list) == 0 {
		t.Fatal("no provenance in the answer")
	}
	out := make([]map[string]any, len(list))
	for i, e := range list {
		out[i], _ = e.(map[string]any)
	}
	return out
}

func checkHidden(t *testing.T, what string, result any) {
	t.Helper()
	for _, p := range provenanceOf(t, result) {
		for _, k := range []string{"sampleSize", "docsReturned", "docsExamined"} {
			if v, has := p[k]; !has || v != nil {
				t.Errorf("%s: provenance %v shows %s = %v (want a stable key with no count)", what, p["window"], k, v)
			}
		}
		if p["countsHidden"] != true || p["completeness"] != "complete" || p["elapsedMs"] != float64(12) || p["scanClass"] != "narrow" {
			t.Errorf("%s: the rest of the entry changed: %v", what, p)
		}
	}
}

func TestProvenanceCountsAreHiddenUnderSuppression(t *testing.T) {
	srv, q := provenanceUpstream()
	defer srv.Close()
	raw, _ := json.Marshal(map[string]any{"query": q})
	b := newBridge(fixedToken(srv.URL, "tok"), srv.Client(), WithMinCohort(5))
	result, err := b.call(context.Background(), "audit_compare", raw)
	if err != nil {
		t.Fatal(err)
	}
	checkHidden(t, "audit_compare", result)
}

// The breakdown's own provenance holds the size of the whole selection (matched), the figure its total hides when it is small. The
// upstream here reports a small selection for a comparison of one big group, so the refusal of breakdownAllowed does not apply.
func TestBreakdownProvenanceCountsAreHiddenUnderSuppression(t *testing.T) {
	entry := audit.Provenance{SourceID: "s", Window: "breakdown", QueryMode: audit.QueryModeExact, ScanClass: "narrow", Completeness: "complete", DocsReturned: 3, SampleSize: 3, ElapsedMs: 12}
	bd := audit.Breakdown{Dim: audit.BreakdownStatusCode, Total: 3, Rows: []audit.BreakdownRow{row("200", 3)}, Provenance: []audit.Provenance{entry}}
	b := bridgeWithResult(t, bd, audit.Comparison{Key: "g", CurrentEvents: 100, Current: audit.Stats{Count: 100}})
	got, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"r","dim":"statusCode"}`))
	if err != nil {
		t.Fatal(err)
	}
	if asJSON(t, got)["total"] != "<5" {
		t.Fatalf("total: %v", asJSON(t, got)["total"])
	}
	for _, p := range provenanceOf(t, got) {
		if p["sampleSize"] != nil || p["docsReturned"] != nil {
			t.Errorf("breakdown provenance shows the selection size: %v", p)
		}
	}
}

// Without suppression (stdio, evidence tier) provenance is unchanged.
func TestProvenanceCountsStayWithoutSuppression(t *testing.T) {
	srv, q := provenanceUpstream()
	defer srv.Close()
	raw, _ := json.Marshal(map[string]any{"query": q})
	b := newBridge(fixedToken(srv.URL, "tok"), srv.Client())
	result, err := b.call(context.Background(), "audit_compare", raw)
	if err != nil {
		t.Fatal(err)
	}
	p := provenanceOf(t, result)[0]
	if p["sampleSize"] != float64(203) || p["docsReturned"] != float64(2) || p["docsExamined"] != float64(9000) {
		t.Errorf("counts changed: %v", p)
	}
	if _, has := p["countsHidden"]; has {
		t.Errorf("countsHidden on an unscoped answer: %v", p)
	}
}

// "distinct" minus the listed rows is the number of values folded into Other; next to other.count it bounds their counts (five folded
// values behind other.count 5 had one request each). A suppressing bridge reports the listed rows plus one for Other instead.
func distinctOf(t *testing.T, bd audit.Breakdown, suppress bool) (distinct float64, rows int) {
	t.Helper()
	group := audit.Comparison{Key: "g", CurrentEvents: 100, Current: audit.Stats{Count: 100}}
	b := bridgeWithResult(t, bd, group)
	if !suppress {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(bd)
		})
		b = NewLocal(h, "tok-0123456789abcdef0123456789abcdef", WithSources([]string{"s"}))
		b.result = &audit.Analysis{Query: audit.Query{SourceIDs: []string{"s"}}, APIs: []audit.Comparison{group}}
		b.resultID, b.created = "r", time.Now()
	}
	got, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"r","dim":"statusCode"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := asJSON(t, got)
	list, _ := m["rows"].([]any)
	d, _ := m["distinct"].(float64)
	return d, len(list)
}

func foldedBreakdown() audit.Breakdown {
	return audit.Breakdown{Dim: audit.BreakdownStatusCode, Total: 105, Distinct: 6, Rows: []audit.BreakdownRow{row("200", 100), row("400", 1), row("401", 1), row("403", 1), row("404", 1), row("500", 1)}}
}

func TestBreakdownDistinctHidesFoldedValuesUnderSuppression(t *testing.T) {
	d, rows := distinctOf(t, foldedBreakdown(), true)
	if rows != 1 || d != float64(rows+1) || d == 6 {
		t.Errorf("distinct = %v with %d rows (want rows+1, not the true 6)", d, rows)
	}
}

func TestBreakdownDistinctStaysWithoutSuppression(t *testing.T) {
	if d, _ := distinctOf(t, foldedBreakdown(), false); d != 6 {
		t.Errorf("distinct = %v (want the true 6)", d)
	}
}

func TestBreakdownDistinctWithoutOtherIsTheRowCount(t *testing.T) {
	bd := audit.Breakdown{Dim: audit.BreakdownStatusCode, Total: 100, Distinct: 1, Rows: []audit.BreakdownRow{row("200", 100)}}
	if d, rows := distinctOf(t, bd, true); rows != 1 || d != 1 {
		t.Errorf("distinct = %v with %d rows (want 1)", d, rows)
	}
}
