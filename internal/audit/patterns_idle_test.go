package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func irow(id, kind string, off time.Duration, audit float64) RequestRecord {
	start := patternT0.Add(off)
	a := audit
	return RequestRecord{ID: id, SourceID: "s1", Kind: kind, InitiatedAt: &start, AuditCreateTimeMs: &a}
}

// idleBuilder lays out synchronous rows on s1, each a gap after the one before.
type idleBuilder struct {
	rows []RequestRecord
	at   time.Duration
	n    int
}

func (b *idleBuilder) add(gap time.Duration, audit float64) *idleBuilder {
	b.at += gap
	b.n++
	b.rows = append(b.rows, irow(fmt.Sprintf("r%04d", b.n), "synchronous", b.at, audit))
	return b
}

// many adds n rows, each gap after the one before; the first slow of them are
// slow (6 s), the rest quick (0.1 s).
func (b *idleBuilder) many(gap time.Duration, n, slow int) *idleBuilder {
	for i := 0; i < n; i++ {
		audit := 100.0
		if i < slow {
			audit = 6000
		}
		b.add(gap, audit)
	}
	return b
}

// first starts the series with the row that has no known gap.
func newIdle() *idleBuilder { return (&idleBuilder{}).add(0, 100) }

const (
	quietGap = 650 * time.Second
	shortGap = 5 * time.Second
)

func idleInput(live bool) (PatternInput, PatternReport) {
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0.Add(-time.Hour), End: patternT0.Add(12 * time.Hour), IdleGaps: true}
	prov := Provenance{SourceID: "s1"}
	if !live {
		n := int64(1)
		prov.DocsExamined = &n
	}
	return in, PatternReport{Provenance: []Provenance{prov}}
}

func runIdle(t *testing.T, repo Repository, live bool) (*IdleGaps, []Finding, []Provenance, []string) {
	t.Helper()
	in, r := idleInput(live)
	table, findings, prov, warns, err := buildIdleGaps(context.Background(), repo, in, patternParams(in), r)
	if err != nil {
		t.Fatalf("buildIdleGaps: %v", err)
	}
	return table, findings, prov, warns
}

func bandNs(g *IdleGaps) []int {
	var ns []int
	for _, b := range g.Bands {
		ns = append(ns, b.N)
	}
	return ns
}

func TestIdleBandEdges(t *testing.T) {
	// Gaps of 29.999, 30, 60, 240 and 600 s each fall in the band that starts at
	// the edge or below it. The rows are given newest first, as the store lists them.
	b := newIdle()
	for _, gap := range []time.Duration{29999 * time.Millisecond, 30 * time.Second, 60 * time.Second, 240 * time.Second, 600 * time.Second} {
		b.add(gap, 100)
	}
	for i, j := 0, len(b.rows)-1; i < j; i, j = i+1, j-1 {
		b.rows[i], b.rows[j] = b.rows[j], b.rows[i]
	}
	g, _, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	if fmt.Sprint(bandNs(g)) != "[1 1 1 1 1]" {
		t.Fatalf("bands %v", bandNs(g))
	}
	for i, from := range []int{0, 30, 60, 240, 600} {
		if g.Bands[i].FromS != from {
			t.Fatalf("band %d from %d", i, g.Bands[i].FromS)
		}
		if i < 4 && (g.Bands[i].ToS == nil || *g.Bands[i].ToS != []int{30, 60, 240, 600}[i]) {
			t.Fatalf("band %d to %v", i, g.Bands[i].ToS)
		}
	}
	if g.Bands[4].ToS != nil {
		t.Fatalf("last band has an upper edge %v", *g.Bands[4].ToS)
	}
}

func TestIdleFirstGapUnknown(t *testing.T) {
	g, _, _, _ := runIdle(t, newRepeatFake(newIdle().rows...), false)
	if fmt.Sprint(bandNs(g)) != "[0 0 0 0 0]" {
		t.Fatalf("a lone row has no gap: %v", bandNs(g))
	}
	g, _, _, _ = runIdle(t, newRepeatFake(newIdle().add(quietGap, 100).rows...), false)
	if fmt.Sprint(bandNs(g)) != "[0 0 0 0 1]" {
		t.Fatalf("two rows have one gap: %v", bandNs(g))
	}
}

