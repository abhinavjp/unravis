package casestore

// M4 downsampling (BL-430). A chart column of W pixels cannot show more than
// four values per column without overdraw: the first, the lowest, the highest
// and the last value of the column's time span. Keeping exactly those four per
// column draws the same line as the raw points while the payload is bounded by
// 4 x W numbers, however many rows the case holds. Peaks are never lost,
// because the highest and lowest of every column are kept exactly; the
// percentile and count figures stay computed on the raw events, never on the
// four kept values.
//
// The aggregators in this package and in the Blackbox series already keep min,
// max, sum and count per bucket. M4Edges adds the missing two, first and last,
// and M4BucketMs ties the bucket count to the pixel width.

// MaxM4Width is the widest pixel column count honoured; wider requests use it.
// It equals MaxSeriesBuckets, so a request with a width never meets the
// too-many-buckets refusal.
const MaxM4Width = MaxSeriesBuckets

// M4Edges holds the first and the last value of one bucket by time. Two values
// with the same time are told apart by their sequence (AddSeq: for events the
// source line), so the result does not depend on how the store orders ties; with
// the same time and the same sequence the one that arrives later is the later.
// The zero value is an empty bucket.
type M4Edges struct {
	First, Last   float64
	tFirst, tLast int64
	sFirst, sLast int64
	set           bool
}

// Add folds in one value at time t (any unit, the same for every call), with no
// sequence: ties on t go by arrival order. Use it where values arrive in sample order.
func (e *M4Edges) Add(t int64, v float64) { e.AddSeq(t, 0, v) }

// AddSeq folds in one value at time t whose place among values of the same time
// is seq (lower is earlier). Values of one time arriving out of sequence still
// give the right first and last.
func (e *M4Edges) AddSeq(t, seq int64, v float64) {
	if !e.set {
		e.First, e.Last, e.tFirst, e.tLast, e.sFirst, e.sLast, e.set = v, v, t, t, seq, seq, true
		return
	}
	if t < e.tFirst || (t == e.tFirst && seq < e.sFirst) {
		e.First, e.tFirst, e.sFirst = v, t, seq
	}
	if t > e.tLast || (t == e.tLast && seq >= e.sLast) {
		e.Last, e.tLast, e.sLast = v, t, seq
	}
}

// Set reports whether the bucket holds a value.
func (e *M4Edges) Set() bool { return e.set }

// M4Width returns the number of pixel columns to answer for a requested width:
// at least 1, at most MaxM4Width.
func M4Width(width int) int {
	if width < 1 {
		return 1
	}
	if width > MaxM4Width {
		return MaxM4Width
	}
	return width
}

// M4BucketMs returns the smallest bucket, in milliseconds, with which a span of
// spanMs (inclusive at both ends) falls into at most M4Width(width) buckets,
// that is span/bucket + 1 <= width. The result is at least 1.
func M4BucketMs(spanMs int64, width int) int64 {
	w := int64(M4Width(width))
	if spanMs < 0 {
		spanMs = 0
	}
	return spanMs/w + 1
}
