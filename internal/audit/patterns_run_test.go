package audit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// patternFakeRepo is a Repository (no SlowestReader) for the unavailable case.
type patternFakeRepo struct {
	slow  []RequestRecord // returned by Slowest, filtered by source and MinMs only
	audit []RequestRecord // returned by Requests, filtered by source only
	// analyzeErr makes Analyze fail; quality is what it returns otherwise.
	analyzeErr error
	quality    Quality
	// results is Analysis.Sources; listed is what Sources returns.
	results []SourceResult
	listed  []Source

	slowCalls  []PageInput
	auditCalls []PageInput
}

// patternFakeSlow adds SlowestReader.
type patternFakeSlow struct{ *patternFakeRepo }

// patternFakeCoverage adds SlowestReader and CoverageProber.
type patternFakeCoverage struct {
	*patternFakeRepo
	coverageErr error
	total       CoverageRow
}

func (f *patternFakeRepo) Sources(context.Context) ([]Source, error) { return f.listed, nil }
func (f *patternFakeRepo) AddSource(context.Context, SourceInput) (Source, error) {
	return Source{}, nil
}
func (f *patternFakeRepo) DeleteSource(context.Context, string) error { return nil }
func (f *patternFakeRepo) Analyze(_ context.Context, q Query) (Analysis, error) {
	if err := ValidateQuery(q); err != nil {
		return Analysis{}, err
	}
	if f.analyzeErr != nil {
		return Analysis{}, f.analyzeErr
	}
	return Analysis{Quality: f.quality, Sources: f.results}, nil
}
func (f *patternFakeRepo) Payload(context.Context, PayloadInput) (Payload, error) {
	return Payload{}, nil
}
func (f *patternFakeRepo) Record(context.Context, RecordInput) (RecordDetail, error) {
	return RecordDetail{}, nil
}
func (f *patternFakeRepo) Close(context.Context) error { return nil }

// fakePage pages rows with an offset cursor.
func fakePage(rows []RequestRecord, input PageInput) (Page, error) {
	offset := 0
	if input.Cursor != "" {
		n, err := strconv.Atoi(input.Cursor)
		if err != nil {
			return Page{}, ErrInvalidInput
		}
		offset = n
	}
	page := Page{Rows: []RequestRecord{}, Provenance: []Provenance{{SourceID: input.Query.SourceIDs[0], Window: "page", DocsReturned: 1}}}
	end := offset + input.Limit
	if end > len(rows) {
		end = len(rows)
	}
	if offset < len(rows) {
		page.Rows = append(page.Rows, rows[offset:end]...)
	}
	if end < len(rows) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func (f *patternFakeRepo) Requests(_ context.Context, input PageInput) (Page, error) {
	if err := ValidateQuery(input.Query); err != nil {
		return Page{}, err
	}
	if input.Limit < 1 || input.Limit > MaxPageSize {
		return Page{}, ErrLimit
	}
	f.auditCalls = append(f.auditCalls, input)
	var rows []RequestRecord
	for _, r := range f.audit {
		if r.SourceID == input.Query.SourceIDs[0] {
			rows = append(rows, r)
		}
	}
	return fakePage(rows, input)
}

func (f patternFakeSlow) Slowest(_ context.Context, input PageInput) (Page, error) {
	if err := ValidateSlowest(input); err != nil {
		return Page{}, err
	}
	f.slowCalls = append(f.slowCalls, input)
	var rows []RequestRecord
	for _, r := range f.slow {
		if r.SourceID != input.Query.SourceIDs[0] {
			continue
		}
		if r.DurationMs != nil && input.MinMs != nil && *r.DurationMs < *input.MinMs {
			continue
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].DurationMs, rows[j].DurationMs
		return a != nil && (b == nil || *a > *b)
	})
	return fakePage(rows, input)
}

func (f patternFakeCoverage) Slowest(ctx context.Context, input PageInput) (Page, error) {
	return patternFakeSlow{f.patternFakeRepo}.Slowest(ctx, input)
}