func TestIdleHangfireExcludedAndCounted(t *testing.T) {
	// A Hangfire row inside a quiet gap neither splits it nor is used.
	rows := []RequestRecord{
		irow("a", "synchronous", 0, 100),
		irow("h1", "hangfire", 300*time.Second, 100),
		irow("h2", "hangfire", 500*time.Second, 100),
		irow("b", "synchronous", quietGap, 100),
	}
	g, _, _, warns := runIdle(t, newRepeatFake(rows...), false)
	if g.ExcludedHangfire != 2 || fmt.Sprint(bandNs(g)) != "[0 0 0 0 1]" {
		t.Fatalf("excluded %d bands %v", g.ExcludedHangfire, bandNs(g))
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "2 Hangfire rows were left out") {
		t.Fatalf("warnings %q", warns)
	}
}

func TestIdleOtherKindsIgnored(t *testing.T) {
	rows := []RequestRecord{
		irow("a", "synchronous", 0, 100),
		irow("x1", "asynchronous", 300*time.Second, 100),
		irow("x2", "unknown", 500*time.Second, 100),
		irow("b", "synchronous", quietGap, 100),
	}
	g, _, _, warns := runIdle(t, newRepeatFake(rows...), false)
	if g.ExcludedHangfire != 0 || fmt.Sprint(bandNs(g)) != "[0 0 0 0 1]" || len(warns) != 0 {
		t.Fatalf("excluded %d bands %v warnings %q", g.ExcludedHangfire, bandNs(g), warns)
	}
}

func TestIdleGapsAreMeasuredPerSource(t *testing.T) {
	a1, a2 := irow("a1", "synchronous", 0, 100), irow("a2", "synchronous", quietGap, 100)
	other := irow("o1", "synchronous", 300*time.Second, 100)
	other.SourceID = "s2"
	in, r := idleInput(false)
	in.SourceIDs = []string{"s1", "s2"}
	table, _, _, _, err := buildIdleGaps(context.Background(), newRepeatFake(a1, a2, other), in, patternParams(in), r)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(bandNs(table)) != "[0 0 0 0 1]" {
		t.Fatalf("bands %v", bandNs(table))
	}
}

func TestIdleRowsWithoutAuditTimeStillEndAGap(t *testing.T) {
	b := newIdle().add(quietGap, 100)
	b.rows[1].AuditCreateTimeMs = nil
	b.add(shortGap, 100)
	g, _, _, warns := runIdle(t, newRepeatFake(b.rows...), false)
	if fmt.Sprint(bandNs(g)) != "[1 0 0 0 0]" {
		t.Fatalf("bands %v", bandNs(g))
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "1 row with no audit-create time left out") {
		t.Fatalf("warnings %q", warns)
	}
}

func TestIdleLowNMasking(t *testing.T) {
	for _, tc := range []struct {
		n      int
		masked bool
	}{{MinSampleCount - 1, true}, {MinSampleCount, false}} {
		g, _, _, _ := runIdle(t, newRepeatFake(newIdle().many(shortGap, tc.n, 0).rows...), false)
		b := g.Bands[0]
		if b.N != tc.n {
			t.Fatalf("n %d", b.N)
		}
		if got := b.MedianMs == nil && b.P95Ms == nil && b.SlowRate == nil; got != tc.masked {
			t.Fatalf("n %d: masked %v, band %+v", tc.n, got, b)
		}
		if !tc.masked && (*b.MedianMs != 100 || *b.P95Ms != 100 || *b.SlowRate != 0) {
			t.Fatalf("band %+v", b)
		}
	}
}

func TestIdleLiftWithheldWhenEitherSideIsThin(t *testing.T) {
	thin := MinSampleCount - 1
	for name, b := range map[string]*idleBuilder{
		"thin gap band":  newIdle().many(shortGap, 40, 2).many(quietGap, thin, thin),
		"thin reference": newIdle().many(shortGap, thin, 0).many(quietGap, 40, 20),
	} {
		g, finds, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
		for _, l := range g.Lift {
			if !l.Withheld || l.Value != nil {
				t.Fatalf("%s: lift %+v", name, l)
			}
		}
		if len(finds) != 0 {
			t.Fatalf("%s: finding %+v", name, finds)
		}
	}
}

