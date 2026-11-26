//go:build windows

package sqlite

// W-34 (RV-DAT-3, RV-DAT-4) measurement on the real SQLite case store: what a
// person waits for on the first view of an old, large case. Skipped unless
// UNRAVIS_SCALE=1. Synthetic data only. Run:
//
//	UNRAVIS_SCALE=1 UNRAVIS_SCALE_SIZES=5000000 go test ./internal/casestore/sqlite -run FirstViewMeasure -v -timeout 60m
//
// It reports, for a case of the given size:
//   - EVENT STATS: the first eventStats call after the case is opened (the
//     GROUP BY over the whole table) and the next call (cached);
//   - OLD CASE: a case without the series-key indexes, a series query sent the
//     moment the case is opened (it waits for the build), and the build time.

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestFirstViewMeasure(t *testing.T) {
	if os.Getenv("UNRAVIS_SCALE") != "1" {
		t.Skip("set UNRAVIS_SCALE=1 to run the first-view measurement")
	}
	sizes := []int{500_000}
	if v := os.Getenv("UNRAVIS_SCALE_SIZES"); v != "" {
		sizes = nil
		for _, f := range splitComma(v) {
			n, _ := strconv.Atoi(f)
			sizes = append(sizes, n)
		}
	}
	for _, n := range sizes {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			s, _ := newTestStore(t, func(o *Options) { o.MaxFileRows = 20_000_000 })
			info := mustCreate(t, s, "first view")
			c := mustOpen(t, s, info.ID)
			began := time.Now()
			if _, err := c.Import(context.Background(), tMeta("scale.csv"), tBody("scale"), scaleRun(c, "scale", n)); err != nil {
				t.Fatal(err)
			}
			t.Logf("[%d] IMPORT %v", n, time.Since(began).Round(time.Millisecond))
			checkpoint(t, c)

			// RV-DAT-4: eventStats on a freshly opened case.
			c = reopen(t, s, c)
			ctx := context.Background()
			began = time.Now()
			st := c.eventStats(ctx, c.db)
			first := time.Since(began)
			began = time.Now()
			c.eventStats(ctx, c.db)
			t.Logf("[%d] EVENT STATS first=%v cached=%v total=%d kinds=%d", n, first.Round(time.Millisecond), time.Since(began).Round(time.Microsecond), st.total, len(st.kinds))

			// RV-DAT-3: an old case (no series-key indexes), queried at once.
			for _, ix := range []string{"ev_kind_counter", "ev_kind_metric"} {
				if _, err := c.db.Exec("DROP INDEX " + ix); err != nil {
					t.Fatal(err)
				}
			}
			checkpoint(t, c)
			c = reopen(t, s, c)
			began = time.Now()
			firstQ, res := timeSeries(t, c, n, scaleQueries[0].sel)
			t.Logf("[%d] OLD CASE first series query sent at open=%v attrIdx=%d count=%d partial=%v (since open %v)", n, firstQ.Round(time.Millisecond), c.attrIdx.Load(), res.Series[0].Count, res.Partial, time.Since(began).Round(time.Millisecond))
			secondQ, _ := timeSeries(t, c, n, scaleQueries[0].sel)
			t.Logf("[%d] OLD CASE second query=%v", n, secondQ.Round(time.Millisecond))
			_ = c.Close()
		})
	}
}
