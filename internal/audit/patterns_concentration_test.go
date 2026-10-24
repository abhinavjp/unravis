package audit

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// concFake is a Repository that can break rows down by user.
type concFake struct {
	*patternFakeRepo
	breakdown Breakdown
	err       error
	calls     []BreakdownInput
}

func (f *concFake) Breakdown(_ context.Context, in BreakdownInput) (Breakdown, error) {
	f.calls = append(f.calls, in)
	return f.breakdown, f.err
}

// noBreakdown is a Repository without BreakdownReader.
type noBreakdown struct{ *patternFakeRepo }

func narrowProv() []Provenance {
	return []Provenance{{SourceID: "s1", Window: "breakdown", ScanClass: ScanNarrow}}
}

func concStalls(op string, n, episodes int, userBase int) []Stall {
	controller, action, _ := strings.Cut(op, ".")
	var out []Stall
	for i := 0; i < n; i++ {
		u := userBase + i
		out = append(out, Stall{Controller: controller, Action: action, UserID: &u, Episode: 1 + i%episodes})
	}
	return out
}

func concReport(stalls []Stall, live bool) PatternReport {
	prov := []Provenance{{SourceID: "s1"}}
	if !live {
		n := int64(1)
		prov[0].DocsExamined = &n
	}
	return PatternReport{Stalls: stalls, Provenance: prov}
}

func concInput(window time.Duration) PatternInput {
	return PatternInput{SourceIDs: []string{"s1"}, Start: patternT0, End: patternT0.Add(window)}
}

func rollups(events ...interface{}) *Analysis {
	a := &Analysis{}
	for i := 0; i < len(events); i += 2 {
		controller, action, _ := strings.Cut(events[i].(string), ".")
		a.Rollups = append(a.Rollups, Rollup{Controller: controller, Action: action, Events: int64(events[i+1].(int))})
	}
	return a
}

func runConc(t *testing.T, repo Repository, in PatternInput, r PatternReport, a *Analysis) ([]Concentration, []Finding, []string) {
	t.Helper()
	rows, findings, warns, err := buildConcentration(context.Background(), repo, in, patternParams(in), r, a)
	if err != nil {
		t.Fatalf("buildConcentration: %v", err)
	}
	return rows, findings, warns
}

func TestConcentrationOperationCorrelation(t *testing.T) {
	in := concInput(6 * time.Hour)
	// 12 of 12 stalls from X; X is 24 of 124 requests: lift 124/24 = 5.2.
	r := concReport(concStalls("Docs.Export", 12, 3, 100), false)
	rows, findings, warns := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("Docs.Export", 24, "Other.Op", 100))
	if len(findings) != 1 || len(rows) != 1 {
		t.Fatalf("rows %+v findings %+v warns %v", rows, findings, warns)
	}
	f := findings[0]
	// 12 stalls over 3 episodes is not low-n.
	if f.Rule != "A3" || f.Label != EvidenceCorrelation || f.N != 12 || f.LowN {
		t.Fatalf("%+v", f)
	}
	if f.Headline != "Docs.Export had 100% of stall requests but 19.4% of all requests in this window (5.2x its traffic share)." {
		t.Fatalf("headline %q", f.Headline)
	}
	row := rows[0]
	if row.Withheld || row.Dimension != ConcentrationOperation || row.AllN != 24 || row.StallN != 12 || row.Episodes != 3 || row.Lift == nil {
		t.Fatalf("%+v", row)
	}
}

func TestConcentrationEpisodeGuard(t *testing.T) {
	in := concInput(6 * time.Hour)
	traffic := rollups("Docs.Export", 24, "Other.Op", 100)
	for _, c := range []struct {
		episodes int
		suffix   string
	}{{1, " Single episode."}, {2, " Seen in 2 episodes; 3 or more are needed."}} {
		r := concReport(concStalls("Docs.Export", 12, c.episodes, 100), false)
		_, findings, _ := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, traffic)
		if len(findings) != 1 || findings[0].Label != EvidenceSymptom || !findings[0].LowN || !strings.HasSuffix(findings[0].Headline, c.suffix) {
			t.Fatalf("episodes %d: %+v", c.episodes, findings)
		}
	}
}

