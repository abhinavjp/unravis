package server

import (
	"context"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
)

// The database lane's last fallback (BL-013, R-05/R-07): when the case holds no
// saved live day and no Query Store file for the window, the lane can still show
// the database time the API calls recorded, per step, with reason code "api-seen".
// This reads the LIVE audit source only (the same source the API lane reads); the
// screen never starts a database read. The screen's sentence comes from dbState
// (P-07 maps api-seen to state "api"), so no new copy is written here.
//
// Precedence: saved live day (held-sql / layered-sql) > Query Store file (qs-file)
// > api-seen > nothing. This file only fills the last case and never displaces a
// better source.

const tracingReasonAPISseen = "api-seen"

// apiSeenBuckets reads the stored database time per step from the selected audit
// sources and maps each step onto its axis cell. The repository (P-38) merges the
// selected sources internally and sums BusyMs/Calls per step, so this calls SQLBusy
// ONCE with the axis window and the selected audit source ids (DATA-02); there is
// no per-source loop here. sourceIDs are the audit ids the trace selected — the
// same ids the audit lane reads, not SQL/IIS import ids; empty leaves the repository
// reading every connected source.
//
// recorded is false when the source does not store the number at all, or when the
// window cannot be read in one bounded call (a step length outside the allowed
// set, too long a span, or too many steps): the lane then keeps its previous
// state. A read error is returned unchanged (fail-closed) for the caller to map.
// MaxMs and AvgMs stay nil: the stored number is a per-step timed total, not a
// per-call distribution, so no maximum or average is invented from it.
func apiSeenBuckets(ctx context.Context, reader audit.SQLBusyReader, sourceIDs []string, axis tracingAxis) (map[int]*tracingBucket, bool, error) {
	if reader == nil || axis.WidthMs <= 0 {
		return nil, false, nil
	}
	in := audit.SQLBusyInput{Start: axis.Start, End: axis.End, BucketSeconds: int(axis.WidthMs / 1000), SourceIDs: sourceIDs}
	// One bounded call only. A window the frozen SQLBusy contract cannot answer
	// (its step set is 60/300/600/900 s, at most 31 days and 120 steps) is left
	// unread rather than turned into a request error: the lane stays as it was.
	if err := in.Validate(); err != nil {
		return nil, false, nil
	}
	res, err := reader.SQLBusy(ctx, in)
	if err != nil {
		return nil, false, err
	}
	if !res.Recorded {
		return nil, false, nil
	}
	cells := map[int]*tracingBucket{}
	for _, bin := range res.Bins {
		k := int((bin.Start.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
		if k < 0 || k >= axis.Buckets {
			continue
		}
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
		}
		b := cells[k]
		if b == nil {
			b = &tracingBucket{Start: from, End: to, Clipped: clipped, Complete: true}
			cells[k] = b
		}
		// The stored number is a timed total: every counted call had a timing.
		busy := bin.BusyMs
		if b.BusyMs == nil {
			b.BusyMs = &busy
		} else {
			*b.BusyMs += busy
		}
		b.Count += bin.Calls
		b.TimedCount += bin.Calls
	}
	return cells, true, nil
}

// tracingAPISseenLane is the read-only api-seen fallback, run AFTER the held /
// layered decision and the qs-file fallback. It fills the lane only when the SQL
// lane would otherwise be empty: no bucket already carries data. A nil reader (the
// repository does not implement audit.SQLBusyReader) is treated as not-recorded
// and leaves the lane untouched, mirroring readAuditLane's nil-reader guard.
//
// The lane is stamped as a live read (origin "live", consistency "live-non-snapshot",
// asOf the read time), like the audit lane, because api-seen reads the live audit
// source rather than a stored case copy. now is the read clock (a parameter so a
// test can fix it). sourceIDs are the selected audit source ids, forwarded to the
// reader so only those sources contribute (DATA-02). A read error is returned
// unchanged with the lane untouched.
func tracingAPISseenLane(ctx context.Context, reader audit.SQLBusyReader, sourceIDs []string, axis tracingAxis, lane tracingLane, now func() time.Time) (tracingLane, error) {
	if lane.Key != calltrace.LaneSQL || lane.Disabled != "" || lane.State == calltrace.StateClockUnchecked ||
		lane.ReasonCode == tracingReasonHeldSQL || lane.ReasonCode == tracingReasonLayeredSQL || lane.ReasonCode == tracingReasonQSFile {
		return lane, nil
	}
	if reader == nil {
		return lane, nil
	}
	// api-seen is the LAST fallback: a bucket that already carries data wins.
	for _, b := range lane.Buckets {
		if b.Count != 0 || b.BusyMs != nil || b.TimedCount != 0 || b.AvgMs != nil || b.MaxMs != nil {
			return lane, nil
		}
	}
	cells, recorded, err := apiSeenBuckets(ctx, reader, sourceIDs, axis)
	if err != nil {
		return lane, err
	}
	// Recorded with no bins is "recorded, no calls in window": the lane stays
	// empty, not an error. Not recorded leaves the lane as it was too.
	if !recorded || len(cells) == 0 {
		return lane, nil
	}
	lane.Buckets = tracingBuckets(axis, nil, nil)
	lane.Scanned = 0
	lane.BlockSeconds = 0
	lane.Origin, lane.Consistency = tracingOriginLive, tracingConsLive
	lane.AsOf = now().UTC()
	lane.ReasonCode, lane.Reason = tracingReasonAPISseen, ""
	lane.State = calltrace.StateEvents
	lane.Complete, lane.Truncated = true, false
	for i := range lane.Buckets {
		k := int((lane.Buckets[i].Start.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
		if b := cells[k]; b != nil {
			lane.Buckets[i] = *b
			lane.Scanned += b.Count
		}
	}
	return lane, nil
}
