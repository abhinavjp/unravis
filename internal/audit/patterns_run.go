package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrPatternsUnavailable is returned (wrapped with ErrInvalidInput) when the
// repository cannot rank requests by duration, so the stall rules cannot run.
// A server may map it to "unavailable" as it does for the slowest list.
var ErrPatternsUnavailable = errors.New("incident patterns are unavailable for this repository")

const (
	// patternStallPage is one page of the slowest ranking.
	patternStallPage = MaxSlowestPageSize
	// patternAuditPage is one page of the request list.
	patternAuditPage = MaxPageSize
	// maxEpisodeGapSeconds bounds the episode gap override at the longest window.
	maxEpisodeGapSeconds = int(MaxInterval / time.Second)
	// maxPatternOperations is how many operations an episode lists.
	maxPatternOperations = 5
)

// ValidatePattern checks a PatternInput: 1 to MaxSources distinct, non-blank
// sources, a window of more than zero and at most MaxInterval, and overrides
// that are finite and not negative (zero means the default).
func ValidatePattern(in PatternInput) error {
	if len(in.SourceIDs) == 0 || len(in.SourceIDs) > MaxSources {
		return invalidInputf("sourceIds must contain 1 to %d sources", MaxSources)
	}
	seen := make(map[string]struct{}, len(in.SourceIDs))
	for _, id := range in.SourceIDs {
		if strings.TrimSpace(id) == "" {
			return invalidInputf("sourceIds must not contain blank values")
		}
		if utf8.RuneCountInString(id) > MaxFilterLength {
			return invalidInputf("sourceIds must be at most %d characters", MaxFilterLength)
		}
		if _, ok := seen[id]; ok {
			return invalidInputf("sourceIds must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	if in.Start.IsZero() || in.End.IsZero() {
		return invalidInputf("start and end are required")
	}
	if err := validateInterval(in.Start, in.End, "pattern"); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if s := in.StallMs; math.IsNaN(s) || math.IsInf(s, 0) || s < 0 || s > MaxRangeMs {
		return invalidInputf("stallMs must be a number from 0 to %g", float64(MaxRangeMs))
	}
	if g := in.EpisodeGapSeconds; g < 0 || g > maxEpisodeGapSeconds {
		return invalidInputf("episodeGapSeconds must be from 0 to %d", maxEpisodeGapSeconds)
	}
	return nil
}

// patternParams resolves the thresholds actually used.
func patternParams(in PatternInput) PatternParams {
	p := PatternParams{
		StallMs:           DefaultStallMs,
		EpisodeGapSeconds: DefaultEpisodeGapSeconds,
		AuditStallMs:      DefaultAuditStallMs,
		AuditStallShare:   DefaultAuditStallShare,
		VolleyWindowMs:    DefaultVolleyWindowMs,
	}
	if in.StallMs > 0 {
		p.StallMs = in.StallMs
	}
	if in.EpisodeGapSeconds > 0 {
		p.EpisodeGapSeconds = in.EpisodeGapSeconds
	}
	return p
}

// patternQuery is the read query for one window: every kind and outcome, total
// time, and a one-bucket reference window just before the start. The request
// lists ignore the reference window; Analyze needs a valid one, and a short one
// costs little.
func patternQuery(in PatternInput, sourceIDs []string) Query {
	bucket := int(math.Ceil(in.End.Sub(in.Start).Seconds() / 100))
	if bucket < 10 {
		bucket = 10
	}
	if bucket > 86400 {
		bucket = 86400
	}
	return Query{
		SourceIDs:     append([]string(nil), sourceIDs...),
		Start:         in.Start,
		End:           in.End,
		BaselineStart: in.Start.Add(-time.Duration(bucket) * time.Second),
		BaselineEnd:   in.Start,
		Kind:          "all",
		Outcome:       "all",
		Metric:        "total",
		BucketSeconds: bucket,
	}
}

// RunPatterns runs rules B1, B2, B6 and B12 over the window. It reads through
// SlowestReader (stalls) and Repository.Requests (audit-create stalls), one
// source at a time, at most MaxPatternRows rows per source and rule.
func RunPatterns(ctx context.Context, repo Repository, in PatternInput) (PatternReport, error) {
	if err := ValidatePattern(in); err != nil {
		return PatternReport{}, err
	}
	reader, ok := repo.(SlowestReader)
	if repo == nil || !ok {
		return PatternReport{}, fmt.Errorf("%w: %w", ErrInvalidInput, ErrPatternsUnavailable)
	}
	if err := ValidateQuery(patternQuery(in, in.SourceIDs)); err != nil {
		return PatternReport{}, err
	}
	params := patternParams(in)
	report := PatternReport{
		Params:      params,
		Stalls:      []Stall{},
		Episodes:    []Episode{},
		Volleys:     []Volley{},
		AuditStalls: []AuditStall{},
		Findings:    []Finding{},
		Warnings:    []string{},
	}

	// B1: stalls, read source by source (concurrency 1).
	var stallRows []RequestRecord
	stallsCut, missingStall := false, 0
	for _, src := range in.SourceIDs {
		rows, cut, prov, err := readPatternStalls(ctx, reader, patternQuery(in, []string{src}), params.StallMs)
		if err != nil {
			return PatternReport{}, err
		}
		report.Provenance = append(report.Provenance, prov...)
		stallsCut = stallsCut || cut
		for _, r := range rows {
			if r.InitiatedAt == nil || !usableMs(r.DurationMs) {
				missingStall++
				continue
			}
			if *r.DurationMs < params.StallMs {
				continue
			}
			stallRows = append(stallRows, r)
		}
	}
	report.Stalls = buildStalls(stallRows)

	// B2 and B12.
	report.Episodes = buildEpisodes(report.Stalls, time.Duration(params.EpisodeGapSeconds)*time.Second)
	report.Volleys = buildVolleys(report.Stalls, time.Duration(params.VolleyWindowMs)*time.Millisecond)
	report.Recurrence = recurrenceOf(len(report.Episodes))

	// B6: audit-create stalls, source by source.
	auditCut, missingAudit := false, 0
	for _, src := range in.SourceIDs {
		q := patternQuery(in, []string{src})
		min := params.AuditStallMs
		q.AuditCreateTimeMs = &Range{Min: &min}
		rows, cut, prov, err := readPatternAuditRows(ctx, repo, q)
		if err != nil {
			return PatternReport{}, err
		}
		report.Provenance = append(report.Provenance, prov...)
		auditCut = auditCut || cut
		for _, r := range rows {
			a, ok, missing := auditStallOf(r, params)
			if missing {
				missingAudit++
				continue
			}
			if ok {
				report.AuditStalls = append(report.AuditStalls, a)
			}
		}
	}
	sort.SliceStable(report.AuditStalls, func(i, j int) bool {
		a, b := report.AuditStalls[i], report.AuditStalls[j]
		if !a.InitiatedAt.Equal(b.InitiatedAt) {
			return a.InitiatedAt.Before(b.InitiatedAt)
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.ID < b.ID
	})

	report.Truncated = stallsCut || auditCut
	if stallsCut {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Only the %d longest requests per source were read. The episode count is partial.", MaxPatternRows))
	}
	if auditCut {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Only the %d newest audit-create rows per source were read. That count is partial.", MaxPatternRows))
	}
	if missingStall > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d long request rows had no start time or no duration. They are counted here and left out of the stall list.", missingStall))
	}
	if missingAudit > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d audit-create rows had no start time, duration or audit time. They are counted here and left out.", missingAudit))
	}
	if n := incompleteEnds(report.Stalls); n > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d stall rows have no end time. Their episode end uses the start time.", n))
	}

	// Quality counts over the same window.
	if analysis, err := repo.Analyze(ctx, patternQuery(in, in.SourceIDs)); err != nil {
		if ctx.Err() != nil {
			return PatternReport{}, ctx.Err()
		}
		report.Warnings = append(report.Warnings, "Incomplete, pending and invalid rows could not be counted for this window ("+plainReason(err)+").")
	} else {
		q := analysis.Quality
		incomplete := q.Incomplete - q.PendingAsync // Quality.Incomplete includes pending work
		if incomplete < 0 {
			incomplete = 0
		}
		report.Quality = PatternQuality{Incomplete: incomplete, Pending: q.PendingAsync, Invalid: q.Invalid}
	}

	coverage, err := patternCoverage(ctx, repo, in)
	if err != nil {
		return PatternReport{}, err
	}
	report.Coverage = coverage

	report.Findings = buildFindings(in, params, report)
	return report, nil
}

