package audit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

var seriesT0 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// srow is one request on s1 that started at the given time.
func srow(id, kind string, at time.Time, audit, total float64) RequestRecord {
	a, d := audit, total
	rec := RequestRecord{ID: id, SourceID: "s1", Kind: kind, Controller: "Timesheets", Action: "Approve", InitiatedAt: &at, AuditCreateTimeMs: &a}
	if total > 0 {
		rec.DurationMs = &d
	}
	return rec
}

func seriesInput(days int, live bool) (PatternInput, PatternReport) {
	in := PatternInput{SourceIDs: []string{"s1"}, Start: seriesT0, End: seriesT0.AddDate(0, 0, days), AuditSeries: true}
	prov := Provenance{SourceID: "s1"}
	if !live {
		n := int64(1)
		prov.DocsExamined = &n
	}
	return in, PatternReport{Provenance: []Provenance{prov}}
}

func runSeries(t *testing.T, repo Repository, in PatternInput, r PatternReport) (*AuditSeries, []Finding, []Provenance, []string) {
	t.Helper()
	series, finds, prov, warns, err := buildAuditSeries(context.Background(), repo, in, patternParams(in), r)
	if err != nil {
		t.Fatalf("buildAuditSeries: %v", err)
	}
	return series, finds, prov, warns
}

// bandRows adds count delays of the given base on a day, 100 ms apart.
func bandRows(prefix string, day int, count int, base float64) []RequestRecord {
	var out []RequestRecord
	for i := 0; i < count; i++ {
		at := seriesT0.AddDate(0, 0, day).Add(10*time.Hour + time.Duration(i)*time.Minute)
		out = append(out, srow(fmt.Sprintf("%s-%d-%d", prefix, day, i), "synchronous", at, base+float64(i)*100, base+float64(i)*100+2000))
	}
	return out
}

