package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Rule B9 (gap list row 4; addendum 1.3): dating a step change. For the busiest
// operations it builds a daily p95 series in the display zone, cuts each series
// into two parts at the best place, and reports a date only when several
// operations step on the same day. The date is a statement about the numbers.
// It is a Symptom and never names a cause.
//
// The rule is opt-in (PatternInput.StepChange), reads one day at a time (see
// dailyseries.go) and does not run on a live source.
const (
	// stepTopOperations is how many operations, by volume, are fitted.
	stepTopOperations = 10
	// stepMinSegmentDays is the fewest used days on each side of a step.
	stepMinSegmentDays = 5
	// stepMinRatio is the smallest change in p95, either way, that is a step.
	stepMinRatio = 2.0
	// stepTrafficLow and stepTrafficHigh bound the change in mean daily traffic.
	// A step in p95 beside a large change in traffic is not dated.
	stepTrafficLow  = 0.67
	stepTrafficHigh = 1.5
	// stepMinOperations is how many operations must share the date.
	stepMinOperations = 3
	// stepShareDays is how far apart, in days, two operations may date the step.
	stepShareDays = 1
	// stepSharpRatio is the smallest change between the last and the first three
	// days around the cut. It turns away a slow drift that a cut would split.
	stepSharpRatio = 1.41
	// stepSplitDays is the longest span, in days, either side of a suggested split.
	stepSplitDays = 7
)

// StepChangeInput is the window B9 reads. It may be up to MaxSeriesDays whole
// days, unlike the window of the other rules, and is read in the zone of
// PatternInput.TimeZone.
type StepChangeInput struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// StepOperation is one fitted operation. When Found is false, Reason says why no
// step is reported for it. The figures are null when there is no fit to print.
type StepOperation struct {
	Operation    string   `json:"operation"`
	Volume       int64    `json:"volume"`
	DaysUsed     int      `json:"daysUsed"`
	Found        bool     `json:"found"`
	Date         string   `json:"date,omitempty"`
	BeforeMs     *float64 `json:"beforeMs,omitempty"`
	AfterMs      *float64 `json:"afterMs,omitempty"`
	Ratio        *float64 `json:"ratio,omitempty"`
	TrafficRatio *float64 `json:"trafficRatio,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// SuggestedSplit is a suggestion for the before and after comparison. It is
// never applied by itself: the analyst confirms it. Start and End are the
// "after" window, BaselineStart and BaselineEnd the "before" window, each at most
// MaxInterval, both cut at the start of Date in the display zone.
type SuggestedSplit struct {
	Date          string    `json:"date"`
	Zone          string    `json:"zone"`
	BaselineStart time.Time `json:"baselineStart"`
	BaselineEnd   time.Time `json:"baselineEnd"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
}

// StepChange is B9. Date is empty when no date is shared. Sharing lists the
// operations whose step falls within a day of Date.
type StepChange struct {
	Zone       string          `json:"zone"`
	Start      time.Time       `json:"start"`
	End        time.Time       `json:"end"`
	DaysRead   int             `json:"daysRead"`
	DaysLeft   int             `json:"daysLeftOut"`
	Operations []StepOperation `json:"operations"`
	Date       string          `json:"date,omitempty"`
	Sharing    []string        `json:"sharing,omitempty"`
	Split      *SuggestedSplit `json:"split,omitempty"`
	Partial    bool            `json:"partial"`
}

// dayPoint is one used day of one operation.
type dayPoint struct {
	date  string
	num   int
	count int64
	p95   float64
	hist  *dayHist
}

// stepFit is the best cut of one series, whether or not it passes the tests.
type stepFit struct {
	cut          int // index of the first day after the step
	date         string
	before       float64
	after        float64
	ratio        float64 // larger over smaller, at least 1
	trafficRatio float64 // mean daily count after over before
}

// Reasons an operation has no step. They are plain text shown beside the row.
const (
	stepReasonShort   = "Fewer than 10 days with enough requests."
	stepReasonSmall   = "The p95 changed by less than 2 times."
	stepReasonTraffic = "Traffic changed too much to compare the two parts."
	stepReasonDrift   = "The p95 moved gradually, not in one step."
)