// readPatternStalls pages the slowest ranking for one source down to stallMs,
// up to MaxPatternRows rows. cut is true when more rows were left unread.
func readPatternStalls(ctx context.Context, reader SlowestReader, q Query, stallMs float64) ([]RequestRecord, bool, []Provenance, error) {
	min := stallMs
	input := PageInput{Query: q, Limit: patternStallPage, MinMs: &min}
	return readPatternPages(ctx, input, reader.Slowest)
}

// readPatternAuditRows pages the newest request list for one source, up to
// MaxPatternRows rows. cut is true when more rows were left unread.
func readPatternAuditRows(ctx context.Context, repo Repository, q Query) ([]RequestRecord, bool, []Provenance, error) {
	input := PageInput{Query: q, Limit: patternAuditPage, Order: OrderNewest}
	return readPatternPages(ctx, input, repo.Requests)
}

// readPatternPages follows NextCursor until the list ends or MaxPatternRows
// rows are read. cut is true when the cap left rows unread.
func readPatternPages(ctx context.Context, input PageInput, read func(context.Context, PageInput) (Page, error)) (rows []RequestRecord, cut bool, prov []Provenance, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, nil, err
		}
		page, err := read(ctx, input)
		if err != nil {
			return nil, false, nil, err
		}
		prov = append(prov, page.Provenance...)
		rows = append(rows, page.Rows...)
		if len(rows) >= MaxPatternRows {
			cut = len(rows) > MaxPatternRows || page.NextCursor != ""
			return rows[:MaxPatternRows], cut, prov, nil
		}
		if page.NextCursor == "" || len(page.Rows) == 0 {
			return rows, false, prov, nil
		}
		input.Cursor = page.NextCursor
	}
}