func TestIdleLiftValue(t *testing.T) {
	// Reference 2 of 40 slow (5%); gap band 10 of 20 slow (50%): lift 10.
	b := newIdle().many(shortGap, 40, 2).many(quietGap, 20, 10)
	g, finds, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	for _, l := range g.Lift {
		if l.Withheld || l.Value == nil || *l.Value != 10 {
			t.Fatalf("lift %+v", l)
		}
	}
	if len(g.Lift) != 3 || g.Lift[0].GS != 60 || g.Lift[1].GS != 240 || g.Lift[2].GS != 600 {
		t.Fatalf("lift gaps %+v", g.Lift)
	}
	if len(finds) != 1 || finds[0].Rule != "B6g" || finds[0].N != 20 || finds[0].LowN {
		t.Fatalf("findings %+v", finds)
	}
}

func TestIdleLiftWithNoSlowReferenceRowUsesHalfARow(t *testing.T) {
	// Reference 0 of 40 slow counts as half a row (1.25%); gap band 50%: lift 40.
	b := newIdle().many(shortGap, 40, 0).many(quietGap, 20, 10)
	g, _, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	if v := g.Lift[2].Value; v == nil || *v != 40 {
		t.Fatalf("lift %+v", g.Lift)
	}
}

func TestIdleRaisedPicksLargestLiftThenLongerGap(t *testing.T) {
	// 60-239 s gaps are quick, so G = 60 has a smaller lift than G = 240 and 600,
	// which share the same pool; the longer gap is named.
	b := newIdle().many(shortGap, 40, 2).many(100*time.Second, 20, 0).many(quietGap, 20, 15)
	g, finds, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	if *g.Lift[0].Value >= *g.Lift[1].Value || *g.Lift[1].Value != *g.Lift[2].Value {
		t.Fatalf("lift %+v %+v %+v", g.Lift[0], g.Lift[1], g.Lift[2])
	}
	if len(finds) != 1 || !strings.Contains(finds[0].Headline, "of at least 10 min, audit create took 6 s at the median (n = 20)") {
		t.Fatalf("findings %+v", finds)
	}
}

func TestIdleRaisedWording(t *testing.T) {
	b := newIdle().many(shortGap, 40, 2).many(quietGap, 20, 12)
	_, finds, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	want := "Raised after quiet gaps. After quiet gaps of at least 10 min, audit create took 6 s at the median (n = 20). After gaps under 30 s it took 0.1 s (n = 40). Audit create is measured from the client side and covers setup work and the database write, not just the write."
	if len(finds) != 1 || finds[0].Headline != want || finds[0].Label != EvidenceSymptom || finds[0].EvidenceID == "" {
		t.Fatalf("findings %+v", finds)
	}
}

func TestIdleNotObservedNeedsFiftyRows(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want bool
	}{{idleFalsifyRows - 1, false}, {idleFalsifyRows, true}} {
		b := newIdle().many(shortGap, 60, 3).many(quietGap, tc.n, 0)
		g, finds, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
		if g.Lift[2].Value == nil || *g.Lift[2].Value > idleFalsifyLift {
			t.Fatalf("lift %+v", g.Lift[2])
		}
		if got := len(finds) == 1; got != tc.want {
			t.Fatalf("n %d: findings %+v", tc.n, finds)
		}
		if tc.want && (finds[0].Headline != "Not observed: audit create was not slower after quiet gaps of at least 10 min in this window." || finds[0].Label != EvidenceSymptom || finds[0].N != tc.n) {
			t.Fatalf("finding %+v", finds[0])
		}
	}
}

func TestIdleIntermediateLiftPrintsNothing(t *testing.T) {
	// Lift 2: neither raised (5) nor flat (1.5).
	b := newIdle().many(shortGap, 40, 4).many(quietGap, 60, 12)
	g, finds, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	if *g.Lift[2].Value != 2 || len(finds) != 0 {
		t.Fatalf("lift %+v findings %+v", g.Lift[2], finds)
	}
}

func TestIdleRowCapMakesItPartial(t *testing.T) {
	old := gapRowCap
	gapRowCap = 50
	defer func() { gapRowCap = old }()
	b := newIdle().many(shortGap, 60, 0).many(quietGap, 40, 40)
	fake := newRepeatFake(b.rows...)
	g, finds, prov, warns := runIdle(t, fake, false)
	if !g.Partial || !strings.HasPrefix(g.PartialReason, "row cap reached") {
		t.Fatalf("table %+v", g)
	}
	for _, l := range g.Lift {
		if !l.Withheld || l.Value != nil {
			t.Fatalf("lift %+v", l)
		}
	}
	if len(finds) != 0 {
		t.Fatalf("findings %+v", finds)
	}
	total := 0
	for _, n := range bandNs(g) {
		total += n
	}
	if total == 0 || total > 50 {
		t.Fatalf("a cut read still shows its bands, within the cap: %v", bandNs(g))
	}
	if len(prov) != 1 || prov[0].Completeness != Partial || prov[0].PartialReason != "rowcap" {
		t.Fatalf("provenance %+v", prov)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "newest 50 rows per source") {
		t.Fatalf("warnings %q", warns)
	}
}