// fitStep cuts a series into two parts of at least stepMinSegmentDays days where
// the sum of squared differences of ln(p95) from each part's mean is smallest
// (the earlier cut wins a tie), then tests the best cut. The reason is "" when
// the cut is a step.
func fitStep(pts []dayPoint) (stepFit, string) {
	n := len(pts)
	if n < 2*stepMinSegmentDays {
		return stepFit{}, stepReasonShort
	}
	logs := make([]float64, n)
	for i, p := range pts {
		logs[i] = math.Log(p.p95)
	}
	sse := func(from, to int) float64 {
		var mean float64
		for _, v := range logs[from:to] {
			mean += v
		}
		mean /= float64(to - from)
		var s float64
		for _, v := range logs[from:to] {
			s += (v - mean) * (v - mean)
		}
		return s
	}
	best, bestSSE := -1, math.Inf(1)
	for k := stepMinSegmentDays; k <= n-stepMinSegmentDays; k++ {
		if s := sse(0, k) + sse(k, n); s < bestSSE-1e-12 {
			best, bestSSE = k, s
		}
	}
	before, after := newDayHist(), newDayHist()
	var beforeCount, afterCount int64
	for i, p := range pts {
		if i < best {
			before.addHist(p.hist)
			beforeCount += p.count
		} else {
			after.addHist(p.hist)
			afterCount += p.count
		}
	}
	a, okA := before.quantile(0.95)
	b, okB := after.quantile(0.95)
	if !okA || !okB || a <= 0 || b <= 0 {
		return stepFit{}, stepReasonShort
	}
	fit := stepFit{cut: best, date: pts[best].date, before: a, after: b}
	fit.ratio = math.Max(a, b) / math.Min(a, b)
	fit.trafficRatio = (float64(afterCount) / float64(n-best)) / (float64(beforeCount) / float64(best))
	if fit.ratio < stepMinRatio {
		return fit, stepReasonSmall
	}
	if fit.trafficRatio < stepTrafficLow || fit.trafficRatio > stepTrafficHigh {
		return fit, stepReasonTraffic
	}
	near := func(from, to int) float64 {
		v := make([]float64, 0, to-from)
		for _, p := range pts[from:to] {
			v = append(v, p.p95)
		}
		return medianOf(v)
	}
	lo, hi := near(best-3, best), near(best, best+3)
	if lo <= 0 || hi <= 0 || math.Max(lo, hi)/math.Min(lo, hi) < stepSharpRatio {
		return fit, stepReasonDrift
	}
	return fit, ""
}

// sharedStepDate finds the date that the most operations share within
// stepShareDays days. dates holds one day number per operation; it returns the
// shared day number and the indexes into dates that share it, or ok = false when
// fewer than stepMinOperations do. A tie goes to the date more operations hit
// exactly, then to the earlier date.
func sharedStepDate(dates []int) (day int, members []int, ok bool) {
	bestCount, bestExact := 0, 0
	for _, center := range dates {
		var in []int
		exact := 0
		for i, d := range dates {
			if d >= center-stepShareDays && d <= center+stepShareDays {
				in = append(in, i)
				if d == center {
					exact++
				}
			}
		}
		better := len(in) > bestCount ||
			(len(in) == bestCount && exact > bestExact) ||
			(len(in) == bestCount && exact == bestExact && center < day)
		if better {
			day, members, bestCount, bestExact = center, in, len(in), exact
		}
	}
	return day, members, bestCount >= stepMinOperations
}

// seriesEvidenceID is the stable reference of a series rule over one window,
// set of sources and zone. It never names a row.
func seriesEvidenceID(rule string, sources []string, start, end time.Time, zone string) string {
	s := append([]string(nil), sources...)
	sort.Strings(s)
	key := fmt.Sprintf("%s|%s|%s|%s|%s", rule, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), strings.Join(s, ","), zone)
	sum := sha256.Sum256([]byte(key))
	return "p2:" + strings.ToLower(rule) + ":" + hex.EncodeToString(sum[:8])
}

