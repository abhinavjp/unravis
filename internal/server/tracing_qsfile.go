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

func qsFileExecutions(e evidence.Event) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(e.Attrs["executions"]), 10, 64)
	return n, err == nil && n >= 0
}

func qsFileInterval(e evidence.Event, axis tracingAxis) bool {
	return e.Kind == sqlserver.KindQueryInterval && !e.Start.Before(axis.Start) && e.Start.Before(axis.End)
}

// qsFileBuckets keeps each interval's whole busy time at its start. The web
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
		k := int((e.Start.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
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
	events, capped, timedOut, err := readLaneEvents(ctx, c, axis, []string{sqlserver.QSName})
	if err != nil {
		return lane, err
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
	if !lane.Complete {
		for i := range lane.Buckets {
			lane.Buckets[i].Complete = false
		}
	}
	return lane, nil
}
