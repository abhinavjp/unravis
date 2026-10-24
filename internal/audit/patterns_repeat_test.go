package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// repeatFake is a Repository whose Requests honours the source, the user filter
// and the window, as the real store does, and records every read.
type repeatFake struct {
	*patternFakeRepo
	rows  []RequestRecord
	live  bool // narrow provenance with no DocsExamined, as a live store reports
	broad bool // with live: a scan class that is not narrow
	err   error
	calls []PageInput
}

func (f *repeatFake) Requests(ctx context.Context, input PageInput) (Page, error) {
	f.calls = append(f.calls, input)
	if f.err != nil {
		return Page{}, f.err
	}
	users := map[int]bool{}
	for _, u := range input.Query.UserIDs {
		users[u] = true
	}
	var rows []RequestRecord
	for _, r := range f.rows {
		if r.SourceID != input.Query.SourceIDs[0] || r.InitiatedAt == nil {
			continue
		}
		if r.InitiatedAt.Before(input.Query.Start) || !r.InitiatedAt.Before(input.Query.End) {
			continue
		}
		if len(users) > 0 && (r.UserID == nil || !users[*r.UserID]) {
			continue
		}
		rows = append(rows, r)
	}
	page, err := fakePage(rows, input)
	if err != nil {
		return page, err
	}
	scan := ScanNarrow
	if f.broad {
		scan = ScanBroad
	}
	prov := Provenance{SourceID: input.Query.SourceIDs[0], Window: "page", ScanClass: scan}
	if !f.live {
		n := int64(len(page.Rows))
		prov.DocsExamined = &n
	}
	page.Provenance = []Provenance{prov}
	return page, nil
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// rrow is a complete request on s1 by user, starting offset after patternT0.
func rrow(id string, user int, op string, offset time.Duration, kb float64) RequestRecord {
	controller, action, _ := strings.Cut(op, ".")
	start := patternT0.Add(offset)
	u := user
	return RequestRecord{ID: id, SourceID: "s1", Controller: controller, Action: action, InitiatedAt: &start, UserID: &u, RequestKB: kb}
}

// rstall is the stall of a request: it starts at offset and lasts dur, or never
// completed when dur is zero.
func rstall(id string, user int, op string, offset, dur time.Duration) Stall {
	controller, action, _ := strings.Cut(op, ".")
	start := patternT0.Add(offset)
	u := user
	s := Stall{SourceID: "s1", ID: id, Controller: controller, Action: action, InitiatedAt: start, UserID: &u, Episode: 1, ObservedMs: float64(dur.Milliseconds())}
	if dur > 0 {
		end := start.Add(dur)
		s.CompletedAt = &end
	}
	return s
}

func repeatReport(live bool, stalls ...Stall) PatternReport {
	prov := Provenance{SourceID: "s1"}
	if !live {
		n := int64(1)
		prov.DocsExamined = &n
	}
	r := PatternReport{Stalls: stalls, Provenance: []Provenance{prov}}
	byEpisode := map[int]*Episode{}
	for _, s := range stalls {
		end := s.InitiatedAt.Add(ms(int(s.ObservedMs)))
		e := byEpisode[s.Episode]
		if e == nil {
			e = &Episode{Index: s.Episode, Start: s.InitiatedAt, End: end}
			byEpisode[s.Episode] = e
		}
		if s.InitiatedAt.Before(e.Start) {
			e.Start = s.InitiatedAt
		}
		if end.After(e.End) {
			e.End = end
		}
		e.Requests++
	}
	for i := 1; i <= len(byEpisode); i++ {
		r.Episodes = append(r.Episodes, *byEpisode[i])
	}
	return r
}

func runRepeats(t *testing.T, repo Repository, r PatternReport) ([]Repeat, []Finding, []Provenance, []string) {
	t.Helper()
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0.Add(-time.Hour), End: patternT0.Add(6 * time.Hour)}
	rows, findings, prov, warns, err := buildRepeats(context.Background(), repo, in, patternParams(in), r)
	if err != nil {
		t.Fatalf("buildRepeats: %v", err)
	}
	return rows, findings, prov, warns
}

func newRepeatFake(rows ...RequestRecord) *repeatFake {
	return &repeatFake{patternFakeRepo: &patternFakeRepo{}, rows: rows}
}

