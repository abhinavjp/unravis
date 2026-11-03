package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
)

// Characterization of tracingSQLIISLanes: the lanes and warnings it returns for
// a fixed set of cases are written as JSON and compared with a golden file, so a
// refactor of the function can be shown to keep the output byte-identical.
// Regenerate only on purpose: UPDATE_TRACING_GOLDEN=1 go test -run TestTracingSQLIILanesGolden.

const tracingLanesGoldenFile = "testdata/tracing_lanes_golden.json"

// laneGoldenCase serves a fixed list of events through Events and Each, window
// and importer name filtered like a store (the window is inclusive at the end).
type laneGoldenCase struct {
	stubTraceCase
	rows []evidence.Event
}

func (c *laneGoldenCase) match(q casestore.EventQuery) []evidence.Event {
	names := map[string]bool{}
	for _, s := range q.Sources {
		names[s] = true
	}
	var out []evidence.Event
	for _, e := range c.rows {
		if len(names) > 0 && !names[e.Source] {
			continue
		}
		if e.Start.Before(q.Window.From) || e.Start.After(q.Window.To) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func (c *laneGoldenCase) Events(_ context.Context, q casestore.EventQuery) ([]evidence.Event, error) {
	c.queries = append(c.queries, q)
	out := c.match(q)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (c *laneGoldenCase) Each(_ context.Context, q casestore.EventQuery, fn func(evidence.Event) error) error {
	for _, e := range c.match(q) {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func goldenImport(id int64, importer, rule string, imported time.Time) casestore.ImportRecord {
	rec := stubImport(importer, tracingLaneT0.Add(-time.Hour), tracingLaneT0.Add(2*time.Hour))
	rec.ID = id
	rec.TZRule = rule
	rec.HostLabel = "web-a"
	rec.Imported = imported
	return rec
}

func goldenEvent(id, source, kind string, at time.Time, dur float64, sev string, attrs map[string]string) evidence.Event {
	d := dur
	e := evidence.Event{EvidenceID: id, Source: source, Kind: kind, Host: "web-a", Start: at, End: at.Add(time.Duration(dur) * time.Millisecond), Severity: sev, Attrs: attrs}
	if dur > 0 {
		e.DurationMs = &d
	}
	return e
}

type laneGoldenOut struct {
	Lanes    []tracingLane `json:"lanes"`
	Warnings []string      `json:"warnings"`
	Queries  []string      `json:"queries"`
}

func TestTracingSQLIILanesGolden(t *testing.T) {
	t0 := tracingLaneT0
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	axis := newTracingAxis(t0, t0.Add(time.Hour))
	ref := func(id, label string) tracingRef { return tracingRef{SourceID: id, Label: label} }

	mixedRows := []evidence.Event{
		goldenEvent("s1", "sql-xel", "slow-op", t0.Add(5*time.Minute), 1200, "warn", map[string]string{"wait_class": "lock"}),
		goldenEvent("s2", "sql-xel", "deadlock", t0.Add(6*time.Minute), 0, "error", map[string]string{"win32": "1205"}),
		goldenEvent("s3", "sql-xel", "slow-op", t0.Add(59*time.Minute+59*time.Second), 300, "", nil),
		goldenEvent("s4", "sql-xel", "slow-op", t0.Add(time.Hour), 10, "", nil),           // at End: half open, left out
		goldenEvent("s5", "sql-xel", "slow-op", t0.Add(-time.Minute), 10, "", nil),        // before the window
		goldenEvent("i1", "iis-w3c", "request", t0.Add(10*time.Minute), 80, "", map[string]string{"status": "200"}),
		goldenEvent("i2", "iis-w3c", "request", t0.Add(10*time.Minute), 5000, "", map[string]string{"status": "503"}),
		goldenEvent("i3", "iis-w3c", "request", t0.Add(30*time.Minute), 12, "", map[string]string{"status": "404"}),
		goldenEvent("h1", "iis-httperr", "httperr", t0.Add(20*time.Minute), 0, "", map[string]string{"reason": "other", "status": "503"}),
		goldenEvent("h2", "iis-httperr", "httperr", t0.Add(20*time.Minute), 0, "", map[string]string{"reason": "Not A Known Reason"}),
		goldenEvent("h3", "iis-httperr", "httperr", t0.Add(40*time.Minute), 0, "", nil),
	}
	mixedImports := []casestore.ImportRecord{
		goldenImport(1, "sql-xel", clock.RuleUTCField, now.Add(-3*time.Hour)),
		goldenImport(2, "iis-w3c", clock.RuleUTCField, now.Add(-2*time.Hour)),
		goldenImport(3, "iis-httperr", clock.RuleUTCField, now.Add(-time.Hour)),
		goldenImport(4, "mongo-stat", clock.RuleUTCField, now),
		func() casestore.ImportRecord {
			r := goldenImport(5, "sql-xel", clock.RuleUTCField, now)
			r.Status = "failed"
			return r
		}(),
	}
	selOf := func(pairs ...any) tracingSelection {
		sel := tracingSelection{Refs: []tracingRef{}, ImportOf: map[string]int64{}}
		for i := 0; i < len(pairs); i += 3 {
			id, label, imp := pairs[i].(string), pairs[i+1].(string), pairs[i+2].(int64)
			sel.Refs = append(sel.Refs, ref(id, label))
			sel.ImportOf[id] = imp
		}
		return sel
	}

	capRows := make([]evidence.Event, casestore.MaxEventQuery+10)
	for i := range capRows {
		capRows[i] = goldenEvent(fmt.Sprintf("c%d", i), "iis-w3c", "request", t0.Add(time.Duration(i)*time.Millisecond), 1, "", map[string]string{"status": "200"})
	}

	cases := []struct {
		name    string
		imports []casestore.ImportRecord
		rows    []evidence.Event
		sel     tracingSelection
		partial bool
	}{
		{"mixed", mixedImports, mixedRows,
			selOf("imp_1", "sql-xel", int64(1), "imp_2", "iis-w3c", int64(2), "imp_3", "iis-httperr", int64(3), "cs_9", "no file", int64(0), "imp_4", "mongo-stat", int64(4), "imp_5", "failed", int64(5)), false},
		{"sql-only", mixedImports, mixedRows, selOf("imp_1", "sql-xel", int64(1)), false},
		{"nothing-selected", mixedImports, mixedRows, selOf(), false},
		{"unknown-import-id", mixedImports, mixedRows, selOf("imp_77", "ghost", int64(77)), false},
		{"duplicate-import-ids", mixedImports, mixedRows, selOf("imp_2", "iis-w3c", int64(2), "cs_2", "same file again", int64(2)), false},
		{"ambiguous-two-files", append(append([]casestore.ImportRecord{}, mixedImports...), goldenImport(6, "iis-w3c", clock.RuleUTCField, now.Add(time.Hour))), mixedRows,
			selOf("imp_2", "iis-w3c", int64(2), "imp_6", "iis-w3c b", int64(6)), false},
		{"iis-clock-unchecked", []casestore.ImportRecord{
			goldenImport(1, "sql-xel", clock.RuleUTCField, now),
			goldenImport(2, "iis-w3c", "", now),
		}, mixedRows, selOf("imp_1", "sql-xel", int64(1), "imp_2", "iis-w3c", int64(2)), false},
		{"unchecked-file-in-valid-name", []casestore.ImportRecord{
			goldenImport(2, "iis-w3c", clock.RuleUTCField, now),
			goldenImport(6, "iis-w3c", "", now),
		}, mixedRows, selOf("imp_2", "iis-w3c", int64(2), "imp_6", "iis-w3c b", int64(6)), false},
		{"row-cap", []casestore.ImportRecord{goldenImport(2, "iis-w3c", clock.RuleUTCField, now)}, capRows,
			selOf("imp_2", "iis-w3c", int64(2)), false},
		{"scan-budget-partial", mixedImports, mixedRows, selOf("imp_1", "sql-xel", int64(1), "imp_2", "iis-w3c", int64(2)), true},
	}

	var sb strings.Builder
	sb.WriteString("{\n")
	for i, tc := range cases {
		if tc.partial {
			orig := tracingCountBuckets
			tracingCountBuckets = func(ctx context.Context, c casestore.Case, q casestore.BucketQuery) (casestore.BucketResult, error) {
				res, err := orig(ctx, c, q)
				res.Partial = true
				return res, err
			}
			t.Cleanup(func() { tracingCountBuckets = orig })
		}
		c := &laneGoldenCase{rows: tc.rows}
		c.imports = tc.imports
		lanes, warns, err := tracingSQLIISLanes(context.Background(), c, axis, tc.sel, now)
		if tc.partial {
			// restore right away so the other cases keep the real counter
			tracingCountBuckets = casestore.CountBuckets
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		out := laneGoldenOut{Lanes: lanes, Warnings: warns}
		for _, q := range c.queries {
			out.Queries = append(out.Queries, fmt.Sprintf("sources=%v limit=%d from=%s to=%s", q.Sources, q.Limit,
				q.Window.From.UTC().Format(time.RFC3339Nano), q.Window.To.UTC().Format(time.RFC3339Nano)))
		}
		raw, err := json.MarshalIndent(out, "  ", "  ")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if i > 0 {
			sb.WriteString(",\n")
		}
		name, _ := json.Marshal(tc.name)
		sb.WriteString("  " + string(name) + ": " + string(raw))
	}
	sb.WriteString("\n}\n")
	got := sb.String()

	if os.Getenv("UPDATE_TRACING_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(tracingLanesGoldenFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tracingLanesGoldenFile, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("golden file rewritten")
	}
	want, err := os.ReadFile(tracingLanesGoldenFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("tracing lanes output changed from %s (set UPDATE_TRACING_GOLDEN=1 only for an intended change)", tracingLanesGoldenFile)
		// show the first differing line
		wl, gl := strings.Split(strings.ReplaceAll(string(want), "\r\n", "\n"), "\n"), strings.Split(got, "\n")
		for i := 0; i < len(wl) && i < len(gl); i++ {
			if wl[i] != gl[i] {
				t.Errorf("first difference at line %d:\nwant %s\ngot  %s", i+1, wl[i], gl[i])
				break
			}
		}
	}
}