// validateStepChange checks the window of B9: a positive span of whole days, at
// most MaxSeriesDays of them.
func validateStepChange(sc *StepChangeInput, loc *time.Location) error {
	if sc.Start.IsZero() || sc.End.IsZero() || !sc.End.After(sc.Start) {
		return invalidInputf("stepChange needs a start before its end")
	}
	if sc.End.Sub(sc.Start) > time.Duration(MaxSeriesDays+2)*24*time.Hour {
		return invalidInputf("stepChange covers at most %d days", MaxSeriesDays)
	}
	if len(localDays(sc.Start, sc.End, loc, MaxSeriesDays)) == 0 {
		return fmt.Errorf("%w: stepChange: %v", ErrInvalidInput, ErrNoWholeDays)
	}
	return nil
}

// buildStepChange runs B9. It returns the result, at most one finding, the
// provenance of its reads and warnings. A live source and a failed read are
// warnings, not errors: the report keeps its other rules. Only a cancelled
// context is an error.
func buildStepChange(ctx context.Context, repo Repository, in PatternInput, r PatternReport) (*StepChange, []Finding, []Provenance, []string, error) {
	loc, err := LoadZone(in.TimeZone)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sc := in.StepChange
	if isLiveRead(r.Provenance) {
		return nil, nil, nil, []string{"Step dating reads the log one day at a time and is not run on a live source. Run it on a restored copy."}, nil
	}
	days := localDays(sc.Start, sc.End, loc, MaxSeriesDays)
	reads, prov, err := readDays(ctx, repo, in.SourceIDs, days, "", 0)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, nil, ctx.Err()
		}
		if errors.Is(err, ErrBroadScanRefused) {
			return nil, nil, nil, []string{"Step dating reads the log one day at a time and is not run on a live source. Run it on a restored copy."}, nil
		}
		return nil, nil, nil, []string{"Step dating could not read every day (the source returned an error), so it did not run."}, nil
	}

	result, warnings := stepChangeFrom(reads, loc)
	result.Start, result.End = days[0].Start, days[len(days)-1].End
	return result, stepFindings(in, result), prov, warnings, nil
}

// stepChangeFrom runs the fit on read days. It is the part of B9 with no
// repository in it, so tests feed it days directly.
func stepChangeFrom(reads []dayRead, loc *time.Location) (*StepChange, []string) {
	result := &StepChange{Zone: loc.String(), Operations: []StepOperation{}}
	var warnings []string

	var used []dayRead
	for _, d := range reads {
		if d.partial {
			result.DaysLeft++
			continue
		}
		used = append(used, d)
	}
	result.DaysRead = len(reads)
	result.Partial = result.DaysLeft > 0
	if result.DaysLeft > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s read only in part and left out of step dating.", result.DaysLeft, plural(result.DaysLeft, "day was", "days were")))
	}

	volume := map[string]int64{}
	for _, d := range used {
		for op, h := range d.ops {
			volume[op] += h.count
		}
	}
	ops := make([]string, 0, len(volume))
	for op := range volume {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool {
		if volume[ops[i]] != volume[ops[j]] {
			return volume[ops[i]] > volume[ops[j]]
		}
		return ops[i] < ops[j]
	})
	if len(ops) > stepTopOperations {
		ops = ops[:stepTopOperations]
	}

	type found struct {
		op  int // index into result.Operations
		day int
	}
	var hits []found
	for _, op := range ops {
		var pts []dayPoint
		for _, d := range used {
			if h := d.ops[op]; h != nil && h.count >= MinSeriesDayCount {
				if p95, ok := h.quantile(0.95); ok && p95 > 0 {
					pts = append(pts, dayPoint{date: d.day.Date, num: dayNumber(d.day.Date), count: h.count, p95: p95, hist: h})
				}
			}
		}
		row := StepOperation{Operation: op, Volume: volume[op], DaysUsed: len(pts)}
		fit, reason := fitStep(pts)
		if fit.date != "" {
			before, after := math.Round(fit.before*10)/10, math.Round(fit.after*10)/10
			ratio, traffic := math.Round(fit.ratio*100)/100, math.Round(fit.trafficRatio*100)/100
			row.BeforeMs, row.AfterMs, row.Ratio, row.TrafficRatio = &before, &after, &ratio, &traffic
		}
		if reason == "" {
			row.Found, row.Date = true, fit.date
			hits = append(hits, found{op: len(result.Operations), day: dayNumber(fit.date)})
		} else {
			row.Reason = reason
		}
		result.Operations = append(result.Operations, row)
	}

	dates := make([]int, len(hits))
	for i, h := range hits {
		dates[i] = h.day
	}
	if day, members, ok := sharedStepDate(dates); ok {
		result.Date = time.Unix(int64(day)*86400, 0).UTC().Format("2006-01-02")
		for _, m := range members {
			result.Sharing = append(result.Sharing, result.Operations[hits[m].op].Operation)
		}
		result.Split = suggestSplit(result.Date, loc, used)
	}
	return result, warnings
}