func TestConcentrationLiftEdges(t *testing.T) {
	in := concInput(6 * time.Hour)
	// 10 stalls of 10: stall share 1. Lift is total/all.
	r := concReport(concStalls("A.B", 10, 3, 100), false)
	// all 50, total 100: lift 2 exactly is concentrated.
	_, findings, _ := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 50, "C.D", 50))
	if len(findings) != 1 || !strings.Contains(findings[0].Headline, "(2x its traffic share)") {
		t.Fatalf("lift 2: %+v", findings)
	}
	// all 51, total 101: lift just under 2 is not.
	_, findings, _ = runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 51, "C.D", 50))
	if len(findings) != 0 {
		t.Fatalf("lift under 2: %+v", findings)
	}
	// Nine stalls are below the stall floor even at high lift.
	r9 := concReport(concStalls("A.B", 9, 3, 100), false)
	rows, findings, _ := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r9, rollups("A.B", 20, "C.D", 500))
	if len(findings) != 0 || len(rows) != 0 {
		t.Fatalf("nine stalls: %+v %+v", rows, findings)
	}
}

func TestConcentrationWithheldBelowTwenty(t *testing.T) {
	in := concInput(6 * time.Hour)
	r := concReport(concStalls("A.B", 12, 3, 100), false)
	rows, findings, _ := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 19, "C.D", 500))
	if len(findings) != 0 || len(rows) != 1 || !rows[0].Withheld || rows[0].Lift != nil || rows[0].StallShare != nil || rows[0].TrafficShare != nil || rows[0].AllN != 19 {
		t.Fatalf("%+v %+v", rows, findings)
	}
	rows, findings, _ = runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", MinSampleCount, "C.D", 500))
	if len(findings) != 1 || rows[0].Withheld {
		t.Fatalf("%+v %+v", rows, findings)
	}
}

func TestConcentrationNotObservedSpreadLikeTraffic(t *testing.T) {
	in := concInput(6 * time.Hour)
	// 50 stalls, half from each of two operations that are half of traffic each.
	stalls := append(concStalls("A.B", 25, 4, 1000), concStalls("C.D", 25, 4, 2000)...)
	r := concReport(stalls, false)
	_, findings, _ := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 500, "C.D", 500))
	if len(findings) != 0 {
		// 25 stalls per operation is below the 50-stall bar for each row.
		t.Fatalf("%+v", findings)
	}
	// 50 stalls from one operation that is the whole of traffic: lift 1.
	r = concReport(concStalls("A.B", 50, 4, 1000), false)
	_, findings, _ = runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 500))
	if len(findings) != 1 || findings[0].Headline != "Not observed: stalls are spread like traffic." || findings[0].Label != EvidenceSymptom || findings[0].N != 50 || findings[0].LowN {
		t.Fatalf("%+v", findings)
	}
	// 49 stalls say nothing.
	r = concReport(concStalls("A.B", 49, 4, 1000), false)
	_, findings, _ = runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 500))
	if len(findings) != 0 {
		t.Fatalf("%+v", findings)
	}
}

func TestConcentrationTruncatedIsWithheld(t *testing.T) {
	in := concInput(6 * time.Hour)
	r := concReport(concStalls("A.B", 12, 3, 100), false)
	r.StallsTruncated = true
	rows, findings, warns := runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, rollups("A.B", 24, "C.D", 100))
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "partial: stall rows capped") {
		t.Fatalf("%+v %+v %v", rows, findings, warns)
	}
}

func TestConcentrationLiveOverADayRefused(t *testing.T) {
	one := 77
	var stalls []Stall
	for i := 0; i < 12; i++ {
		stalls = append(stalls, Stall{Controller: "A", Action: "B", UserID: &one, Episode: 1 + i%3})
	}
	r := concReport(stalls, true)
	fake := &concFake{patternFakeRepo: &patternFakeRepo{}, breakdown: Breakdown{Total: 100, Provenance: narrowProv()}}
	rows, findings, warns := runConc(t, fake, concInput(25*time.Hour), r, rollups("A.B", 24, "C.D", 100))
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "over 24 hours") || len(fake.calls) != 0 {
		t.Fatalf("%+v %+v %v calls %d", rows, findings, warns, len(fake.calls))
	}
	// Exactly 24 hours on live is read, in hint mode.
	_, _, warns = runConc(t, fake, concInput(24*time.Hour), r, rollups("A.B", 24, "C.D", 100))
	if len(fake.calls) != 1 || fake.calls[0].Query.QueryMode != QueryModeHint || fake.calls[0].Dim != BreakdownUserID {
		t.Fatalf("calls %+v warns %v", fake.calls, warns)
	}
	// A long window on a source that measures docsExamined is not refused.
	rd := concReport(concStalls("A.B", 12, 3, 100), false)
	rows, _, _ = runConc(t, &noBreakdown{&patternFakeRepo{}}, concInput(72*time.Hour), rd, rollups("A.B", 24, "C.D", 100))
	if len(rows) != 1 {
		t.Fatalf("%+v", rows)
	}
}