func (f patternFakeCoverage) Coverage(_ context.Context, in CoverageInput) (CoverageReport, error) {
	if f.coverageErr != nil {
		return CoverageReport{}, f.coverageErr
	}
	return CoverageReport{SourceID: in.SourceID, Total: f.total}, nil
}

var patternT0 = time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)

func patternInput() PatternInput {
	return PatternInput{SourceIDs: []string{"s1"}, Start: patternT0, End: patternT0.Add(6 * time.Hour)}
}

// stallRow is a complete row on s1 starting at offset from patternT0.
func stallRow(id string, offset time.Duration, durationMs float64, user int) RequestRecord {
	start := patternT0.Add(offset)
	end := start.Add(time.Duration(durationMs * float64(time.Millisecond)))
	d := durationMs
	api := durationMs - 100
	u := user
	return RequestRecord{ID: id, SourceID: "s1", Controller: "Payroll", Action: "Run", URL: "/secret", UserIP: "203.0.113.9", UserAgent: "agent",
		InitiatedAt: &start, CompletedAt: &end, DurationMs: &d, ApiTimeMs: &api, UserID: &u}
}

func auditRow(id string, offset time.Duration, durationMs, auditMs float64) RequestRecord {
	r := stallRow(id, offset, durationMs, 1)
	a := auditMs
	r.AuditCreateTimeMs = &a
	return r
}

func runFake(t *testing.T, repo *patternFakeRepo, in PatternInput) PatternReport {
	t.Helper()
	report, err := RunPatterns(context.Background(), patternFakeSlow{repo}, in)
	if err != nil {
		t.Fatalf("RunPatterns: %v", err)
	}
	return report
}

func findingByRule(r PatternReport, rule string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].Rule == rule {
			return &r.Findings[i]
		}
	}
	return nil
}

func TestRunPatternsRecurrence(t *testing.T) {
	var fortyThree []RequestRecord
	for i := 0; i < 43; i++ {
		fortyThree = append(fortyThree, stallRow(fmt.Sprintf("r%02d", i), time.Duration(i)*10*time.Second, 50000, 100+i))
	}
	cases := []struct {
		name       string
		rows       []RequestRecord
		recurrence string
		episodes   int
		headline   string // B2 headline must contain this
		notContain string
		lowN       bool
	}{
		{"three independent stalls", []RequestRecord{
			stallRow("a", 0, 50000, 1), stallRow("b", 30*time.Minute, 60000, 2), stallRow("c", 90*time.Minute, 47000, 3),
		}, RecurrenceRecurring, 3, "Recurring: 3 separate episodes, 3 requests in all.", "", true},
		{"one episode of 43 requests", fortyThree, RecurrenceSingleEpisode, 1, "single episode, 43 requests", "43 stalls", true},
		{"two episodes", []RequestRecord{stallRow("a", 0, 50000, 1), stallRow("b", time.Hour, 50000, 2)},
			RecurrenceTwoEpisodes, 2, "Two separate episodes, 2 requests in all.", "Recurring", true},
		{"one stall", []RequestRecord{stallRow("a", 0, 50000, 1)}, RecurrenceSingleEpisode, 1, "single episode, 1 request.", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runFake(t, &patternFakeRepo{slow: tc.rows}, patternInput())
			if r.Recurrence != tc.recurrence || len(r.Episodes) != tc.episodes {
				t.Fatalf("recurrence %q episodes %d, want %q %d", r.Recurrence, len(r.Episodes), tc.recurrence, tc.episodes)
			}
			b2 := findingByRule(r, "B2")
			if b2 == nil {
				t.Fatal("no B2 finding")
			}
			if !strings.Contains(b2.Headline, tc.headline) || (tc.notContain != "" && strings.Contains(b2.Headline, tc.notContain)) {
				t.Fatalf("B2 headline %q, want %q and not %q", b2.Headline, tc.headline, tc.notContain)
			}
			if b2.LowN != tc.lowN || b2.Label != EvidenceSymptom || b2.N != len(tc.rows) {
				t.Fatalf("B2 finding %+v", *b2)
			}
			b1 := findingByRule(r, "B1")
			if b1 == nil || b1.N != len(tc.rows) || b1.Label != EvidenceSymptom || b1.LowN != (len(tc.rows) < LowNRows) {
				t.Fatalf("B1 finding %+v", b1)
			}
			total := 0
			for _, e := range r.Episodes {
				total += e.Requests
			}
			if total != len(tc.rows) || len(r.Stalls) != len(tc.rows) {
				t.Fatalf("episode requests %d stalls %d, want %d", total, len(r.Stalls), len(tc.rows))
			}
		})
	}
}