func TestIdleReadErrorIsAWarningNotAnError(t *testing.T) {
	fake := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	fake.err = errors.New("boom")
	g, finds, _, warns := runIdle(t, fake, false)
	if !g.Partial || g.PartialReason == "" || len(finds) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "could not read 1 of 1 source") {
		t.Fatalf("table %+v findings %+v warnings %q", g, finds, warns)
	}
	if strings.Contains(strings.Join(warns, " "), "boom") {
		t.Fatalf("the raw error must not be printed: %q", warns)
	}
}

func TestIdleLiveWindowOverADayIsRefusedBeforeAnyRead(t *testing.T) {
	fake := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	fake.live = true
	in, r := idleInput(true)
	in.End = in.Start.Add(24*time.Hour + time.Second)
	_, _, _, _, err := buildIdleGaps(context.Background(), fake, in, patternParams(in), r)
	if !errors.Is(err, ErrBroadScanRefused) || len(fake.calls) != 0 {
		t.Fatalf("err %v, %d reads", err, len(fake.calls))
	}
	// A window of exactly 24 hours is allowed.
	in.End = in.Start.Add(24 * time.Hour)
	if _, _, _, _, err := buildIdleGaps(context.Background(), fake, in, patternParams(in), r); err != nil {
		t.Fatal(err)
	}
}

func TestIdleLongWindowIsFineOnANonLiveSource(t *testing.T) {
	fake := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	in, r := idleInput(false)
	in.End = in.Start.Add(72 * time.Hour)
	if _, _, _, _, err := buildIdleGaps(context.Background(), fake, in, patternParams(in), r); err != nil {
		t.Fatal(err)
	}
}

func TestIdleLiveReadThatIsNotNarrowIsRefused(t *testing.T) {
	fake := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	fake.live, fake.broad = true, true
	in, r := idleInput(true)
	table, finds, _, _, err := buildIdleGaps(context.Background(), fake, in, patternParams(in), r)
	if !errors.Is(err, ErrBroadScanRefused) || table != nil || finds != nil {
		t.Fatalf("table %+v findings %+v err %v", table, finds, err)
	}
}

func TestIdleLiveReadsAreHintBoundedOneAtATime(t *testing.T) {
	fake := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	fake.live = true
	runIdle(t, fake, true)
	if len(fake.calls) == 0 {
		t.Fatal("no read")
	}
	for _, c := range fake.calls {
		if c.Query.QueryMode != QueryModeHint || c.Order != OrderNewest || c.Limit != patternAuditPage || len(c.Query.SourceIDs) != 1 {
			t.Fatalf("read %+v", c)
		}
	}
	// A source that is not live keeps the default mode.
	plain := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	runIdle(t, plain, false)
	if plain.calls[0].Query.QueryMode == QueryModeHint {
		t.Fatalf("read %+v", plain.calls[0])
	}
}

func TestIdleProvenanceIsOneRowPerSourceAndWindow(t *testing.T) {
	b := newIdle().many(shortGap, 250, 0)
	_, _, prov, _ := runIdle(t, newRepeatFake(b.rows...), false)
	if len(prov) != 1 || prov[0].SourceID != "s1" || prov[0].DocsExamined == nil || *prov[0].DocsExamined != 251 {
		t.Fatalf("provenance %+v", prov)
	}
}

// idleRunFake is a repeatFake that can also rank the slowest requests, with none,
// so RunPatterns runs end to end.
type idleRunFake struct{ *repeatFake }

func (f idleRunFake) Slowest(ctx context.Context, input PageInput) (Page, error) {
	return patternFakeSlow{f.patternFakeRepo}.Slowest(ctx, input)
}

