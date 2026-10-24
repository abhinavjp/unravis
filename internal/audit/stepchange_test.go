package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// histOf builds a day histogram from n requests that all took ms.
func histOf(n int64, ms float64) *dayHist {
	h := newDayHist()
	h.count, h.min, h.max = n, ms, ms
	h.bins[BinIndex(ms)] = n
	return h
}

// daySpec is one operation's day: the p95 (every request took that long) and the
// request count.
type daySpec struct {
	ms    float64
	count int64
}

// readsFrom lays out one dayRead per day from 2026-03-01 in UTC. series maps an
// operation to its days, oldest first; every series must have the same length.
func readsFrom(series map[string][]daySpec) []dayRead {
	var n int
	for _, s := range series {
		n = len(s)
	}
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	reads := make([]dayRead, n)
	for i := 0; i < n; i++ {
		day := start.AddDate(0, 0, i)
		reads[i] = dayRead{
			day: dayWindow{Date: day.Format("2006-01-02"), Start: day, End: day.AddDate(0, 0, 1)},
			ops: map[string]*dayHist{},
		}
		for op, s := range series {
			reads[i].ops[op] = histOf(s[i].count, s[i].ms)
		}
	}
	return reads
}

// stepSeries is n days of before (days 0..cut-1) then after, at 40 requests a day.
func stepSeries(n, cut int, before, after float64) []daySpec {
	out := make([]daySpec, n)
	for i := range out {
		out[i] = daySpec{before, 40}
		if i >= cut {
			out[i] = daySpec{after, 40}
		}
	}
	return out
}

