package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// demoAnalysisDigest hashes an analysis without its provenance, so a digest
// taken before provenance existed still matches after it is added.
func demoAnalysisDigest(t *testing.T, analysis Analysis) string {
	t.Helper()
	raw, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "provenance")
	canonical, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// preProvenanceDemoDigest is the digest of NewDemo's analysis for demoQuery
// at 2026-10-22 12:00 UTC, taken before this change. The demo's output may
// gain provenance, but nothing else.
const preProvenanceDemoDigest = "5d8fd2e6abe1a74572c4d699e997b164daf7c6ef7529478ce283b5a41f9f9d4b"

func TestNewDemoOutputUnchangedExceptProvenance(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	analysis, err := NewDemo(now).Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	got := demoAnalysisDigest(t, analysis)
	if got != preProvenanceDemoDigest {
		t.Fatalf("demo analysis changed beyond provenance: digest %s", got)
	}
}

// findDemoCohort returns the comparison for one source's API cohort.
func findDemoCohort(t *testing.T, analysis Analysis, sourceID, controller string) Comparison {
	t.Helper()
	for _, c := range analysis.APIs {
		if c.SourceID == sourceID && c.Controller == controller {
			return c
		}
	}
	t.Fatalf("no %s cohort for %s", controller, sourceID)
	return Comparison{}
}

// findDemoProvenance returns the provenance for one source and window.
func findDemoProvenance(t *testing.T, list []Provenance, sourceID, window string) Provenance {
	t.Helper()
	for _, p := range list {
		if p.SourceID == sourceID && p.Window == window {
			return p
		}
	}
	t.Fatalf("no %s provenance for %s in %#v", window, sourceID, list)
	return Provenance{}
}

// TestDemoExactModeIncludesLateWrittenRow is the positive demo of §8.2: the
// late-written Payroll.Run row is initiated in the reference window and read by
// an exact scan, which runs to the end and reports docsExamined.
func TestDemoExactModeIncludesLateWrittenRow(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := newDemoWithLateWrite(now)
	q := demoQuery(now)
	q.QueryMode = QueryModeExact
	analysis, err := d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	cohort := findDemoCohort(t, analysis, "demo-west", "Payroll")
	if cohort.ReferenceEvents != 126 {
		t.Fatalf("exact reference count should include the late row: got %d, want 126", cohort.ReferenceEvents)
	}
	p := findDemoProvenance(t, analysis.Provenance, "demo-west", "reference")
	if p.QueryMode != QueryModeExact || p.Completeness != Complete {
		t.Fatalf("exact reference window: mode %q completeness %q", p.QueryMode, p.Completeness)
	}
	if p.DocsExamined == nil {
		t.Fatal("exact mode on the demo must report docsExamined")
	}
	if p.ScanClass != ScanNarrow {
		t.Fatalf("demo scan class = %q, want narrow", p.ScanClass)
	}
}

// TestDemoHintModeMissesLateRowAndWarns is the negative demo of §8.2: the hint
// bound is calibrated on the rows written close to their request, so the row
// written ten minutes after the reference window falls outside it.
func TestDemoHintModeMissesLateRowAndWarns(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := newDemoWithLateWrite(now)
	q := demoQuery(now)
	q.QueryMode = QueryModeHint
	analysis, err := d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	cohort := findDemoCohort(t, analysis, "demo-west", "Payroll")
	if cohort.ReferenceEvents != 125 {
		t.Fatalf("hint reference count should exclude the late row: got %d, want 125", cohort.ReferenceEvents)
	}
	p := findDemoProvenance(t, analysis.Provenance, "demo-west", "reference")
	if p.QueryMode != QueryModeHint || p.Completeness != HintBounded {
		t.Fatalf("hint reference window: mode %q completeness %q", p.QueryMode, p.Completeness)
	}
	if p.Margin == nil {
		t.Fatal("hint mode must carry its margin")
	}
	if !strings.Contains(p.Margin.Label, "rows outside") || !strings.Contains(p.Margin.Label, "would be missed") {
		t.Fatalf("hint label does not warn: %q", p.Margin.Label)
	}
	if p.Margin.Label != HintLabel(*p.Margin) {
		t.Fatalf("margin label must come from HintLabel: %q", p.Margin.Label)
	}
	if p.Margin.SampleSize != 1500 {
		t.Fatalf("calibration sample = %d rows, want the 1500 generated rows", p.Margin.SampleSize)
	}
}

// TestDemoHintFallsBackToExactWithNote covers a demo too small to calibrate:
// the query still runs, in exact mode, and the provenance says why.
func TestDemoHintFallsBackToExactWithNote(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now).(*demoRepository)
	d.records = d.records[:10]
	q := demoQuery(now)
	q.QueryMode = QueryModeHint
	analysis, err := d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	p := findDemoProvenance(t, analysis.Provenance, "demo-west", "current")
	if p.QueryMode != QueryModeExact || p.Margin != nil {
		t.Fatalf("fallback should run exact without a margin: mode %q margin %v", p.QueryMode, p.Margin)
	}
	if !strings.Contains(p.Note, "Hint mode was not available") || !strings.Contains(p.Note, "used exact mode") {
		t.Fatalf("fallback note = %q", p.Note)
	}
}

// TestDemoProvenanceOnEveryResponse checks that each demo response carries
// provenance: two analysis windows per source, and one entry per source for the
// page, breakdown and activity responses.
func TestDemoProvenanceOnEveryResponse(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now).(*demoRepository)
	ctx := context.Background()
	q := demoQuery(now)

	analysis, err := d.Analyze(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Provenance) != 4 {
		t.Fatalf("analysis provenance = %d entries, want 2 per source", len(analysis.Provenance))
	}

	page, err := d.Requests(ctx, PageInput{Query: q, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Provenance) != 2 {
		t.Fatalf("page provenance = %d entries, want 1 per source", len(page.Provenance))
	}

	breakdown, err := d.Breakdown(ctx, BreakdownInput{Query: q, Dim: "statusCode", Top: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown.Provenance) != 2 {
		t.Fatalf("breakdown provenance = %d entries, want 1 per source", len(breakdown.Provenance))
	}

	activity, err := d.Activity(ctx, ActivityInput{SourceIDs: q.SourceIDs, Start: q.Start, End: q.End, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(activity.Provenance) != 2 {
		t.Fatalf("activity provenance = %d entries, want 1 per source", len(activity.Provenance))
	}
}