func TestRepeatsBoundaries(t *testing.T) {
	op := "Payroll.Run"
	stall := rstall("a", 7, op, 0, 60*time.Second)
	fake := newRepeatFake(
		rrow("a", 7, op, 0, 2),
		rrow("r1", 7, op, ms(10000), 2),
		rrow("r2", 7, op, ms(30000), 2),
		rrow("r3", 7, op, ms(59900), 2),
		rrow("r4", 7, op, ms(60000), 2), // starts as the stall completes: not while it was running
		rrow("r5", 7, op, ms(500), 2),   // inside the volley window: B12's shape
		rrow("r6", 7, op, ms(1000), 2),  // exactly the window: not after it
		rrow("r7", 7, op, ms(1001), 2),  // just past the window
		rrow("o1", 8, op, ms(20000), 2), // another user
		rrow("o2", 7, "Payroll.Print", ms(20000), 2),
	)
	rows, findings, prov, warns := runRepeats(t, fake, repeatReport(false, stall))
	if len(warns) != 0 || len(prov) != 1 {
		t.Fatalf("warns %v prov %+v", warns, prov)
	}
	if len(rows) != 1 || rows[0] != (Repeat{Episode: 1, UserID: 7, Operation: op, Repeats: 4, SameSize: true, LongestChain: 5}) {
		t.Fatalf("rows %+v", rows)
	}
	if len(findings) != 1 {
		t.Fatalf("findings %+v", findings)
	}
	f := findings[0]
	if f.Rule != "B12r" || f.Label != EvidenceCorrelation || f.N != 4 || !f.LowN || !strings.HasPrefix(f.EvidenceID, "p2:b12r:") {
		t.Fatalf("%+v", f)
	}
	if f.Headline != "The same user started Payroll.Run again 4 times while an earlier one was still running, during this stall (episode 1)." {
		t.Fatalf("headline %q", f.Headline)
	}
	// The read is one filtered Requests call over the episode plus the tail pad.
	if len(fake.calls) != 1 {
		t.Fatalf("calls %d", len(fake.calls))
	}
	q := fake.calls[0].Query
	if len(q.UserIDs) != 1 || q.UserIDs[0] != 7 || !q.Start.Equal(stall.InitiatedAt) || !q.End.Equal(patternT0.Add(60*time.Second+repeatTailPad)) ||
		q.Kind != "all" || fake.calls[0].Order != OrderNewest || q.QueryMode == QueryModeHint {
		t.Fatalf("query %+v order %v", q, fake.calls[0].Order)
	}
}

