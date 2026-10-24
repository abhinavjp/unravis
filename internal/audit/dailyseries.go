package audit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // embedded zone database; Windows has none otherwise
	"unicode/utf8"
)

// Daily series (B9 and the endpoint daily view). No read path covers more than
// MaxInterval, so a longer series is built one local day at a time: each day is
// one Analyze call over exactly that day, and the day's histograms are kept per
// operation. Nothing here adds a repository method; the reads are the ones the
// comparison already makes. Quantiles come from the merged histogram bins, so a
// daily p95 is the bin midpoint, accurate to about 5% (bins are 10% wide).
const (
	// MaxSeriesDays is the longest series, in whole local days. It equals the
	// bucket cap, so a daily rollup of the same size would hold the same window.
	MaxSeriesDays = MaxTimeBuckets
	// MinSeriesDayCount is the fewest timed requests a day needs to be used.
	MinSeriesDayCount = MinSampleCount
	// liveSeriesMaxDays is the longest endpoint series read from a live source.
	liveSeriesMaxDays = 14
	// maxZoneNameLength bounds the time zone name a caller may send.
	maxZoneNameLength = 64
)

// ErrNoWholeDays is returned (wrapped with ErrInvalidInput) when the window holds
// no whole local day.
var ErrNoWholeDays = errors.New("the window holds no whole day in the display time zone")

// LoadZone resolves an IANA zone name. "" means UTC. Only a real zone name is
// accepted: "Local" and names with odd characters are not.
func LoadZone(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	if len(name) > maxZoneNameLength || !utf8.ValidString(name) || strings.EqualFold(name, "local") {
		return nil, invalidInputf("timeZone must be an IANA zone name such as UTC or Europe/Paris")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '/' || r == '_' || r == '-' || r == '+') {
			return nil, invalidInputf("timeZone must be an IANA zone name such as UTC or Europe/Paris")
		}
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalidInputf("timeZone must be an IANA zone name such as UTC or Europe/Paris")
	}
	return loc, nil
}

// dayWindow is one whole local day: [Start, End), 23 to 25 hours long. Date is
// its calendar date in the zone.
type dayWindow struct {
	Date  string
	Start time.Time
	End   time.Time
}

// localDays lists the whole local days inside [start, end], oldest first, the
// newest maxDays of them. A day that is cut by either edge is left out, so no
// day is short of traffic only because the window ended partway through it.
func localDays(start, end time.Time, loc *time.Location, maxDays int) []dayWindow {
	if loc == nil {
		loc = time.UTC
	}
	s := start.In(loc)
	y, m, d := s.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if day.Before(start) {
		day = time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}
	var days []dayWindow
	for {
		y, m, d := day.Date()
		next := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		if next.After(end) || !next.After(day) {
			break
		}
		days = append(days, dayWindow{Date: day.Format("2006-01-02"), Start: day.UTC(), End: next.UTC()})
		day = next
		if len(days) > 4*MaxSeriesDays {
			break
		}
	}
	if maxDays > 0 && len(days) > maxDays {
		days = days[len(days)-maxDays:]
	}
	return days
}

// dayNumber is the day count since 1970-01-01 of a "2006-01-02" date, so two
// dates can be compared in days without a time zone.
func dayNumber(date string) int {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0
	}
	return int(t.Unix() / 86400)
}

// dayHist is a merged histogram of one operation on one day.
type dayHist struct {
	count    int64
	min, max float64
	bins     map[int]int64
}

func newDayHist() *dayHist { return &dayHist{bins: map[int]int64{}} }

// addStats merges one cohort's current-window stats. It reports false when the
// stats hold no timing.
func (h *dayHist) addStats(s Stats) bool {
	if s.Count <= 0 || s.MinMs == nil || s.MaxMs == nil || len(s.Histogram) == 0 {
		return false
	}
	var n int64
	for _, b := range s.Histogram {
		if b.Count <= 0 || b.Bin < 0 || b.Bin > overflowBin {
			continue
		}
		h.bins[b.Bin] += b.Count
		n += b.Count
	}
	if n == 0 {
		return false
	}
	if h.count == 0 || *s.MinMs < h.min {
		h.min = *s.MinMs
	}
	if h.count == 0 || *s.MaxMs > h.max {
		h.max = *s.MaxMs
	}
	h.count += n
	return true
}

