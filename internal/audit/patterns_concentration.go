package audit

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Rule A3 (incident-gap addendum §1.6): is one operation or one user a larger
// share of the stalls than of all traffic? It adds no row reads. Operation
// denominators come from the Analyze result RunPatterns already holds; user
// denominators come from one userId breakdown over the same window.
const (
	// concentrationLift, concentrationMinStalls and concentrationMinAll define "concentrated".
	concentrationLift      = 2.0
	concentrationMinStalls = 10
	// concentrationMinEpisodes is the independent-episode count for Correlation.
	concentrationMinEpisodes = MinRecurringEpisodes
	// concentrationFalsifyStalls and concentrationFalsifyLift define "spread like traffic".
	concentrationFalsifyStalls = 50
	concentrationFalsifyLift   = 1.2
	// maxConcentrationCandidates is how many operations and users, by stall count, are looked at.
	maxConcentrationCandidates = 10
	// liveReadMaxWindow is the longest window a new live read may cover (§6.5).
	liveReadMaxWindow = 24 * time.Hour
)

// isLiveRead reports whether the provenance of this run came from a source that
// does not measure docsExamined, which is every live source. The demo always does.
func isLiveRead(prov []Provenance) bool {
	for _, p := range prov {
		if p.DocsExamined == nil {
			return true
		}
	}
	return false
}

// allNarrow reports whether every provenance row is a narrow scan, and there is at least one.
func allNarrow(prov []Provenance) bool {
	if len(prov) == 0 {
		return false
	}
	for _, p := range prov {
		if p.ScanClass != ScanNarrow {
			return false
		}
	}
	return true
}

type concentrationCandidate struct {
	dimension string
	key       string
	label     string
	stalls    int
	episodes  map[int]bool
}

// concentrationCandidates counts stalls by operation and by user and keeps the top
// maxConcentrationCandidates of each, by stall count then key.
func concentrationCandidates(stalls []Stall) (ops, users []*concentrationCandidate) {
	byOp := map[string]*concentrationCandidate{}
	byUser := map[string]*concentrationCandidate{}
	add := func(m map[string]*concentrationCandidate, dim, key, label string, episode int) {
		c := m[key]
		if c == nil {
			c = &concentrationCandidate{dimension: dim, key: key, label: label, episodes: map[int]bool{}}
			m[key] = c
		}
		c.stalls++
		c.episodes[episode] = true
	}
	for _, s := range stalls {
		op := s.Controller + "." + s.Action
		add(byOp, ConcentrationOperation, op, op, s.Episode)
		if s.UserID != nil {
			id := strconv.Itoa(*s.UserID)
			add(byUser, ConcentrationUser, id, "User "+id, s.Episode)
		}
	}
	top := func(m map[string]*concentrationCandidate) []*concentrationCandidate {
		list := make([]*concentrationCandidate, 0, len(m))
		for _, c := range m {
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].stalls != list[j].stalls {
				return list[i].stalls > list[j].stalls
			}
			return list[i].key < list[j].key
		})
		if len(list) > maxConcentrationCandidates {
			list = list[:maxConcentrationCandidates]
		}
		return list
	}
	return top(byOp), top(byUser)
}

// operationTraffic sums the audit rows of every kind per Controller.Action, and
// in all. Rollup.Events counts every audit row in the window.
func operationTraffic(rollups []Rollup) (map[string]int64, int64) {
	byOp := map[string]int64{}
	var total int64
	for _, r := range rollups {
		byOp[r.Controller+"."+r.Action] += r.Events
		total += r.Events
	}
	return byOp, total
}

// concentrationRow is the A3 row for one candidate. The shares and the lift are
// withheld below MinSampleCount rows overall, so a thin cell is never printed.
func concentrationRow(c *concentrationCandidate, totalStalls int, all, total int64) Concentration {
	row := Concentration{Dimension: c.dimension, Key: c.key, StallN: c.stalls, AllN: all, Episodes: len(c.episodes)}
	if all < MinSampleCount || total <= 0 || totalStalls <= 0 {
		row.Withheld = true
		return row
	}
	stallShare := float64(c.stalls) / float64(totalStalls)
	trafficShare := float64(all) / float64(total)
	lift := stallShare / trafficShare
	row.StallShare, row.TrafficShare, row.Lift = &stallShare, &trafficShare, &lift
	return row
}

func isConcentrated(r Concentration) bool {
	return !r.Withheld && r.Lift != nil && *r.Lift >= concentrationLift && r.StallN >= concentrationMinStalls && r.AllN >= MinSampleCount
}

// userTraffic reads the userId breakdown for the window. On a live source the
// read runs in hint mode and is used only when every page came back as a narrow
// scan, so this rule never adds a broad scan. ok is false when it was not read.
func userTraffic(ctx context.Context, repo Repository, in PatternInput, live bool) (counts map[string]int64, total int64, warning string, err error) {
	reader, ok := repo.(BreakdownReader)
	if !ok {
		return nil, 0, "Traffic share by user was not read: this source cannot break rows down by user.", nil
	}
	q := patternQuery(in, in.SourceIDs)
	if live {
		q.QueryMode = QueryModeHint
	}
	b, err := reader.Breakdown(ctx, BreakdownInput{Query: q, Dim: BreakdownUserID, Top: MaxUserIDs})
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, "", ctx.Err()
		}
		return nil, 0, "Traffic share by user was not read (" + plainReason(err) + ").", nil
	}
	if live && !allNarrow(b.Provenance) {
		return nil, 0, "Traffic share by user was not read: the read would not be a narrow scan.", nil
	}
	counts = make(map[string]int64, len(b.Rows))
	for _, row := range b.Rows {
		if row.Missing {
			continue
		}
		counts[strings.TrimSpace(row.Value)] += row.Count
	}
	return counts, b.Total, "", nil
}

