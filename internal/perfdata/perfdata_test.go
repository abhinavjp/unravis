package perfdata

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

func tinyCfg(t *testing.T) Config {
	t.Helper()
	c, err := Preset("T")
	if err != nil {
		t.Fatal(err)
	}
	c.IISRows, c.SQLRows = 6000, 90
	c.Out = t.TempDir()
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPlanDeterministicAndSeedSensitive(t *testing.T) {
	c := tinyCfg(t)
	a, err := PlanDay(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PlanDay(c, 1)
	if a.IISMin != b.IISMin || a.SQLMin != b.SQLMin || a.IIS != b.IIS {
		t.Fatal("same config gave different plans")
	}
	c2 := c
	c2.Seed = 7
	d, _ := PlanDay(c2, 1)
	if a.IISMin == d.IISMin {
		t.Fatal("seed did not change the plan")
	}
	var sum int64
	p := BuildPlan(c)
	for _, dp := range p.Days() {
		sum += dp.IIS
	}
	if sum != c.IISRows {
		t.Fatalf("plan sums to %d, want %d", sum, c.IISRows)
	}
	if len(p.Outages) == 0 {
		t.Fatal("no outage planned")
	}
}

func TestWindowForMonths(t *testing.T) {
	c, err := Preset("S")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Start().Format("2006-01-02"); got != "2026-09-30" || c.Days() != 31 {
		t.Fatalf("S window starts %s with %d days", got, c.Days())
	}
	l, _ := Preset("L")
	if got := l.Start().Format("2006-01-02"); got != "2026-04-30" {
		t.Fatalf("L window starts %s", got)
	}
}

func TestGenerateMatchesManifest(t *testing.T) {
	if _, err := sealedfile.CurrentUser(); err != nil {
		t.Skip("the case store needs Windows:", err)
	}
	c := tinyCfg(t)
	ctx := context.Background()
	r1, err := Generate(ctx, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := r1.Manifest
	if m.Totals.IIS != c.IISRows || m.Totals.SQL != c.SQLRows {
		t.Fatalf("totals %+v", m.Totals)
	}
	// A second run with the same seed gives the same counts.
	c2 := c
	c2.Out = t.TempDir()
	r2, err := Generate(ctx, c2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PerDay) != len(r2.Manifest.PerDay) {
		t.Fatal("day count differs")
	}
	for i := range m.PerDay {
		if m.PerDay[i] != r2.Manifest.PerDay[i] {
			t.Fatalf("day %d differs between runs", i)
		}
	}
	// Counts in the store equal the manifest, day by day.
	prot, _ := sealedfile.CurrentUser()
	st, err := sqlite.New(sqlite.Options{Root: CaseRoot(c.Out), Protector: prot, MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cs, err := st.Open(ctx, casestore.CaseID(m.CaseID))
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, lane := range []struct{ name, imp string }{{LaneIIS, ImporterIIS}, {LaneSQL, ImporterSQL}} {
		res, err := casestore.CountBuckets(ctx, cs, casestore.BucketQuery{
			AxisStart: m.WindowStart, From: m.WindowStart, To: m.WindowEnd, WidthMs: 86400000,
			Sources: []string{lane.imp}, CountErrors: lane.name == LaneIIS,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := map[int]int64{}
		var errs int64
		for _, b := range res.Counts {
			got[b.Index] += b.Count
			errs += b.Errors
		}
		var wantErrs int64
		for i, d := range m.PerDay {
			want := d.IIS
			if lane.name == LaneSQL {
				want = d.SQL
			}
			if got[i] != want {
				t.Fatalf("%s day %d: store %d, manifest %d", lane.name, i, got[i], want)
			}
			wantErrs += d.Errors5xxIS
		}
		if lane.name == LaneIIS && errs != wantErrs {
			t.Fatalf("5xx: store %d, manifest %d", errs, wantErrs)
		}
	}
	// Outage hours hold no rows from either lane.
	for _, o := range m.Outages {
		for _, name := range []string{LaneIIS, LaneSQL} {
			n, _, err := m.Sum(name, o.Start, o.End)
			if err != nil || n != 0 {
				t.Fatalf("outage %v has %d %s rows (%v)", o.Start, n, name, err)
			}
		}
		evs, err := cs.Events(ctx, casestore.EventQuery{Window: importer.Window{From: o.Start, To: o.End.Add(-time.Millisecond)}, Limit: 10})
		if err != nil || len(evs) != 0 {
			t.Fatalf("outage window holds %d events (%v)", len(evs), err)
		}
	}
}