// auditStallOf applies rule B6 to one row. missing is true when the row lacks a
// usable start, duration or audit time.
func auditStallOf(r RequestRecord, p PatternParams) (a AuditStall, ok, missing bool) {
	if r.InitiatedAt == nil || !usableMs(r.DurationMs) || !usableMs(r.AuditCreateTimeMs) || *r.DurationMs <= 0 {
		return AuditStall{}, false, true
	}
	audit, observed := *r.AuditCreateTimeMs, *r.DurationMs
	share := audit / observed
	if audit < p.AuditStallMs || share < p.AuditStallShare {
		return AuditStall{}, false, false
	}
	return AuditStall{
		SourceID: r.SourceID, ID: r.ID, Controller: r.Controller, Action: r.Action,
		InitiatedAt: r.InitiatedAt.UTC(), ObservedMs: observed, AuditCreateTimeMs: audit, Share: share,
	}, true, false
}

func usableMs(v *float64) bool {
	return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0
}

// buildStalls copies the stall rows without URL, IP or user agent, ordered by
// start, then source, then ID.
func buildStalls(rows []RequestRecord) []Stall {
	stalls := make([]Stall, 0, len(rows))
	seen := make(map[[2]string]bool, len(rows))
	for _, r := range rows {
		key := [2]string{r.SourceID, r.ID}
		if seen[key] {
			continue
		}
		seen[key] = true
		s := Stall{
			SourceID: r.SourceID, ID: r.ID, Controller: r.Controller, Action: r.Action,
			InitiatedAt: r.InitiatedAt.UTC(), ObservedMs: *r.DurationMs,
		}
		if r.CompletedAt != nil {
			c := r.CompletedAt.UTC()
			s.CompletedAt = &c
		}
		if r.ApiTimeMs != nil {
			v := *r.ApiTimeMs
			s.ApiTimeMs = &v
		}
		if r.UserID != nil {
			u := *r.UserID
			s.UserID = &u
		}
		stalls = append(stalls, s)
	}
	sort.SliceStable(stalls, func(i, j int) bool {
		a, b := stalls[i], stalls[j]
		if !a.InitiatedAt.Equal(b.InitiatedAt) {
			return a.InitiatedAt.Before(b.InitiatedAt)
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.ID < b.ID
	})
	return stalls
}

// stallEnd is the stall's completion, else its start.
func stallEnd(s Stall) time.Time {
	if s.CompletedAt != nil {
		return *s.CompletedAt
	}
	return s.InitiatedAt
}

// buildEpisodes merges stalls (sorted by start) into episodes: a stall joins
// the current episode when its start is at most gap after the episode's latest
// end. It sets each stall's Episode index.
func buildEpisodes(stalls []Stall, gap time.Duration) []Episode {
	episodes := []Episode{}
	var lastEnd time.Time
	for i := range stalls {
		s := &stalls[i]
		if len(episodes) == 0 || s.InitiatedAt.Sub(lastEnd) > gap {
			episodes = append(episodes, Episode{Index: len(episodes) + 1, Start: s.InitiatedAt})
			lastEnd = stallEnd(*s)
		} else if end := stallEnd(*s); end.After(lastEnd) {
			lastEnd = end
		}
		s.Episode = len(episodes)
		episodes[len(episodes)-1].End = lastEnd
	}
	users := make([]map[int]bool, len(episodes))
	ops := make([]map[string]int, len(episodes))
	for i := range episodes {
		users[i], ops[i] = map[int]bool{}, map[string]int{}
	}
	for _, s := range stalls {
		i := s.Episode - 1
		e := &episodes[i]
		e.Requests++
		e.MaxMs = math.Max(e.MaxMs, s.ObservedMs)
		if s.UserID != nil {
			users[i][*s.UserID] = true
		}
		ops[i][s.Controller+"."+s.Action]++
	}
	for i := range episodes {
		episodes[i].Users = len(users[i])
		episodes[i].Operations = topOperations(ops[i])
	}
	return episodes
}

func topOperations(ops map[string]int) []string {
	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if ops[names[i]] != ops[names[j]] {
			return ops[names[i]] > ops[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > maxPatternOperations {
		names = names[:maxPatternOperations]
	}
	return names
}

// buildVolleys finds, within each episode, two or more stalls of one user whose
// starts fall within window of the first one. Stalls must be in start order and
// carry their episode index, so a volley always sits inside one episode.
func buildVolleys(stalls []Stall, window time.Duration) []Volley {
	type key struct{ episode, user int }
	groups := map[key][]Stall{}
	var order []key
	for _, s := range stalls {
		if s.UserID == nil {
			continue
		}
		k := key{s.Episode, *s.UserID}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], s)
	}
	volleys := []Volley{}
	for _, k := range order {
		group := groups[k]
		for i := 0; i < len(group); {
			j := i + 1
			for j < len(group) && group[j].InitiatedAt.Sub(group[i].InitiatedAt) <= window {
				j++
			}
			if j-i >= 2 {
				volleys = append(volleys, volleyOf(k.episode, k.user, group[i:j]))
			}
			i = j
		}
	}
	sort.SliceStable(volleys, func(i, j int) bool {
		if !volleys[i].FirstStart.Equal(volleys[j].FirstStart) {
			return volleys[i].FirstStart.Before(volleys[j].FirstStart)
		}
		return volleys[i].UserID < volleys[j].UserID
	})
	return volleys
}

func volleyOf(episode, user int, members []Stall) Volley {
	v := Volley{
		Episode: episode, UserID: user, Requests: len(members), FirstStart: members[0].InitiatedAt,
		StartSpreadMs: members[len(members)-1].InitiatedAt.Sub(members[0].InitiatedAt).Milliseconds(),
		EndGapsMs:     []int64{},
	}
	var ends []time.Time
	for _, m := range members {
		if m.CompletedAt == nil {
			v.IncompleteEnds++
			continue
		}
		ends = append(ends, *m.CompletedAt)
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].Before(ends[j]) })
	if len(ends) >= 2 {
		v.EndStaggerMs = ends[len(ends)-1].Sub(ends[0]).Milliseconds()
		for i := 1; i < len(ends); i++ {
			v.EndGapsMs = append(v.EndGapsMs, ends[i].Sub(ends[i-1]).Milliseconds())
		}
	}
	return v
}