func TestRunPatternsIdleGapsIsOptIn(t *testing.T) {
	rows := newIdle().many(shortGap, 5, 0).rows
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0.Add(-time.Hour), End: patternT0.Add(time.Hour)}
	offFake := newRepeatFake(rows...)
	off := idleRunFake{offFake}
	report, err := RunPatterns(context.Background(), off, in)
	if err != nil {
		t.Fatal(err)
	}
	if report.IdleGaps != nil || findingByRule(report, "B6g") != nil {
		t.Fatalf("%+v", report.IdleGaps)
	}
	in.IdleGaps = true
	onFake := newRepeatFake(rows...)
	report, err = RunPatterns(context.Background(), idleRunFake{onFake}, in)
	if err != nil {
		t.Fatal(err)
	}
	if report.IdleGaps == nil || len(onFake.calls) <= len(offFake.calls) {
		t.Fatalf("table %+v, reads %d vs %d", report.IdleGaps, len(onFake.calls), len(offFake.calls))
	}
}

func TestRunPatternsIdleGapsRefusalIsAnError(t *testing.T) {
	fake := newRepeatFake(newIdle().many(shortGap, 5, 0).rows...)
	fake.live = true
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0.Add(-30 * time.Hour), End: patternT0, IdleGaps: true}
	if _, err := RunPatterns(context.Background(), idleRunFake{fake}, in); !errors.Is(err, ErrBroadScanRefused) {
		t.Fatalf("err %v", err)
	}
}

func TestIdleJSONContract(t *testing.T) {
	b := newIdle().many(shortGap, 40, 2).many(quietGap, 20, 10)
	g, _, _, _ := runIdle(t, newRepeatFake(b.rows...), false)
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bands", "lift", "excludedHangfire", "partial"} {
		if _, ok := top[k]; !ok {
			t.Fatalf("missing %q in %s", k, raw)
		}
	}
	if _, ok := top["partialReason"]; ok {
		t.Fatalf("partialReason is omitted when not partial: %s", raw)
	}
	var bands []map[string]any
	if err := json.Unmarshal(top["bands"], &bands); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"fromS", "toS", "n", "medianMs", "p95Ms", "slowRate"} {
		if _, ok := bands[4][k]; !ok {
			t.Fatalf("band missing %q: %v", k, bands[4])
		}
	}
	if bands[4]["toS"] != nil || bands[1]["medianMs"] != nil {
		t.Fatalf("nulls: %v %v", bands[4], bands[1])
	}
	var lifts []map[string]any
	if err := json.Unmarshal(top["lift"], &lifts); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"gS", "value", "withheld"} {
		if _, ok := lifts[0][k]; !ok {
			t.Fatalf("lift missing %q: %v", k, lifts[0])
		}
	}
}

func TestIdleMedianAndNearestRank(t *testing.T) {
	if got := medianOf([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Fatalf("even median %v", got)
	}
	if got := medianOf([]float64{5, 1, 3}); got != 3 {
		t.Fatalf("odd median %v", got)
	}
	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(20 - i)
	}
	if got := nearestRank(values, 0.95); got != 19 {
		t.Fatalf("p95 of 1..20 is %v", got)
	}
}

func TestIdleFmtGap(t *testing.T) {
	for in, want := range map[int]string{30: "30 s", 60: "1 min", 240: "4 min", 600: "10 min", 90: "90 s"} {
		if got := fmtGap(in); got != want {
			t.Fatalf("fmtGap(%d) = %q, want %q", in, got, want)
		}
	}
}

// Copy never states a cause or uses the wording the roadmap rules out.
func TestIdleCopyAvoidsCausalWording(t *testing.T) {
	raised := newIdle().many(shortGap, 40, 2).many(quietGap, 20, 12)
	flat := newIdle().many(shortGap, 60, 3).many(quietGap, 60, 0)
	cut := newIdle().many(shortGap, 60, 3).many(quietGap, 60, 0)
	var text []string
	for _, b := range []*idleBuilder{raised, flat} {
		_, finds, _, warns := runIdle(t, newRepeatFake(b.rows...), false)
		for _, f := range finds {
			text = append(text, f.Headline)
		}
		text = append(text, warns...)
	}
	old := gapRowCap
	gapRowCap = 30
	defer func() { gapRowCap = old }()
	_, _, _, warns := runIdle(t, newRepeatFake(cut.rows...), false)
	text = append(text, warns...)
	if len(text) < 3 {
		t.Fatalf("no copy to check: %q", text)
	}
	for _, s := range text {
		lower := strings.ToLower(s)
		for _, bad := range []string{"retry", "retries", "insert took", "cold connection", "blocked by", "caused", "because", "due to"} {
			if strings.Contains(lower, bad) {
				t.Fatalf("%q contains %q", s, bad)
			}
		}
	}
}
