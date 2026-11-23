package sqlite

// BL-535 on the real SQLite case store: events of one millisecond come back from Each in evidence_id order (the events table is
// keyed by it), which has nothing to do with the order in the file. The M4 first and last of such a bucket must follow the source
// line, not the order the store returns the rows.

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func TestSeriesM4SameMillisecondFollowsTheSourceLine(t *testing.T) {
	const n = 12
	s, err := New(Options{Root: filepath.Join(t.TempDir(), "cases"), Protector: storetest.RandomProtector(), MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Create(context.Background(), "BL-535")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: testImporter, KnownGaps: []string{"synthetic"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		// Twelve events at one instant. Line k carries duration 10*k, so the first is 10 and the last is 120.
		for k := int64(1); k <= n; k++ {
			d := float64(10 * k)
			e := evidence.Event{
				EvidenceID: evidence.EventID(string(info.ID), testImporter, "tie", k, 0),
				Source:     testImporter, Kind: "request", Host: "web-a", DurationMs: &d, Unit: "ms",
				Line: k, TZRule: "utc-field", Start: testBase,
				Attrs: map[string]string{"status": "200"},
			}
			e.End = e.Start
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	if _, err := c.Import(context.Background(), tMeta("tie.csv"), tBody("tie"), run); err != nil {
		t.Fatal(err)
	}
	res, err := casestore.Series(context.Background(), c, casestore.SeriesQuery{From: testBase, To: testBase.Add(1000e6), Width: 1, Sel: []string{"kind=request;field=duration"}})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Series[0].Points[0]
	if p.Count != n || p.First == nil || p.Last == nil || *p.First != 10 || *p.Last != 10*n {
		var f, l float64
		if p.First != nil {
			f = *p.First
		}
		if p.Last != nil {
			l = *p.Last
		}
		t.Fatalf("count=%d first=%v last=%v, want first 10 and last %d", p.Count, f, l, 10*n)
	}
}
