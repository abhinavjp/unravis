package store

import (
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

func TestNewProvenanceFillsScanClassAndBudget(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	indexed := &profile{source: audit.Source{ID: "a"}}
	pv := newProvenance(indexed, "current", start, start.Add(15*time.Minute), "", nil)
	if pv.SourceID != "a" || pv.Window != "current" || pv.QueryMode != audit.QueryModeExact || pv.ScanClass != audit.ScanNarrow {
		t.Fatalf("pv = %+v", pv)
	}
	if pv.MaxTimeMs != audit.InteractiveQueryBudget.Milliseconds() || pv.DocsExamined != nil || pv.Margin != nil {
		t.Fatalf("pv = %+v", pv)
	}
	if pv.Completeness != audit.RanToEnd {
		t.Fatalf("a fresh live provenance is %q, want ran_to_end", pv.Completeness)
	}
	// A window over 24 h on an exact scan is broad, and so is an unindexed source.
	if got := newProvenance(indexed, "current", start, start.Add(48*time.Hour), audit.QueryModeExact, nil).ScanClass; got != audit.ScanBroad {
		t.Fatalf("48 h exact scan = %q", got)
	}
	unindexed := &profile{source: audit.Source{ID: "b"}, indexWarning: "no index on RequestInitiatedAt"}
	if got := newProvenance(unindexed, "current", start, start.Add(time.Hour), audit.QueryModeExact, nil).ScanClass; got != audit.ScanBroad {
		t.Fatalf("unindexed exact scan = %q", got)
	}
	// Hint mode bounds the scan by _id even without an index warning being clear.
	m := &audit.IDMargin{MarginMs: 70000, SampleSize: 1000}
	hint := newProvenance(unindexed, "current", start, start.Add(time.Hour), audit.QueryModeHint, m)
	if hint.ScanClass != audit.ScanNarrow || hint.QueryMode != audit.QueryModeHint || hint.Margin != m || hint.Completeness != audit.HintBounded {
		t.Fatalf("hint pv = %+v", hint)
	}
	// A margin is only carried in hint mode.
	if pv := newProvenance(indexed, "current", start, start.Add(time.Hour), audit.QueryModeExact, m); pv.Margin != nil {
		t.Fatal("an exact result must not carry a margin")
	}
}

func TestFinishProvenanceMapsCodes(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	p := &profile{source: audit.Source{ID: "a"}}
	cases := []struct {
		code, reason, completeness string
	}{
		{"", "", audit.RanToEnd},
		{"query_timeout", "timeout", audit.Partial},
		{"rowcap", "rowcap", audit.Partial},
		{"query_failed", "error", audit.Partial},
		{"connection_failed", "error", audit.Partial},
	}
	for _, c := range cases {
		pv := newProvenance(p, "page", start, start.Add(time.Hour), "", nil)
		finishProvenance(&pv, 7, 42, 1500*time.Millisecond, c.code)
		if pv.PartialReason != c.reason || pv.Completeness != c.completeness {
			t.Errorf("code %q: reason=%q completeness=%q", c.code, pv.PartialReason, pv.Completeness)
		}
		if pv.DocsReturned != 7 || pv.SampleSize != 42 || pv.ElapsedMs != 1500 || pv.DocsExamined != nil {
			t.Errorf("code %q: pv = %+v", c.code, pv)
		}
		if pv.Completeness == audit.Complete {
			t.Errorf("code %q: a live result must never be complete", c.code)
		}
	}
	// A hint result stays hint even when it is cut short.
	hint := newProvenance(p, "page", start, start.Add(time.Hour), audit.QueryModeHint, &audit.IDMargin{})
	finishProvenance(&hint, 1, 1, time.Second, "query_timeout")
	if hint.Completeness != audit.HintBounded || hint.PartialReason != "timeout" {
		t.Fatalf("hint = %+v", hint)
	}
}
