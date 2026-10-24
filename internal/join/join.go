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
// one candidate, ambiguous for more than one, none for no candidate.
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
	// prefixMaxEnd[i] is the latest End among events[0..i]; it lets the
	// backward scan stop once nothing earlier can reach the interval.
	prefixMaxEnd := make([]time.Time, len(events))
	for i, e := range events {
		end := e.End
		if end.Before(e.Start) {
			end = e.Start
		}
		if i > 0 && prefixMaxEnd[i-1].After(end) {
			end = prefixMaxEnd[i-1]
		}
		prefixMaxEnd[i] = end
	}

	out := make([]Match, 0, len(iv))
	for _, v := range iv {
		start, end := v.Start, v.End
		if end.Before(start) {
			start, end = end, start
		}
		lo, hi := start.Add(-slack), end.Add(slack)
		// First index whose Start is after hi.
		cut := sort.Search(len(events), func(i int) bool { return events[i].Start.After(hi) })
		var ids []string
		for i := cut - 1; i >= 0 && !prefixMaxEnd[i].Before(lo); i-- {
			e := events[i]
			eEnd := e.End
			if eEnd.Before(e.Start) {
				eEnd = e.Start
			}
			if !eEnd.Before(lo) {
				ids = append(ids, e.EvidenceID)
			}
		}
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
	return out
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