func TestRunPatternsSingleEpisodeDetail(t *testing.T) {
	var rows []RequestRecord
	for i := 0; i < 43; i++ {
		rows = append(rows, stallRow(fmt.Sprintf("r%02d", i), time.Duration(i)*10*time.Second, 50000+float64(i), 100+i%5))
	}
	r := runFake(t, &patternFakeRepo{slow: rows}, patternInput())
	e := r.Episodes[0]
	if e.Index != 1 || e.Requests != 43 || e.Users != 5 || e.MaxMs != 50042 || !e.Start.Equal(patternT0) {
		t.Fatalf("episode %+v", e)
	}
	if want := patternT0.Add(420*time.Second + 50042*time.Millisecond); !e.End.Equal(want) {
		t.Fatalf("episode end %v, want %v", e.End, want)
	}
	if len(e.Operations) != 1 || e.Operations[0] != "Payroll.Run" {
		t.Fatalf("operations %v", e.Operations)
	}
	for _, s := range r.Stalls {
		if s.Episode != 1 {
			t.Fatalf("stall %s episode %d", s.ID, s.Episode)
		}
	}
	for _, f := range r.Findings {
		if strings.Contains(f.Headline, "43 stalls") {
			t.Fatalf("headline counts stalls as events: %q", f.Headline)
		}
	}
}

func TestRunPatternsEpisodeGapBoundary(t *testing.T) {
	gap := time.Duration(DefaultEpisodeGapSeconds) * time.Second
	firstEnd := 50 * time.Second // stall a: start 0, 50 s
	noEnd := func(r RequestRecord) RequestRecord { r.CompletedAt = nil; return r }
	cases := []struct {
		name     string
		rows     []RequestRecord
		in       func(PatternInput) PatternInput
		episodes int
	}{
		{"exactly the gap after the last end merges", []RequestRecord{stallRow("a", 0, 50000, 1), stallRow("b", firstEnd+gap, 50000, 2)}, nil, 1},
		{"one ms past the gap splits", []RequestRecord{stallRow("a", 0, 50000, 1), stallRow("b", firstEnd+gap+time.Millisecond, 50000, 2)}, nil, 2},
		{"gap counts from the latest end, not the last start", []RequestRecord{
			stallRow("a", 0, 900000, 1), stallRow("b", 10*time.Second, 50000, 2), stallRow("c", 900*time.Second+gap, 50000, 3),
		}, nil, 1},
		{"no completion: end is the start", []RequestRecord{noEnd(stallRow("a", 0, 50000, 1)), stallRow("b", gap, 50000, 2)}, nil, 1},
		{"no completion: past the gap from the start splits", []RequestRecord{noEnd(stallRow("a", 0, 50000, 1)), stallRow("b", gap+time.Millisecond, 50000, 2)}, nil, 2},
		{"gap override", []RequestRecord{stallRow("a", 0, 50000, 1), stallRow("b", firstEnd+61*time.Second, 50000, 2)},
			func(in PatternInput) PatternInput { in.EpisodeGapSeconds = 60; return in }, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := patternInput()
			if tc.in != nil {
				in = tc.in(in)
			}
			r := runFake(t, &patternFakeRepo{slow: tc.rows}, in)
			if len(r.Episodes) != tc.episodes {
				t.Fatalf("episodes %d, want %d: %+v", len(r.Episodes), tc.episodes, r.Episodes)
			}
		})
	}
}

