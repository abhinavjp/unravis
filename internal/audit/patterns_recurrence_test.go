package audit

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var onsetBase = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// onsetEpisodes builds episodes that start at the given (day, minute) pairs.
func onsetEpisodes(starts ...[2]int) []Episode {
	eps := make([]Episode, 0, len(starts))
	for i, s := range starts {
		at := onsetBase.Add(time.Duration(s[0])*24*time.Hour + 9*time.Hour + time.Duration(s[1])*time.Minute)
		eps = append(eps, Episode{Index: i + 1, Start: at, End: at.Add(time.Minute), Requests: 1})
	}
	return eps
}

func recurrenceFor(eps []Episode, truncated bool) ([]OnsetMinute, []Finding, []string) {
	in := PatternInput{SourceIDs: []string{"s"}, Start: onsetBase, End: onsetBase.Add(10 * 24 * time.Hour)}
	return buildRecurrence(in, patternParams(in), PatternReport{Episodes: eps, StallsTruncated: truncated})
}

func TestOnsetRecurringAcrossDays(t *testing.T) {
	rows, findings, warns := recurrenceFor(onsetEpisodes([2]int{0, 17}, [2]int{1, 17}, [2]int{2, 17}), false)
	if len(warns) != 0 || len(findings) != 1 {
		t.Fatalf("%v %+v", warns, findings)
	}
	f := findings[0]
	if f.Rule != "B3a" || f.Label != EvidenceSymptom || !f.LowN || f.N != 3 || f.EvidenceID == "" || !strings.HasPrefix(f.EvidenceID, "p2:b3a:") {
		t.Fatalf("%+v", f)
	}
	if f.Headline != "Stalls started near minute :17 of the hour on 3 different days in this window." {
		t.Fatalf("headline %q", f.Headline)
	}
	if len(rows) != 1 || rows[0].Minute != 17 || rows[0].Days != 3 || rows[0].Onsets != 3 || fmt.Sprint(rows[0].Episodes) != "[1 2 3]" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestOnsetWindowEdges(t *testing.T) {
	cases := []struct {
		name      string
		minutes   []int
		recurring bool
	}{
		{"within two minutes either side", []int{15, 17, 19}, true},
		{"one minute too wide", []int{14, 17, 19}, false},
		{"wraps the hour", []int{59, 1, 0}, true},
		{"wrap edge at two", []int{58, 0, 2}, true},
		{"wrap edge at three", []int{57, 0, 2}, false},
	}
	for _, c := range cases {
		var starts [][2]int
		for day, m := range c.minutes {
			starts = append(starts, [2]int{day, m})
		}
		_, findings, _ := recurrenceFor(onsetEpisodes(starts...), false)
		got := len(findings) == 1 && strings.HasPrefix(findings[0].Headline, "Stalls started near")
		if got != c.recurring {
			t.Errorf("%s: recurring %v, findings %+v", c.name, got, findings)
		}
	}
}

func TestOnsetNeedsThreeDistinctDays(t *testing.T) {
	// Three onsets at one minute, but on two days.
	rows, findings, _ := recurrenceFor(onsetEpisodes([2]int{0, 17}, [2]int{0, 17}, [2]int{1, 17}), false)
	if len(findings) != 0 || len(rows) != 1 || rows[0].Days != 2 || rows[0].Onsets != 3 {
		t.Fatalf("%+v %+v", rows, findings)
	}
	// Three onsets on one day: single day, not recurring, and too few for "not observed".
	rows, findings, _ = recurrenceFor(onsetEpisodes([2]int{0, 17}, [2]int{0, 17}, [2]int{0, 18}), false)
	if len(findings) != 0 || rows[0].Days != 1 {
		t.Fatalf("%+v %+v", rows, findings)
	}
}

func TestOnsetUniformExpectation(t *testing.T) {
	// 24 onsets: a five-minute window should hold 2 by chance, so 3 is below 3x.
	starts := [][2]int{{0, 17}, {1, 17}, {2, 17}}
	for i := 0; i < 21; i++ {
		starts = append(starts, [2]int{i % 7, 30 + (i%4)*8}) // minutes 30, 38, 46, 54
	}
	rows, findings, _ := recurrenceFor(onsetEpisodes(starts...), false)
	for _, f := range findings {
		if strings.HasPrefix(f.Headline, "Stalls started near minute :17") {
			t.Fatalf("below the uniform factor but printed: %+v (rows %+v)", f, rows)
		}
	}
	// With 3 onsets in a window of 6, the same window passes: 3 >= 3 x 0.5.
	_, findings, _ = recurrenceFor(onsetEpisodes([2]int{0, 17}, [2]int{1, 17}, [2]int{2, 17}, [2]int{0, 40}, [2]int{1, 50}, [2]int{2, 5}), false)
	if len(findings) != 1 || !strings.HasPrefix(findings[0].Headline, "Stalls started near minute :17") {
		t.Fatalf("%+v", findings)
	}
}

func TestOnsetNotObserved(t *testing.T) {
	// Five onsets on five days, no two within four minutes.
	_, findings, _ := recurrenceFor(onsetEpisodes([2]int{0, 0}, [2]int{1, 10}, [2]int{2, 20}, [2]int{3, 30}, [2]int{4, 40}), false)
	if len(findings) != 1 || findings[0].Headline != "Not observed: stall onsets do not repeat at one clock minute." || findings[0].Label != EvidenceSymptom || findings[0].N != 5 {
		t.Fatalf("%+v", findings)
	}
	// Four onsets are too few to say it.
	_, findings, _ = recurrenceFor(onsetEpisodes([2]int{0, 0}, [2]int{1, 10}, [2]int{2, 20}, [2]int{3, 30}), false)
	if len(findings) != 0 {
		t.Fatalf("%+v", findings)
	}
	// Five onsets where two days share a window: nothing to falsify, nothing recurring.
	_, findings, _ = recurrenceFor(onsetEpisodes([2]int{0, 0}, [2]int{1, 1}, [2]int{2, 20}, [2]int{3, 30}, [2]int{4, 40}), false)
	if len(findings) != 0 {
		t.Fatalf("%+v", findings)
	}
}

func TestOnsetWithheldWhenTruncated(t *testing.T) {
	rows, findings, warns := recurrenceFor(onsetEpisodes([2]int{0, 17}, [2]int{1, 17}, [2]int{2, 17}), true)
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "partial: stall rows capped") {
		t.Fatalf("%+v %+v %v", rows, findings, warns)
	}
}