func incompleteEnds(stalls []Stall) int {
	n := 0
	for _, s := range stalls {
		if s.CompletedAt == nil {
			n++
		}
	}
	return n
}

func recurrenceOf(episodes int) string {
	switch {
	case episodes <= 0:
		return RecurrenceNone
	case episodes == 1:
		return RecurrenceSingleEpisode
	case episodes == 2:
		return RecurrenceTwoEpisodes
	default:
		return RecurrenceRecurring
	}
}

// patternCoverage runs the P1a census per source when the repository offers
// it. Any refusal or error leaves Run false with a plain reason.
func patternCoverage(ctx context.Context, repo Repository, in PatternInput) (*PatternCoverage, error) {
	prober, ok := repo.(CoverageProber)
	if !ok {
		return &PatternCoverage{Run: false, Note: "Coverage is not available for this source, so the share of rows with a start time is unknown."}, nil
	}
	var docs, ria, account int64
	for _, src := range in.SourceIDs {
		rep, err := prober.Coverage(ctx, CoverageInput{SourceID: src, Start: in.Start, End: in.End})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, ErrBroadScanRefused) {
				return &PatternCoverage{Run: false, Note: "Coverage did not run: broad scans are off for this live source. Run Coverage first."}, nil
			}
			return &PatternCoverage{Run: false, Note: "Coverage did not run (" + plainReason(err) + "). Run Coverage first."}, nil
		}
		docs += rep.Total.Docs
		ria += rep.Total.RequestInitiatedAt.NonNull
		account += rep.Total.AccountID.NonNull
	}
	riaPct, accountPct := CoveragePct(ria, docs), CoveragePct(account, docs)
	return &PatternCoverage{Run: true, RequestInitiatedAtPct: &riaPct, AccountIDPct: &accountPct, Note: fmt.Sprintf("Coverage over the same window: %d rows.", docs)}, nil
}