func TestRepeatsCountedOnceAgainstEarliestStall(t *testing.T) {
	op := "Payroll.Run"
	a := rstall("a", 7, op, 0, 60*time.Second)
	b := rstall("b", 7, op, 20*time.Second, 60*time.Second)
	fake := newRepeatFake(
		rrow("a", 7, op, 0, 2), rrow("b", 7, op, 20*time.Second, 2),
		rrow("r1", 7, op, 30*time.Second, 2), // follows both: counted for a only
		rrow("r2", 7, op, 70*time.Second, 2), // a is done; b is still running
	)
	rows, _, _, _ := runRepeats(t, fake, repeatReport(false, a, b))
	// b follows a, r1 follows a, r2 follows b: three repeats, two of them on a.
	if len(rows) != 1 || rows[0].Repeats != 3 || rows[0].LongestChain != 3 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestRepeatsIncompleteEarlierStallStillRunning(t *testing.T) {
	op := "Payroll.Run"
	// The earlier stall never completed, so any later start counts as while it ran.
	a := rstall("a", 7, op, 0, 0)
	a.ObservedMs = 45000
	fake := newRepeatFake(rrow("a", 7, op, 0, 2), rrow("r1", 7, op, 100*time.Second, 2))
	rows, findings, _, _ := runRepeats(t, fake, repeatReport(false, a))
	if len(rows) != 1 || rows[0].Repeats != 1 || len(findings) != 1 || findings[0].Label != EvidenceCorrelation {
		t.Fatalf("rows %+v findings %+v", rows, findings)
	}
	if !strings.Contains(findings[0].Headline, "again 1 time while") {
		t.Fatalf("headline %q", findings[0].Headline)
	}
}

func TestRepeatsSizeSplit(t *testing.T) {
	op := "Payroll.Run"
	a := rstall("a", 7, op, 0, 60*time.Second)
	fake := newRepeatFake(
		rrow("a", 7, op, 0, 2),
		rrow("r1", 7, op, 10*time.Second, 2),
		rrow("r2", 7, op, 20*time.Second, 9), // another request body
		rrow("r3", 7, op, 30*time.Second, 9),
	)
	rows, findings, _, _ := runRepeats(t, fake, repeatReport(false, a))
	if len(rows) != 2 || rows[0].Repeats != 2 || rows[0].SameSize || rows[1].Repeats != 1 || !rows[1].SameSize {
		t.Fatalf("rows %+v", rows)
	}
	// Only the same-size row is worded as the same request started again.
	if len(findings) != 1 || findings[0].N != 1 || !strings.Contains(findings[0].Headline, "again 1 time while") {
		t.Fatalf("findings %+v", findings)
	}
	// With no same-size row there is a table row but no finding: nothing to claim.
	fake = newRepeatFake(rrow("a", 7, op, 0, 2), rrow("r2", 7, op, 20*time.Second, 9))
	rows, findings, _, _ = runRepeats(t, fake, repeatReport(false, a))
	if len(rows) != 1 || rows[0].SameSize || len(findings) != 0 {
		t.Fatalf("rows %+v findings %+v", rows, findings)
	}
}

func TestRepeatsNotObserved(t *testing.T) {
	op := "Payroll.Run"
	a := rstall("a", 7, op, 0, 60*time.Second)
	fake := newRepeatFake(rrow("a", 7, op, 0, 2), rrow("late", 7, op, 61*time.Second, 2))
	rows, findings, _, warns := runRepeats(t, fake, repeatReport(false, a))
	if len(rows) != 0 || len(warns) != 0 || len(findings) != 1 {
		t.Fatalf("rows %+v findings %+v warns %v", rows, findings, warns)
	}
	f := findings[0]
	if f.Rule != "B12r" || f.Label != EvidenceSymptom || f.N != 1 || !f.LowN || f.Headline != "Not observed: no user started the same operation again while it was running." {
		t.Fatalf("%+v", f)
	}
	// The stall's own row was not read, so nothing could be checked: say nothing.
	fake = newRepeatFake(rrow("late", 7, op, 61*time.Second, 2))
	rows, findings, _, _ = runRepeats(t, fake, repeatReport(false, a))
	if len(rows) != 0 || len(findings) != 0 {
		t.Fatalf("rows %+v findings %+v", rows, findings)
	}
	// A stall without a user cannot be matched to a repeat.
	anon := a
	anon.UserID = nil
	rows, findings, _, _ = runRepeats(t, newRepeatFake(rrow("a", 7, op, 0, 2)), repeatReport(false, anon))
	if len(rows) != 0 || len(findings) != 0 {
		t.Fatalf("rows %+v findings %+v", rows, findings)
	}
}

func TestRepeatsNoEpisodesNoReads(t *testing.T) {
	fake := newRepeatFake()
	rows, findings, prov, warns := runRepeats(t, fake, PatternReport{})
	if rows != nil || findings != nil || prov != nil || warns != nil || len(fake.calls) != 0 {
		t.Fatalf("%+v %+v %+v %v %d", rows, findings, prov, warns, len(fake.calls))
	}
}

func TestRepeatsWithheldWhenStallsTruncated(t *testing.T) {
	fake := newRepeatFake()
	r := repeatReport(false, rstall("a", 7, "Payroll.Run", 0, 60*time.Second))
	r.StallsTruncated = true
	rows, findings, _, warns := runRepeats(t, fake, r)
	if len(rows) != 0 || len(findings) != 0 || len(fake.calls) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "partial: stall rows capped") {
		t.Fatalf("%+v %+v %v calls %d", rows, findings, warns, len(fake.calls))
	}
}

func TestRepeatsEpisodeCap(t *testing.T) {
	var stalls []Stall
	var rows []RequestRecord
	for i := 1; i <= maxRepeatEpisodes+2; i++ {
		off := time.Duration(i) * time.Hour
		s := rstall(fmt.Sprintf("s%d", i), 7, "Payroll.Run", off, 60*time.Second)
		s.Episode = i
		stalls = append(stalls, s)
		rows = append(rows, rrow(s.ID, 7, "Payroll.Run", off, 2))
	}
	fake := newRepeatFake(rows...)
	r := repeatReport(false, stalls...)
	// The first two episodes are the smallest, so they are the ones left out.
	r.Episodes[0].Requests, r.Episodes[1].Requests = 1, 1
	for i := 2; i < len(r.Episodes); i++ {
		r.Episodes[i].Requests = 2
	}
	_, findings, _, warns := runRepeats(t, fake, r)
	if len(fake.calls) != maxRepeatEpisodes {
		t.Fatalf("reads %d, want %d", len(fake.calls), maxRepeatEpisodes)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "10 largest of 12 episodes (partial)") {
		t.Fatalf("warns %v", warns)
	}
	// Left-out episodes make the count partial, so there is no "not observed".
	if len(findings) != 0 {
		t.Fatalf("findings %+v", findings)
	}
	for _, c := range fake.calls {
		if c.Query.Start.Equal(patternT0.Add(time.Hour)) || c.Query.Start.Equal(patternT0.Add(2*time.Hour)) {
			t.Fatalf("a small episode was read: %+v", c.Query.Start)
		}
	}
}

