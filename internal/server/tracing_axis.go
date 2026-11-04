package server

import "time"

// The tracing time axis (ruling R6-02). One axis per request, in UTC, all in
// whole milliseconds since the Unix epoch: the grid starts on the hour at or
// before the window start, ends on the hour at or after the window end, and is
// never shifted to the window start. There is no wall clock, no local time
// zone and no floating point here, so a daylight-saving change cannot move a
// bucket edge. The window is half open: [start, end).

const (
	hourMs int64 = 3600000
	// tracingMaxBuckets is the bucket bound of the contract. It equals
	// audit.MaxTimeBuckets by value, kept local so the ladder reads on its own.
	tracingMaxBuckets int64 = 120
)

// shortRungs serve spans up to 24 h; longRungs serve longer spans.
var shortRungs = []int64{60000, 300000, 600000, 900000}
var longRungs = []int64{3600000, 7200000, 14400000, 21600000, 28800000, 43200000, 86400000, 172800000}

// allRungs is every allowed bucket width in order: 60 s, 300 s, 600 s, 900 s,
// 1 h, 2 h, 4 h, 6 h, 8 h, 12 h, 24 h, 48 h. (48 h serves windows over
// 120 days, up to the 186-day cap of the tracing route.)
var allRungs = append(append([]int64{}, shortRungs...), longRungs...)

// floorMs rounds ms down to a multiple of step (toward negative infinity, so
// it is right before 1970 too). step must be positive.
func floorMs(ms, step int64) int64 {
	q := ms / step
	if ms%step != 0 && ms < 0 {
		q--
	}
	return q * step
}

// ceilMs rounds ms up to a multiple of step.
func ceilMs(ms, step int64) int64 {
	f := floorMs(ms, step)
	if f == ms {
		return f
	}
	return f + step
}

// ceilDiv is a/b rounded up, for a >= 0 and b > 0.
func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// ladderWidth is the bucket width for the raw span of the window. A span up to
// 24 h uses the short rungs, a longer one the long rungs; the first rung that
// gives at most 120 buckets wins (48 h if none does).
func ladderWidth(spanMs int64) int64 {
	if spanMs <= 24*hourMs {
		for _, w := range shortRungs {
			if ceilDiv(spanMs, w) <= tracingMaxBuckets {
				return w
			}
		}
	}
	for _, w := range longRungs {
		if ceilDiv(spanMs, w) <= tracingMaxBuckets {
			return w
		}
	}
	return longRungs[len(longRungs)-1]
}

// alignedRung is the first rung of allRungs that covers the hour-aligned axis
// in at most 120 buckets (the last rung if none does).
func alignedRung(axisStartMs, axisEndMs int64) int64 {
	span := axisEndMs - axisStartMs
	for _, w := range allRungs {
		if ceilDiv(span, w) <= tracingMaxBuckets {
			return w
		}
	}
	return allRungs[len(allRungs)-1]
}

// tracingAxis is the one grid of a request. Start and End are the window (at
// millisecond precision, UTC); AxisStart and AxisEnd the hour-aligned frame.
type tracingAxis struct {
	Start, End, AxisStart, AxisEnd time.Time
	WidthMs                        int64
	Buckets                        int
}

// msUp is t in whole milliseconds, rounded up when t carries a finer fraction,
// so that a window end is never cut short.
func msUp(t time.Time) int64 {
	ms := t.UnixMilli()
	if t.Nanosecond()%1000000 != 0 {
		ms++
	}
	return ms
}

// newTracingAxis builds the grid for the window [start, end). The window is
// taken to the millisecond: the start rounds down, the end rounds up.
func newTracingAxis(start, end time.Time) tracingAxis {
	startMs, endMs := start.UnixMilli(), msUp(end)
	axisStartMs := floorMs(startMs, hourMs)
	axisEndMs := ceilMs(endMs, hourMs)
	width := ladderWidth(endMs - startMs)
	if aligned := alignedRung(axisStartMs, axisEndMs); aligned > width {
		width = aligned
	}
	return tracingAxis{
		Start:     time.UnixMilli(startMs).UTC(),
		End:       time.UnixMilli(endMs).UTC(),
		AxisStart: time.UnixMilli(axisStartMs).UTC(),
		AxisEnd:   time.UnixMilli(axisEndMs).UTC(),
		WidthMs:   width,
		Buckets:   int(ceilDiv(axisEndMs-axisStartMs, width)),
	}
}

// Bucket is cell k of the grid, [AxisStart + k*W, AxisStart + (k+1)*W), cut to
// the window [Start, End). ok is false when the cut is empty (the cell lies
// wholly before Start or at or after End, or k is outside the grid). clipped is
// true when the cut is smaller than the cell.
func (a tracingAxis) Bucket(k int) (from, to time.Time, clipped, ok bool) {
	if k < 0 || k >= a.Buckets || a.WidthMs <= 0 {
		return time.Time{}, time.Time{}, false, false
	}
	cellStart := a.AxisStart.UnixMilli() + int64(k)*a.WidthMs
	cellEnd := cellStart + a.WidthMs
	lo, hi := cellStart, cellEnd
	if s := a.Start.UnixMilli(); s > lo {
		lo = s
	}
	if e := a.End.UnixMilli(); e < hi {
		hi = e
	}
	if hi <= lo {
		return time.Time{}, time.Time{}, false, false
	}
	return time.UnixMilli(lo).UTC(), time.UnixMilli(hi).UTC(), lo != cellStart || hi != cellEnd, true
}
