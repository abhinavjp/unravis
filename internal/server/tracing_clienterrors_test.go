package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// DATA-12: the IIS lane counts 4xx on every bucket, so a bucket with none says 0 (counted,
// none found), not nothing. A lane that does not count 4xx (sql) leaves the key out.
func TestTracingIISLaneSaysZeroClientErrorsWhereItCounted(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		ok := map[string]string{"status": "200"}
		early := tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 2, time.Minute, ok)
		late := tracingKindEvents("request", tracingLaneT0.Add(45*time.Minute), 1, time.Minute, map[string]string{"status": "404"})
		iis := seedTracingImport(t, e.store, id, "iis-w3c", "b.log", append(early, late...))
		sql := seedTracingImport(t, e.store, id, "sql-xel", "a.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(time.Minute), 1, time.Minute, nil))
		out, raw := tracingPost(t, e, id, tracingLaneBody(tracingImportID(sql.ID), tracingImportID(iis.ID)))

		l := tracingLaneOf(t, out, "iis")
		if len(l.Buckets) == 0 {
			t.Fatal("iis lane has no buckets")
		}
		var total int64
		for _, b := range l.Buckets {
			if b.ClientErrors == nil {
				t.Fatalf("bucket %v has no clientErrors although 4xx was counted: %+v", b.Start, b)
			}
			total += *b.ClientErrors
			if b.Start.Before(tracingLaneT0.Add(30*time.Minute)) && *b.ClientErrors != 0 {
				t.Errorf("bucket %v counted %d 4xx, want 0", b.Start, *b.ClientErrors)
			}
		}
		if total != 1 {
			t.Errorf("4xx total = %d, want 1", total)
		}
		if !strings.Contains(raw, `"clientErrors":0`) {
			t.Errorf("the answer never says clientErrors:0: %s", raw)
		}
		for _, b := range tracingLaneOf(t, out, "sql").Buckets {
			if b.ClientErrors != nil {
				t.Errorf("sql lane does not count 4xx but sent clientErrors: %+v", b)
			}
		}
	})
}

// DATA-12: cells the count never reached (it ran out of time) stay without clientErrors, so
// "not counted" is not shown as 0.
func TestTracingIISLaneLeavesClientErrorsOutOfUncountedCells(t *testing.T) {
	orig := tracingCountBuckets
	t.Cleanup(func() { tracingCountBuckets = orig })
	doneTo := tracingLaneT0.Add(30 * time.Minute)
	tracingCountBuckets = func(ctx context.Context, c casestore.Case, q casestore.BucketQuery) (casestore.BucketResult, error) {
		res, err := casestore.CountBuckets(ctx, c, q)
		if err != nil || len(q.Sources) == 0 || q.Sources[0] != "iis-w3c" {
			return res, err
		}
		kept := res.Counts[:0]
		res.Total = 0
		for _, bc := range res.Counts {
			at := q.AxisStart.Add(time.Duration(bc.Index*int(q.WidthMs)) * time.Millisecond)
			if at.Before(doneTo) {
				kept = append(kept, bc)
				res.Total += bc.Count
			}
		}
		res.Counts = kept
		res.Partial, res.TimedOut, res.DoneTo = true, true, doneTo
		return res, nil
	}
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		ok := map[string]string{"status": "200"}
		evs := append(tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 1, time.Minute, ok),
			tracingKindEvents("request", tracingLaneT0.Add(45*time.Minute), 1, time.Minute, ok)...)
		iis := seedTracingImport(t, e.store, id, "iis-w3c", "b.log", evs)
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(iis.ID)))
		var counted, uncounted int
		for _, b := range tracingLaneOf(t, out, "iis").Buckets {
			switch {
			case b.Start.Before(doneTo) && b.ClientErrors == nil:
				t.Errorf("counted bucket %v has no clientErrors", b.Start)
			case !b.Start.Before(doneTo) && b.ClientErrors != nil:
				t.Errorf("uncounted bucket %v says clientErrors %d", b.Start, *b.ClientErrors)
			}
			if b.Start.Before(doneTo) {
				counted++
			} else {
				uncounted++
			}
		}
		if counted == 0 || uncounted == 0 {
			t.Fatalf("test did not cover both sides of DoneTo: %d before, %d after", counted, uncounted)
		}
	})
}
