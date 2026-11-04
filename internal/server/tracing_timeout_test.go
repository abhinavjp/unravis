package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

// A lane whose bucket count ran out of time is answered partial with reason
// "timeout": the cells before DoneTo stay complete, the later ones are marked
// incomplete, and the other lane and the request itself are untouched (PF-B-01).
func TestTracingLaneTimeoutIsPartialAndIsolated(t *testing.T) {
	orig := tracingCountBuckets
	t.Cleanup(func() { tracingCountBuckets = orig })
	doneTo := tracingLaneT0.Add(30 * time.Minute)
	tracingCountBuckets = func(ctx context.Context, c casestore.Case, q casestore.BucketQuery) (casestore.BucketResult, error) {
		res, err := casestore.CountBuckets(ctx, c, q)
		if err != nil || len(q.Sources) == 0 || q.Sources[0] != "sql-xel" {
			return res, err
		}
		// The sql lane's count "timed out" at 10:30: keep only the earlier cells.
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
		evs := func(kind string) []evidence.Event {
			early := tracingKindEvents(kind, tracingLaneT0.Add(time.Minute), 1, time.Minute, nil)
			late := tracingKindEvents(kind, tracingLaneT0.Add(45*time.Minute), 1, time.Minute, nil)
			return append(early, late...)
		}
		s := seedTracingImport(t, e.store, id, "sql-xel", "a.xel", evs("slow-op"))
		i := seedTracingImport(t, e.store, id, "iis-w3c", "b.log", evs("request"))
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(s.ID), tracingImportID(i.ID)))

		sql := tracingLaneOf(t, out, "sql")
		if sql.Complete || !sql.Truncated || sql.ReasonCode != "timeout" || sql.Reason != tracingTimeoutMsg {
			t.Fatalf("sql lane = complete %v truncated %v %q %q", sql.Complete, sql.Truncated, sql.ReasonCode, sql.Reason)
		}
		var early, late int
		for _, b := range sql.Buckets {
			switch {
			case b.Start.Before(doneTo) && !b.Complete:
				t.Errorf("bucket %v before DoneTo is marked incomplete", b.Start)
			case !b.Start.Before(doneTo) && b.Complete:
				t.Errorf("bucket %v from DoneTo on is marked complete", b.Start)
			}
			if b.Start.Before(doneTo) {
				early += int(b.Count)
			} else {
				late += int(b.Count)
			}
		}
		if early != 1 || late != 0 {
			t.Errorf("counts before DoneTo %d, after %d; want 1 and 0", early, late)
		}

		iis := tracingLaneOf(t, out, "iis")
		if !iis.Complete || iis.Truncated || iis.ReasonCode != "" || tracingBucketSum(iis) != 2 {
			t.Errorf("iis lane was affected: complete %v truncated %v %q sum %d", iis.Complete, iis.Truncated, iis.ReasonCode, tracingBucketSum(iis))
		}
		if !out.Truncated {
			t.Error("top-level truncated is false for a partial lane")
		}
	})
}

// budgetCase answers Events like the SQLite store does when the time budget
// passes: the events read so far plus ErrScanBudget.
type budgetCase struct {
	casestore.Case
	got []evidence.Event
	err error
}

func (b *budgetCase) Events(_ context.Context, q casestore.EventQuery) ([]evidence.Event, error) {
	if q.MaxScanTime != tracingLaneBudget {
		return nil, errors.New("the lane read set no time budget")
	}
	return b.got, b.err
}

// readLaneEvents turns ErrScanBudget into a timed-out read that keeps the
// events so far, but never hides a dropped request or another store error.
func TestReadLaneEventsTimeout(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	got := tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 3, time.Minute, nil)

	evs, capped, timedOut, err := readLaneEvents(context.Background(), &budgetCase{got: got, err: casestore.ErrScanBudget}, axis, []string{"iis-w3c"})
	if err != nil || !timedOut || capped || len(evs) != 3 {
		t.Fatalf("timeout: %d events, capped %v, timedOut %v, err %v", len(evs), capped, timedOut, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, timedOut, err = readLaneEvents(ctx, &budgetCase{err: casestore.ErrScanBudget}, axis, []string{"iis-w3c"}); err == nil || timedOut {
		t.Errorf("a dropped request must stay an error, got timedOut %v err %v", timedOut, err)
	}

	boom := errors.New("boom")
	if _, _, timedOut, err = readLaneEvents(context.Background(), &budgetCase{err: boom}, axis, []string{"iis-w3c"}); !errors.Is(err, boom) || timedOut {
		t.Errorf("a store error must stay an error, got timedOut %v err %v", timedOut, err)
	}
}
