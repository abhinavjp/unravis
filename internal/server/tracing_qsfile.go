package server

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/sqlserver"
)

const (
	tracingReasonQSFile = "qs-file"
	tracingQSFileMsg    = "Drawn from the Query Store export imported into this case on the date shown. Each bar is the statements run in that interval; the line is their average time."
)

// qsFileLookback is how far before the window the file read starts. The store
// lists events by their start time, and Query Store blocks are at most one day
// long (1440 minutes), so a block that began earlier than this cannot reach the
// window.
const qsFileLookback = 24 * time.Hour

func qsFileExecutions(e evidence.Event) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(e.Attrs["executions"]), 10, 64)
	return n, err == nil && n >= 0
}

// qsFileInterval reports whether a Query Store interval overlaps the window:
// it starts before the window ends and either starts inside the window or is
// still running at the window start. An interval that began earlier and ended at
// or before the window start is outside.
func qsFileInterval(e evidence.Event, axis tracingAxis) bool {
	if e.Kind != sqlserver.KindQueryInterval || !e.Start.Before(axis.End) {
		return false
	}
	return !e.Start.Before(axis.Start) || e.End.After(axis.Start)
}

// qsFileBucketIndex is the bucket an interval is drawn in: the one holding its
// start, or the first bucket when it began before the window. Each interval is
// drawn once, in one bucket, so nothing is counted twice; its whole totals stay
// there (they include time before the window, which the bucket's Clipped flag
// reports).
func qsFileBucketIndex(e evidence.Event, axis tracingAxis) int {
	start := e.Start
	if start.Before(axis.Start) {
		start = axis.Start
	}
	return int((start.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
}

// qsFileBuckets keeps each interval's whole busy time at its start (at the
// window start for an interval that began before it). The web
// model uses BlockSeconds to spread that total; it is not spread here.
// A skipped row leaves its cell incomplete, even when that cell has no totals.
func qsFileBuckets(events []evidence.Event, axis tracingAxis) (buckets map[int]*tracingBucket, blockSeconds int) {
	buckets = map[int]*tracingBucket{}
	lengths := map[int]int{}
	missingDuration := map[int]bool{}
	for _, e := range events {
		if !qsFileInterval(e, axis) || axis.WidthMs <= 0 {
			continue
		}
		k := qsFileBucketIndex(e, axis)
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
		}
		if e.Start.Before(axis.Start) {
			clipped = true
		}
		if seconds := int(e.End.Sub(e.Start) / time.Second); seconds > 0 {
			lengths[seconds]++
		}
		b := buckets[k]
		if b == nil {
			b = &tracingBucket{Start: from, End: to, Clipped: clipped, Complete: true}
			buckets[k] = b
		}
		executions, ok := qsFileExecutions(e)
		if !ok {
			b.Complete = false
			continue
		}
		b.Count += executions
		if mx, err := strconv.ParseFloat(e.Attrs["max_duration_ms"], 64); err == nil && mx >= 0 && !math.IsNaN(mx) && !math.IsInf(mx, 0) {
			if b.MaxMs == nil || mx > *b.MaxMs {
				b.MaxMs = &mx
			}
		}
		if e.DurationMs == nil || *e.DurationMs < 0 || math.IsNaN(*e.DurationMs) || math.IsInf(*e.DurationMs, 0) {
			missingDuration[k], b.Complete = true, false
			continue
		}
		busy := float64(executions) * *e.DurationMs
		if b.BusyMs == nil {
			b.BusyMs = &busy
		} else {
			*b.BusyMs += busy
		}
		b.TimedCount += executions
	}
	most := 0
	for seconds, n := range lengths {
		// A tie uses the shorter interval, independent of map iteration order.
		if n > most || n == most && seconds < blockSeconds {
			blockSeconds, most = seconds, n
		}
	}
	for k, b := range buckets {
		if missingDuration[k] {
			// A partial timing is not the busy time of the whole bucket.
			b.BusyMs = nil
		} else if b.Count > 0 && b.BusyMs != nil {
			avg := *b.BusyMs / float64(b.Count)
			b.AvgMs = &avg
		}
	}
	return buckets, blockSeconds
}

// tracingQSFileLane is a read-only fallback AFTER the held/layered decision.
// Events has attributes and the same cap as /events; no Each or bucket scan is
// used for these totals. Like readLaneEvents, it filters by importer name, not
// import id: the store's event type cannot distinguish files with that name.
func tracingQSFileLane(ctx context.Context, c casestore.Case, axis tracingAxis, sel tracingSelection, lane tracingLane) (tracingLane, error) {
	if lane.Key != calltrace.LaneSQL || lane.Disabled != "" || lane.State == calltrace.StateClockUnchecked || lane.ReasonCode == tracingReasonHeldSQL || lane.ReasonCode == tracingReasonLayeredSQL {
		return lane, nil
	}
	picked := map[int64]bool{}
	for _, ref := range sel.Refs {
		if id := sel.ImportOf[ref.SourceID]; id != 0 {
			picked[id] = true
		}
	}
	if len(picked) == 0 {
		return lane, nil
	}
	recs, err := c.Imports(ctx)
	if err != nil {
		return lane, err
	}
	var asOf time.Time
	found := false
	for _, rec := range recs {
		if !picked[rec.ID] || rec.Importer != sqlserver.QSName || !traceRuleValidated(rec) || rec.Status != casestore.StatusOK && rec.Status != "" {
			continue
		}
		found = true
		if rec.Imported.After(asOf) {
			asOf = rec.Imported.UTC()
		}
	}
	if !found {
		return lane, nil
	}
	// Window read: only this one sets the cap/timeout flags the lane reports.
	events, capped, timedOut, err := readLaneEvents(ctx, c, axis, []string{sqlserver.QSName})
	if err != nil {
		return lane, err
	}
	// Early read: intervals that began before the window (up to qsFileLookback) and may run into it.
	earlyAxis := axis
	earlyAxis.Start = axis.Start.Add(-qsFileLookback)
	earlyAxis.End = axis.Start
	early, earlyCapped, earlyTimedOut, err := readLaneEvents(ctx, c, earlyAxis, []string{sqlserver.QSName})
	if err != nil {
		return lane, err
	}
	for _, e := range early {
		if e.End.After(axis.Start) {
			events = append(events, e)
		}
	}
	intervals, skipped := 0, 0
	for _, e := range events {
		if !qsFileInterval(e, axis) {
			continue
		}
		intervals++
		if _, ok := qsFileExecutions(e); !ok {
			skipped++
		}
	}
	if intervals == 0 {
		// A capped or timed-out read may have run out before reaching the window: say so, not "no data".
		if capped {
			lane.Complete, lane.Truncated = false, true
			lane.Reason = strings.SplitN(tracingRowCapMsg, ";", 2)[0] + "; Query Store figures from the imported file may be missing."
		}
		if timedOut {
			lane.Complete, lane.Truncated = false, true
			lane.Reason = strings.TrimSpace(lane.Reason + " " + tracingTimeoutMsg)
		}
		if earlyCapped || earlyTimedOut {
			lane.Complete = false
			lane.Reason = strings.TrimSpace(lane.Reason + " An interval that began before this window may be missing.")
		}
		return lane, nil
	}
	cells, blockSeconds := qsFileBuckets(events, axis)
	lane.Buckets = tracingBuckets(axis, nil, nil)
	lane.Scanned = 0
	lane.BlockSeconds = blockSeconds
	lane.Origin, lane.AsOf, lane.Consistency = tracingOriginCase, asOf, tracingConsStored
	lane.ReasonCode, lane.Reason = tracingReasonQSFile, tracingQSFileMsg
	for i := range lane.Buckets {
		k := int((lane.Buckets[i].Start.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
		if b := cells[k]; b != nil {
			lane.Buckets[i] = *b
			lane.Scanned += b.Count
			if !b.Complete {
				lane.Complete = false
			}
		}
	}
	if skipped > 0 {
		lane.Complete = false
		lane.Reason += fmt.Sprintf(" %d Query Store rows were skipped; their execution counts were not recorded.", skipped)
	}
	if capped {
		lane.Complete, lane.Truncated = false, true
		// Reuse rowcap's wording without its exact-count claim: these totals
		// come from the capped rows, unlike bucketLane's store aggregates.
		lane.Reason += " " + strings.SplitN(tracingRowCapMsg, ";", 2)[0] + "; the file totals may be too low."
	}
	if timedOut {
		lane.Complete, lane.Truncated = false, true
		lane.Reason += " " + tracingTimeoutMsg
	}
	if earlyCapped || earlyTimedOut {
		lane.Complete = false
		lane.Reason += " An interval that began before this window may be missing."
	}
	if !lane.Complete {
		for i := range lane.Buckets {
			lane.Buckets[i].Complete = false
		}
	}
	return lane, nil
}
