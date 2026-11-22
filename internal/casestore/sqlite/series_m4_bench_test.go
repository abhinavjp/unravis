package sqlite

// BL-430 measurement on the real SQLite case store (not a fixture): a 1M-row
// case, the series route's aggregation before (bucket asked by the client, up to
// 2 000 buckets, min/mean/max/count) and after (width in pixels, M4: first,
// min, max, last). Run:
//
//	go test ./internal/casestore/sqlite -run '^$' -bench SeriesM4 -benchtime 5x -timeout 30m
//
// Set UNRAVIS_BENCH_ROWS to change the row count (default 1 000 000). It also
// carries a correctness check on the same store: the spikes planted in the case
// are present at every width.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// m4Rows is the number of request events of the synthetic case.
func m4Rows() int {
	if v, err := strconv.Atoi(os.Getenv("UNRAVIS_BENCH_ROWS")); err == nil && v > 0 {
		return v
	}
	return 1_000_000
}

const (
	m4Step      = 86 * time.Millisecond // 1M rows span about a day
	m4SpikeHigh = 98_765.0
	m4SpikeLow  = 0.25
)

// m4Spike returns the row numbers of the planted slow call and the planted fast call.
func m4Spikes(n int) (hi, lo int) { return n * 37 / 100, n * 71 / 100 }

// m4Run streams n request events (a duration in ms, a status class) without holding them.
func m4Run(id string, n int) casestore.RunFunc {
	hi, lo := m4Spikes(n)
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: testImporter, KnownGaps: []string{"synthetic benchmark data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for i := 0; i < n; i++ {
			d := 20 + float64((i*7919)%180) // 20..199 ms, a repeatable scatter
			switch i {
			case hi:
				d = m4SpikeHigh
			case lo:
				d = m4SpikeLow
			}
			line := int64(i + 2)
			e := evidence.Event{
				EvidenceID: evidence.EventID(id, testImporter, "bench", line, 0),
				Source:     testImporter, Kind: "request", Host: "web-a", DurationMs: &d, Unit: "ms",
				Line: line, TZRule: "utc-field", Start: testBase.Add(time.Duration(i) * m4Step),
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
}

func m4Case(b *testing.B, n int) casestore.Case {
	b.Helper()
	root := filepath.Join(b.TempDir(), "cases")
	s, err := New(Options{Root: root, Protector: storetest.RandomProtector(), MinFreeBytes: -1})
	if err != nil {
		b.Fatal(err)
	}
	info, err := s.Create(context.Background(), "BL-430 bench")
	if err != nil {
		b.Fatal(err)
	}
	c, err := s.Open(context.Background(), info.ID)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = c.Close() })
	began := time.Now()
	if _, err := c.Import(context.Background(), tMeta("bench.csv"), tBody("bench"), m4Run(string(info.ID), n)); err != nil {
		b.Fatal(err)
	}
	b.Logf("loaded %d rows into the real store in %s", n, time.Since(began).Round(time.Millisecond))
	return c
}

// benchRaw is the baseline nobody should ship: every row of the case read through
// Each and answered as [t ms, duration] pairs, what a chart would carry with no
// downsampling at all.
func benchRaw(b *testing.B, c casestore.Case, from, to time.Time) {
	var bytesOut, rows int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pts := make([][2]float64, 0, 1<<20)
		err := c.Each(context.Background(), casestore.EventQuery{Window: importer.Window{From: from, To: to}, Kinds: []string{"request"}}, func(ev evidence.Event) error {
			pts = append(pts, [2]float64{float64(ev.Start.UnixMilli()), *ev.DurationMs})
			return nil
		})
		if err != nil && !errors.Is(err, casestore.ErrScanBudget) { // a stopped scan keeps what it read
			b.Fatal(err)
		}
		raw, _ := json.Marshal(pts)
		bytesOut, rows = len(raw), len(pts)
	}
	b.ReportMetric(float64(bytesOut), "payload-bytes")
	b.ReportMetric(float64(rows), "points")
	b.ReportMetric(float64(rows*2), "numbers")
}

func BenchmarkSeriesM4(b *testing.B) {
	n := m4Rows()
	c := m4Case(b, n)
	b.Run("raw/before-every-row", func(b *testing.B) {
		benchRaw(b, c, testBase, testBase.Add(time.Duration(n)*m4Step))
	})
	span := time.Duration(n) * m4Step
	hi, lo := m4Spikes(n)
	for _, sel := range []struct{ name, sel string }{
		{"minmeanmax", "kind=request;field=duration"},
		{"with-p95", "kind=request;field=duration;q=95"},
	} {
		for _, run := range []struct {
			name   string
			bucket int64 // before: what a client asks for (the most the old route allows is 2000 buckets)
			width  int   // after: pixels
		}{
			{"before-2000-buckets", int64(span/time.Millisecond)/casestore.MaxSeriesBuckets + 1, 0},
			{"before-240-buckets", int64(span/time.Millisecond)/240 + 1, 0},
			{"after-m4-width-600", 0, 600},
			{"after-m4-width-1500", 0, 1500},
			{"after-m4-width-3000", 0, 3000},
		} {
			b.Run(sel.name+"/"+run.name, func(b *testing.B) {
				q := casestore.SeriesQuery{From: testBase, To: testBase.Add(span), BucketMs: run.bucket, Width: run.width, Sel: []string{sel.sel}}
				var res casestore.SeriesResult
				var bytesOut int
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var err error
					if res, err = casestore.Series(context.Background(), c, q); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				raw, _ := json.Marshal(res)
				bytesOut = len(raw)
				pts := res.Series[0].Points
				per := 4 // before: min, mean, max, count are four numbers per point
				if run.width > 0 {
					per = 6 // after: those four plus first and last
				}
				var peak, low = math.Inf(-1), math.Inf(1)
				for _, p := range pts {
					peak, low = math.Max(peak, p.Max), math.Min(low, p.Min)
				}
				// The case store stops a scan at its budget (MaxScanTime, 10 s) and answers partial: say so, and
				// check the planted spikes only when the whole case was read.
				b.ReportMetric(float64(res.Scanned), "rows-scanned")
				if !res.Partial && (res.Scanned != int64(n) || peak != m4SpikeHigh || low != m4SpikeLow) {
					b.Fatalf("scanned=%d peak=%v low=%v (spikes at rows %d and %d)", res.Scanned, peak, low, hi, lo)
				}
				if res.Partial {
					b.Logf("partial answer: the scan budget stopped it after %d of %d rows", res.Scanned, n)
				}
				b.ReportMetric(float64(bytesOut), "payload-bytes")
				b.ReportMetric(float64(len(pts)), "points")
				b.ReportMetric(float64(len(pts)*per), "numbers")
			})
		}
	}
}