func TestRunPatternsVolley(t *testing.T) {
	ms := time.Millisecond
	noEnd := func(r RequestRecord) RequestRecord { r.CompletedAt = nil; return r }
	cases := []struct {
		name    string
		rows    []RequestRecord
		volleys []Volley
	}{
		{"three requests, end stagger", []RequestRecord{
			stallRow("a", 0, 60000, 7), stallRow("b", 300*ms, 64100, 7), stallRow("c", 700*ms, 68000, 7),
			stallRow("other-user", 500*ms, 50000, 8),
		}, []Volley{{Episode: 1, UserID: 7, Requests: 3, FirstStart: patternT0, StartSpreadMs: 700, EndStaggerMs: 8700, EndGapsMs: []int64{4400, 4300}}}},
		{"window boundary is inclusive", []RequestRecord{stallRow("a", 0, 50000, 7), stallRow("b", 1000*ms, 50000, 7)},
			[]Volley{{Episode: 1, UserID: 7, Requests: 2, FirstStart: patternT0, StartSpreadMs: 1000, EndStaggerMs: 1000, EndGapsMs: []int64{1000}}}},
		{"one ms past the window is no volley", []RequestRecord{stallRow("a", 0, 50000, 7), stallRow("b", 1001*ms, 50000, 7)}, []Volley{}},
		{"different users are no volley", []RequestRecord{stallRow("a", 0, 50000, 7), stallRow("b", 10*ms, 50000, 8)}, []Volley{}},
		{"missing user is no volley", func() []RequestRecord {
			a, b := stallRow("a", 0, 50000, 7), stallRow("b", 10*ms, 50000, 7)
			a.UserID, b.UserID = nil, nil
			return []RequestRecord{a, b}
		}(), []Volley{}},
		{"incomplete end", []RequestRecord{stallRow("a", 0, 50000, 7), noEnd(stallRow("b", 200*ms, 50000, 7))},
			[]Volley{{Episode: 1, UserID: 7, Requests: 2, FirstStart: patternT0, StartSpreadMs: 200, EndGapsMs: []int64{}, IncompleteEnds: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runFake(t, &patternFakeRepo{slow: tc.rows}, patternInput())
			if fmt.Sprintf("%+v", r.Volleys) != fmt.Sprintf("%+v", tc.volleys) {
				t.Fatalf("volleys %+v\nwant %+v", r.Volleys, tc.volleys)
			}
			if len(r.Episodes) != 1 {
				t.Fatalf("a volley is one episode, got %d", len(r.Episodes))
			}
			b12 := findingByRule(r, "B12")
			if len(tc.volleys) == 0 {
				if b12 != nil {
					t.Fatalf("unexpected B12 %+v", *b12)
				}
				return
			}
			if b12 == nil || b12.Label != EvidenceCorrelation || b12.N != tc.volleys[0].Requests || !b12.LowN {
				t.Fatalf("B12 finding %+v", b12)
			}
		})
	}
	r := runFake(t, &patternFakeRepo{slow: cases[0].rows}, patternInput())
	if h := findingByRule(r, "B12").Headline; h != "One user started 3 requests of 45 s or longer within 1 s of each other. Their end times spread over 8.7 s." {
		t.Fatalf("B12 headline %q", h)
	}
}

func TestRunPatternsAuditStallEdges(t *testing.T) {
	cases := []struct {
		name            string
		durationMs      float64
		auditMs         float64
		want            bool
		missing         bool
		wantShare       float64
		wantAuditString string
	}{
		{"4999 ms is below", 6000, 4999, false, false, 0, ""},
		{"5000 ms meets", 6000, 5000, true, false, 5000.0 / 6000, "5"},
		{"share 0.29 is below", 20000, 5800, false, false, 0, ""},
		{"share 0.3 meets", 20000, 6000, true, false, 0.3, "6"},
		{"no duration is counted, not dropped", -1, 6000, false, true, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := auditRow("x", time.Minute, tc.durationMs, tc.auditMs)
			if tc.durationMs < 0 {
				row.DurationMs = nil
			}
			repo := &patternFakeRepo{audit: []RequestRecord{row}}
			r := runFake(t, repo, patternInput())
			if got := len(r.AuditStalls) == 1; got != tc.want {
				t.Fatalf("audit stall %v, want %v (%+v)", got, tc.want, r.AuditStalls)
			}
			hasWarning := false
			for _, w := range r.Warnings {
				hasWarning = hasWarning || strings.Contains(w, "audit-create rows had no")
			}
			if hasWarning != tc.missing {
				t.Fatalf("missing warning %v, want %v: %v", hasWarning, tc.missing, r.Warnings)
			}
			if len(repo.auditCalls) != 1 || repo.auditCalls[0].Limit != MaxPageSize || repo.auditCalls[0].Order != OrderNewest {
				t.Fatalf("audit calls %+v", repo.auditCalls)
			}
			if rg := repo.auditCalls[0].Query.AuditCreateTimeMs; rg == nil || rg.Min == nil || *rg.Min != DefaultAuditStallMs || rg.Max != nil {
				t.Fatalf("audit range %+v", rg)
			}
			b6 := findingByRule(r, "B6")
			if !tc.want {
				if b6 != nil {
					t.Fatalf("unexpected B6 %+v", *b6)
				}
				return
			}
			if math.Abs(r.AuditStalls[0].Share-tc.wantShare) > 1e-12 {
				t.Fatalf("share %v, want %v", r.AuditStalls[0].Share, tc.wantShare)
			}
			want := "Audit create took " + tc.wantAuditString + " s as measured from the client side."
			if b6 == nil || b6.Headline != want || b6.Label != EvidenceSymptom || b6.N != 1 || !b6.LowN {
				t.Fatalf("B6 finding %+v, want headline %q", b6, want)
			}
		})
	}
	// Several rows: the exact wording first, then the plain count.
	repo := &patternFakeRepo{audit: []RequestRecord{auditRow("a", time.Minute, 10000, 7240), auditRow("b", 2*time.Minute, 10000, 5000)}}
	b6 := findingByRule(runFake(t, repo, patternInput()), "B6")
	if want := "Audit create took 7.2 s as measured from the client side. That is the longest of 2 requests at or above 5 s and 30% of observed time."; b6 == nil || b6.Headline != want {
		t.Fatalf("B6 headline %+v, want %q", b6, want)
	}
}

