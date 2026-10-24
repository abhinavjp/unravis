package audit

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Rule B12r (incident-gap addendum §1.5): did one user start the same operation
// again while an earlier stall of it was still running? B12 keeps the shape where
// the starts fall within VolleyWindowMs of each other; B12r is the staggered
// shape, so a start within that window is left to B12 and not counted twice.
//
// It makes one read per episode and source, through the Requests pager, filtered
// to that episode's stall users, over the episode span plus two minutes.
const (
	// maxRepeatEpisodes is how many episodes are read, largest first.
	maxRepeatEpisodes = 10
	// repeatTailPad is added after the episode end, to catch a start just after it.
	repeatTailPad = 2 * time.Minute
)

type repeatKey struct {
	episode int
	user    int
	op      string
	same    bool
}

type repeatCount struct {
	repeats int
	// perAnchor counts the repeats attributed to each earlier stall, by ID.
	perAnchor map[string]int
}

// repeatUsers returns the distinct stall users of an episode, at most MaxUserIDs,
// most stalls first. capped is true when more were left out.
func repeatUsers(stalls []Stall) (users []int, capped bool) {
	counts := map[int]int{}
	for _, s := range stalls {
		if s.UserID != nil {
			counts[*s.UserID]++
		}
	}
	for u := range counts {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool {
		if counts[users[i]] != counts[users[j]] {
			return counts[users[i]] > counts[users[j]]
		}
		return users[i] < users[j]
	})
	if len(users) > MaxUserIDs {
		users, capped = users[:MaxUserIDs], true
	}
	sort.Ints(users)
	return users, capped
}

// countRepeats applies the B12r definition to one source's rows of one episode.
// anchors are the episode's stalls on that source, in start order. A request is a
// repeat of an earlier stall when it is by the same user on the same operation,
// starts more than window after the stall's start, and starts strictly before the
// stall completed (a stall with no end time counts as still running). Each request
// is counted once, against the earliest stall it follows.
//
// It returns how many stalls could be checked: those whose own row was read.
func countRepeats(episode int, anchors []Stall, rows []RequestRecord, window time.Duration, into map[repeatKey]*repeatCount) (checked int) {
	byID := make(map[string]RequestRecord, len(rows))
	type opKey struct {
		user int
		op   string
	}
	byOp := map[opKey][]RequestRecord{}
	for _, r := range rows {
		if r.InitiatedAt == nil || r.UserID == nil {
			continue
		}
		byID[r.ID] = r
		k := opKey{*r.UserID, r.Controller + "." + r.Action}
		byOp[k] = append(byOp[k], r)
	}
	for _, list := range byOp {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].InitiatedAt.Equal(*list[j].InitiatedAt) {
				return list[i].InitiatedAt.Before(*list[j].InitiatedAt)
			}
			return list[i].ID < list[j].ID
		})
	}
	counted := map[string]bool{}
	for _, s := range anchors {
		if s.UserID == nil {
			continue
		}
		// The stall's own row gives its size; without it "same request" is unknown.
		self, ok := byID[s.ID]
		if !ok {
			continue
		}
		checked++
		op := s.Controller + "." + s.Action
		for _, r := range byOp[opKey{*s.UserID, op}] {
			if r.ID == s.ID || counted[r.ID] {
				continue
			}
			start := r.InitiatedAt.UTC()
			if !start.After(s.InitiatedAt.Add(window)) {
				continue
			}
			if s.CompletedAt != nil && !start.Before(*s.CompletedAt) {
				continue
			}
			counted[r.ID] = true
			k := repeatKey{episode, *s.UserID, op, r.RequestKB == self.RequestKB}
			c := into[k]
			if c == nil {
				c = &repeatCount{perAnchor: map[string]int{}}
				into[k] = c
			}
			c.repeats++
			c.perAnchor[s.ID]++
		}
	}
	return checked
}

