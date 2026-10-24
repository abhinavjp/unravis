package ledger

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Guard thresholds (roadmap §8.6). Tunable defaults.
const (
	minEpisodes         = 3
	maxJoinAmbiguous    = 10.0 // percent of rows
	presenceNumerator   = 4    // minimum presence rate 4/5 = 80 %
	presenceDenominator = 5
)

// Guards lists, in a fixed order, the plain-language reasons a hypothesis with
// these facts and evidence references cannot be marked "strong correlation".
// An empty result means no guard blocks it. Guards never set a state, never
// grant support and apply only to StateStrong.
func Guards(f Facts, evidenceIDs []string) []string {
	r := []string{} // never nil, so JSON shows [] when nothing blocks

	if f.CoverageGap {
		r = append(r, "The evidence window has a coverage gap.")
	}

	n, k := f.TotalN, f.PresentK
	switch {
	case n < 0:
		r = append(r, "The number of independent episodes is not valid.")
	case n < minEpisodes:
		r = append(r, fmt.Sprintf("%s; strong correlation needs at least %d (a volley counts as one episode).", episodes(n), minEpisodes))
	}

	countsValid := n >= 0 && k >= 0 && k <= n
	switch {
	case n == 0 && k == 0:
		r = append(r, "The presence rate is not stated (k of n episodes with the linked event present).")
	case !countsValid:
		r = append(r, fmt.Sprintf("The presence rate %d of %d is not valid.", k, n))
	case k*presenceDenominator < n*presenceNumerator:
		r = append(r, fmt.Sprintf("The presence rate is %d of %d, below the minimum (3 of 3, 4 of 4, or 80%% of 5 or more).", k, n))
	}

	if countsValid {
		listed := 0
		for _, c := range f.Counterexamples {
			if strings.TrimSpace(c) != "" {
				listed++
			}
		}
		if missing := n - k; listed < missing {
			r = append(r, fmt.Sprintf("%d counterexample(s) listed, but %s lacked the linked event; list each one.", listed, episodes(missing)))
		}
	}

	if len(f.Alternatives) == 0 {
		r = append(r, "No alternative explanation is named; name every plausible alternative with its status.")
	}
	for i, a := range f.Alternatives {
		if strings.TrimSpace(a.Text) == "" {
			r = append(r, fmt.Sprintf("Alternative explanation %d has no text.", i+1))
		}
		if !validAltStatus(a.Status) {
			r = append(r, fmt.Sprintf("Alternative explanation %d has no valid status (open, ruled out or not testable).", i+1))
		}
	}

	switch p := f.JoinAmbiguousPct; {
	case p == nil:
		r = append(r, "The join class is not stated (share of rows with an ambiguous join).")
	case math.IsNaN(*p) || *p < 0 || *p > 100:
		r = append(r, "The share of rows with an ambiguous join is not valid.")
	case *p > maxJoinAmbiguous:
		r = append(r, fmt.Sprintf("The join is ambiguous for %s%% of rows, above the 10%% limit.", strconv.FormatFloat(*p, 'f', -1, 64)))
	}

	if !f.TimezoneRecorded {
		r = append(r, "The source's timezone mapping is not recorded.")
	}
	if f.ConfounderOverlap {
		r = append(r, "A known confounder (a scheduled job or a recycle event) overlaps the episodes.")
	}
	if f.OverlapOnly {
		r = append(r, "The support is temporal overlap alone; temporal overlap alone never reaches strong correlation.")
	}
	if len(evidenceIDs) == 0 {
		r = append(r, "No evidence references are linked.")
	}
	return r
}

func episodes(n int) string {
	if n == 1 {
		return "1 independent episode"
	}
	return fmt.Sprintf("%d independent episodes", n)
}

func validAltStatus(s string) bool {
	switch s {
	case AltOpen, AltRuledOut, AltNotTestable:
		return true
	}
	return false
}