func TestRunPatternsZeroStalls(t *testing.T) {
	r := runFake(t, &patternFakeRepo{}, patternInput())
	if r.Recurrence != RecurrenceNone || len(r.Findings) != 0 || r.Stalls == nil || r.Episodes == nil || r.Volleys == nil || r.AuditStalls == nil || r.Warnings == nil || r.Truncated {
		t.Fatalf("report %+v", r)
	}
	if r.Params != (PatternParams{StallMs: 45000, EpisodeGapSeconds: 600, AuditStallMs: 5000, AuditStallShare: 0.3, VolleyWindowMs: 1000}) {
		t.Fatalf("params %+v", r.Params)
	}
}

func TestRunPatternsNotSlowestReader(t *testing.T) {
	_, err := RunPatterns(context.Background(), &patternFakeRepo{}, patternInput())
	if !errors.Is(err, ErrPatternsUnavailable) || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err %v", err)
	}
	if _, err := RunPatterns(context.Background(), nil, patternInput()); !errors.Is(err, ErrPatternsUnavailable) {
		t.Fatalf("nil repo err %v", err)
	}
}

func TestRunPatternsTruncation(t *testing.T) {
	cases := []struct {
		name      string
		rows      int
		truncated bool
	}{
		{"at the cap", MaxPatternRows, false},
		{"over the cap", MaxPatternRows + 20, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rows []RequestRecord
			for i := 0; i < tc.rows; i++ {
				rows = append(rows, stallRow(fmt.Sprintf("r%04d", i), time.Duration(i)*time.Second, 50000+float64(i), i))
			}
			repo := &patternFakeRepo{slow: rows}
			r := runFake(t, repo, patternInput())
			if r.Truncated != tc.truncated || r.StallsTruncated != tc.truncated || r.AuditsTruncated || len(r.Stalls) != MaxPatternRows {
				t.Fatalf("truncated %v stalls truncated %v audits truncated %v stalls %d", r.Truncated, r.StallsTruncated, r.AuditsTruncated, len(r.Stalls))
			}
			b1 := findingByRule(r, "B1")
			if strings.Contains(b1.Headline, "(at least; capped at 500 rows per source)") != tc.truncated {
				t.Fatalf("B1 headline %q", b1.Headline)
			}
			for _, c := range repo.slowCalls {
				if c.Limit != MaxSlowestPageSize || c.Query.Metric != "total" || c.MinMs == nil || *c.MinMs != DefaultStallMs {
					t.Fatalf("slowest call %+v", c)
				}
			}
			if len(repo.slowCalls) != MaxPatternRows/MaxSlowestPageSize {
				t.Fatalf("slowest calls %d", len(repo.slowCalls))
			}
			partial := strings.Contains(strings.Join(r.Warnings, " "), "episode count is partial")
			b2 := findingByRule(r, "B2")
			if partial != tc.truncated || strings.Contains(b2.Headline, "partial") != tc.truncated {
				t.Fatalf("partial warning %v headline %q", r.Warnings, b2.Headline)
			}
		})
	}
}