// buildRepeats runs B12r. It returns the rows, at most one finding, the
// provenance of its reads and warnings. It reads nothing when there is no
// episode, and nothing when the stall read was capped (the episodes would be
// partial). On a live source an episode over 24 hours is not read and a read that
// is not a narrow scan is not used; both leave the result partial.
func buildRepeats(ctx context.Context, repo Repository, in PatternInput, p PatternParams, r PatternReport) ([]Repeat, []Finding, []Provenance, []string, error) {
	if len(r.Episodes) == 0 {
		return nil, nil, nil, nil, nil
	}
	if r.StallsTruncated {
		return nil, nil, nil, []string{"Repeats were not checked (partial: stall rows capped)."}, nil
	}
	live := isLiveRead(r.Provenance)
	window := time.Duration(p.VolleyWindowMs) * time.Millisecond

	order := make([]int, len(r.Episodes))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := r.Episodes[order[i]], r.Episodes[order[j]]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Index < b.Index
	})
	var warnings []string
	partial := false
	if len(order) > maxRepeatEpisodes {
		order = order[:maxRepeatEpisodes]
		partial = true
		warnings = append(warnings, fmt.Sprintf("Repeats were checked in the %d largest of %d episodes (partial).", maxRepeatEpisodes, len(r.Episodes)))
	}
	sort.Ints(order)

	stallsOf := map[int][]Stall{}
	for _, s := range r.Stalls {
		stallsOf[s.Episode] = append(stallsOf[s.Episode], s)
	}

	counts := map[repeatKey]*repeatCount{}
	var prov []Provenance
	checked := 0
	for _, idx := range order {
		ep := r.Episodes[idx]
		stalls := stallsOf[ep.Index]
		users, capped := repeatUsers(stalls)
		if len(users) == 0 {
			continue
		}
		start, end := ep.Start, ep.End.Add(repeatTailPad)
		span := end.Sub(start)
		if span > MaxInterval || (live && span > liveReadMaxWindow) {
			partial = true
			warnings = append(warnings, fmt.Sprintf("Repeats in episode %d were not read: the episode is over 24 hours on a live source.", ep.Index))
			continue
		}
		if capped {
			partial = true
			warnings = append(warnings, fmt.Sprintf("Repeats in episode %d were checked for the %d users with the most stalls (partial).", ep.Index, MaxUserIDs))
		}
		sources := map[string][]Stall{}
		var sourceIDs []string
		for _, s := range stalls {
			if _, ok := sources[s.SourceID]; !ok {
				sourceIDs = append(sourceIDs, s.SourceID)
			}
			sources[s.SourceID] = append(sources[s.SourceID], s)
		}
		sort.Strings(sourceIDs)
		for _, src := range sourceIDs {
			q := patternQuery(PatternInput{SourceIDs: []string{src}, Start: start, End: end}, []string{src})
			q.UserIDs = users
			if live {
				q.QueryMode = QueryModeHint
			}
			rows, cut, readProv, err := readPatternPages(ctx, PageInput{Query: q, Limit: patternAuditPage, Order: OrderNewest}, repo.Requests)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, nil, nil, ctx.Err()
				}
				partial = true
				warnings = append(warnings, fmt.Sprintf("Repeats in episode %d were not read (%s).", ep.Index, plainReason(err)))
				continue
			}
			prov = append(prov, readProv...)
			if live && !allNarrow(readProv) {
				partial = true
				warnings = append(warnings, fmt.Sprintf("Repeats in episode %d were not read: the read would not be a narrow scan.", ep.Index))
				continue
			}
			if cut {
				partial = true
				warnings = append(warnings, fmt.Sprintf("Repeats in episode %d are a lower bound: only the newest %d rows were read.", ep.Index, MaxPatternRows))
			}
			checked += countRepeats(ep.Index, sources[src], rows, window, counts)
		}
	}

	rows := repeatRows(counts)
	return rows, repeatFindings(in, p, rows, checked, partial), prov, warnings, nil
}

// repeatRows turns the counts into rows, ordered by episode, then most repeats.
// The chain is the earlier stall plus the most repeats attributed to one stall.
func repeatRows(counts map[repeatKey]*repeatCount) []Repeat {
	var rows []Repeat
	for k, c := range counts {
		longest := 0
		for _, n := range c.perAnchor {
			if n > longest {
				longest = n
			}
		}
		rows = append(rows, Repeat{Episode: k.episode, UserID: k.user, Operation: k.op, Repeats: c.repeats, SameSize: k.same, LongestChain: 1 + longest})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		if a.Repeats != b.Repeats {
			return a.Repeats > b.Repeats
		}
		if a.UserID != b.UserID {
			return a.UserID < b.UserID
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		return a.SameSize && !b.SameSize
	})
	return rows
}

// repeatFindings prints the finding. Rows of the same size are "the same
// request started again"; rows of another size stay in the table only. With no
// row at all and every read complete it prints the falsification.
func repeatFindings(in PatternInput, p PatternParams, rows []Repeat, checked int, partial bool) []Finding {
	var same []Repeat
	total := 0
	for _, row := range rows {
		if row.SameSize {
			same = append(same, row)
			total += row.Repeats
		}
	}
	if len(same) > 0 {
		top := same[0]
		for _, row := range same[1:] {
			if row.Repeats > top.Repeats {
				top = row
			}
		}
		headline := fmt.Sprintf("The same user started %s again %d %s while an earlier one was still running, during this stall (episode %d).",
			top.Operation, top.Repeats, plural(top.Repeats, "time", "times"), top.Episode)
		if more := len(same) - 1; more > 0 {
			headline += fmt.Sprintf(" %d more %s in this window.", more, plural(more, "case", "cases"))
		}
		if partial {
			headline += " Partial count (at least)."
		}
		return []Finding{{
			Rule: "B12r", Label: EvidenceCorrelation, N: total, LowN: total < LowNRows,
			EvidenceID: patternEvidenceID("B12r", in, p), Headline: headline,
		}}
	}
	if len(rows) == 0 && !partial && checked > 0 {
		return []Finding{{
			Rule: "B12r", Label: EvidenceSymptom, N: checked, LowN: checked < LowNRows,
			EvidenceID: patternEvidenceID("B12r", in, p),
			Headline:   "Not observed: no user started the same operation again while it was running.",
		}}
	}
	return nil
}