// buildConcentration runs A3. analysis is the quality read RunPatterns already
// made (nil when it failed). It is withheld when the stall read hit the row cap,
// and on a live source when the window is over 24 hours.
func buildConcentration(ctx context.Context, repo Repository, in PatternInput, p PatternParams, r PatternReport, analysis *Analysis) ([]Concentration, []Finding, []string, error) {
	if len(r.Stalls) == 0 {
		return nil, nil, nil, nil
	}
	if r.StallsTruncated {
		return nil, nil, []string{"Concentration was not checked (partial: stall rows capped)."}, nil
	}
	live := isLiveRead(r.Provenance)
	if live && in.End.Sub(in.Start) > liveReadMaxWindow {
		return nil, nil, []string{"Traffic share not read: the window is over 24 hours on a live source."}, nil
	}
	var warnings []string
	ops, users := concentrationCandidates(r.Stalls)
	totalStalls := len(r.Stalls)

	var rows []Concentration
	var opCandidates []*concentrationCandidate
	for _, c := range ops {
		if c.stalls >= concentrationMinStalls {
			opCandidates = append(opCandidates, c)
		}
	}
	if len(opCandidates) > 0 {
		if analysis == nil || len(analysis.Rollups) == 0 {
			warnings = append(warnings, "Traffic share by operation was not read.")
		} else {
			byOp, total := operationTraffic(analysis.Rollups)
			for _, c := range opCandidates {
				rows = append(rows, concentrationRow(c, totalStalls, byOp[c.key], total))
			}
		}
	}
	userCandidates := users[:0:0]
	for _, c := range users {
		if c.stalls >= concentrationMinStalls {
			userCandidates = append(userCandidates, c)
		}
	}
	if len(userCandidates) > 0 {
		counts, total, warning, err := userTraffic(ctx, repo, in, live)
		switch {
		case err != nil:
			return nil, nil, nil, err
		case warning != "":
			warnings = append(warnings, warning)
		default:
			for _, c := range userCandidates {
				// A user outside the breakdown's top rows has no known denominator.
				if all, ok := counts[c.key]; ok {
					rows = append(rows, concentrationRow(c, totalStalls, all, total))
				}
			}
		}
	}
	sortConcentration(rows)
	return rows, concentrationFindings(in, p, rows), warnings, nil
}

func sortConcentration(rows []Concentration) {
	rank := func(d string) int {
		if d == ConcentrationOperation {
			return 0
		}
		return 1
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if rank(a.Dimension) != rank(b.Dimension) {
			return rank(a.Dimension) < rank(b.Dimension)
		}
		la, lb := -1.0, -1.0
		if a.Lift != nil {
			la = *a.Lift
		}
		if b.Lift != nil {
			lb = *b.Lift
		}
		if la != lb {
			return la > lb
		}
		if a.StallN != b.StallN {
			return a.StallN > b.StallN
		}
		return a.Key < b.Key
	})
}

func fmtPercent(share float64) string {
	return strconv.FormatFloat(math.Round(share*1000)/10, 'f', -1, 64)
}

func fmtLift(lift float64) string {
	return strconv.FormatFloat(math.Round(lift*10)/10, 'f', -1, 64)
}

func concentrationName(r Concentration) string {
	if r.Dimension == ConcentrationUser {
		return "User " + r.Key
	}
	return r.Key
}

// concentrationFindings prints one A3 finding: the most concentrated row, or the
// falsification when nothing is concentrated and a large row looks like traffic.
func concentrationFindings(in PatternInput, p PatternParams, rows []Concentration) []Finding {
	var top *Concentration
	for i := range rows {
		if !isConcentrated(rows[i]) {
			continue
		}
		if top == nil || *rows[i].Lift > *top.Lift {
			top = &rows[i]
		}
	}
	if top != nil {
		headline := fmt.Sprintf("%s had %s%% of stall requests but %s%% of all requests in this window (%sx its traffic share).",
			concentrationName(*top), fmtPercent(*top.StallShare), fmtPercent(*top.TrafficShare), fmtLift(*top.Lift))
		label := EvidenceCorrelation
		switch {
		case top.Episodes >= concentrationMinEpisodes:
		case top.Episodes == 1:
			label = EvidenceSymptom
			headline += " Single episode."
		default:
			label = EvidenceSymptom
			headline += fmt.Sprintf(" Seen in %d episodes; %d or more are needed.", top.Episodes, concentrationMinEpisodes)
		}
		return []Finding{{
			Rule: "A3", Label: label, N: top.StallN, LowN: top.StallN < LowNRows || top.Episodes < concentrationMinEpisodes,
			EvidenceID: patternEvidenceID("A3", in, p), Headline: headline,
		}}
	}
	for _, r := range rows {
		if !r.Withheld && r.Lift != nil && r.StallN >= concentrationFalsifyStalls && *r.Lift <= concentrationFalsifyLift {
			return []Finding{{
				Rule: "A3", Label: EvidenceSymptom, N: r.StallN, LowN: false,
				EvidenceID: patternEvidenceID("A3", in, p), Headline: "Not observed: stalls are spread like traffic.",
			}}
		}
	}
	return nil
}
