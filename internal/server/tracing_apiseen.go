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
// Precedence (W12-03): saved live day (held-sql / layered-sql) > Query Store
// file (qs-file) > Blackbox sampling (blackbox) > api-seen > nothing. Query
// Store duration evidence for the selected database outranks machine-wide
// batch throughput; this file only fills the last case and never displaces a
// better source.

const tracingReasonAPISseen = "api-seen"

// apiSeenStatus is what apiSeenBuckets found for the window, so the caller can
// tell an unsupported window (one the frozen SQLBusy contract cannot answer in a
// single bounded call) apart from a source that simply stores no number.
type apiSeenStatus int

const (
	// apiSeenNotRecorded: no reader, an unusable axis, or the source stores no
	// database time at all. The lane keeps its previous state.
	apiSeenNotRecorded apiSeenStatus = iota
	// apiSeenRecorded: one bounded read returned stored per-step database time.
	apiSeenRecorded
	// apiSeenUnsupported: the window is outside the SQLBusy contract (a step length
	// outside 60/300/600/900 s, a span over 31 days, or more than 120 steps). No
	// read was made; the lane says so with an actionable reason instead of staying
	// silently empty.
	apiSeenUnsupported
)

// apiSeenBuckets reads the stored database time per step from the selected audit
// sources and maps each step onto its axis cell. The repository (P-38) merges the
// selected sources internally and sums BusyMs/Calls per step, so this calls SQLBusy
// ONCE with the axis window and the selected audit source ids (DATA-02); there is
// no per-source loop here. sourceIDs are the audit ids the trace selected — the
// same ids the audit lane reads, not SQL/IIS import ids; empty leaves the repository
// reading every connected source.
//
// The status tells the caller why there are no cells: apiSeenNotRecorded when the
// source does not store the number at all (or there is no reader / usable axis),
// and apiSeenUnsupported when the window cannot be read in one bounded call (a
// step length outside the allowed set, too long a span, or too many steps). The
// unsupported case makes NO SQLBusy call and returns NO error, so the lane can
// stamp an actionable reason instead of staying silently empty. A read error is
// returned unchanged (fail-closed) for the caller to map. MaxMs and AvgMs stay
// nil: the stored number is a per-step timed total, not a per-call distribution,
// so no maximum or average is invented from it.
func apiSeenBuckets(ctx context.Context, reader audit.SQLBusyReader, sourceIDs []string, axis tracingAxis) (map[int]*tracingBucket, apiSeenStatus, error) {
	if reader == nil || axis.WidthMs <= 0 {
		return nil, apiSeenNotRecorded, nil
	}
	in := audit.SQLBusyInput{Start: axis.Start, End: axis.End, BucketSeconds: int(axis.WidthMs / 1000), SourceIDs: sourceIDs}
	// One bounded call only. A window the frozen SQLBusy contract cannot answer
	// (its step set is 60/300/600/900 s, at most 31 days and 120 steps) is left
	// unread rather than turned into a request error: it is reported as an
	// unsupported window so the lane can say so, not silently empty.
	if err := in.Validate(); err != nil {
		return nil, apiSeenUnsupported, nil
	}
	res, err := reader.SQLBusy(ctx, in)
	if err != nil {
		return nil, apiSeenNotRecorded, err
	}
	if !res.Recorded {
		return nil, apiSeenNotRecorded, nil
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
	return cells, apiSeenRecorded, nil
}

// tracingAPISseenLane is the read-only api-seen fallback, run AFTER the held /
// layered decision and the qs-file fallback. It fills the lane only when the SQL
// lane would otherwise be empty: no bucket already carries data. A lane that
// already carries an actionable empty reason — held-sql, layered-sql, qs-file,
// not-imported or import-skipped — is returned unchanged (the reader is not
// called), so api-seen never displaces a better source or overwrites why a listed
// lane holds nothing. A nil reader (the repository does not implement
// audit.SQLBusyReader) is treated as not-recorded and leaves the lane untouched,
// mirroring readAuditLane's nil-reader guard.
//
// An unsupported window (one the frozen SQLBusy contract cannot answer in a
// single bounded call) is stamped with the existing actionable broad-scan reason,
// Complete false and state incomplete, mirroring the audit lane; no read is made
// and no live stamp or bucket fill is claimed. The web already renders broad-scan
// as its own "choose a shorter window" note, so no new copy is written here.
//
// The lane is stamped as a live read (origin "live", consistency "live-non-snapshot",
// asOf the read time), like the audit lane, because api-seen reads the live audit
// source rather than a stored case copy. now is the read clock (a parameter so a
// test can fix it). sourceIDs are the selected audit source ids, forwarded to the
// reader so only those sources contribute (DATA-02). A read error is returned
// unchanged with the lane untouched.
func tracingAPISseenLane(ctx context.Context, reader audit.SQLBusyReader, sourceIDs []string, axis tracingAxis, lane tracingLane, now func() time.Time) (tracingLane, error) {
	if lane.Key != calltrace.LaneSQL || lane.Disabled != "" || lane.State == calltrace.StateClockUnchecked ||
		lane.ReasonCode == tracingReasonHeldSQL || lane.ReasonCode == tracingReasonLayeredSQL || lane.ReasonCode == tracingReasonQSFile ||
		lane.ReasonCode == tracingReasonNotImported || lane.ReasonCode == tracingReasonSkipped || lane.ReasonCode == tracingReasonBlackbox {
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
	cells, status, err := apiSeenBuckets(ctx, reader, sourceIDs, axis)
	if err != nil {
		return lane, err
	}
	// An unsupported window is not "not recorded": the frozen SQLBusy contract
	// cannot answer it in one bounded call. Say so with the EXISTING actionable
	// broad-scan reason, incomplete, mirroring the audit lane, so the web renders
	// its own "choose a shorter window" note. No read was made, so no live stamp
	// (origin/asOf) and no filled buckets: the empty axis grid stays as it was.
	if status == apiSeenUnsupported {
		lane.ReasonCode, lane.Reason = auditCodeBroadScan, tracingAuditBroadMsg
		lane.Complete = false
		lane.State = calltrace.StateIncomplete
		return lane, nil
	}
	// Recorded with no bins is "recorded, no calls in window": the lane stays
	// empty, not an error. Not recorded leaves the lane as it was too.
	if status != apiSeenRecorded || len(cells) == 0 {
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
