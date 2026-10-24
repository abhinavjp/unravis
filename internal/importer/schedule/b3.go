package schedule

import (
	"fmt"
	"sort"
	"time"

	"api-audit-explorer/internal/evidence"
)

// B3 counts how many independent stall episodes start within ±tol of a
// scheduled boundary. stallStarts holds one start time per independent episode
// (the caller deduplicates), and b holds the events this package emitted.
//
//   - k: episodes with a known boundary within ±tol.
//   - n: all episodes given.
//   - unknown: episodes with no known boundary in reach that cannot be called
//     misses: the stall start is not a real time, a schedule in the file could
//     not be expanded (schedule-unknown), or the only boundary in reach sits in
//     a DST gap or fold and so has an uncertain run time.
//
// An unknown never counts as a miss, so n-k-unknown is the count of real
// misses. The stall starts must lie inside the window the file was imported
// with; the importer cannot tell a start outside its coverage from a miss.
// A near boundary is a clock coincidence (Correlation); the wording says so.
func B3(stallStarts []time.Time, b []evidence.Event, tol time.Duration) (k, n, unknown int) {
	if tol < 0 {
		tol = 0
	}
	var known, flagged []time.Time
	globalUnknown := false
	for _, e := range b {
		switch e.Kind {
		case KindUnknown:
			globalUnknown = true
		case KindBoundary:
			if e.Attrs["dst"] != "" {
				flagged = append(flagged, e.Start)
			} else {
				known = append(known, e.Start)
			}
		}
	}
	sortTimes(known)
	sortTimes(flagged)
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
		if globalUnknown || near(flagged, s, tol) {
			unknown++
		}
	}
	return k, n, unknown
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
		s += fmt.Sprintf(" %d could not be checked (schedule or run time unknown) and are not counted as misses.", unknown)
	}
	return s
}

func fmtTol(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int64(d/time.Minute))
	}
	return fmt.Sprintf("%d s", int64(d/time.Second))
}