// plainReason names an error class without driver or connection text.
func plainReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "the query ran out of time"
	case errors.Is(err, ErrDocsExaminedBudget):
		return "the collection is above the docs-examined budget"
	case errors.Is(err, ErrLimit):
		return "a row or size limit was reached"
	case errors.Is(err, ErrBusy):
		return "the source is busy"
	case errors.Is(err, ErrNotFound):
		return "the source was not found"
	case errors.Is(err, ErrInvalidInput):
		return "the request was not accepted"
	default:
		return "the source returned an error"
	}
}

// ---- Findings ----

// patternEvidenceID is a stable reference to one rule over one window and set
// of sources with the thresholds used. It never names a row.
func patternEvidenceID(rule string, in PatternInput, p PatternParams) string {
	sources := append([]string(nil), in.SourceIDs...)
	sort.Strings(sources)
	key := fmt.Sprintf("%s|%s|%s|%s|%g|%d|%g|%g|%d", rule,
		in.Start.UTC().Format(time.RFC3339Nano), in.End.UTC().Format(time.RFC3339Nano), strings.Join(sources, ","),
		p.StallMs, p.EpisodeGapSeconds, p.AuditStallMs, p.AuditStallShare, p.VolleyWindowMs)
	sum := sha256.Sum256([]byte(key))
	return "p2:" + rule + ":" + hex.EncodeToString(sum[:8])
}