func TestStepFoundWhenThreeOperationsShareADate(t *testing.T) {
	reads := readsFrom(map[string][]daySpec{
		"Invoices.Search":  stepSeries(24, 12, 300, 900),
		"Orders.Submit":    stepSeries(24, 12, 300, 900),
		"Catalog.Browse":   stepSeries(24, 12, 400, 1200),
		"Accounts.Summary": stepSeries(24, 13, 250, 800), // a day later: within the tolerance
		"Health.Ping":      stepSeries(24, 12, 50, 50),
	})
	sc, _ := stepChangeFrom(reads, time.UTC)
	if sc.Date != "2026-03-13" {
		t.Fatalf("date %q operations %+v", sc.Date, sc.Operations)
	}
	if len(sc.Sharing) != 4 {
		t.Fatalf("sharing %v", sc.Sharing)
	}
	for _, op := range sc.Operations {
		if op.Operation == "Health.Ping" && op.Found {
			t.Fatalf("a flat operation was dated: %+v", op)
		}
	}
	if sc.Split == nil || sc.Split.Date != "2026-03-13" || sc.Split.Zone != "UTC" {
		t.Fatalf("split %+v", sc.Split)
	}
	// Seven whole days each side, cut at the start of the date.
	if !sc.Split.Start.Equal(time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC)) || !sc.Split.BaselineEnd.Equal(sc.Split.Start) ||
		sc.Split.End.Sub(sc.Split.Start) != 7*24*time.Hour || sc.Split.BaselineEnd.Sub(sc.Split.BaselineStart) != 7*24*time.Hour {
		t.Fatalf("split %+v", sc.Split)
	}
	finds := stepFindings(PatternInput{SourceIDs: []string{"s1"}}, sc)
	if len(finds) != 1 || finds[0].Rule != "B9" || finds[0].Label != EvidenceSymptom || !strings.HasPrefix(finds[0].EvidenceID, "p2:b9:") ||
		!strings.HasPrefix(finds[0].Headline, "Observed: daily p95 for ") || !strings.Contains(finds[0].Headline, "around 2026-03-13") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStepNotFoundWhenDatesAreSpread(t *testing.T) {
	reads := readsFrom(map[string][]daySpec{
		"A.One":   stepSeries(30, 8, 300, 900),
		"B.Two":   stepSeries(30, 14, 300, 900),
		"C.Three": stepSeries(30, 20, 300, 900),
	})
	sc, _ := stepChangeFrom(reads, time.UTC)
	sc.Start, sc.End = reads[0].day.Start, reads[len(reads)-1].day.End
	if sc.Date != "" || sc.Split != nil || len(sc.Sharing) != 0 {
		t.Fatalf("a date was shared: %+v", sc)
	}
	found := 0
	for _, op := range sc.Operations {
		if op.Found {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("each operation should still have its own step: %+v", sc.Operations)
	}
	finds := stepFindings(PatternInput{SourceIDs: []string{"s1"}}, sc)
	if len(finds) != 1 || !strings.HasPrefix(finds[0].Headline, "Not observed: no step found from 2026-03-01 to 2026-03-30.") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStepNotFoundOnFlatSeries(t *testing.T) {
	reads := readsFrom(map[string][]daySpec{
		"A.One":   stepSeries(20, 10, 300, 300),
		"B.Two":   stepSeries(20, 10, 300, 450), // 1.5 times: under the 2 times floor
		"C.Three": stepSeries(20, 10, 300, 500),
	})
	sc, _ := stepChangeFrom(reads, time.UTC)
	if sc.Date != "" {
		t.Fatalf("date %q", sc.Date)
	}
	for _, op := range sc.Operations {
		if op.Found || op.Reason != stepReasonSmall {
			t.Fatalf("%+v", op)
		}
	}
	if finds := stepFindings(PatternInput{SourceIDs: []string{"s1"}}, sc); len(finds) != 1 || !strings.HasPrefix(finds[0].Headline, "Not observed:") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStepRejectedWhenTrafficChanges(t *testing.T) {
	doubled := stepSeries(20, 10, 300, 900)
	for i := 10; i < 20; i++ {
		doubled[i].count = 80 // twice the traffic after the step: over 1.5
	}
	halved := stepSeries(20, 10, 300, 900)
	for i := 10; i < 20; i++ {
		halved[i].count = 20 // half the traffic: under 0.67
	}
	reads := readsFrom(map[string][]daySpec{"A.One": doubled, "B.Two": halved, "C.Three": doubled})
	sc, _ := stepChangeFrom(reads, time.UTC)
	if sc.Date != "" {
		t.Fatalf("date %q", sc.Date)
	}
	for _, op := range sc.Operations {
		if op.Found || op.Reason != stepReasonTraffic || op.TrafficRatio == nil {
			t.Fatalf("%+v", op)
		}
	}
	// Traffic within the band is fine: 1.4 times and 0.7 times.
	ok1 := stepSeries(20, 10, 300, 900)
	ok2 := stepSeries(20, 10, 300, 900)
	for i := 10; i < 20; i++ {
		ok1[i].count = 56 // 1.4
		ok2[i].count = 28 // 0.7
	}
	sc, _ = stepChangeFrom(readsFrom(map[string][]daySpec{"A.One": ok1, "B.Two": ok2, "C.Three": ok1}), time.UTC)
	if sc.Date != "2026-03-11" {
		t.Fatalf("date %q %+v", sc.Date, sc.Operations)
	}
}

func TestStepNeedsThreeOperations(t *testing.T) {
	reads := readsFrom(map[string][]daySpec{
		"A.One": stepSeries(20, 10, 300, 900),
		"B.Two": stepSeries(20, 10, 300, 900),
	})
	sc, _ := stepChangeFrom(reads, time.UTC)
	if sc.Date != "" || sc.Split != nil {
		t.Fatalf("two operations dated a step: %+v", sc)
	}
	for _, op := range sc.Operations {
		if !op.Found {
			t.Fatalf("each operation has its own step: %+v", op)
		}
	}
	// Three operations that step, but two are three days from the third.
	reads = readsFrom(map[string][]daySpec{
		"A.One":   stepSeries(30, 10, 300, 900),
		"B.Two":   stepSeries(30, 11, 300, 900),
		"C.Three": stepSeries(30, 14, 300, 900),
	})
	sc, _ = stepChangeFrom(reads, time.UTC)
	if sc.Date != "" {
		t.Fatalf("date %q", sc.Date)
	}
	// Only two operations could be fitted, so there is no "not observed" either.
	if finds := stepFindings(PatternInput{SourceIDs: []string{"s1"}}, &StepChange{Zone: "UTC", Operations: []StepOperation{{Operation: "A.One"}, {Operation: "B.Two"}, {Operation: "C.Three", Reason: stepReasonShort}}}); len(finds) != 0 {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStepSegmentTooShort(t *testing.T) {
	// Nine days: no cut leaves five days on both sides.
	sc, _ := stepChangeFrom(readsFrom(map[string][]daySpec{
		"A.One": stepSeries(9, 4, 300, 900), "B.Two": stepSeries(9, 4, 300, 900), "C.Three": stepSeries(9, 4, 300, 900),
	}), time.UTC)
	if sc.Date != "" {
		t.Fatalf("date %q", sc.Date)
	}
	for _, op := range sc.Operations {
		if op.Found || op.Reason != stepReasonShort {
			t.Fatalf("%+v", op)
		}
	}
	// A step with only three days before it: the cut is forced to five days, so
	// the earlier part holds both levels and the p95 does not change by 2 times.
	sc, _ = stepChangeFrom(readsFrom(map[string][]daySpec{
		"A.One": stepSeries(20, 3, 300, 900), "B.Two": stepSeries(20, 3, 300, 900), "C.Three": stepSeries(20, 3, 300, 900),
	}), time.UTC)
	if sc.Date != "" {
		t.Fatalf("date %q %+v", sc.Date, sc.Operations)
	}
	// Exactly five days on each side is enough.
	sc, _ = stepChangeFrom(readsFrom(map[string][]daySpec{
		"A.One": stepSeries(10, 5, 300, 900), "B.Two": stepSeries(10, 5, 300, 900), "C.Three": stepSeries(10, 5, 300, 900),
	}), time.UTC)
	if sc.Date != "2026-03-06" {
		t.Fatalf("date %q %+v", sc.Date, sc.Operations)
	}
}

func TestStepDaysWithTooFewRequestsAreLeftOut(t *testing.T) {
	series := stepSeries(14, 7, 300, 900)
	series[2].count = MinSeriesDayCount - 1
	reads := readsFrom(map[string][]daySpec{"A.One": series, "B.Two": series, "C.Three": series})
	sc, _ := stepChangeFrom(reads, time.UTC)
	for _, op := range sc.Operations {
		if op.DaysUsed != 13 {
			t.Fatalf("%+v", op)
		}
	}
	if sc.Date != "2026-03-08" {
		t.Fatalf("date %q", sc.Date)
	}
}

func TestStepRampIsNotAStep(t *testing.T) {
	ramp := make([]daySpec, 24)
	level := 100.0
	for i := range ramp {
		ramp[i] = daySpec{level, 40}
		level *= 1.1
	}
	sc, _ := stepChangeFrom(readsFrom(map[string][]daySpec{"A.One": ramp, "B.Two": ramp, "C.Three": ramp}), time.UTC)
	if sc.Date != "" {
		t.Fatalf("a gradual rise was dated: %+v", sc.Operations)
	}
	for _, op := range sc.Operations {
		if op.Found {
			t.Fatalf("%+v", op)
		}
	}
}

func TestStepPartialDaysAreLeftOutAndCounted(t *testing.T) {
	reads := readsFrom(map[string][]daySpec{
		"A.One": stepSeries(20, 10, 300, 900), "B.Two": stepSeries(20, 10, 300, 900), "C.Three": stepSeries(20, 10, 300, 900),
	})
	reads[4].partial = true
	sc, warns := stepChangeFrom(reads, time.UTC)
	if sc.DaysRead != 20 || sc.DaysLeft != 1 || !sc.Partial || len(warns) != 1 || !strings.Contains(warns[0], "1 day was read only in part") {
		t.Fatalf("%+v %v", sc, warns)
	}
	if sc.Date != "2026-03-11" {
		t.Fatalf("date %q", sc.Date)
	}
}

func TestStepTopTenByVolume(t *testing.T) {
	series := map[string][]daySpec{}
	for i := 0; i < 12; i++ {
		s := stepSeries(14, 7, 300, 900)
		for d := range s {
			s[d].count = int64(100 - i) // later operations have less volume
		}
		series[fmt.Sprintf("Op%02d.Run", i)] = s
	}
	sc, _ := stepChangeFrom(readsFrom(series), time.UTC)
	if len(sc.Operations) != 10 || sc.Operations[0].Operation != "Op00.Run" || sc.Operations[9].Operation != "Op09.Run" {
		t.Fatalf("operations %+v", sc.Operations)
	}
}

func TestFitStepTieGoesToTheEarlierCut(t *testing.T) {
	pts := make([]dayPoint, 14)
	for i := range pts {
		h := histOf(40, 300)
		pts[i] = dayPoint{date: fmt.Sprintf("2026-03-%02d", i+1), count: 40, p95: 300, hist: h}
	}
	fit, reason := fitStep(pts)
	if reason != stepReasonSmall || fit.cut != stepMinSegmentDays {
		t.Fatalf("cut %d reason %q", fit.cut, reason)
	}
}

func TestSharedStepDate(t *testing.T) {
	day, members, ok := sharedStepDate([]int{100, 101, 99, 140})
	if !ok || day != 100 || len(members) != 3 {
		t.Fatalf("%d %v %v", day, members, ok)
	}
	if _, _, ok := sharedStepDate([]int{100, 102, 104}); ok {
		t.Fatal("dates two days apart must not share")
	}
	if _, _, ok := sharedStepDate([]int{100, 101}); ok {
		t.Fatal("two operations are not enough")
	}
	// Ties go to the date most operations hit exactly, then the earlier one.
	day, _, ok = sharedStepDate([]int{100, 101, 101, 102})
	if !ok || day != 101 {
		t.Fatalf("day %d", day)
	}
}

func TestSuggestSplitNeedsBothSides(t *testing.T) {
	reads := readsFrom(map[string][]daySpec{"A.One": stepSeries(10, 5, 1, 2)})
	if suggestSplit("2026-03-01", time.UTC, reads) != nil {
		t.Fatal("no day before the date")
	}
	if suggestSplit("2026-03-20", time.UTC, reads) != nil {
		t.Fatal("no day from the date on")
	}
	s := suggestSplit("2026-03-04", time.UTC, reads)
	if s == nil || s.BaselineEnd.Sub(s.BaselineStart) != 3*24*time.Hour || s.End.Sub(s.Start) != 3*24*time.Hour {
		t.Fatalf("split %+v", s)
	}
}

// --- DST and day windows -------------------------------------------------

func TestLocalDaysAcrossDST(t *testing.T) {
	ny, err := LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Spring forward on 2026-03-08: the day is 23 hours long.
	start := time.Date(2026, 3, 7, 0, 0, 0, 0, ny)
	days := localDays(start, time.Date(2026, 3, 10, 0, 0, 0, 0, ny), ny, 0)
	if len(days) != 3 {
		t.Fatalf("%d days: %+v", len(days), days)
	}
	for i, want := range []time.Duration{24 * time.Hour, 23 * time.Hour, 24 * time.Hour} {
		if got := days[i].End.Sub(days[i].Start); got != want {
			t.Fatalf("day %s is %v, want %v", days[i].Date, got, want)
		}
	}
	if days[1].Date != "2026-03-08" || !days[1].Start.Equal(time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", days[1])
	}
	// Fall back on 2026-11-01: the day is 25 hours long.
	days = localDays(time.Date(2026, 10, 31, 0, 0, 0, 0, ny), time.Date(2026, 11, 3, 0, 0, 0, 0, ny), ny, 0)
	if len(days) != 3 || days[1].Date != "2026-11-01" || days[1].End.Sub(days[1].Start) != 25*time.Hour {
		t.Fatalf("%+v", days)
	}
	// No day is skipped or repeated in a zone whose clock jumps over midnight.
	sp, err := LoadZone("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	all := localDays(time.Date(2018, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2018, 11, 8, 0, 0, 0, 0, time.UTC), sp, 0)
	seen := map[string]bool{}
	for _, d := range all {
		if seen[d.Date] {
			t.Fatalf("day %s repeated", d.Date)
		}
		seen[d.Date] = true
	}
}

func TestLocalDaysDropsCutDaysAndKeepsTheNewest(t *testing.T) {
	// A window that starts and ends at noon holds whole days only between.
	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	days := localDays(start, end, time.UTC, 0)
	if len(days) != 3 || days[0].Date != "2026-03-02" || days[2].Date != "2026-03-04" {
		t.Fatalf("%+v", days)
	}
	days = localDays(start, end, time.UTC, 2)
	if len(days) != 2 || days[0].Date != "2026-03-03" {
		t.Fatalf("%+v", days)
	}
	// Less than a day holds none.
	if got := localDays(start, start.Add(10*time.Hour), time.UTC, 0); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// A window cut at midnight in another zone is whole there, not in UTC.
	tokyo, _ := LoadZone("Asia/Tokyo")
	s := time.Date(2026, 3, 1, 0, 0, 0, 0, tokyo)
	if got := localDays(s, s.Add(48*time.Hour), tokyo, 0); len(got) != 2 || got[0].Date != "2026-03-01" {
		t.Fatalf("%+v", got)
	}
}

func TestLoadZone(t *testing.T) {
	for _, ok := range []string{"", "UTC", "Europe/Paris", "America/Argentina/Buenos_Aires", "Etc/GMT+5"} {
		if _, err := LoadZone(ok); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"Local", "local", "Nowhere/Land", "../etc/passwd", "UTC\x00", strings.Repeat("a", 65), "Europe/Paris ", "UTC;x"} {
		if _, err := LoadZone(bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestValidateStepChange(t *testing.T) {
	good := &StepChangeInput{Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	if err := validateStepChange(good, time.UTC); err != nil {
		t.Fatal(err)
	}
	for name, sc := range map[string]*StepChangeInput{
		"zero":     {},
		"backward": {Start: good.End, End: good.Start},
		"too long": {Start: good.Start, End: good.Start.Add(130 * 24 * time.Hour)},
		"no whole day": {Start: time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC),
			End: time.Date(2026, 1, 1, 18, 0, 0, 0, time.UTC)},
	} {
		if err := validateStepChange(sc, time.UTC); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0, End: patternT0.Add(time.Hour), StepChange: &StepChangeInput{Start: good.Start, End: good.End}}
	if err := ValidatePattern(in); err != nil {
		t.Fatal(err)
	}
	in.TimeZone = "Nowhere/Land"
	if err := ValidatePattern(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zone: %v", err)
	}
	in.TimeZone = ""
	in.StepChange.End = in.StepChange.Start
	if err := ValidatePattern(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("window: %v", err)
	}
}

// --- Reads through a repository -----------------------------------------

// dailyFake answers Analyze one window at a time from per-day durations.
type dailyFake struct {
	*patternFakeRepo
	mu      sync.Mutex
	queries []Query
	// values returns the request durations of one operation on the day that starts at the given instant.
	values func(day time.Time, op string) []float64
	ops    []string
	live   bool
	err    error
	// partialDay marks the day that starts at this instant as partially read.
	partialDay time.Time
}

func newDailyFake(ops []string, values func(time.Time, string) []float64) *dailyFake {
	return &dailyFake{patternFakeRepo: &patternFakeRepo{}, ops: ops, values: values}
}

func (f *dailyFake) Analyze(_ context.Context, q Query) (Analysis, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	if f.err != nil {
		return Analysis{}, f.err
	}
	prov := Provenance{SourceID: q.SourceIDs[0], Window: "current", Start: q.Start, End: q.End}
	if !f.live {
		n := int64(1)
		prov.DocsExamined = &n
	}
	if !f.partialDay.IsZero() && f.partialDay.Equal(q.Start) {
		prov.Completeness = Partial
	}
	out := Analysis{Provenance: []Provenance{prov}}
	for _, op := range f.ops {
		if q.Operation != "" && q.Operation != op {
			continue
		}
		vals := f.values(q.Start, op)
		if len(vals) == 0 {
			continue
		}
		acc := newHistogramAccumulator()
		for _, v := range vals {
			_ = acc.add(BinIndex(v), 1, v, v, v)
		}
		controller, action, _ := strings.Cut(op, ".")
		out.APIs = append(out.APIs, Comparison{SourceID: q.SourceIDs[0], Controller: controller, Action: action, Current: acc.stats()})
	}
	return out, nil
}

func constant(n int, ms float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = ms
	}
	return out
}

func TestRunEndpointDailyDSTDayEdges(t *testing.T) {
	ny, _ := LoadZone("America/New_York")
	fake := newDailyFake([]string{"Orders.Submit"}, func(day time.Time, op string) []float64 {
		return constant(30, 500)
	})
	in := EndpointDailyInput{
		SourceIDs: []string{"s1"}, Operation: "Orders.Submit", TimeZone: "America/New_York",
		Start: time.Date(2026, 3, 7, 0, 0, 0, 0, ny), End: time.Date(2026, 3, 10, 0, 0, 0, 0, ny),
	}
	out, err := RunEndpointDaily(context.Background(), fake, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Days) != 3 || out.Zone != "America/New_York" {
		t.Fatalf("%+v", out)
	}
	for i, want := range []time.Duration{24 * time.Hour, 23 * time.Hour, 24 * time.Hour} {
		if got := out.Days[i].End.Sub(out.Days[i].Start); got != want {
			t.Fatalf("day %s: %v, want %v", out.Days[i].Date, got, want)
		}
		q := fake.queries[i]
		if !q.Start.Equal(out.Days[i].Start) || !q.End.Equal(out.Days[i].End) || q.Operation != "Orders.Submit" {
			t.Fatalf("query %d: %+v", i, q)
		}
	}
	if out.Days[1].Date != "2026-03-08" || out.Days[1].Count != 30 || out.Days[1].P95Ms == nil {
		t.Fatalf("%+v", out.Days[1])
	}
	// The 5 % bin accuracy: the p95 of 500 ms is between 475 and 525.
	if p := *out.Days[1].P95Ms; p < 475 || p > 525 {
		t.Fatalf("p95 %v", p)
	}
	// Fall back: the 25-hour day.
	in.Start, in.End = time.Date(2026, 10, 31, 0, 0, 0, 0, ny), time.Date(2026, 11, 3, 0, 0, 0, 0, ny)
	out, err = RunEndpointDaily(context.Background(), fake, in)
	if err != nil || len(out.Days) != 3 || out.Days[1].End.Sub(out.Days[1].Start) != 25*time.Hour {
		t.Fatalf("%v %+v", err, out.Days)
	}
}

func TestRunEndpointDailyLowAndPartialDays(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	fake := newDailyFake([]string{"Orders.Submit"}, func(day time.Time, op string) []float64 {
		switch day.Day() {
		case 2:
			return constant(MinSeriesDayCount-1, 500)
		case 3:
			return nil
		}
		return constant(MinSeriesDayCount, 500)
	})
	fake.partialDay = start.AddDate(0, 0, 3)
	out, err := RunEndpointDaily(context.Background(), fake, EndpointDailyInput{
		SourceIDs: []string{"s1"}, Operation: "Orders.Submit", Start: start, End: start.AddDate(0, 0, 5),
	})
	if err != nil || len(out.Days) != 5 {
		t.Fatalf("%v %+v", err, out)
	}
	d := out.Days
	if d[0].P95Ms == nil || d[0].Count != int64(MinSeriesDayCount) {
		t.Fatalf("a day at the floor is used: %+v", d[0])
	}
	if d[1].P95Ms != nil || !d[1].Low || d[1].Count != int64(MinSeriesDayCount-1) {
		t.Fatalf("a day under the floor has no p95: %+v", d[1])
	}
	if d[2].P95Ms != nil || d[2].Low || d[2].Count != 0 {
		t.Fatalf("an empty day: %+v", d[2])
	}
	if d[3].P95Ms != nil || !d[3].Partial {
		t.Fatalf("a partly read day has no p95: %+v", d[3])
	}
	if len(out.Warnings) != 2 {
		t.Fatalf("warnings %v", out.Warnings)
	}
}

func TestRunEndpointDailyValidation(t *testing.T) {
	good := EndpointDailyInput{SourceIDs: []string{"s1"}, Operation: "Orders.Submit",
		Start: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)}
	if err := ValidateEndpointDaily(good); err != nil {
		t.Fatal(err)
	}
	mut := func(f func(*EndpointDailyInput)) EndpointDailyInput {
		c := good
		c.SourceIDs = append([]string(nil), good.SourceIDs...)
		f(&c)
		return c
	}
	for name, in := range map[string]EndpointDailyInput{
		"no sources":    mut(func(c *EndpointDailyInput) { c.SourceIDs = nil }),
		"blank source":  mut(func(c *EndpointDailyInput) { c.SourceIDs = []string{" "} }),
		"dup source":    mut(func(c *EndpointDailyInput) { c.SourceIDs = []string{"a", "a"} }),
		"no operation":  mut(func(c *EndpointDailyInput) { c.Operation = "" }),
		"no dot":        mut(func(c *EndpointDailyInput) { c.Operation = "Orders" }),
		"nul char":      mut(func(c *EndpointDailyInput) { c.Operation = "Orders.Sub\x00mit" }),
		"bad zone":      mut(func(c *EndpointDailyInput) { c.TimeZone = "Nowhere/Land" }),
		"too many days": mut(func(c *EndpointDailyInput) { c.End = c.Start.Add(130 * 24 * time.Hour) }),
		"backward":      mut(func(c *EndpointDailyInput) { c.Start, c.End = c.End, c.Start }),
		"under one day": mut(func(c *EndpointDailyInput) { c.End = c.Start.Add(6 * time.Hour) }),
		"zero":          mut(func(c *EndpointDailyInput) { c.Start = time.Time{} }),
	} {
		if err := ValidateEndpointDaily(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestRunEndpointDailyLiveSourceIsCapped(t *testing.T) {
	fake := newDailyFake([]string{"Orders.Submit"}, func(time.Time, string) []float64 { return constant(30, 500) })
	fake.live = true
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	in := EndpointDailyInput{SourceIDs: []string{"s1"}, Operation: "Orders.Submit", Start: start, End: start.AddDate(0, 0, liveSeriesMaxDays)}
	if out, err := RunEndpointDaily(context.Background(), fake, in); err != nil || len(out.Days) != liveSeriesMaxDays {
		t.Fatalf("%v %d", err, len(out.Days))
	}
	fake.queries = nil
	in.End = start.AddDate(0, 0, liveSeriesMaxDays+1)
	_, err := RunEndpointDaily(context.Background(), fake, in)
	if !errors.Is(err, ErrBroadScanRefused) {
		t.Fatalf("err %v", err)
	}
	if len(fake.queries) != 1 {
		t.Fatalf("the refusal comes after one day was read, not all: %d", len(fake.queries))
	}
	// A source that is not live reads the full span.
	fake.live = false
	in.End = start.AddDate(0, 0, 60)
	if out, err := RunEndpointDaily(context.Background(), fake, in); err != nil || len(out.Days) != 60 {
		t.Fatalf("%v %d", err, len(out.Days))
	}
}

func TestRunEndpointDailyKeepsTheNewestHundredTwentyDays(t *testing.T) {
	fake := newDailyFake([]string{"Orders.Submit"}, func(time.Time, string) []float64 { return constant(25, 500) })
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out, err := RunEndpointDaily(context.Background(), fake, EndpointDailyInput{
		SourceIDs: []string{"s1"}, Operation: "Orders.Submit", Start: start, End: start.AddDate(0, 0, 121),
	})
	if err != nil || len(out.Days) != MaxSeriesDays || out.Days[len(out.Days)-1].Date != "2026-05-01" {
		t.Fatalf("%v %d", err, len(out.Days))
	}
}

func TestRunEndpointDailyReadErrorIsAnError(t *testing.T) {
	fake := newDailyFake(nil, nil)
	fake.err = errors.New("source down")
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RunEndpointDaily(context.Background(), fake, EndpointDailyInput{
		SourceIDs: []string{"s1"}, Operation: "Orders.Submit", Start: start, End: start.AddDate(0, 0, 3),
	}); err == nil {
		t.Fatal("want an error")
	}
}

func TestBuildStepChangeThroughRepository(t *testing.T) {
	ops := []string{"A.One", "B.Two", "C.Three", "D.Flat"}
	step := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	fake := newDailyFake(ops, func(day time.Time, op string) []float64 {
		if op != "D.Flat" && !day.Before(step) {
			return constant(30, 900)
		}
		return constant(30, 300)
	})
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0, End: patternT0.Add(time.Hour),
		StepChange: &StepChangeInput{Start: start, End: start.AddDate(0, 0, 24)}}
	report := PatternReport{Provenance: []Provenance{{SourceID: "s1", DocsExamined: func() *int64 { n := int64(1); return &n }()}}}
	sc, finds, prov, warns, err := buildStepChange(context.Background(), fake, in, report)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if sc.Date != "2026-03-12" || len(sc.Sharing) != 3 || sc.DaysRead != 24 || len(finds) != 1 {
		t.Fatalf("%+v", sc)
	}
	// One read per day, each narrowed to nothing in particular and bounded to its day.
	if len(fake.queries) != 24 {
		t.Fatalf("%d reads", len(fake.queries))
	}
	for i, q := range fake.queries {
		if !q.Start.Equal(start.AddDate(0, 0, i)) || q.End.Sub(q.Start) != 24*time.Hour || q.End.Sub(q.Start) > MaxInterval {
			t.Fatalf("query %d: %+v", i, q)
		}
	}
	if len(prov) != 1 || !prov[0].Start.Equal(start) || !prov[0].End.Equal(start.AddDate(0, 0, 24)) {
		t.Fatalf("provenance %+v", prov)
	}
}

func TestBuildStepChangeLiveSourceIsAWarning(t *testing.T) {
	fake := newDailyFake([]string{"A.One"}, func(time.Time, string) []float64 { return constant(30, 300) })
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	in := PatternInput{SourceIDs: []string{"s1"}, StepChange: &StepChangeInput{Start: start, End: start.AddDate(0, 0, 24)}}
	sc, finds, _, warns, err := buildStepChange(context.Background(), fake, in, PatternReport{Provenance: []Provenance{{SourceID: "s1"}}})
	if err != nil || sc != nil || finds != nil || len(warns) != 1 || len(fake.queries) != 0 {
		t.Fatalf("%v %v %v %v reads %d", err, sc, finds, warns, len(fake.queries))
	}
	// A non-live report whose day reads turn out to be live is refused after one read.
	fake.live = true
	sc, _, _, warns, err = buildStepChange(context.Background(), fake, in, PatternReport{Provenance: []Provenance{{SourceID: "s1", DocsExamined: func() *int64 { n := int64(1); return &n }()}}})
	if err != nil || sc != nil || len(warns) != 1 {
		t.Fatalf("%v %v %v", err, sc, warns)
	}
}

func TestBuildStepChangeReadFailureIsAWarningButCancelIsAnError(t *testing.T) {
	fake := newDailyFake(nil, nil)
	fake.err = errors.New("source down")
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	in := PatternInput{SourceIDs: []string{"s1"}, StepChange: &StepChangeInput{Start: start, End: start.AddDate(0, 0, 12)}}
	n := int64(1)
	report := PatternReport{Provenance: []Provenance{{SourceID: "s1", DocsExamined: &n}}}
	sc, _, _, warns, err := buildStepChange(context.Background(), fake, in, report)
	if err != nil || sc != nil || len(warns) != 1 {
		t.Fatalf("%v %v %v", err, sc, warns)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := buildStepChange(ctx, fake, in, report); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

// --- The demo -----------------------------------------------------------

func TestDemoStepAndBandEndToEnd(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 30, 0, 0, time.UTC)
	repo := NewDemoWithPatterns(now)
	in := PatternInput{
		SourceIDs: []string{demoPatternSource}, Start: now.Add(-4 * time.Hour), End: now,
		StepChange:  &StepChangeInput{Start: now.Add(-40 * 24 * time.Hour), End: now.Add(-8 * 24 * time.Hour)},
		AuditSeries: true,
	}
	// The audit series and the band need a window that holds the band rows.
	in.Start = now.Add(-7 * 24 * time.Hour)
	if err := ValidatePattern(in); err != nil {
		t.Fatal(err)
	}
	report, err := RunPatterns(context.Background(), repo, in)
	if err != nil {
		t.Fatal(err)
	}
	wantDate := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -demoStepDay).Format("2006-01-02")
	if report.StepChange == nil || report.StepChange.Date != wantDate || len(report.StepChange.Sharing) != 4 || report.StepChange.Split == nil {
		t.Fatalf("%+v", report.StepChange)
	}
	var b9, b6f *Finding
	for i := range report.Findings {
		switch report.Findings[i].Rule {
		case "B9":
			b9 = &report.Findings[i]
		case "B6f":
			b6f = &report.Findings[i]
		}
	}
	if b9 == nil || b6f == nil || !strings.HasPrefix(b6f.Headline, "Observed: 6 audit-create delays fell between 26.2 s and") {
		t.Fatalf("b9 %+v b6f %+v", b9, b6f)
	}
	if report.AuditSeries == nil || report.AuditSeries.Band == nil || report.AuditSeries.Band.Count != 6 || len(report.AuditSeries.Band.Days) != 2 || report.AuditSeries.ExcludedHangfire != 4 {
		t.Fatalf("%+v", report.AuditSeries)
	}
	// Earlier demo windows do not see the new rows: the four-hour window is unchanged.
	short, err := RunPatterns(context.Background(), repo, PatternInput{SourceIDs: []string{demoPatternSource}, Start: now.Add(-4 * time.Hour), End: now})
	if err != nil || short.StepChange != nil || short.AuditSeries != nil || len(short.Stalls) != 6 {
		t.Fatalf("%v stalls %d", err, len(short.Stalls))
	}
}