// addHist merges another day's histogram into this one.
func (h *dayHist) addHist(o *dayHist) {
	if o == nil || o.count == 0 {
		return
	}
	if h.count == 0 || o.min < h.min {
		h.min = o.min
	}
	if h.count == 0 || o.max > h.max {
		h.max = o.max
	}
	for bin, c := range o.bins {
		h.bins[bin] += c
	}
	h.count += o.count
}

// quantile is the nearest-rank quantile by bin, as the Analyze statistics print
// it: the midpoint of the bin that holds the rank, clipped to the observed range.
// The overflow bin has no upper edge, so its lower edge is returned.
func (h *dayHist) quantile(p float64) (float64, bool) {
	if h == nil || h.count == 0 {
		return 0, false
	}
	bins := make([]int, 0, len(h.bins))
	for bin := range h.bins {
		bins = append(bins, bin)
	}
	sort.Ints(bins)
	rank := int64(math.Ceil(p * float64(h.count)))
	var cumulative int64
	for _, bin := range bins {
		cumulative += h.bins[bin]
		if cumulative < rank {
			continue
		}
		lower, upper := BinBounds(bin)
		if lower < h.min {
			lower = h.min
		}
		if upper == nil {
			return lower, true
		}
		hi := *upper
		if hi > h.max {
			hi = h.max
		}
		if lower > hi {
			lower = hi
		}
		return lower + (hi-lower)/2, true
	}
	return 0, false
}

// dayRead is one day's per-operation histograms.
type dayRead struct {
	day dayWindow
	ops map[string]*dayHist
	// partial is true when a source returned this day only in part. Such a day
	// is not used.
	partial bool
}

// dayQuery is the Analyze query for one day: every kind and outcome, total time,
// one daily bucket, and a short reference window the series does not use.
func dayQuery(sourceIDs []string, w dayWindow, operation string) Query {
	return Query{
		SourceIDs:     append([]string(nil), sourceIDs...),
		Start:         w.Start,
		End:           w.End,
		BaselineStart: w.Start.Add(-10 * time.Second),
		BaselineEnd:   w.Start,
		Kind:          "all",
		Outcome:       "all",
		Metric:        "total",
		BucketSeconds: 86400,
		Operation:     operation,
	}
}

// readDays reads each day with one Analyze call and folds its cohorts into one
// histogram per operation. operation, when set, narrows every read to it. A live
// source (docsExamined is not measured) is allowed at most liveMaxDays days:
// past that the read is refused, after the first day shows what the source is.
// Any other read error stops the series; the caller decides what to say.
func readDays(ctx context.Context, repo Repository, sourceIDs []string, days []dayWindow, operation string, liveMaxDays int) ([]dayRead, []Provenance, error) {
	if len(days) == 0 {
		return nil, nil, nil
	}
	reads := make([]dayRead, 0, len(days))
	var prov []Provenance
	for i, w := range days {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		analysis, err := repo.Analyze(ctx, dayQuery(sourceIDs, w, operation))
		if err != nil {
			return nil, nil, err
		}
		if i == 0 && isLiveRead(analysis.Provenance) && len(days) > liveMaxDays {
			return nil, nil, fmt.Errorf("%w: a daily series reads one day at a time, so on a live source it covers at most %d days", ErrBroadScanRefused, liveMaxDays)
		}
		read := dayRead{day: w, ops: map[string]*dayHist{}}
		for _, c := range analysis.APIs {
			h := newDayHist()
			if !h.addStats(c.Current) {
				continue
			}
			key := c.Controller + "." + c.Action
			if have, ok := read.ops[key]; ok {
				have.addHist(h)
			} else {
				read.ops[key] = h
			}
		}
		for _, p := range analysis.Provenance {
			if p.Window == "current" && p.Completeness == Partial {
				read.partial = true
			}
		}
		prov = append(prov, dailyProvenance(analysis.Provenance)...)
		reads = append(reads, read)
	}
	merged := mergeGapProvenance(prov, false)
	for i := range merged {
		merged[i].Start, merged[i].End = days[0].Start, days[len(days)-1].End
	}
	return reads, merged, nil
}

// dailyProvenance keeps the current-window rows of one day's read.
func dailyProvenance(prov []Provenance) []Provenance {
	var out []Provenance
	for _, p := range prov {
		if p.Window == "current" {
			out = append(out, p)
		}
	}
	return out
}
