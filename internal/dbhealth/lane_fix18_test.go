package dbhealth

import (
	"strings"
	"testing"
	"time"
)

// FIX-18 (DATA-04): thinning the interval series must not merge points across the gap between the reference and incident windows, and a merged
// point must carry the block time it really covers so busy time per second stays right.
// FIX-19 (DATA-08): one window may be at most 90 days long.

const fix18Block = 900 // seconds in one Query Store block

// fix18Series makes n consecutive blocks starting at start, every point with the same executions and average.
func fix18Series(start time.Time, n int, execs int64, avgMs float64) []LaneSeriesPoint {
	out := make([]LaneSeriesPoint, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, LaneSeriesPoint{T: start.Unix() + int64(i)*fix18Block, Execs: execs, AvgMs: avgMs, MaxMs: avgMs})
	}
	return out
}

// fix18Request has a reference of refN blocks and an incident of incN blocks a week later.
func fix18Request(refN, incN int) LaneRequest {
	t0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	refEnd := t0.Add(time.Duration(refN*fix18Block) * time.Second)
	incStart := refEnd.Add(7 * 24 * time.Hour)
	return LaneRequest{
		Reference: Window{Start: t0, End: refEnd},
		Incident:  Window{Start: incStart, End: incStart.Add(time.Duration(incN*fix18Block) * time.Second)},
		Database:  "AuditDb",
	}
}

func TestFix18SeriesIsNotMergedAcrossTheWindowGap(t *testing.T) {
	// 2001 reference blocks: a plain grouping by 2 would pair the last reference block with the first incident block.
	req := fix18Request(2001, 1500)
	pts := append(fix18Series(req.Reference.Start, 2001, 1, 10), fix18Series(req.Incident.Start, 1500, 1000, 10)...)
	if len(pts) <= laneSeriesCap {
		t.Fatalf("test setup: %d points do not exceed the cap", len(pts))
	}
	b := newLaneBuilder(req)
	b.setSeries(pts)
	if len(b.res.Series) > laneSeriesCap {
		t.Fatalf("%d points kept, cap is %d", len(b.res.Series), laneSeriesCap)
	}
	var refExecs, incExecs int64
	for _, p := range b.res.Series {
		switch {
		case req.Reference.Contains(time.Unix(p.T, 0)):
			refExecs += p.Execs
		case req.Incident.Contains(time.Unix(p.T, 0)):
			incExecs += p.Execs
		default:
			t.Fatalf("point at %d lies in neither window", p.T)
		}
	}
	if refExecs != 2001 {
		t.Errorf("reference executions = %d, want 2001 (incident points were merged into the reference)", refExecs)
	}
	if incExecs != 1500*1000 {
		t.Errorf("incident executions = %d, want %d", incExecs, 1500*1000)
	}
}

func TestFix18EachWindowKeepsPointsWhenOneIsShort(t *testing.T) {
	// A short reference next to a long incident: the reference must not be thinned to nothing or swallowed.
	req := fix18Request(10, 4000)
	pts := append(fix18Series(req.Reference.Start, 10, 5, 10), fix18Series(req.Incident.Start, 4000, 5, 10)...)
	b := newLaneBuilder(req)
	b.setSeries(pts)
	refPoints := 0
	var total int64
	for _, p := range b.res.Series {
		total += p.Execs
		if req.Reference.Contains(time.Unix(p.T, 0)) {
			refPoints++
		}
	}
	if refPoints == 0 {
		t.Fatal("the reference window lost every point")
	}
	if total != 4010*5 {
		t.Errorf("executions %d, want %d", total, 4010*5)
	}
}

