package audit

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func rollupPlanFor(t *testing.T, zone string, from, to time.Time) RollupPlan {
	t.Helper()
	plan, err := PlanRollup(RollupInput{SourceIDs: []string{"s"}, From: from, To: to, TimeZone: zone})
	if err != nil {
		t.Fatalf("PlanRollup(%s): %v", zone, err)
	}
	return plan
}

func rollupLocal(t *testing.T, zone string, y int, m time.Month, d, h, mi int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(y, m, d, h, mi, 0, 0, loc).UTC()
}

func msp(v float64) *float64 { return &v }

func TestPlanRollupValidation(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	good := RollupInput{SourceIDs: []string{"a"}, From: from, To: from.Add(48 * time.Hour)}
	cases := map[string]func(*RollupInput){
		"no sources":        func(in *RollupInput) { in.SourceIDs = nil },
		"blank source":      func(in *RollupInput) { in.SourceIDs = []string{""} },
		"duplicate source":  func(in *RollupInput) { in.SourceIDs = []string{"a", "a"} },
		"too many sources":  func(in *RollupInput) { in.SourceIDs = make([]string, MaxSources+1) },
		"zero from":         func(in *RollupInput) { in.From = time.Time{} },
		"zero to":           func(in *RollupInput) { in.To = time.Time{} },
		"from after to":     func(in *RollupInput) { in.From, in.To = in.To, in.From },
		"from equals to":    func(in *RollupInput) { in.To = in.From },
		"121 days":          func(in *RollupInput) { in.To = in.From.Add(121 * 24 * time.Hour) },
		"before 1970":       func(in *RollupInput) { in.From = time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC) },
		"NaN slowMs":        func(in *RollupInput) { in.SlowMs = math.NaN() },
		"Inf slowMs":        func(in *RollupInput) { in.SlowMs = math.Inf(1) },
		"negative slowMs":   func(in *RollupInput) { in.SlowMs = -5 },
		"huge slowMs":       func(in *RollupInput) { in.SlowMs = MaxRollupSlowMs + 1 },
		"fractional slowMs": func(in *RollupInput) { in.SlowMs = 0.5 },
		"unknown zone":      func(in *RollupInput) { in.TimeZone = "Mars/Olympus" },
		"local zone":        func(in *RollupInput) { in.TimeZone = "Local" },
		"zone with a dot":   func(in *RollupInput) { in.TimeZone = "../etc/passwd" },
		"long zone":         func(in *RollupInput) { in.TimeZone = strings.Repeat("A", 65) },
	}
	for name, mutate := range cases {
		in := good
		in.SourceIDs = append([]string(nil), good.SourceIDs...)
		mutate(&in)
		if _, err := PlanRollup(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	plan, err := PlanRollup(good)
	if err != nil || plan.Zone != "UTC" || plan.SlowMs != DefaultRollupSlowMs || plan.Width != time.Hour {
		t.Fatalf("defaults: %+v, %v", plan, err)
	}
	in := good
	in.To = in.From.Add(120 * 24 * time.Hour)
	if _, err := PlanRollup(in); err != nil {
		t.Errorf("exactly 120 days must be allowed: %v", err)
	}
	// The rollup window never lifts the 7-day limit of the other endpoints.
	if MaxRollupWindow <= MaxInterval || MaxInterval != 7*24*time.Hour {
		t.Errorf("MaxInterval %v and MaxRollupWindow %v must stay separate", MaxInterval, MaxRollupWindow)
	}
}

func TestRollupSlotWidthFollowsTheZone(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for zone, want := range map[string]time.Duration{
		"UTC": time.Hour, "America/New_York": time.Hour, "Asia/Kolkata": 30 * time.Minute,
		"Asia/Kathmandu": 15 * time.Minute, "Australia/Lord_Howe": 30 * time.Minute,
	} {
		if got := rollupPlanFor(t, zone, from, from.Add(72*time.Hour)).Width; got != want {
			t.Errorf("%s width = %v, want %v", zone, got, want)
		}
	}
}

// A local day is 23, 24 or 25 hours across a DST change, and every request lands
// on the right local day, including the first and last minutes of that day.
func TestDailyAcrossDSTChanges(t *testing.T) {
	cases := []struct {
		zone  string
		month time.Month
		day   int
		hours float64
	}{
		{"America/New_York", time.March, 8, 23},
		{"America/New_York", time.November, 1, 25},
		{"Europe/Berlin", time.March, 29, 23},
		{"Europe/Berlin", time.October, 25, 25},
		{"Australia/Lord_Howe", time.October, 4, 23.5},
		{"Australia/Lord_Howe", time.April, 5, 24.5},
		{"Asia/Kolkata", time.June, 10, 24},
	}
	for _, c := range cases {
		t.Run(c.zone+"/"+c.month.String(), func(t *testing.T) {
			from := rollupLocal(t, c.zone, 2026, c.month, c.day-1, 0, 0)
			to := rollupLocal(t, c.zone, 2026, c.month, c.day+2, 0, 0)
			plan := rollupPlanFor(t, c.zone, from, to)
			b := NewRollupBuilder(plan.Width)
			for _, at := range []time.Time{
				rollupLocal(t, c.zone, 2026, c.month, c.day-1, 23, 30),
				rollupLocal(t, c.zone, 2026, c.month, c.day, 0, 30),
				rollupLocal(t, c.zone, 2026, c.month, c.day, 23, 30),
				rollupLocal(t, c.zone, 2026, c.month, c.day+1, 0, 0),
			} {
				b.AddRow(at, "synchronous", "valid", "", msp(120), 1000)
			}
			days, err := BuildDaily(plan, b)
			if err != nil || len(days) != 3 {
				t.Fatalf("days = %d, err %v", len(days), err)
			}
			wantTotals := []int64{1, 2, 1}
			for i, d := range days {
				if d.Total != wantTotals[i] || d.Partial {
					t.Errorf("day %d %s: total %d partial %v, want %d false", i, d.Date, d.Total, d.Partial, wantTotals[i])
				}
			}
			mid := days[1]
			if mid.Hours != c.hours || mid.End.Sub(mid.Start).Hours() != c.hours {
				t.Errorf("DST day: hours %v, span %v, want %v", mid.Hours, mid.End.Sub(mid.Start).Hours(), c.hours)
			}
		})
	}
}

func TestHourGridAcrossDSTChanges(t *testing.T) {
	zone := "America/New_York"
	from := rollupLocal(t, zone, 2026, time.March, 7, 0, 0)
	to := rollupLocal(t, zone, 2026, time.March, 10, 0, 0)
	grid, err := BuildHourGrid(rollupPlanFor(t, zone, from, to), NewRollupBuilder(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	perDay := map[string]int{}
	for _, c := range grid.Cells {
		perDay[c.Date]++
		if c.Date == "2026-03-08" && c.Hour == 2 {
			t.Error("the skipped 02:00 hour must have no cell")
		}
	}
	if perDay["2026-03-07"] != 24 || perDay["2026-03-08"] != 23 || perDay["2026-03-09"] != 24 {
		t.Errorf("cells per day = %v, want 24, 23, 24", perDay)
	}
	if len(grid.Days) != 3 || grid.Days[1].Hours != 23 {
		t.Errorf("grid days = %+v", grid.Days)
	}

	from = rollupLocal(t, zone, 2026, time.November, 1, 0, 0)
	to = rollupLocal(t, zone, 2026, time.November, 2, 0, 0)
	b := NewRollupBuilder(time.Hour)
	// One request in each of the two 01:00 hours: both belong to one cell.
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	for _, at := range []time.Time{first, first.Add(time.Hour)} {
		b.AddRow(at, "synchronous", "valid", "", msp(10), 1000)
	}
	plan := rollupPlanFor(t, zone, from, to)
	grid, err = BuildHourGrid(plan, b)
	if err != nil || len(grid.Cells) != 24 {
		t.Fatalf("fall-back day cells = %d, err %v, want 24", len(grid.Cells), err)
	}
	var repeated *HourCell
	for i := range grid.Cells {
		if grid.Cells[i].Hour == 1 {
			repeated = &grid.Cells[i]
		}
	}
	if repeated == nil || repeated.Count != 2 || repeated.End.Sub(repeated.Start) != 2*time.Hour {
		t.Fatalf("repeated hour cell = %+v, want one cell of 2 requests spanning 2 hours", repeated)
	}
	if grid.Days[0].Hours != 25 || grid.Days[0].Partial {
		t.Errorf("fall-back grid day = %+v", grid.Days[0])
	}
}

func TestDailyMarksPartialFirstAndLastDays(t *testing.T) {
	from := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 12, 6, 0, 0, 0, time.UTC)
	plan := rollupPlanFor(t, "UTC", from, to)
	days, err := BuildDaily(plan, NewRollupBuilder(plan.Width))
	if err != nil || len(days) != 3 {
		t.Fatalf("days %d err %v", len(days), err)
	}
	for i, want := range []bool{true, false, true} {
		if days[i].Partial != want {
			t.Errorf("day %s partial = %v, want %v", days[i].Date, days[i].Partial, want)
		}
	}
	if !days[0].Start.Equal(from) || !days[2].End.Equal(to) || days[1].Hours != 24 {
		t.Errorf("window cut: first start %v, last end %v, middle hours %v", days[0].Start, days[2].End, days[1].Hours)
	}
	grid, err := BuildHourGrid(plan, NewRollupBuilder(plan.Width))
	if err != nil || len(grid.Days) != 3 || !grid.Days[0].Partial || grid.Days[1].Partial || !grid.Days[2].Partial {
		t.Fatalf("grid days = %+v err %v", grid.Days, err)
	}
	if !grid.Cells[0].Start.Equal(from) || !grid.Cells[len(grid.Cells)-1].End.Equal(to) {
		t.Errorf("grid cells must be cut to the window: %v .. %v", grid.Cells[0].Start, grid.Cells[len(grid.Cells)-1].End)
	}
}

func TestDailyKindsStatusAndLatencyMasking(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	plan := rollupPlanFor(t, "UTC", from, from.Add(48*time.Hour))
	b := NewRollupBuilder(plan.Width)
	at := from.Add(3 * time.Hour)
	for i := 0; i < MinSampleCount-1; i++ {
		b.AddRow(at, "synchronous", "valid", "", msp(float64(100+i)), 1000)
	}
	b.AddRow(at, "synchronous", "valid", StatusClientError, msp(100), 1000)
	b.AddRow(at, "synchronous", "valid", StatusServerError, msp(100), 1000)
	b.AddRow(at, "hangfire", "valid", "", nil, 1000)
	b.AddRow(at, "asynchronous", "incomplete", "", nil, 1000)
	b.AddRow(at, "synchronous", "invalid", "", msp(5), 1000)
	// Day two has 19 valid synchronous rows: no percentile.
	for i := 0; i < MinSampleCount-1; i++ {
		b.AddRow(from.Add(30*time.Hour), "synchronous", "valid", "", msp(50), 1000)
	}
	days, err := BuildDaily(plan, b)
	if err != nil || len(days) != 2 {
		t.Fatalf("days %d err %v", len(days), err)
	}
	d := days[0]
	if d.Total != int64(MinSampleCount+4) || d.Sync != int64(MinSampleCount+2) || d.Background != 1 || d.Other != 1 {
		t.Errorf("counts: total %d sync %d background %d other %d", d.Total, d.Sync, d.Background, d.Other)
	}
	if d.Count4xx != 1 || d.Count5xx != 1 {
		t.Errorf("status counts: %d %d", d.Count4xx, d.Count5xx)
	}
	if d.LatencyCount != int64(MinSampleCount+1) || d.P50 == nil || d.P95 == nil {
		t.Errorf("a day with %d valid sync rows needs percentiles: count %d p50 %v", MinSampleCount+1, d.LatencyCount, d.P50)
	}
	if days[1].LatencyCount != int64(MinSampleCount-1) || days[1].P50 != nil || days[1].P95 != nil {
		t.Errorf("a thin day must be masked: %+v", days[1])
	}
}

func TestDailyBusyLowAndTrend(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	plan := rollupPlanFor(t, "UTC", from, from.Add(7*24*time.Hour))
	cases := []struct {
		name   string
		totals []int64
		want   []string // VsPrev per day
		low    []bool
		base   []string // VsPrevDate per day
	}{
		{
			name:   "busy, low days skipped as base, exact 10 percent counts as a move",
			totals: []int64{200, 300, 50, 330, 297, 300, 303},
			want:   []string{"", "up", "", "up", "down", "flat", "flat"},
			low:    []bool{false, false, true, false, false, false, false},
			base:   []string{"", "2026-06-01", "", "2026-06-02", "2026-06-04", "2026-06-05", "2026-06-06"},
		},
		{
			name:   "first busy day has no base",
			totals: []int64{10, 20, 500, 520, 40, 40, 600},
			want:   []string{"", "", "", "flat", "", "", "up"},
			low:    []bool{true, true, false, false, true, true, false},
			base:   []string{"", "", "", "2026-06-03", "", "", "2026-06-04"},
		},
		{
			name:   "99 is low, 100 is busy",
			totals: []int64{100, 99, 100, 100, 100, 100, 100},
			want:   []string{"", "", "flat", "flat", "flat", "flat", "flat"},
			low:    []bool{false, true, false, false, false, false, false},
			base:   []string{"", "", "2026-06-01", "2026-06-03", "2026-06-04", "2026-06-05", "2026-06-06"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := NewRollupBuilder(plan.Width)
			for i, total := range c.totals {
				b.AddCounts(from.Add(time.Duration(i)*24*time.Hour).Unix(), RollupCounts{Total: total, Sync: total})
			}
			days, err := BuildDaily(plan, b)
			if err != nil || len(days) != len(c.totals) {
				t.Fatalf("days %d err %v", len(days), err)
			}
			for i, d := range days {
				if d.VsPrev != c.want[i] || d.LowTraffic != c.low[i] || d.VsPrevDate != c.base[i] {
					t.Errorf("day %d total %d: vsPrev %q (want %q) low %v (want %v) base %q (want %q)", i, d.Total, d.VsPrev, c.want[i], d.LowTraffic, c.low[i], d.VsPrevDate, c.base[i])
				}
				if (d.VsPrev == "") != (d.VsPrevPct == nil) {
					t.Errorf("day %d: vsPrev %q and vsPrevPct %v must go together", i, d.VsPrev, d.VsPrevPct)
				}
			}
		})
	}
}

func TestDailyPartialDayIsNeverACompareBase(t *testing.T) {
	// The window starts at noon, so the first day is partial and gets no arrow and
	// is no base for the second day.
	from := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := rollupPlanFor(t, "UTC", from, from.Add(48*time.Hour))
	b := NewRollupBuilder(plan.Width)
	b.AddCounts(from.Unix(), RollupCounts{Total: 500})
	b.AddCounts(from.Add(24*time.Hour).Unix(), RollupCounts{Total: 900})
	days, err := BuildDaily(plan, b)
	if err != nil || len(days) != 3 {
		t.Fatalf("days %d err %v", len(days), err)
	}
	for _, d := range days {
		if d.Partial && d.VsPrev != "" {
			t.Errorf("partial day %s has arrow %q", d.Date, d.VsPrev)
		}
	}
	if days[1].VsPrev != "" {
		t.Errorf("the day after a partial day has no base, got %q", days[1].VsPrev)
	}
}

func TestHourGridNoiseMaskingAndSlowCount(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	plan := rollupPlanFor(t, "UTC", from, from.Add(24*time.Hour))
	b := NewRollupBuilder(plan.Width)
	// Hour 1: four rows, noise. Hour 2: five rows, two of them slow.
	for i := 0; i < RollupNoiseCount-1; i++ {
		b.AddRow(from.Add(time.Hour+time.Duration(i)*time.Minute), "synchronous", "valid", "", msp(2000), 1000)
	}
	for i := 0; i < RollupNoiseCount; i++ {
		d := 100.0
		if i < 2 {
			d = 1500
		}
		b.AddRow(from.Add(2*time.Hour+time.Duration(i)*time.Minute), "synchronous", "valid", "", msp(d), 1000)
	}
	grid, err := BuildHourGrid(plan, b)
	if err != nil || len(grid.Cells) != 24 || len(grid.Days) != 1 {
		t.Fatalf("cells %d days %d err %v", len(grid.Cells), len(grid.Days), err)
	}
	empty, noisy, solid := grid.Cells[0], grid.Cells[1], grid.Cells[2]
	if empty.Count != 0 || !empty.Noise || empty.P95 != nil {
		t.Errorf("empty cell = %+v", empty)
	}
	if noisy.Count != 4 || !noisy.Noise || noisy.P95 != nil {
		t.Errorf("a cell under %d requests is noise with no p95: %+v", RollupNoiseCount, noisy)
	}
	if solid.Count != 5 || solid.Noise || solid.P95 == nil || solid.SlowCount != 2 {
		t.Errorf("solid cell = %+v", solid)
	}
	if !solid.Start.Equal(from.Add(2*time.Hour)) || !solid.End.Equal(from.Add(3*time.Hour)) {
		t.Errorf("cell bounds %v .. %v", solid.Start, solid.End)
	}
}

func TestHourGridHalfHourZoneCellsFoldTwoSlots(t *testing.T) {
	zone := "Asia/Kolkata"
	from := rollupLocal(t, zone, 2026, time.June, 10, 0, 0)
	plan := rollupPlanFor(t, zone, from, from.Add(24*time.Hour))
	if plan.Width != 30*time.Minute {
		t.Fatalf("width %v", plan.Width)
	}
	b := NewRollupBuilder(plan.Width)
	b.AddRow(from.Add(10*time.Minute), "synchronous", "valid", "", msp(10), 1000)
	b.AddRow(from.Add(40*time.Minute), "synchronous", "valid", "", msp(10), 1000)
	grid, err := BuildHourGrid(plan, b)
	if err != nil || len(grid.Cells) != 24 || grid.Cells[0].Count != 2 || grid.Cells[0].End.Sub(grid.Cells[0].Start) != time.Hour {
		t.Fatalf("cells %d first %+v err %v", len(grid.Cells), grid.Cells[0], err)
	}
}

func TestSizeBucketEdges(t *testing.T) {
	cases := []struct {
		kb   float64
		want string
	}{
		{0, SizeBucketUnder1}, {0.999, SizeBucketUnder1}, {1, SizeBucket1to10}, {9.999, SizeBucket1to10},
		{10, SizeBucket10to100}, {99.9, SizeBucket10to100}, {100, SizeBucket100to1000}, {999.9, SizeBucket100to1000},
		{1000, SizeBucket1000Plus}, {1e9, SizeBucket1000Plus},
		{-1, SizeBucketUnknown}, {math.NaN(), SizeBucketUnknown}, {math.Inf(1), SizeBucketUnknown},
	}
	for _, c := range cases {
		if got := SizeBucket(c.kb); got != c.want {
			t.Errorf("SizeBucket(%v) = %q, want %q", c.kb, got, c.want)
		}
	}
	if got := BreakdownKey(BreakdownRequestSizeBucket, RequestRecord{RequestKBMissing: true}); got != SizeBucketUnknown {
		t.Errorf("a missing size is %q, not a size", got)
	}
	if got := BreakdownKey(BreakdownRequestSizeBucket, RequestRecord{RequestKB: 0}); got != SizeBucketUnder1 {
		t.Errorf("a recorded zero size is %q", got)
	}
	if f := NewBreakdownFilter(BreakdownRequestSizeBucket, SizeBucket1to10, 0); f != nil {
		t.Error("size buckets have no drill-down filter")
	}
	if f := NewBreakdownFilter(BreakdownHourOfDay, "13:00", 0); f != nil {
		t.Error("hours of day have no drill-down filter")
	}
}

func TestHourOfDayBreakdownUsesTheDisplayZoneAndMasksThinRows(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	at := time.Date(2026, 6, 10, 20, 15, 0, 0, time.UTC) // 01:45 next day in Kolkata
	b := NewBreakdownBuilder(BreakdownHourOfDay, 5)
	b.SetLocation(loc)
	for i := 0; i < MinSampleCount; i++ {
		b.AddRecord(RequestRecord{InitiatedAt: &at}, "valid", false, "", msp(100))
	}
	b.AddRecord(RequestRecord{InitiatedAt: &at}, "valid", false, StatusServerError, msp(100))
	late := at.Add(4 * time.Hour)
	for i := 0; i < MinSampleCount-1; i++ {
		b.AddRecord(RequestRecord{InitiatedAt: &late}, "valid", false, "", msp(100))
	}
	b.AddRecord(RequestRecord{}, "valid", false, "", nil)
	out, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	byValue := map[string]BreakdownRow{}
	for _, r := range out.Rows {
		byValue[r.Value] = r
	}
	if r := byValue["01:00"]; r.Count != int64(MinSampleCount+1) || r.Count5xx != 1 || r.Stats.P50 == nil {
		t.Errorf("01:00 row = %+v", r)
	}
	// The same masking as every other dimension: no p99 below its sample floor, and
	// no latency at all when the query pools request types.
	if r := byValue["05:00"]; r.Count != int64(MinSampleCount-1) || r.Stats.P99 != nil {
		t.Errorf("a thin row must have no p99: %+v", r)
	}
	b.SetPooled(true)
	pooled, err := b.Build()
	if err != nil || !pooled.PooledKinds {
		t.Fatalf("pooled build: %v", err)
	}
	for _, r := range pooled.Rows {
		if r.Stats.P50 != nil || r.Stats.P95 != nil || r.Stats.MeanMs != nil {
			t.Errorf("a pooled query must show no latency for %s: %+v", r.Value, r.Stats)
		}
	}
	if r := byValue[BreakdownMissingLabel]; !r.Missing || r.Count != 1 {
		t.Errorf("a row with no time must be missing: %+v", out.Rows)
	}
	if err := ValidateBreakdown(BreakdownInput{Query: validQuery(), Dim: BreakdownHourOfDay, TimeZone: "Mars/Olympus"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a bad zone must be rejected, got %v", err)
	}
	if err := ValidateBreakdown(BreakdownInput{Query: validQuery(), Dim: BreakdownHourOfDay, TimeZone: "Asia/Kolkata"}); err != nil {
		t.Errorf("a good zone must pass: %v", err)
	}
}

func TestDemoRollupServesDailyAndHourGrid(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	reader, ok := d.(RollupReader)
	if !ok {
		t.Fatal("demo repository must implement RollupReader")
	}
	sources, err := d.Sources(context.Background())
	if err != nil || len(sources) == 0 {
		t.Fatalf("sources %v err %v", sources, err)
	}
	plan, err := PlanRollup(RollupInput{SourceIDs: []string{sources[0].ID}, From: now.Add(-30 * 24 * time.Hour), To: now, TimeZone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	slots, err := reader.Rollup(context.Background(), plan)
	if err != nil || len(slots.Sources) != 1 || !slots.Sources[0].OK || len(slots.Provenance) != 1 {
		t.Fatalf("rollup: %+v err %v", slots, err)
	}
	days, err := BuildDaily(plan, slots.Builder)
	if err != nil || len(days) < 30 {
		t.Fatalf("days %d err %v", len(days), err)
	}
	var low, busy int
	for _, day := range days {
		if day.LowTraffic {
			low++
		} else {
			busy++
		}
	}
	if low == 0 || busy == 0 {
		t.Errorf("the demo history should show both quiet and busy days, got %d low and %d busy", low, busy)
	}
	grid, err := BuildHourGrid(plan, slots.Builder)
	if err != nil || len(grid.Cells) < 30*23 {
		t.Fatalf("cells %d err %v", len(grid.Cells), err)
	}
	if _, err := reader.Rollup(context.Background(), RollupPlan{SourceIDs: []string{"missing"}, From: plan.From, To: plan.To, Loc: plan.Loc, Width: plan.Width}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown source: %v", err)
	}
}