// An audit-side cut must not mark the stall-based episode count as partial,
// and it must mark the B6 figure as a lower bound.
func TestRunPatternsAuditTruncationIsSeparate(t *testing.T) {
	var audits []RequestRecord
	for i := 0; i < MaxPatternRows+5; i++ {
		audits = append(audits, auditRow(fmt.Sprintf("a%04d", i), time.Duration(i)*time.Second, 10000, 7000))
	}
	repo := &patternFakeRepo{slow: []RequestRecord{stallRow("s", 0, 50000, 1)}, audit: audits}
	r := runFake(t, repo, patternInput())
	if !r.Truncated || !r.AuditsTruncated || r.StallsTruncated {
		t.Fatalf("truncated %v audits %v stalls %v", r.Truncated, r.AuditsTruncated, r.StallsTruncated)
	}
	if b2 := findingByRule(r, "B2"); b2 == nil || strings.Contains(b2.Headline, "partial") {
		t.Fatalf("B2 headline %+v", b2)
	}
	if b1 := findingByRule(r, "B1"); b1 == nil || strings.Contains(b1.Headline, "at least") {
		t.Fatalf("B1 headline %+v", b1)
	}
	warnings := strings.Join(r.Warnings, "\n")
	if strings.Contains(warnings, "episode count is partial") || !strings.Contains(warnings, "audit-create rows per source were read") {
		t.Fatalf("warnings %v", r.Warnings)
	}
	b6 := findingByRule(r, "B6")
	if b6 == nil || !strings.HasPrefix(b6.Headline, "Audit create took 7 s as measured from the client side.") ||
		!strings.HasSuffix(b6.Headline, " Partial count (at least; capped at 500 rows per source).") {
		t.Fatalf("B6 headline %+v", b6)
	}
}