// suggestSplit cuts the window at the start of date in the zone. Each side is at
// most stepSplitDays whole days, never more than the days that were read on that
// side, and never over MaxInterval.
func suggestSplit(date string, loc *time.Location, used []dayRead) *SuggestedSplit {
	cutDay := dayNumber(date)
	var before, after []dayWindow
	for _, d := range used {
		if dayNumber(d.day.Date) < cutDay {
			before = append(before, d.day)
		} else {
			after = append(after, d.day)
		}
	}
	if len(before) == 0 || len(after) == 0 {
		return nil
	}
	n := stepSplitDays
	if len(before) < n {
		n = len(before)
	}
	if len(after) < n {
		n = len(after)
	}
	split := &SuggestedSplit{
		Date:          date,
		Zone:          loc.String(),
		BaselineStart: before[len(before)-n].Start,
		BaselineEnd:   before[len(before)-1].End,
		Start:         after[0].Start,
		End:           after[n-1].End,
	}
	if split.BaselineEnd.Sub(split.BaselineStart) > MaxInterval || split.End.Sub(split.Start) > MaxInterval {
		return nil
	}
	return split
}

// stepFindings prints the finding: the shared date, or "not observed" when at
// least stepMinOperations operations had enough days to be fitted and none
// shared a date. It prints nothing when too few operations could be fitted.
func stepFindings(in PatternInput, sc *StepChange) []Finding {
	id := seriesEvidenceID("B9", in.SourceIDs, sc.Start, sc.End, sc.Zone)
	if sc.Date != "" {
		var lead *StepOperation
		var n int
		for i := range sc.Operations {
			op := &sc.Operations[i]
			if !op.Found {
				continue
			}
			for _, s := range sc.Sharing {
				if s == op.Operation {
					n += int(op.Volume)
					if lead == nil {
						lead = op
					}
				}
			}
		}
		if lead == nil || lead.BeforeMs == nil || lead.AfterMs == nil {
			return nil
		}
		return []Finding{{
			Rule: "B9", Label: EvidenceSymptom, N: n, LowN: n < LowNRows, EvidenceID: id,
			Headline: fmt.Sprintf("Observed: daily p95 for %s moved from %s s to %s s around %s. %d of the top %d %s show a step of at least %g times within a day of that date. This dates a change in the numbers; it does not explain the change. Days with fewer than %d timed requests are left out, and each p95 is accurate to about 5%%.",
				lead.Operation, fmtSeconds(*lead.BeforeMs), fmtSeconds(*lead.AfterMs), sc.Date,
				len(sc.Sharing), len(sc.Operations), plural(len(sc.Sharing), "operation", "operations"), stepMinRatio, MinSeriesDayCount),
		}}
	}
	fitted := 0
	for _, op := range sc.Operations {
		if op.Reason != stepReasonShort {
			fitted++
		}
	}
	if fitted < stepMinOperations || len(sc.Operations) == 0 {
		return nil
	}
	from, to := sc.Start.In(zoneOf(sc.Zone)).Format("2006-01-02"), sc.End.Add(-time.Second).In(zoneOf(sc.Zone)).Format("2006-01-02")
	return []Finding{{
		Rule: "B9", Label: EvidenceSymptom, N: fitted, LowN: fitted < LowNRows, EvidenceID: id,
		Headline: fmt.Sprintf("Not observed: no step found from %s to %s. No date was shared by %d or more of the top operations.", from, to, stepMinOperations),
	}}
}

// zoneOf resolves a zone name that LoadZone already accepted.
func zoneOf(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