func TestFix18MergedPointCarriesTheSumOfBlockSeconds(t *testing.T) {
	pts := fix18Series(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), 6, 10, 20)
	out := downsampleSeries(pts, 2) // groups of 3 blocks
	if len(out) != 2 {
		t.Fatalf("%d points, want 2", len(out))
	}
	for i, p := range out {
		if p.BlockSeconds != 3*fix18Block {
			t.Errorf("point %d covers %d s, want %d", i, p.BlockSeconds, 3*fix18Block)
		}
	}
	// A point that already covers more than one block counts for what it covers.
	pts[0].BlockSeconds = 2 * fix18Block
	out = downsampleSeries(pts, 2)
	if got, want := out[0].BlockSeconds, int64(2*fix18Block+2*fix18Block); got != want {
		t.Errorf("first merged point covers %d s, want %d", got, want)
	}
	if got, want := out[1].BlockSeconds, int64(3*fix18Block); got != want {
		t.Errorf("second merged point covers %d s, want %d", got, want)
	}
}

func TestFix18SeriesWithinTheLimitIsLeftAlone(t *testing.T) {
	pts := fix18Series(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), 5, 10, 20)
	for i, p := range downsampleSeries(pts, 5) {
		if p != pts[i] || p.BlockSeconds != 0 {
			t.Errorf("point %d changed: %+v", i, p)
		}
	}
}

func TestFix18BusyPerSecondIsUnchangedByGrouping(t *testing.T) {
	for _, qs := range []*LaneQueryStore{nil, {State: QSStateOn, IntervalMinutes: 15}} {
		req := fix18Request(2001, 1500)
		pts := append(fix18Series(req.Reference.Start, 2001, 10, 20), fix18Series(req.Incident.Start, 1500, 10, 20)...)
		b := newLaneBuilder(req)
		b.res.QueryStore = qs
		b.setSeries(pts)
		perBlock := 10 * 20.0 / fix18Block // busy ms per second inside one block
		var span int64
		for _, p := range b.res.Series {
			s := p.BlockSeconds
			if s == 0 {
				s = fix18Block
			}
			span += s
			if got := p.AvgMs * float64(p.Execs) / float64(s); got < perBlock-1e-9 || got > perBlock+1e-9 {
				t.Fatalf("point at %d: busy per second %v, want %v", p.T, got, perBlock)
			}
		}
		if want := int64(3501 * fix18Block); span != want {
			t.Errorf("spans add up to %d s, want %d", span, want)
		}
	}
}

func fix18Valid(refDays, incDays time.Duration, extra time.Duration) LaneRequest {
	day := 24 * time.Hour
	incStart := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	return LaneRequest{
		Reference: Window{Start: incStart.Add(-refDays*day - 24*time.Hour), End: incStart.Add(-24 * time.Hour).Add(extra)},
		Incident:  Window{Start: incStart, End: incStart.Add(incDays * day).Add(extra)},
	}
}

func TestFix19NinetyDaysIsAcceptedNinetyOneIsNot(t *testing.T) {
	if err := fix18Valid(90, 90, 0).Validate(); err != nil {
		t.Fatalf("90 days refused: %v", err)
	}
	over := fix18Valid(90, 91, 0).Validate()
	if over == nil || !strings.Contains(over.Error(), "90 days") || !strings.Contains(over.Error(), "incident") {
		t.Errorf("a 91 day incident window gave %v", over)
	}
	over = fix18Valid(91, 90, 0).Validate()
	if over == nil || !strings.Contains(over.Error(), "90 days") || !strings.Contains(over.Error(), "reference") {
		t.Errorf("a 91 day reference window gave %v", over)
	}
	if err := fix18Valid(90, 90, time.Second).Validate(); err == nil {
		t.Error("one second over 90 days was accepted")
	}
}

func TestFix19HugeWindowIsRefusedWithoutWork(t *testing.T) {
	huge := LaneRequest{
		Reference: Window{Start: time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		Incident:  Window{Start: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), End: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	started := time.Now()
	err := huge.Validate()
	if err == nil {
		t.Fatal("a window of thousands of years was accepted")
	}
	if time.Since(started) > time.Second {
		t.Errorf("refusing took %v", time.Since(started))
	}
	// The plain message is a sentence a first-time investigator can act on.
	if msg := err.Error(); !strings.Contains(msg, "90 days") || strings.Contains(strings.ToLower(msg), "validat") {
		t.Errorf("message %q", msg)
	}
}