// fmtSeconds prints milliseconds as seconds with at most one decimal.
func fmtSeconds(ms float64) string {
	return strconv.FormatFloat(math.Round(ms/100)/10, 'f', -1, 64)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// buildFindings prints one finding per rule that matched. Headlines describe
// what was measured, with the §6.6 wording for audit create, and never a cause.
// A window with no stall prints no B1/B2 finding; Recurrence says "none".
func buildFindings(in PatternInput, p PatternParams, r PatternReport) []Finding {
	findings := []Finding{}
	stallSec := fmtSeconds(p.StallMs)
	if n := len(r.Stalls); n > 0 {
		findings = append(findings, Finding{
			Rule: "B1", Label: EvidenceSymptom, N: n, LowN: n < LowNRows, EvidenceID: patternEvidenceID("B1", in, p),
			Headline: fmt.Sprintf("%d %s took %s s or longer.", n, plural(n, "request", "requests"), stallSec),
		})
	}
	if e := len(r.Episodes); e > 0 {
		n := len(r.Stalls)
		var headline string
		switch r.Recurrence {
		case RecurrenceSingleEpisode:
			headline = fmt.Sprintf("Seen once: single episode, %d %s.", n, plural(n, "request", "requests"))
		case RecurrenceTwoEpisodes:
			headline = fmt.Sprintf("Two separate episodes, %d requests in all. That is not recurring; it takes %d or more.", n, MinRecurringEpisodes)
		default:
			headline = fmt.Sprintf("Recurring: %d separate episodes, %d requests in all.", e, n)
		}
		if r.Truncated {
			headline += " The row limit was reached, so the episode count is partial."
		}
		findings = append(findings, Finding{
			Rule: "B2", Label: EvidenceSymptom, N: n, LowN: n < LowNRows || e < MinRecurringEpisodes,
			EvidenceID: patternEvidenceID("B2", in, p), Headline: headline,
		})
	}
	if n := len(r.AuditStalls); n > 0 {
		longest := 0.0
		for _, a := range r.AuditStalls {
			longest = math.Max(longest, a.AuditCreateTimeMs)
		}
		headline := fmt.Sprintf("Audit create took %s s as measured from the client side.", fmtSeconds(longest))
		if n > 1 {
			share := strconv.FormatFloat(math.Round(p.AuditStallShare*1000)/10, 'f', -1, 64)
			headline += fmt.Sprintf(" That is the longest of %d requests at or above %s s and %s%% of observed time.", n, fmtSeconds(p.AuditStallMs), share)
		}
		findings = append(findings, Finding{
			Rule: "B6", Label: EvidenceSymptom, N: n, LowN: n < LowNRows,
			EvidenceID: patternEvidenceID("B6", in, p), Headline: headline,
		})
	}
	if v := len(r.Volleys); v > 0 {
		n := 0
		for _, vol := range r.Volleys {
			n += vol.Requests
		}
		windowSec := fmtSeconds(float64(p.VolleyWindowMs))
		var headline string
		if v == 1 {
			vol := r.Volleys[0]
			headline = fmt.Sprintf("One user started %d requests of %s s or longer within %s s of each other.", vol.Requests, stallSec, windowSec)
			if vol.Requests-vol.IncompleteEnds >= 2 {
				headline += fmt.Sprintf(" Their end times spread over %s s.", fmtSeconds(float64(vol.EndStaggerMs)))
			}
		} else {
			headline = fmt.Sprintf("%d times, one user started 2 or more requests of %s s or longer within %s s of each other.", v, stallSec, windowSec)
		}
		findings = append(findings, Finding{
			Rule: "B12", Label: EvidenceCorrelation, N: n, LowN: n < LowNRows,
			EvidenceID: patternEvidenceID("B12", in, p), Headline: headline,
		})
	}
	return findings
}
