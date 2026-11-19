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
	tracingQSFileMsg    = "Drawn from the Query Store export imported into this case on the date shown. Each interval's statements are spread over the minutes it ran; the line is their average time."
)

// qsFileLookback is how far before the window the file read starts. The store
// lists events by their start time, and Query Store blocks are at most one day
// long (1440 minutes), so a block that began earlier than this cannot reach the
// window.
const qsFileLookback = 24 * time.Hour

// qsFileEarlySteps are the look-backs the early read tries, longest first. The
// store lists events oldest first, so a read that hits the 50,000-row cap keeps
// the OLDEST rows and drops the ones nearest the window, which are the only ones
// that can still run into it. When a read is capped, a shorter one is tried. One
// hour covers every Query Store block length except one day; 15 minutes covers
// the common default.
var qsFileEarlySteps = []time.Duration{qsFileLookback, time.Hour, 15 * time.Minute}

// qsFileEarlyRead reads the intervals that began before the window and still run
// into it (BL-037). It starts with the full day look-back and, while a read is
// capped, retries with a shorter one (qsFileEarlySteps). A capped read still
// counts: the rows it holds that began before the next, shorter read starts are
// kept when they end after the window start, and every row it holds is measured,
// so a long interval seen only there still counts toward missed. The shorter read
// covers the rest, so no row is kept twice.
//
// missed is true when an interval may still be absent: the last read was capped or
// ran out of time, or a shorter look-back was used and an interval LONGER than it
// was seen (one exactly as long starts at the read's first instant and cannot
// reach the window, so it is not counted). capped says the row cap was involved,
// so the lane can name it.
func qsFileEarlyRead(ctx context.Context, c casestore.Case, axis tracingAxis, inWindow []evidence.Event) (early []evidence.Event, capped, missed bool, err error) {
	var longest time.Duration
	note := func(evs []evidence.Event) {
		for _, e := range evs {
			if e.Kind == sqlserver.KindQueryInterval {
				longest = max(longest, e.End.Sub(e.Start))
			}
		}
	}
	note(inWindow)
	for i, back := range qsFileEarlySteps {
		earlyAxis := axis
		earlyAxis.Start = axis.Start.Add(-back)
		earlyAxis.End = axis.Start
		rows, rowsCapped, timedOut, rerr := readLaneEvents(ctx, c, earlyAxis, []string{sqlserver.QSName})
		if rerr != nil {
			return nil, false, false, rerr
		}
		note(rows)
		if rowsCapped && i+1 < len(qsFileEarlySteps) {
			capped = true
			nextFrom := axis.Start.Add(-qsFileEarlySteps[i+1])
			for _, e := range rows {
				if e.Start.Before(nextFrom) && e.End.After(axis.Start) {
					early = append(early, e)
				}
			}
			continue
		}
		for _, e := range rows {
			if e.End.After(axis.Start) {
				early = append(early, e)
			}
		}
		capped = capped || rowsCapped
		shortened := back < qsFileLookback
		missed = rowsCapped || timedOut || shortened && longest > back
		if !missed {
			capped = false
		}
		return early, capped, missed, nil
	}
	return early, capped, missed, nil
}

// qsFileEarlyMsg is the note for an early read that may have missed an interval.
func qsFileEarlyMsg(capped bool) string {
	if capped {
		return "The read before this window hit the 50,000-row limit, so an interval that began before this window may be missing."
	}
	return "An interval that began before this window may be missing."
}

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