func TestRepeatsUserCap(t *testing.T) {
	var stalls []Stall
	var rows []RequestRecord
	for u := 1; u <= MaxUserIDs+10; u++ {
		s := rstall(fmt.Sprintf("s%d", u), u, "Payroll.Run", time.Duration(u)*time.Second, 90*time.Second)
		stalls = append(stalls, s)
		rows = append(rows, rrow(s.ID, u, "Payroll.Run", time.Duration(u)*time.Second, 2))
	}
	fake := newRepeatFake(rows...)
	_, findings, _, warns := runRepeats(t, fake, repeatReport(false, stalls...))
	if len(fake.calls) != 1 || len(fake.calls[0].Query.UserIDs) != MaxUserIDs {
		t.Fatalf("calls %+v", fake.calls)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "50 users with the most stalls (partial)") || len(findings) != 0 {
		t.Fatalf("warns %v findings %+v", warns, findings)
	}
}

func TestRepeatsLiveRefusesOverTwentyFourHours(t *testing.T) {
	op := "Payroll.Run"
	long := rstall("a", 7, op, 0, 25*time.Hour)
	fake := newRepeatFake(rrow("a", 7, op, 0, 2))
	fake.live = true
	rows, findings, prov, warns := runRepeats(t, fake, repeatReport(true, long))
	if len(fake.calls) != 0 || len(rows) != 0 || len(findings) != 0 || len(prov) != 0 {
		t.Fatalf("a live read over 24 h ran: calls %d rows %+v findings %+v", len(fake.calls), rows, findings)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "over 24 hours on a live source") {
		t.Fatalf("warns %v", warns)
	}
	// The same episode on a source that reports documents examined is read.
	fake = newRepeatFake(rrow("a", 7, op, 0, 2))
	_, _, _, warns = runRepeats(t, fake, repeatReport(false, long))
	if len(fake.calls) != 1 || len(warns) != 0 {
		t.Fatalf("calls %d warns %v", len(fake.calls), warns)
	}
}

func TestRepeatsLiveReadIsHintAndMustBeNarrow(t *testing.T) {
	op := "Payroll.Run"
	a := rstall("a", 7, op, 0, 60*time.Second)
	rowsIn := []RequestRecord{rrow("a", 7, op, 0, 2), rrow("r1", 7, op, 10*time.Second, 2)}

	fake := newRepeatFake(rowsIn...)
	fake.live = true
	rows, findings, prov, warns := runRepeats(t, fake, repeatReport(true, a))
	if len(fake.calls) != 1 || fake.calls[0].Query.QueryMode != QueryModeHint {
		t.Fatalf("calls %+v", fake.calls)
	}
	if len(rows) != 1 || len(findings) != 1 || len(prov) != 1 || len(warns) != 0 {
		t.Fatalf("rows %+v findings %+v prov %+v warns %v", rows, findings, prov, warns)
	}

	// A read that comes back as a broad scan fails closed: nothing from it is used,
	// but its provenance stays in the ledger.
	fake = newRepeatFake(rowsIn...)
	fake.live, fake.broad = true, true
	rows, findings, prov, warns = runRepeats(t, fake, repeatReport(true, a))
	if len(rows) != 0 || len(findings) != 0 || len(prov) != 1 || len(warns) != 1 || !strings.Contains(warns[0], "would not be a narrow scan") {
		t.Fatalf("rows %+v findings %+v prov %+v warns %v", rows, findings, prov, warns)
	}
}

