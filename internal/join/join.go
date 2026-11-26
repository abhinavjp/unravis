// Package join pairs audit intervals with imported events by time overlap.
// It reports how well each interval matched (unique, ambiguous, none) and
// never states a cause: a match is a Correlation at best.
package join

import (
	"sort"
	"time"

	"api-audit-explorer/internal/evidence"
)

// Interval is a stretch of time to explain, for example a stalled audit create.
type Interval struct {
	ID         string
	Start, End time.Time
}

// Tolerance is how far an event may sit from an interval and still count.
// Resolution is the coarsest timestamp resolution of either side, Skew the
// clock difference between the sources, Method the uncertainty of how the
// event time was measured. The three add up.
type Tolerance struct{ Resolution, Skew, Method time.Duration }

// Total is the sum of the three terms; a negative term counts as zero.
func (t Tolerance) Total() time.Duration {
	var sum time.Duration
	for _, d := range []time.Duration{t.Resolution, t.Skew, t.Method} {
		if d > 0 {
			sum += d
		}
	}
	return sum
}

// Match is the result for one interval. Candidates are the evidence IDs of
// the qualifying events that overlap it, sorted. Class is unique for exactly
// one candidate that no other interval also has as its only candidate,
// ambiguous for more than one candidate or for a single candidate shared as the
// only candidate of several intervals (an event explains at most one interval),
// none for no candidate.
type Match struct {
	IntervalID string
	Class      evidence.JoinClass
	Candidates []string
}

// Overlap returns one Match per interval, in input order. An event is a
// candidate when it passes qualifies (nil means every event qualifies) and
// its [Start, End] range touches the interval widened by tol.Total() on both
// sides (closed on both ends). An interval whose End precedes its Start is
// treated as if the two were swapped. An event ID given twice counts once.
// The inputs are not modified.
func Overlap(iv []Interval, ev []evidence.Event, tol Tolerance, qualifies func(evidence.Event) bool) []Match {
	slack := tol.Total()
	events := make([]evidence.Event, 0, len(ev))
	for _, e := range ev {
		if qualifies == nil || qualifies(e) {
			events = append(events, e)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	tree := newEndTree(events)

	out := make([]Match, 0, len(iv))
	for _, v := range iv {
		start, end := v.Start, v.End
		if end.Before(start) {
			start, end = end, start
		}
		lo, hi := start.Add(-slack), end.Add(slack)
		ids := tree.overlapping(lo, hi)
		sort.Strings(ids)
		ids = compact(ids)
		m := Match{IntervalID: v.ID, Class: evidence.JoinNone}
		switch {
		case len(ids) == 1:
			m.Class = evidence.JoinUnique
		case len(ids) > 1:
			m.Class = evidence.JoinAmbiguous
		}
		m.Candidates = ids
		out = append(out, m)
	}
	// One event cannot explain two intervals. When the single candidate of
	// several intervals is the same event, each of those matches is ambiguous
	// (still listed, never counted); otherwise CountUnique would count one
	// event once per interval.
	claims := map[string]int{}
	for _, m := range out {
		if m.Class == evidence.JoinUnique {
			claims[m.Candidates[0]]++
		}
	}
	for i := range out {
		if out[i].Class == evidence.JoinUnique && claims[out[i].Candidates[0]] > 1 {
			out[i].Class = evidence.JoinAmbiguous
		}
	}
	return out
}

// endTree answers "which events touch [lo, hi]" in O(log n + k), however long
// any one event is. The events are sorted by Start and the tree is implicit:
// the node of a range [l, r) is its middle element, and maxEnd[mid] is the
// latest End inside that whole range. A range whose maxEnd is before lo, or
// whose middle Start is after hi on the right side, is skipped.
type endTree struct {
	events []evidence.Event
	ends   []time.Time // End of each event, never before its Start
	maxEnd []time.Time // maxEnd[mid] = latest End in the range whose middle is mid
}

func newEndTree(events []evidence.Event) *endTree {
	t := &endTree{events: events, ends: make([]time.Time, len(events)), maxEnd: make([]time.Time, len(events))}
	for i, e := range events {
		t.ends[i] = e.End
		if e.End.Before(e.Start) {
			t.ends[i] = e.Start
		}
	}
	t.build(0, len(events))
	return t
}

// build fills maxEnd for [l, r) and returns the latest End in it.
func (t *endTree) build(l, r int) time.Time {
	if l >= r {
		return time.Time{}
	}
	mid := l + (r-l)/2
	m := t.ends[mid]
	if left := t.build(l, mid); left.After(m) {
		m = left
	}
	if right := t.build(mid+1, r); right.After(m) {
		m = right
	}
	t.maxEnd[mid] = m
	return m
}

// overlapping returns the evidence IDs of the events with Start <= hi and
// End >= lo, in no particular order.
func (t *endTree) overlapping(lo, hi time.Time) []string {
	var ids []string
	t.collect(0, len(t.events), lo, hi, &ids)
	return ids
}

func (t *endTree) collect(l, r int, lo, hi time.Time, ids *[]string) {
	for l < r {
		mid := l + (r-l)/2
		if t.maxEnd[mid].Before(lo) {
			return // nothing in this range reaches lo
		}
		t.collect(l, mid, lo, hi, ids)
		if t.events[mid].Start.After(hi) {
			return // this event and everything right of it starts too late
		}
		if !t.ends[mid].Before(lo) {
			*ids = append(*ids, t.events[mid].EvidenceID)
		}
		l = mid + 1
	}
}

func compact(s []string) []string {
	if len(s) < 2 {
		return s
	}
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// CountUnique counts the intervals matched uniquely. Ambiguous and none are
// never counted.
func CountUnique(m []Match) int {
	n := 0
	for _, x := range m {
		if x.Class == evidence.JoinUnique {
			n++
		}
	}
	return n
}