// qsFileBucketIndex is the bucket that holds the start of an interval's part in
// the window: the bucket of its start, or the first bucket when it began before
// the window.
func qsFileBucketIndex(e evidence.Event, axis tracingAxis) int {
	start := e.Start
	if start.Before(axis.Start) {
		start = axis.Start
	}
	return int((start.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
}

// qsFileShare is one bucket's part of an interval: the share of its length that
// falls in the bucket, and that share of its executions.
type qsFileShare struct {
	k          int
	from, to   time.Time
	clipped    bool
	frac       float64
	executions int64
}

// qsFileShares spreads an interval over the buckets it runs through (BL-037,
// DATA-10). Each bucket gets the share of the interval's length that falls in it,
// and the same share of its executions and busy time. The part before or after
// the window is left out, since it is not this window's time. Executions are
// whole numbers: each bucket gets round(n x end share) - round(n x start share),
// so the parts always add up to the rounded in-window total. An interval with no
// usable length (end not after start) stays whole in the bucket of its start.
func qsFileShares(e evidence.Event, axis tracingAxis, executions int64) []qsFileShare {
	first := qsFileBucketIndex(e, axis)
	length := e.End.Sub(e.Start)
	if length <= 0 {
		from, to, clipped, ok := axis.Bucket(first)
		if !ok {
			return nil
		}
		return []qsFileShare{{k: first, from: from, to: to, clipped: clipped, frac: 1, executions: executions}}
	}
	share := func(t time.Time) float64 {
		f := float64(t.Sub(e.Start)) / float64(length)
		return math.Min(1, math.Max(0, f))
	}
	var out []qsFileShare
	for k := first; k < axis.Buckets; k++ {
		from, to, clipped, ok := axis.Bucket(k)
		if !ok || !from.Before(e.End) {
			break
		}
		lo, hi := share(from), share(to)
		if hi <= lo {
			continue
		}
		n := int64(math.Round(float64(executions)*hi)) - int64(math.Round(float64(executions)*lo))
		out = append(out, qsFileShare{k: k, from: from, to: to, clipped: clipped, frac: hi - lo, executions: n})
	}
	return out
}

// qsFileBuckets spreads each interval's executions and busy time over the buckets
// it runs through (qsFileShares), so a 15-minute block on a 1-minute axis fills 15
// buckets instead of one. The slowest execution is kept in every bucket the
// interval touches, since it cannot be placed more exactly. A skipped row leaves
// each cell it touches incomplete, even when that cell has no totals.
func qsFileBuckets(events []evidence.Event, axis tracingAxis) (buckets map[int]*tracingBucket, blockSeconds int) {
	buckets = map[int]*tracingBucket{}
	lengths := map[int]int{}
	missingDuration := map[int]bool{}
	// timedShare is each bucket's unrounded share of timed executions, so the
	// average is busy / share and is not skewed by rounding the counts.
	timedShare := map[int]float64{}
	for _, e := range events {
		if !qsFileInterval(e, axis) || axis.WidthMs <= 0 {
			continue
		}
		if seconds := int(e.End.Sub(e.Start) / time.Second); seconds > 0 {
			lengths[seconds]++
		}
		executions, execOK := qsFileExecutions(e)
		mx, err := strconv.ParseFloat(e.Attrs["max_duration_ms"], 64)
		mxOK := err == nil && mx >= 0 && !math.IsNaN(mx) && !math.IsInf(mx, 0)
		durOK := e.DurationMs != nil && *e.DurationMs >= 0 && !math.IsNaN(*e.DurationMs) && !math.IsInf(*e.DurationMs, 0)
		for _, sh := range qsFileShares(e, axis, executions) {
			b := buckets[sh.k]
			if b == nil {
				b = &tracingBucket{Start: sh.from, End: sh.to, Clipped: sh.clipped, Complete: true}
				buckets[sh.k] = b
			}
			if !execOK {
				b.Complete = false
				continue
			}
			b.Count += sh.executions
			if mxOK && (b.MaxMs == nil || mx > *b.MaxMs) {
				v := mx
				b.MaxMs = &v
			}
			if !durOK {
				missingDuration[sh.k], b.Complete = true, false
				continue
			}
			busy := float64(executions) * *e.DurationMs * sh.frac
			if b.BusyMs == nil {
				b.BusyMs = &busy
			} else {
				*b.BusyMs += busy
			}
			b.TimedCount += sh.executions
			timedShare[sh.k] += float64(executions) * sh.frac
		}
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
		} else if share := timedShare[k]; b.Count > 0 && b.BusyMs != nil && share > 0 {
			avg := *b.BusyMs / share
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
	if lane.Key != calltrace.LaneSQL || lane.Disabled != "" || lane.State == calltrace.StateClockUnchecked || lane.ReasonCode == tracingReasonHeldSQL || lane.ReasonCode == tracingReasonLayeredSQL || lane.ReasonCode == tracingReasonBlackbox {
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
	early, earlyCapped, earlyMissed, err := qsFileEarlyRead(ctx, c, axis, events)
	if err != nil {
		return lane, err
	}
	events = append(events, early...)
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
		// The note is added to the lane's reason, never put in its place: a Query Store state lane (qs-off and the
		// like) keeps its reason code, so its text must still say the same thing (BL-039).
		if capped {
			lane.Complete, lane.Truncated = false, true
			lane.Reason = strings.TrimSpace(lane.Reason + " " + strings.SplitN(tracingRowCapMsg, ";", 2)[0] + "; Query Store figures from the imported file may be missing.")
		}
		if timedOut {
			lane.Complete, lane.Truncated = false, true
			lane.Reason = strings.TrimSpace(lane.Reason + " " + tracingTimeoutMsg)
		}
		if earlyMissed {
			lane.Complete = false
			lane.Reason = strings.TrimSpace(lane.Reason + " " + qsFileEarlyMsg(earlyCapped))
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
	if earlyMissed {
		lane.Complete = false
		lane.Reason += " " + qsFileEarlyMsg(earlyCapped)
	}
	if !lane.Complete {
		for i := range lane.Buckets {
			lane.Buckets[i].Complete = false
		}
	}
	return lane, nil
}