func TestConcentrationUserDimension(t *testing.T) {
	in := concInput(6 * time.Hour)
	// One user has 12 stalls, in 3 episodes; the breakdown says 24 of 224 requests.
	var stalls []Stall
	u := 77
	for i := 0; i < 12; i++ {
		stalls = append(stalls, Stall{Controller: "A", Action: "b" + strconv.Itoa(i), UserID: &u, Episode: 1 + i%3})
	}
	fake := &concFake{patternFakeRepo: &patternFakeRepo{}, breakdown: Breakdown{
		Total: 224, Rows: []BreakdownRow{{Value: "77", Count: 24}, {Value: "5", Count: 100}}, Provenance: narrowProv(),
	}}
	rows, findings, warns := runConc(t, fake, in, concReport(stalls, true), nil)
	if len(findings) != 1 || len(rows) != 1 || rows[0].Dimension != ConcentrationUser || rows[0].Key != "77" {
		t.Fatalf("%+v %+v %v", rows, findings, warns)
	}
	if !strings.HasPrefix(findings[0].Headline, "User 77 had 100% of stall requests but 10.7% of all requests") || findings[0].Label != EvidenceCorrelation {
		t.Fatalf("%q", findings[0].Headline)
	}
	// A user with fewer than 20 rows is withheld.
	fake.breakdown.Rows[0].Count = 19
	rows, findings, _ = runConc(t, fake, in, concReport(stalls, true), nil)
	if len(findings) != 0 || len(rows) != 1 || !rows[0].Withheld {
		t.Fatalf("%+v %+v", rows, findings)
	}
	// A user outside the breakdown has no denominator and no row.
	fake.breakdown.Rows = []BreakdownRow{{Value: "5", Count: 100}}
	rows, findings, _ = runConc(t, fake, in, concReport(stalls, true), nil)
	if len(findings) != 0 || len(rows) != 0 {
		t.Fatalf("%+v %+v", rows, findings)
	}
}

func TestConcentrationUserReadFailsClosed(t *testing.T) {
	in := concInput(6 * time.Hour)
	u := 77
	var stalls []Stall
	for i := 0; i < 12; i++ {
		// One operation each, so only the user row is a candidate.
		stalls = append(stalls, Stall{Controller: "A", Action: "b" + strconv.Itoa(i), UserID: &u, Episode: 1 + i%3})
	}
	r := concReport(stalls, true)
	good := Breakdown{Total: 224, Rows: []BreakdownRow{{Value: "77", Count: 24}}}

	broad := &concFake{patternFakeRepo: &patternFakeRepo{}, breakdown: good}
	broad.breakdown.Provenance = []Provenance{{ScanClass: ScanBroad}}
	rows, findings, warns := runConc(t, broad, in, r, nil)
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "narrow scan") {
		t.Fatalf("broad: %+v %+v %v", rows, findings, warns)
	}

	none := &concFake{patternFakeRepo: &patternFakeRepo{}, breakdown: good}
	rows, findings, warns = runConc(t, none, in, r, nil)
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 {
		t.Fatalf("no provenance: %+v %+v %v", rows, findings, warns)
	}

	failing := &concFake{patternFakeRepo: &patternFakeRepo{}, err: errors.New("boom")}
	rows, findings, warns = runConc(t, failing, in, r, nil)
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "Traffic share by user was not read") {
		t.Fatalf("error: %+v %+v %v", rows, findings, warns)
	}

	// No BreakdownReader at all.
	rows, findings, warns = runConc(t, &noBreakdown{&patternFakeRepo{}}, in, r, nil)
	if len(rows) != 0 || len(findings) != 0 || len(warns) != 1 {
		t.Fatalf("no reader: %+v %+v %v", rows, findings, warns)
	}
}

func TestConcentrationCancelledContextReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u := 77
	var stalls []Stall
	for i := 0; i < 12; i++ {
		stalls = append(stalls, Stall{Controller: "A", Action: "b", UserID: &u, Episode: 1})
	}
	fake := &concFake{patternFakeRepo: &patternFakeRepo{}, err: context.Canceled}
	in := concInput(time.Hour)
	_, _, _, err := buildConcentration(ctx, fake, in, patternParams(in), concReport(stalls, true), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestConcentrationNoStallsNoWork(t *testing.T) {
	in := concInput(time.Hour)
	fake := &concFake{patternFakeRepo: &patternFakeRepo{}}
	rows, findings, warns := runConc(t, fake, in, concReport(nil, true), nil)
	if rows != nil || findings != nil || warns != nil || len(fake.calls) != 0 {
		t.Fatalf("%+v %+v %v", rows, findings, warns)
	}
}
