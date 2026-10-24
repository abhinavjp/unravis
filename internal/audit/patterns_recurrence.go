package audit

import (
	"fmt"
	"sort"
)

// Rule B3a (incident-gap addendum §1.4): do stalls start at the same minute of
// the hour on several days? It reads nothing new; it only looks at the first
// stall start of each B2 episode. It never names a job or a schedule.
const (
	// onsetWindowMinutes is the half-width of the window around a minute.
	onsetWindowMinutes = 2
	// onsetMinDays and onsetMinOnsets are the fewest distinct days and onsets in
	// one window for it to count as recurring.
	onsetMinDays   = 3
	onsetMinOnsets = 3
	// onsetUniformFactor is how many times the uniform expectation a window must hold.
	onsetUniformFactor = 3.0
	// onsetFalsifyMin is the fewest onsets in all before "not observed" is printed.
	onsetFalsifyMin = 5
	// maxOnsetRows bounds PatternReport.OnsetMinutes.
	maxOnsetRows = 12
)

// onsetPoint is the start of one episode, as a minute of the UTC hour and a UTC day.
type onsetPoint struct {
	episode int
	minute  int
	day     string
}

// onsetWindow is the onsets within onsetWindowMinutes of one center minute.
type onsetWindow struct {
	center   int
	points   []onsetPoint
	days     int
	distance int // sum of circular distances to the center, for tie-breaks
}

// circularMinuteDistance is the distance between two minutes of an hour, across the wrap.
func circularMinuteDistance(a, b int) int {
	d := a - b
	if d < 0 {
		d = -d
	}
	if d > 30 {
		d = 60 - d
	}
	return d
}

func onsetPoints(episodes []Episode) []onsetPoint {
	points := make([]onsetPoint, 0, len(episodes))
	for _, e := range episodes {
		t := e.Start.UTC()
		points = append(points, onsetPoint{episode: e.Index, minute: t.Minute(), day: t.Format("2006-01-02")})
	}
	return points
}

// onsetWindows slides the window over the 60 minutes of the hour and keeps the
// centers that hold at least one onset.
func onsetWindows(points []onsetPoint) []onsetWindow {
	var out []onsetWindow
	for c := 0; c < 60; c++ {
		w := onsetWindow{center: c}
		days := map[string]bool{}
		for _, p := range points {
			d := circularMinuteDistance(p.minute, c)
			if d > onsetWindowMinutes {
				continue
			}
			w.points = append(w.points, p)
			w.distance += d
			days[p.day] = true
		}
		if len(w.points) == 0 {
			continue
		}
		w.days = len(days)
		out = append(out, w)
	}
	return out
}

// rankOnsetWindows orders windows best first: more days, more onsets, the center
// closest to its own onsets, then the lower minute.
func rankOnsetWindows(ws []onsetWindow) {
	sort.SliceStable(ws, func(i, j int) bool {
		a, b := ws[i], ws[j]
		if a.days != b.days {
			return a.days > b.days
		}
		if len(a.points) != len(b.points) {
			return len(a.points) > len(b.points)
		}
		if a.distance != b.distance {
			return a.distance < b.distance
		}
		return a.center < b.center
	})
}

// buildOnsetMinutes returns the report rows: the best windows, none closer than
// five minutes to a better one so their onsets do not overlap, by minute.
func buildOnsetMinutes(points []onsetPoint) []OnsetMinute {
	ws := onsetWindows(points)
	rankOnsetWindows(ws)
	var picked []onsetWindow
	for _, w := range ws {
		free := true
		for _, p := range picked {
			if circularMinuteDistance(w.center, p.center) <= 2*onsetWindowMinutes {
				free = false
				break
			}
		}
		if free {
			picked = append(picked, w)
		}
		if len(picked) == maxOnsetRows {
			break
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].center < picked[j].center })
	rows := make([]OnsetMinute, 0, len(picked))
	for _, w := range picked {
		eps := make([]int, 0, len(w.points))
		for _, p := range w.points {
			eps = append(eps, p.episode)
		}
		sort.Ints(eps)
		rows = append(rows, OnsetMinute{Minute: w.center, Days: w.days, Onsets: len(w.points), Episodes: eps})
	}
	return rows
}

// recurringOnsetWindow is the best window that meets the recurring rule: at
// least three onsets on at least three distinct days, and at least three times
// the onsets a uniform spread over the hour would put in a five-minute window.
func recurringOnsetWindow(points []onsetPoint) (onsetWindow, bool) {
	ws := onsetWindows(points)
	rankOnsetWindows(ws)
	expected := float64(len(points)) * float64(2*onsetWindowMinutes+1) / 60
	for _, w := range ws {
		if w.days >= onsetMinDays && len(w.points) >= onsetMinOnsets && float64(len(w.points)) >= onsetUniformFactor*expected {
			return w, true
		}
	}
	return onsetWindow{}, false
}

// onsetsRepeat reports whether any window holds onsets from more than one day.
func onsetsRepeat(points []onsetPoint) bool {
	for _, w := range onsetWindows(points) {
		if w.days > 1 {
			return true
		}
	}
	return false
}

// buildRecurrence runs B3a over the B2 episodes. It is withheld, with a warning,
// when the stall read hit the row cap, because the episode list is then partial.
func buildRecurrence(in PatternInput, p PatternParams, r PatternReport) (rows []OnsetMinute, findings []Finding, warnings []string) {
	if len(r.Episodes) == 0 {
		return nil, nil, nil
	}
	if r.StallsTruncated {
		return nil, nil, []string{"Stall onset minutes were not checked (partial: stall rows capped)."}
	}
	points := onsetPoints(r.Episodes)
	rows = buildOnsetMinutes(points)
	if w, ok := recurringOnsetWindow(points); ok {
		findings = append(findings, Finding{
			Rule: "B3a", Label: EvidenceSymptom, N: len(w.points), LowN: len(w.points) < LowNRows,
			EvidenceID: patternEvidenceID("B3a", in, p),
			Headline:   fmt.Sprintf("Stalls started near minute :%02d of the hour on %d different days in this window.", w.center, w.days),
		})
		return rows, findings, nil
	}
	if len(points) >= onsetFalsifyMin && !onsetsRepeat(points) {
		findings = append(findings, Finding{
			Rule: "B3a", Label: EvidenceSymptom, N: len(points), LowN: len(points) < LowNRows,
			EvidenceID: patternEvidenceID("B3a", in, p),
			Headline:   "Not observed: stall onsets do not repeat at one clock minute.",
		})
	}
	return rows, findings, nil
}