func TestStallBandFoundAcrossTwoDays(t *testing.T) {
	rows := append(bandRows("a", 1, 3, 26200), bandRows("b", 3, 3, 28000)...)
	in, r := seriesInput(5, false)
	series, finds, _, warns := runSeries(t, newRepeatFake(rows...), in, r)
	if len(warns) != 0 {
		t.Fatalf("warnings %v", warns)
	}
	b := series.Band
	if b == nil || b.Count != 6 || len(b.Days) != 2 || b.Days[0] != "2026-03-02" || b.Days[1] != "2026-03-04" || b.FromMs != 26200 || b.ToMs != 28200 || b.OfLong != 6 {
		t.Fatalf("band %+v", b)
	}
	if len(finds) != 1 || finds[0].Rule != "B6f" || finds[0].Label != EvidenceSymptom || !strings.HasPrefix(finds[0].Headline, "Observed: 6 audit-create delays fell between 26.2 s and 28.2 s") ||
		!strings.Contains(finds[0].Headline, "2026-03-02, 2026-03-04") || !strings.Contains(finds[0].Headline, "client side") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStallBandSpreadOutIsRejected(t *testing.T) {
	// Eight delays between 21 and 60 s, no five of them inside 5 s.
	var rows []RequestRecord
	for i, d := range []float64{21000, 27000, 33000, 39000, 45000, 51000, 57000, 60000} {
		at := seriesT0.AddDate(0, 0, i%3).Add(9 * time.Hour)
		rows = append(rows, srow(fmt.Sprintf("x%d", i), "synchronous", at, d, d+1000))
	}
	in, r := seriesInput(5, false)
	series, finds, _, _ := runSeries(t, newRepeatFake(rows...), in, r)
	if series.Band != nil {
		t.Fatalf("band %+v", series.Band)
	}
	if len(finds) != 1 || !strings.HasPrefix(finds[0].Headline, "Not observed: 8 audit-create delays of 20 s or more did not cluster") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStallBandOnOneDayIsRejected(t *testing.T) {
	in, r := seriesInput(5, false)
	series, finds, _, _ := runSeries(t, newRepeatFake(bandRows("a", 2, 8, 26000)...), in, r)
	if series.Band != nil {
		t.Fatalf("a single day is not a pattern: %+v", series.Band)
	}
	if len(finds) != 1 || !strings.HasPrefix(finds[0].Headline, "Not observed:") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestStallBandTooFewDelaysPrintsNothing(t *testing.T) {
	rows := append(bandRows("a", 1, 2, 26000), bandRows("b", 2, 2, 26000)...)
	in, r := seriesInput(5, false)
	series, finds, _, _ := runSeries(t, newRepeatFake(rows...), in, r)
	if series.Band != nil || len(finds) != 0 {
		t.Fatalf("%+v %+v", series.Band, finds)
	}
}

func TestStallBandEdges(t *testing.T) {
	// A band of exactly 5 s inclusive counts; 5.001 s does not.
	mk := func(top float64) []RequestRecord {
		var rows []RequestRecord
		for i, d := range []float64{25000, 26000, 27000, 28000, top} {
			at := seriesT0.AddDate(0, 0, i%2).Add(8 * time.Hour)
			rows = append(rows, srow(fmt.Sprintf("e%d", i), "synchronous", at, d, d+1000))
		}
		return rows
	}
	in, r := seriesInput(3, false)
	if s, _, _, _ := runSeries(t, newRepeatFake(mk(30000)...), in, r); s.Band == nil || s.Band.Count != 5 {
		t.Fatalf("band %+v", s.Band)
	}
	if s, _, _, _ := runSeries(t, newRepeatFake(mk(30001)...), in, r); s.Band != nil {
		t.Fatalf("band %+v", s.Band)
	}
	// Delays under 20 s are not looked at, however tight.
	var short []RequestRecord
	for i := 0; i < 6; i++ {
		at := seriesT0.AddDate(0, 0, i%2).Add(8 * time.Hour)
		short = append(short, srow(fmt.Sprintf("s%d", i), "synchronous", at, 19999, 21000))
	}
	if s, _, _, _ := runSeries(t, newRepeatFake(short...), in, r); s.Band != nil {
		t.Fatalf("band %+v", s.Band)
	}
}

func TestStallBandDaysAreLocalDays(t *testing.T) {
	// Three delays at 23:00 UTC and three at 01:00 UTC the next day. In UTC that is
	// two days; in Pacific/Auckland (UTC+13 in March) all six fall on one local day.
	var rows []RequestRecord
	for i := 0; i < 3; i++ {
		rows = append(rows, srow(fmt.Sprintf("p%d", i), "synchronous", seriesT0.Add(23*time.Hour+time.Duration(i)*time.Minute), 26000+float64(i)*100, 27000))
		rows = append(rows, srow(fmt.Sprintf("q%d", i), "synchronous", seriesT0.Add(25*time.Hour+time.Duration(i)*time.Minute), 26000+float64(i)*100, 27000))
	}
	in, r := seriesInput(3, false)
	if s, _, _, _ := runSeries(t, newRepeatFake(rows...), in, r); s.Band == nil || len(s.Band.Days) != 2 {
		t.Fatalf("utc %+v", s.Band)
	}
	in.TimeZone = "Pacific/Auckland"
	if s, _, _, _ := runSeries(t, newRepeatFake(rows...), in, r); s.Band != nil || s.Zone != "Pacific/Auckland" {
		t.Fatalf("auckland %+v", s)
	}
}

func TestAuditSeriesExcludesHangfireExactlyAsTheGapRuleDoes(t *testing.T) {
	// The only band is made of Hangfire rows plus one synchronous row: with the
	// Hangfire rows left out, no band remains.
	rows := bandRows("sync", 1, 1, 26000)
	for i := 0; i < 5; i++ {
		at := seriesT0.AddDate(0, 0, 1+i%2).Add(15 * time.Hour)
		rows = append(rows, srow(fmt.Sprintf("h%d", i), "hangfire", at, 26100+float64(i)*100, 40000))
	}
	in, r := seriesInput(5, false)
	series, finds, _, warns := runSeries(t, newRepeatFake(rows...), in, r)
	if series.Band != nil || series.ExcludedHangfire != 5 {
		t.Fatalf("%+v", series)
	}
	if len(finds) != 0 {
		t.Fatalf("one long delay is too few to print anything: %+v", finds)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "5 Hangfire rows were left out") {
		t.Fatalf("warnings %v", warns)
	}
	for _, d := range series.Days {
		if d.N != 1 {
			t.Fatalf("a Hangfire row reached the series: %+v", d)
		}
	}
	// The same count the gap rule keeps out of its own table.
	idleIn, idleR := idleInput(false)
	idleIn.Start, idleIn.End = in.Start, in.End
	g, _, _, _, err := buildIdleGaps(context.Background(), newRepeatFake(rows...), idleIn, patternParams(idleIn), idleR)
	if err != nil {
		t.Fatal(err)
	}
	if g.ExcludedHangfire != series.ExcludedHangfire {
		t.Fatalf("gap rule excluded %d, series %d", g.ExcludedHangfire, series.ExcludedHangfire)
	}
}

func TestAuditSeriesDaysShareAndQuantiles(t *testing.T) {
	var rows []RequestRecord
	// Day 0: 20 requests, audit 100..2000 ms (step 100), total 1,000 ms each.
	for i := 1; i <= 20; i++ {
		rows = append(rows, srow(fmt.Sprintf("d0-%d", i), "synchronous", seriesT0.Add(time.Duration(i)*time.Minute), float64(i)*100, 10000))
	}
	// Day 1: 19 requests: under the floor, so no p50, p95 or share.
	for i := 1; i <= 19; i++ {
		rows = append(rows, srow(fmt.Sprintf("d1-%d", i), "synchronous", seriesT0.AddDate(0, 0, 1).Add(time.Duration(i)*time.Minute), 500, 1000))
	}
	// A row with no audit-create time is counted, not used.
	noAudit := srow("n", "synchronous", seriesT0.Add(time.Hour), 0, 1000)
	noAudit.AuditCreateTimeMs = nil
	rows = append(rows, noAudit)
	in, r := seriesInput(3, false)
	series, _, _, warns := runSeries(t, newRepeatFake(rows...), in, r)
	if len(series.Days) != 2 || series.WithoutAudit != 1 || len(warns) != 1 {
		t.Fatalf("%+v %v", series, warns)
	}
	d0, d1 := series.Days[0], series.Days[1]
	if d0.Date != "2026-03-01" || d0.N != 20 || d0.P50Ms == nil || *d0.P50Ms != 1050 && *d0.P50Ms != 1000 || d0.P95Ms == nil || *d0.P95Ms != 1900 {
		t.Fatalf("day 0 %+v p50 %v p95 %v", d0, d0.P50Ms, d0.P95Ms)
	}
	// Sum of audit 100..2000 = 21,000 over 20 x 10,000 = 0.105.
	if d0.InsertShare == nil || *d0.InsertShare != 0.105 {
		t.Fatalf("share %v", d0.InsertShare)
	}
	if d0.PartDay || d1.N != 19 || d1.P50Ms != nil || d1.P95Ms != nil || d1.InsertShare != nil {
		t.Fatalf("day 1 %+v", d1)
	}
}

func TestAuditSeriesPartDaysAndDSTEdges(t *testing.T) {
	ny, _ := LoadZone("America/New_York")
	// Window starts at local noon on the spring-forward day 2026-03-08 and ends at
	// local midnight three days later. The first local day is cut short, and the
	// 23-hour day itself is the cut one here.
	start := time.Date(2026, 3, 8, 12, 0, 0, 0, ny)
	end := time.Date(2026, 3, 11, 0, 0, 0, 0, ny)
	var rows []RequestRecord
	// A row at 01:30 local on 2026-03-09 (after the jump) and one at 23:30 local on 03-08.
	rows = append(rows, srow("a", "synchronous", time.Date(2026, 3, 8, 23, 30, 0, 0, ny), 100, 1000))
	rows = append(rows, srow("b", "synchronous", time.Date(2026, 3, 9, 0, 30, 0, 0, ny), 100, 1000))
	rows = append(rows, srow("c", "synchronous", time.Date(2026, 3, 10, 23, 59, 0, 0, ny), 100, 1000))
	in := PatternInput{SourceIDs: []string{"s1"}, Start: start, End: end, TimeZone: "America/New_York", AuditSeries: true}
	_, r := seriesInput(1, false)
	series, _, _, _ := runSeries(t, newRepeatFake(rows...), in, r)
	if len(series.Days) != 3 || series.Days[0].Date != "2026-03-08" || series.Days[1].Date != "2026-03-09" || series.Days[2].Date != "2026-03-10" {
		t.Fatalf("%+v", series.Days)
	}
	if !series.Days[0].PartDay || series.Days[1].PartDay || series.Days[2].PartDay {
		t.Fatalf("part days %+v", series.Days)
	}
	// Fall back, 25-hour day: a row at 00:30 and one at 23:30 local are the same day.
	fall := []RequestRecord{
		srow("x", "synchronous", time.Date(2026, 11, 1, 0, 30, 0, 0, ny), 100, 1000),
		srow("y", "synchronous", time.Date(2026, 11, 1, 23, 30, 0, 0, ny), 100, 1000),
		srow("z", "synchronous", time.Date(2026, 11, 2, 0, 10, 0, 0, ny), 100, 1000),
	}
	in.Start, in.End = time.Date(2026, 11, 1, 0, 0, 0, 0, ny), time.Date(2026, 11, 3, 0, 0, 0, 0, ny)
	series, _, _, _ = runSeries(t, newRepeatFake(fall...), in, r)
	if len(series.Days) != 2 || series.Days[0].N != 2 || series.Days[1].N != 1 || series.Days[0].PartDay || series.Days[1].PartDay {
		t.Fatalf("%+v", series.Days)
	}
}

func TestAuditSeriesRowCapMakesItPartialAndWarns(t *testing.T) {
	old := gapRowCap
	gapRowCap = 40
	defer func() { gapRowCap = old }()
	var rows []RequestRecord
	for i := 0; i < 60; i++ {
		rows = append(rows, srow(fmt.Sprintf("r%02d", i), "synchronous", seriesT0.Add(time.Duration(i)*time.Minute), 100, 1000))
	}
	in, r := seriesInput(2, false)
	series, _, prov, warns := runSeries(t, newRepeatFake(rows...), in, r)
	if !series.Partial || !strings.HasPrefix(series.PartialReason, "row cap reached") {
		t.Fatalf("%+v", series)
	}
	total := 0
	for _, d := range series.Days {
		total += d.N
	}
	if total != 40 {
		t.Fatalf("the cut read keeps the newest 40 rows, got %d", total)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "newest 40 rows per source") || !strings.Contains(warns[0], "partial") {
		t.Fatalf("warnings %q", warns)
	}
	if len(prov) != 1 || prov[0].Completeness != Partial || prov[0].PartialReason != "rowcap" {
		t.Fatalf("provenance %+v", prov)
	}
}

func TestStallBandCutReadIsALowerBoundAndNeverSaysNotObserved(t *testing.T) {
	old := gapRowCap
	gapRowCap = 30
	defer func() { gapRowCap = old }()
	var rows []RequestRecord
	for i := 0; i < 50; i++ {
		rows = append(rows, srow(fmt.Sprintf("r%02d", i), "synchronous", seriesT0.Add(time.Duration(i)*time.Minute), 22000+float64(i)*3000, 30000))
	}
	in, r := seriesInput(2, false)
	series, finds, _, _ := runSeries(t, newRepeatFake(rows...), in, r)
	if !series.Partial || series.Band != nil || len(finds) != 0 {
		t.Fatalf("a partial read must not print 'not observed': %+v %+v", series, finds)
	}
	// A cut read that does find a band says it is a lower bound.
	var banded []RequestRecord
	for i := 0; i < 40; i++ {
		banded = append(banded, srow(fmt.Sprintf("b%02d", i), "synchronous", seriesT0.AddDate(0, 0, i%2).Add(time.Duration(i)*time.Minute), 26000+float64(i%5)*100, 30000))
	}
	series, finds, _, _ = runSeries(t, newRepeatFake(banded...), in, r)
	if !series.Partial || series.Band == nil || len(finds) != 1 || !strings.Contains(finds[0].Headline, "lower bound") {
		t.Fatalf("%+v %+v", series, finds)
	}
}

func TestAuditSeriesLiveGating(t *testing.T) {
	rows := bandRows("a", 1, 3, 26000)
	fake := newRepeatFake(rows...)
	fake.live = true
	// A window over 24 hours on a live source is a warning, with no read.
	in, r := seriesInput(3, true)
	series, finds, prov, warns, err := buildAuditSeries(context.Background(), fake, in, patternParams(in), r)
	if err != nil || series != nil || finds != nil || prov != nil || len(fake.calls) != 0 ||
		len(warns) != 1 || !strings.HasPrefix(warns[0], "The audit-create series and the fixed-duration check did not run: ") {
		t.Fatalf("%v %v %v %v reads %d", err, series, finds, warns, len(fake.calls))
	}
	// A narrow window of exactly 24 hours is allowed.
	in.End = in.Start.Add(24 * time.Hour)
	series, _, _, warns, err = buildAuditSeries(context.Background(), fake, in, patternParams(in), r)
	if err != nil || series == nil || len(warns) != 0 {
		t.Fatalf("%v %v %v", err, series, warns)
	}
	// A broad scan on a live source is a warning as well.
	fake.broad = true
	series, _, _, warns, err = buildAuditSeries(context.Background(), fake, in, patternParams(in), r)
	if err != nil || series != nil || len(warns) != 1 || !strings.Contains(warns[0], "narrow scan") {
		t.Fatalf("%v %v %v", err, series, warns)
	}
}

func TestAuditSeriesReadFailureIsAWarningAndCancelIsAnError(t *testing.T) {
	fake := newRepeatFake(bandRows("a", 1, 3, 26000)...)
	fake.err = errors.New("boom")
	in, r := seriesInput(3, false)
	series, _, _, warns := runSeries(t, fake, in, r)
	if !series.Partial || series.PartialReason != "a read failed" || len(warns) != 1 || strings.Contains(warns[0], "boom") {
		t.Fatalf("%+v %v", series, warns)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := buildAuditSeries(ctx, fake, in, patternParams(in), r); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestAuditSeriesBadZoneIsInvalidInput(t *testing.T) {
	in, r := seriesInput(3, false)
	in.TimeZone = "Nowhere/Land"
	if _, _, _, _, err := buildAuditSeries(context.Background(), newRepeatFake(), in, patternParams(in), r); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err %v", err)
	}
}

// --- Wording ------------------------------------------------------------

var forbiddenWording = regexp.MustCompile(`(?i)\b(caused|because|due to|waiting on|blocked by|retry|retries|complete|proven?|root cause|culprit|fault)\b`)

func TestNewWordingStaysPlainAndMakesNoCausalClaim(t *testing.T) {
	var texts []string

	reads := readsFrom(map[string][]daySpec{
		"A.One": stepSeries(24, 12, 300, 900), "B.Two": stepSeries(24, 12, 300, 900), "C.Three": stepSeries(24, 12, 400, 1200),
	})
	found, _ := stepChangeFrom(reads, time.UTC)
	found.Start, found.End = reads[0].day.Start, reads[len(reads)-1].day.End
	for _, f := range stepFindings(PatternInput{SourceIDs: []string{"s1"}}, found) {
		texts = append(texts, f.Headline)
	}
	flat, _ := stepChangeFrom(readsFrom(map[string][]daySpec{
		"A.One": stepSeries(20, 10, 300, 300), "B.Two": stepSeries(20, 10, 300, 300), "C.Three": stepSeries(20, 10, 300, 300),
	}), time.UTC)
	flat.Start, flat.End = reads[0].day.Start, reads[19].day.End
	for _, f := range stepFindings(PatternInput{SourceIDs: []string{"s1"}}, flat) {
		texts = append(texts, f.Headline)
	}
	texts = append(texts, stepReasonShort, stepReasonSmall, stepReasonTraffic, stepReasonDrift)

	rows := append(bandRows("a", 1, 3, 26200), bandRows("b", 3, 3, 28000)...)
	in, r := seriesInput(5, false)
	_, bandFinds, _, _ := runSeries(t, newRepeatFake(rows...), in, r)
	for _, f := range bandFinds {
		texts = append(texts, f.Headline)
	}
	rows = bandRows("c", 2, 8, 26000)
	_, bandFinds, _, _ = runSeries(t, newRepeatFake(rows...), in, r)
	for _, f := range bandFinds {
		texts = append(texts, f.Headline)
	}
	hang := srow("h", "hangfire", seriesT0.Add(time.Hour), 26000, 40000)
	none := srow("n", "synchronous", seriesT0.Add(2*time.Hour), 0, 1000)
	none.AuditCreateTimeMs = nil
	_, _, _, warns := runSeries(t, newRepeatFake(hang, none), in, r)
	texts = append(texts, warns...)

	if len(texts) < 10 {
		t.Fatalf("only %d texts collected", len(texts))
	}
	for _, text := range texts {
		if m := forbiddenWording.FindString(text); m != "" {
			t.Errorf("%q in %q", m, text)
		}
		if strings.ContainsAny(text, "—–") {
			t.Errorf("dash in %q", text)
		}
	}
	for _, f := range stepFindings(PatternInput{SourceIDs: []string{"s1"}}, found) {
		if !strings.HasPrefix(f.Headline, "Observed:") || !strings.Contains(f.Headline, "does not explain the change") {
			t.Errorf("a step finding must say observed and carry the blind-spot line: %q", f.Headline)
		}
	}
}