func TestOnsetNoEpisodes(t *testing.T) {
	rows, findings, warns := recurrenceFor(nil, false)
	if rows != nil || findings != nil || warns != nil {
		t.Fatalf("%+v %+v %v", rows, findings, warns)
	}
}

// The minute is read in UTC, so a whole-hour offset or a DST change in a local
// zone does not move it.
func TestOnsetMinuteIgnoresDST(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	// US clocks went back on 2026-11-01; both sides are :17 local and :17 UTC.
	var eps []Episode
	for i, day := range []int{29, 30, 31, 1} {
		month := time.October
		if day == 1 {
			month = time.November
		}
		at := time.Date(2026, month, day, 10, 17, 0, 0, zone)
		eps = append(eps, Episode{Index: i + 1, Start: at, End: at.Add(time.Minute)})
	}
	rows, findings, _ := recurrenceFor(eps, false)
	if len(findings) != 1 || rows[0].Minute != 17 || rows[0].Days != 4 {
		t.Fatalf("%+v %+v", rows, findings)
	}
}

func TestOnsetRowsDoNotOverlap(t *testing.T) {
	var starts [][2]int
	for day := 0; day < 6; day++ {
		starts = append(starts, [2]int{day, 10 + day%3}, [2]int{day, 40})
	}
	rows, _, _ := recurrenceFor(onsetEpisodes(starts...), false)
	total := 0
	for _, r := range rows {
		total += r.Onsets
	}
	if total != len(starts) {
		t.Fatalf("rows %+v cover %d of %d onsets", rows, total, len(starts))
	}
}

// RunPatterns wires B3a in and adds no read: only the stall and audit reads and the analysis.
func TestRunPatternsOnsetAddsNoReads(t *testing.T) {
	repo := &patternFakeRepo{}
	for day := 0; day < 3; day++ {
		off := time.Duration(day)*24*time.Hour + 17*time.Minute
		repo.slow = append(repo.slow, stallRow(fmt.Sprintf("d%d", day), -off, 60000, 100+day))
	}
	in := patternInput()
	in.Start = in.Start.Add(-4 * 24 * time.Hour)
	r := runFake(t, repo, in)
	if len(repo.auditCalls) != 1 || len(repo.slowCalls) != 1 {
		t.Fatalf("reads: slow %d audit %d", len(repo.slowCalls), len(repo.auditCalls))
	}
	if len(r.OnsetMinutes) == 0 {
		t.Fatalf("no onset rows: %+v", r.Stalls)
	}
}