func TestRepeatsCutReadIsALowerBound(t *testing.T) {
	op := "Payroll.Run"
	a := rstall("a", 7, op, 0, 120*time.Second)
	rowsIn := []RequestRecord{rrow("a", 7, op, 0, 2), rrow("r1", 7, op, 10*time.Second, 2)}
	// More rows than a pattern read keeps, all inside the window and by the same user.
	for i := 0; i < MaxPatternRows+20; i++ {
		rowsIn = append(rowsIn, rrow(fmt.Sprintf("n%d", i), 7, "Payroll.Print", 2*time.Second+ms(i), 2))
	}
	fake := newRepeatFake(rowsIn...)
	rows, findings, _, warns := runRepeats(t, fake, repeatReport(false, a))
	if len(warns) != 1 || !strings.Contains(warns[0], "lower bound: only the newest 500 rows") {
		t.Fatalf("warns %v", warns)
	}
	if len(rows) != 1 || rows[0].Repeats != 1 || len(findings) != 1 || !strings.HasSuffix(findings[0].Headline, " Partial count (at least).") {
		t.Fatalf("rows %+v findings %+v", rows, findings)
	}
	// A cut read with nothing found is never "not observed".
	rowsIn = rowsIn[:1]
	for i := 0; i < MaxPatternRows+20; i++ {
		rowsIn = append(rowsIn, rrow(fmt.Sprintf("n%d", i), 7, "Payroll.Print", 2*time.Second+ms(i), 2))
	}
	rows, findings, _, _ = runRepeats(t, newRepeatFake(rowsIn...), repeatReport(false, a))
	if len(rows) != 0 || len(findings) != 0 {
		t.Fatalf("rows %+v findings %+v", rows, findings)
	}
}

func TestRepeatsReadErrorIsAWarning(t *testing.T) {
	a := rstall("a", 7, "Payroll.Run", 0, 60*time.Second)
	fake := newRepeatFake(rrow("a", 7, "Payroll.Run", 0, 2))
	fake.err = errors.New("mongo said no")
	rows, findings, prov, warns := runRepeats(t, fake, repeatReport(false, a))
	if len(rows) != 0 || len(findings) != 0 || len(prov) != 0 || len(warns) != 1 || !strings.HasPrefix(warns[0], "Repeats in episode 1 were not read (") {
		t.Fatalf("rows %+v findings %+v warns %v", rows, findings, warns)
	}
	if strings.Contains(warns[0], "mongo said no") {
		t.Fatalf("warning leaks the raw error: %q", warns[0])
	}
}

func TestRepeatsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := PatternInput{SourceIDs: []string{"s1"}, Start: patternT0, End: patternT0.Add(time.Hour)}
	a := rstall("a", 7, "Payroll.Run", 0, 60*time.Second)
	_, _, _, _, err := buildRepeats(ctx, newRepeatFake(), in, patternParams(in), repeatReport(false, a))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestRepeatsOneReadPerEpisodeAndSource(t *testing.T) {
	op := "Payroll.Run"
	a := rstall("a", 7, op, 0, 60*time.Second)
	b := rstall("b", 8, op, 5*time.Second, 60*time.Second)
	b.SourceID = "s2"
	rowB := rrow("b", 8, op, 5*time.Second, 2)
	rowB.SourceID = "s2"
	repeatB := rrow("rb", 8, op, 15*time.Second, 2)
	repeatB.SourceID = "s2"
	// User 8 on s1 at the same time is not a repeat of anything on s2.
	fake := newRepeatFake(rrow("a", 7, op, 0, 2), rowB, repeatB, rrow("x", 8, op, 15*time.Second, 2))
	rows, _, prov, _ := runRepeats(t, fake, repeatReport(false, a, b))
	if len(fake.calls) != 2 || len(prov) != 2 {
		t.Fatalf("calls %d prov %d", len(fake.calls), len(prov))
	}
	for _, c := range fake.calls {
		if len(c.Query.SourceIDs) != 1 {
			t.Fatalf("a read spans sources: %v", c.Query.SourceIDs)
		}
	}
	if len(rows) != 1 || rows[0].UserID != 8 || rows[0].Repeats != 1 {
		t.Fatalf("rows %+v", rows)
	}
}

// The demo and fixture reads of a non-live store are not hint reads.
func TestRepeatsNonLiveReadUsesDefaultMode(t *testing.T) {
	a := rstall("a", 7, "Payroll.Run", 0, 60*time.Second)
	fake := newRepeatFake(rrow("a", 7, "Payroll.Run", 0, 2))
	runRepeats(t, fake, repeatReport(false, a))
	if len(fake.calls) != 1 || fake.calls[0].Query.QueryMode == QueryModeHint {
		t.Fatalf("calls %+v", fake.calls)
	}
}