func TestRunPatternsWarnsForSourceWithoutQuality(t *testing.T) {
	repo := &patternFakeRepo{
		results: []SourceResult{{SourceID: "s1", OK: true}, {SourceID: "s2", OK: false}, {SourceID: "s3", OK: false}},
		listed:  []Source{{ID: "s2", Label: "West payroll"}},
	}
	in := patternInput()
	in.SourceIDs = []string{"s1", "s2", "s3"}
	r := runFake(t, repo, in)
	warnings := strings.Join(r.Warnings, "\n")
	if !strings.Contains(warnings, "Quality counts are missing for source West payroll.") ||
		!strings.Contains(warnings, "Quality counts are missing for source s3.") ||
		strings.Contains(warnings, "source s1.") {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestRunPatternsMissingRowGrammar(t *testing.T) {
	noStart := stallRow("no-start", 0, 50000, 1)
	noStart.InitiatedAt = nil
	r := runFake(t, &patternFakeRepo{slow: []RequestRecord{noStart}}, patternInput())
	if warnings := strings.Join(r.Warnings, "\n"); !strings.Contains(warnings, "1 long request row had no start time") {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestRunPatternsMissingTimestamps(t *testing.T) {
	noStart := stallRow("no-start", 0, 50000, 1)
	noStart.InitiatedAt = nil
	noDuration := stallRow("no-duration", time.Minute, 50000, 1)
	noDuration.DurationMs = nil
	noEnd := stallRow("no-end", 2*time.Minute, 50000, 2)
	noEnd.CompletedAt = nil
	r := runFake(t, &patternFakeRepo{slow: []RequestRecord{noStart, noDuration, noEnd, stallRow("ok", 3*time.Minute, 50000, 3)}}, patternInput())
	if len(r.Stalls) != 2 {
		t.Fatalf("stalls %+v", r.Stalls)
	}
	warnings := strings.Join(r.Warnings, "\n")
	if !strings.Contains(warnings, "2 long request rows had no start time or no duration") || !strings.Contains(warnings, "1 stall rows have no end time") {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestRunPatternsStallsCarryNoClientData(t *testing.T) {
	r := runFake(t, &patternFakeRepo{slow: []RequestRecord{stallRow("a", 0, 50000, 1)}}, patternInput())
	s := fmt.Sprintf("%+v", r.Stalls)
	for _, leak := range []string{"/secret", "203.0.113.9", "agent"} {
		if strings.Contains(s, leak) {
			t.Fatalf("stall carries %q: %s", leak, s)
		}
	}
}

func TestRunPatternsPerSourceAndProvenance(t *testing.T) {
	a := stallRow("a", 0, 50000, 1)
	b := stallRow("b", time.Hour, 50000, 2)
	b.SourceID = "s2"
	repo := &patternFakeRepo{slow: []RequestRecord{a, b}}
	in := patternInput()
	in.SourceIDs = []string{"s1", "s2"}
	r := runFake(t, repo, in)
	if len(r.Stalls) != 2 || len(repo.slowCalls) != 2 || len(repo.auditCalls) != 2 {
		t.Fatalf("stalls %d slow calls %d audit calls %d", len(r.Stalls), len(repo.slowCalls), len(repo.auditCalls))
	}
	for i, c := range append(repo.slowCalls, repo.auditCalls...) {
		if len(c.Query.SourceIDs) != 1 || c.Query.SourceIDs[0] != in.SourceIDs[i%2] {
			t.Fatalf("call %d reads %v, want one source at a time", i, c.Query.SourceIDs)
		}
	}
	if len(r.Provenance) != 4 {
		t.Fatalf("provenance %+v", r.Provenance)
	}
}

func TestRunPatternsEvidenceID(t *testing.T) {
	repo := &patternFakeRepo{slow: []RequestRecord{stallRow("row-id-1", 0, 50000, 1)}}
	first, second := runFake(t, repo, patternInput()), runFake(t, repo, patternInput())
	for i, f := range first.Findings {
		if f.EvidenceID != second.Findings[i].EvidenceID || !strings.HasPrefix(f.EvidenceID, "p2:"+strings.ToLower(f.Rule)+":") || strings.Contains(f.EvidenceID, "row-id-1") {
			t.Fatalf("evidence id %q / %q", f.EvidenceID, second.Findings[i].EvidenceID)
		}
	}
	other := patternInput()
	other.End = other.End.Add(time.Hour)
	if findingByRule(runFake(t, repo, other), "B1").EvidenceID == findingByRule(first, "B1").EvidenceID {
		t.Fatal("evidence id ignores the window")
	}
}

func TestRunPatternsQualityAndCoverage(t *testing.T) {
	t.Run("quality counts", func(t *testing.T) {
		r := runFake(t, &patternFakeRepo{quality: Quality{Incomplete: 9, PendingAsync: 4, Invalid: 2}}, patternInput())
		if r.Quality != (PatternQuality{Incomplete: 5, Pending: 4, Invalid: 2}) {
			t.Fatalf("quality %+v", r.Quality)
		}
		if r.Coverage == nil || r.Coverage.Run || r.Coverage.Note == "" {
			t.Fatalf("coverage %+v", r.Coverage)
		}
	})
	t.Run("analyze failure is a warning", func(t *testing.T) {
		r := runFake(t, &patternFakeRepo{analyzeErr: fmt.Errorf("%w: driver text", ErrLimit)}, patternInput())
		w := strings.Join(r.Warnings, "\n")
		if !strings.Contains(w, "could not be counted") || strings.Contains(w, "driver text") {
			t.Fatalf("warnings %v", r.Warnings)
		}
	})
	cases := []struct {
		name string
		err  error
		run  bool
		note string
	}{
		{"broad scan refused", fmt.Errorf("%w: live", ErrBroadScanRefused), false, "Run Coverage first"},
		{"other error", errors.New("socket text"), false, "Run Coverage first"},
		{"runs", nil, true, "Coverage over the same window"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := patternFakeCoverage{patternFakeRepo: &patternFakeRepo{}, coverageErr: tc.err,
				total: CoverageRow{Kind: "all", Docs: 200, RequestInitiatedAt: FieldCoverage{NonNull: 190}, AccountID: FieldCoverage{NonNull: 50}}}
			r, err := RunPatterns(context.Background(), repo, patternInput())
			if err != nil {
				t.Fatal(err)
			}
			c := r.Coverage
			if c == nil || c.Run != tc.run || !strings.Contains(c.Note, tc.note) || strings.Contains(c.Note, "socket text") {
				t.Fatalf("coverage %+v", c)
			}
			if tc.run && (c.RequestInitiatedAtPct == nil || *c.RequestInitiatedAtPct != 95 || c.AccountIDPct == nil || *c.AccountIDPct != 25) {
				t.Fatalf("coverage pct %+v", c)
			}
		})
	}
}

func TestValidatePattern(t *testing.T) {
	nine := make([]string, MaxSources+1)
	for i := range nine {
		nine[i] = fmt.Sprintf("s%d", i)
	}
	cases := []struct {
		name string
		edit func(*PatternInput)
		ok   bool
	}{
		{"valid", func(*PatternInput) {}, true},
		{"seven days", func(in *PatternInput) { in.End = in.Start.Add(MaxInterval) }, true},
		{"overrides", func(in *PatternInput) { in.StallMs, in.EpisodeGapSeconds = 30000, 300 }, true},
		{"no sources", func(in *PatternInput) { in.SourceIDs = nil }, false},
		{"too many sources", func(in *PatternInput) { in.SourceIDs = nine }, false},
		{"blank source", func(in *PatternInput) { in.SourceIDs = []string{" "} }, false},
		{"duplicate source", func(in *PatternInput) { in.SourceIDs = []string{"s1", "s1"} }, false},
		{"end equals start", func(in *PatternInput) { in.End = in.Start }, false},
		{"end before start", func(in *PatternInput) { in.End = in.Start.Add(-time.Second) }, false},
		{"zero start", func(in *PatternInput) { in.Start = time.Time{} }, false},
		{"over seven days", func(in *PatternInput) { in.End = in.Start.Add(MaxInterval + time.Second) }, false},
		{"negative stall", func(in *PatternInput) { in.StallMs = -1 }, false},
		{"NaN stall", func(in *PatternInput) { in.StallMs = math.NaN() }, false},
		{"infinite stall", func(in *PatternInput) { in.StallMs = math.Inf(1) }, false},
		{"negative gap", func(in *PatternInput) { in.EpisodeGapSeconds = -1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := patternInput()
			tc.edit(&in)
			err := ValidatePattern(in)
			if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalidInput)) {
				t.Fatalf("err %v, ok %v", err, tc.ok)
			}
			if !tc.ok {
				if _, err := RunPatterns(context.Background(), patternFakeSlow{&patternFakeRepo{}}, in); !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("RunPatterns err %v", err)
				}
			}
		})
	}
}

func TestRunPatternsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunPatterns(ctx, patternFakeSlow{&patternFakeRepo{}}, patternInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}
