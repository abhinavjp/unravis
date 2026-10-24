package schedule

import (
	"fmt"
	"sort"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Coverage is the window the schedules were expanded over. A boundary can only
// be missing from the events when the stall lies inside it. A zero Coverage
// means the window is not known, and then nothing is judged a miss.
type Coverage struct {
	From, To time.Time
}

// CoverageOf reads the window a report's events were expanded over. It is the
// zero Coverage when the file was imported without a window.
func CoverageOf(rep importer.Report) Coverage {
	var c Coverage
	if rep.Quality.CoverageFirst != nil {
		c.From = *rep.Quality.CoverageFirst
	}
	if rep.Quality.CoverageLast != nil {
		c.To = *rep.Quality.CoverageLast
	}
	return c
}

// B3 is B3Covered with no coverage: a stall is a hit when a known boundary is
// in reach, and every other stall is unknown, because without the imported
// window a missing boundary cannot be told from one that was never expanded.
func B3(stallStarts []time.Time, b []evidence.Event, tol time.Duration) (k, n, unknown int) {
	return B3Covered(stallStarts, b, tol, Coverage{})
}

// B3Covered counts how many independent stall episodes start within ±tol of a
// scheduled boundary. stallStarts holds one start time per independent episode
// (the caller deduplicates), b holds the events this package emitted, and cov
// is the window they were expanded over (CoverageOf).
//
//   - k: episodes with a known boundary within ±tol.
//   - n: all episodes given.
//   - unknown: episodes with no known boundary in reach that cannot be called
//     misses: the stall start is not a real time, the stall or its ±tol reach
//     lies outside the imported window (or no window was given), a
//     schedule-unknown marker covers it, or the only boundary in reach is a DST
//     gap or fold reading or belongs to a schedule that was disabled at export.
//
// A schedule-unknown marker covers the span it carries, not the whole file:
// one schedule that could not be expanded makes only the stalls inside its span
// unknown. An unknown never counts as a miss, so n-k-unknown is the count of
// real misses. A near boundary is a clock coincidence (Correlation); the
// wording says so.
func B3Covered(stallStarts []time.Time, b []evidence.Event, tol time.Duration, cov Coverage) (k, n, unknown int) {
	if tol < 0 {
		tol = 0
	}
	var known, flagged []time.Time
	var spans []span
	for _, e := range b {
		switch e.Kind {
		case KindUnknown:
			if e.Start.IsZero() && e.End.IsZero() {
				spans = append(spans, span{time.Unix(-1<<40, 0), time.Unix(1<<40, 0)})
				continue
			}
			end := e.End
			if end.Before(e.Start) {
				end = e.Start
			}
			spans = append(spans, span{e.Start.Add(-tol), end.Add(tol)})
		case KindBoundary:
			if e.Attrs["dst"] != "" || e.Attrs["basis"] == reasonDisabled {
				flagged = append(flagged, e.Start)
			} else {
				known = append(known, e.Start)
			}
		}
	}
	sortTimes(known)
	sortTimes(flagged)
	spans = mergeSpans(spans)
	covered := !cov.From.IsZero() && !cov.To.IsZero() && !cov.To.Before(cov.From)
	for _, s := range stallStarts {
		n++
		if s.IsZero() {
			unknown++
			continue
		}
		if near(known, s, tol) {
			k++
			continue
		}
		switch {
		case !covered,
			s.Add(-tol).Before(cov.From), s.Add(tol).After(cov.To),
			near(flagged, s, tol),
			inSpans(spans, s):
			unknown++
		}
	}
	return k, n, unknown
}

// span is a closed time range.
type span struct{ from, to time.Time }

// mergeSpans sorts the spans and joins the ones that touch or overlap.
func mergeSpans(in []span) []span {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].from.Before(in[j].from) })
	out := []span{in[0]}
	for _, s := range in[1:] {
		last := &out[len(out)-1]
		if !s.from.After(last.to) {
			if s.to.After(last.to) {
				last.to = s.to
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// inSpans reports whether t lies in one of the merged, sorted spans.
func inSpans(spans []span, t time.Time) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].to.After(t) || spans[i].to.Equal(t) })
	return i < len(spans) && !spans[i].from.After(t)
}

func sortTimes(t []time.Time) {
	sort.Slice(t, func(i, j int) bool { return t[i].Before(t[j]) })
}

// near reports whether sorted holds a time within ±tol of s (inclusive).
func near(sorted []time.Time, s time.Time, tol time.Duration) bool {
	i := sort.Search(len(sorted), func(i int) bool { return !sorted[i].Before(s.Add(-tol)) })
	return i < len(sorted) && !sorted[i].After(s.Add(tol))
}

// B3Wording is the sentence printed for a B3 result. It states a clock
// coincidence only and passes evidence.LintCausal.
func B3Wording(k, n, unknown int, tol time.Duration) string {
	if n == 0 {
		return "No independent stall episodes were provided."
	}
	s := fmt.Sprintf("Stall start falls within ±%s of a scheduled boundary in %d of %d independent episodes.", fmtTol(tol), k, n)
	if unknown > 0 {
		s += fmt.Sprintf(" %d could not be checked (schedule or run time unknown, or outside the imported window) and are not counted as misses.", unknown)
	}
	return s
}

func fmtTol(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int64(d/time.Minute))
	}
	return fmt.Sprintf("%d s", int64(d/time.Second))
}
