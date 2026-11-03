package casestore_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

var bucketT0 = time.Date(2002, 2, 2, 0, 0, 0, 0, time.UTC)

func TestBucketQueryCheck(t *testing.T) {
	valid := casestore.BucketQuery{
		AxisStart: bucketT0, From: bucketT0, To: bucketT0.Add(time.Second), WidthMs: 100,
		Sources: []string{"src"},
	}
	manySources := make([]string, 201)
	for i := range manySources {
		manySources[i] = "s"
	}
	cases := []struct {
		name string
		q    casestore.BucketQuery
		bad  bool
	}{
		{"zero times", casestore.BucketQuery{}, true},
		{"to equal from", func() casestore.BucketQuery { q := valid; q.To = q.From; return q }(), true},
		{"from before axis start", func() casestore.BucketQuery {
			q := valid
			q.AxisStart = bucketT0.Add(time.Hour)
			q.To = bucketT0.Add(2 * time.Hour)
			return q
		}(), true},
		{"width zero", func() casestore.BucketQuery { q := valid; q.WidthMs = 0; return q }(), true},
		{"2001 cells", func() casestore.BucketQuery {
			q := valid
			q.WidthMs = 1
			q.To = q.From.Add(2001 * time.Millisecond)
			return q
		}(), true},
		{"no sources", func() casestore.BucketQuery { q := valid; q.Sources = nil; return q }(), true},
		{"201 sources", func() casestore.BucketQuery { q := valid; q.Sources = manySources; return q }(), true},
		{"valid", valid, false},
	}
	for _, c := range cases {
		err := c.q.Check()
		if c.bad {
			if !errors.Is(err, casestore.ErrBadBucketQuery) {
				t.Errorf("%s: err=%v, want ErrBadBucketQuery", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: err=%v, want nil", c.name, err)
		}
	}
}

// genCase does not apply the query window itself (see series_test.go), which is
// what makes it a good double for the skip rule CountBucketsScan applies.
func TestCountBucketsScanHalfOpen(t *testing.T) {
	const widthMs = 1000
	from := bucketT0
	to := from.Add(1500 * time.Millisecond)
	times := []time.Time{
		from.Add(-time.Millisecond),                          // before From: not counted
		from,                                                 // cell 0
		from.Add(time.Duration(widthMs-1) * time.Millisecond), // cell 0
		from.Add(time.Duration(widthMs) * time.Millisecond),   // cell 1
		to,                                                    // at To: not counted
	}
	g := &genCase{n: len(times), make: func(i int) evidence.Event {
		return evidence.Event{Kind: "load", Source: "src", Start: times[i], Value: fl(1)}
	}}
	q := casestore.BucketQuery{AxisStart: from, From: from, To: to, WidthMs: widthMs, Sources: []string{"src"}}
	res, err := casestore.CountBucketsScan(context.Background(), g, q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 || res.Partial || res.Scanned != int64(len(times)) {
		t.Fatalf("total=%d partial=%v scanned=%d, want total 3, not partial, scanned %d", res.Total, res.Partial, res.Scanned, len(times))
	}
	want := []casestore.BucketCount{
		{Index: 0, Source: "src", Count: 2},
		{Index: 1, Source: "src", Count: 1},
	}
	if !reflect.DeepEqual(res.Counts, want) {
		t.Fatalf("counts = %+v, want %+v", res.Counts, want)
	}
}

// genCase implements only Each, so CountBuckets has no BucketCounter to find
// and falls back to CountBucketsScan: same result, one Each call.
func TestCountBucketsFallsBackToScan(t *testing.T) {
	g := &genCase{n: 25, make: func(i int) evidence.Event {
		return evidence.Event{Kind: "load", Source: "src", Start: bucketT0.Add(time.Duration(i) * time.Second), Value: fl(1)}
	}}
	q := casestore.BucketQuery{
		AxisStart: bucketT0, From: bucketT0, To: bucketT0.Add(25 * time.Second), WidthMs: 10_000, Sources: []string{"src"},
	}

	got, err := casestore.CountBuckets(context.Background(), g, q)
	if err != nil {
		t.Fatal(err)
	}
	if g.calls != 1 {
		t.Fatalf("calls = %d, want 1 (genCase has no BucketCounts)", g.calls)
	}
	want, err := casestore.CountBucketsScan(context.Background(), g, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CountBuckets = %+v, want %+v (CountBucketsScan)", got, want)
	}

	// The same fallback over a real store, built as memory_test.go does, gives
	// the same counts: the memory store does not implement BucketCounter either.
	t.Run("memory store", func(t *testing.T) {
		ctx := context.Background()
		store := casestore.NewMemoryWith(casestore.DefaultCaps, time.Now)
		info, err := store.Create(ctx, "buckets")
		if err != nil {
			t.Fatal(err)
		}
		c, err := store.Open(ctx, info.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()

		const n = 30
		var evs []evidence.Event
		for i := 0; i < n; i++ {
			v := float64(i)
			evs = append(evs, evidence.Event{
				EvidenceID: evidence.EventID(string(info.ID), "contract", "lbl", int64(i+2), 0),
				Source:     "contract", Kind: "load", Host: "web-a",
				Start: bucketT0.Add(time.Duration(i) * time.Second),
				End:   bucketT0.Add(time.Duration(i)*time.Second + time.Millisecond),
				Value: &v, Unit: "pct", Line: int64(i + 2), TZRule: "utc-field",
			})
		}
		run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			rep := importer.Report{Importer: "contract"}
			if _, err := io.Copy(io.Discard, r); err != nil {
				return rep, err
			}
			for _, e := range evs {
				rep.Rows++
				if err := emit(e); err != nil {
					return rep, err
				}
				rep.Emitted++
			}
			return rep, nil
		}
		if _, err := c.Import(ctx, casestore.ImportMeta{
			Importer: "contract", FileName: "f.txt", HostLabel: "web-a", TZRule: "utc-field",
			AckGating: true, AckText: "ack",
		}, strings.NewReader("x"), run); err != nil {
			t.Fatal(err)
		}

		mq := casestore.BucketQuery{
			AxisStart: bucketT0, From: bucketT0, To: bucketT0.Add(time.Duration(n) * time.Second),
			WidthMs: 10_000, Sources: []string{"contract"},
		}
		mgot, err := casestore.CountBuckets(ctx, c, mq)
		if err != nil {
			t.Fatal(err)
		}
		mwant, err := casestore.CountBucketsScan(ctx, c, mq)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(mgot, mwant) {
			t.Fatalf("CountBuckets = %+v, want %+v (scan)", mgot, mwant)
		}
		if mgot.Total != n {
			t.Fatalf("total = %d, want %d", mgot.Total, n)
		}
	})
}
