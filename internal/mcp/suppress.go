package mcp

import (
	"fmt"

	"api-audit-explorer/internal/audit"
)

// Small-count suppression and tier projection for a host that holds the aggregate tier (spec 03 D13, BL-348). The bridge of the stdio
// server and the command line is unscoped and shows every count; the pipe backend sets WithMinCohort for an aggregate profile.
//
// The rule: a count of events that is above zero and below the minimum is never shown. It is shown as the text "<5", a group or
// row that rests on such a count carries no figure built from its timings (no percentile, ratio, mean, histogram), and rows too small to
// be listed are folded into the "Other" row. Zero is not a small count: "nothing happened" points at no one. The text names the minimum,
// which is a constant of the policy and not a fact about the data.

// WithMinCohort turns on suppression of counts below n for this bridge (n of 5 is the policy of the aggregate tier). Zero turns it off.
func WithMinCohort(n int) Option {
	return func(b *Bridge) { b.minCohort = n }
}

// WithHiddenRecordIDs removes the audit record id from the rows of audit_get_requests and audit_get_slowest. The id is an identifier:
// a profile without the identifiers grant must not receive it.
func WithHiddenRecordIDs(hidden bool) Option {
	return func(b *Bridge) { b.hideRecordIDs = hidden }
}

func (b *Bridge) suppressing() bool { return b.minCohort > 0 }

// small reports a count that must not be shown.
func (b *Bridge) small(c int64) bool { return b.suppressing() && c > 0 && c < int64(b.minCohort) }

// count is a count as it is shown: itself, or the text "<n".
func (b *Bridge) count(c int64) any {
	if b.small(c) {
		return b.hiddenCount()
	}
	return c
}

// groupSmall says a group rests on a small count in either period (events or valid timings).
func (b *Bridge) groupSmall(c audit.Comparison) bool {
	return b.small(c.CurrentEvents) || b.small(c.ReferenceEvents) || b.small(c.Current.Count) || b.small(c.Reference.Count)
}

// narrow says the whole selection is small: the events of all groups in the current period add up to a positive number below the
// minimum. Then nothing is listed, because a single listed row would be the answer about one or two requests.
func (b *Bridge) narrow(result *audit.Analysis) bool {
	var total int64
	for _, g := range result.APIs {
		total += g.CurrentEvents
	}
	return b.small(total)
}

// hiddenGroup is a group a suppressing bridge shows no figures for.
func (b *Bridge) hiddenGroup(result *audit.Analysis, g audit.Comparison) bool {
	return b.suppressing() && (b.groupSmall(g) || b.narrow(result))
}

// suppressedGroup is the allow-listed view of a group that rests on a small count: names, the state and the counts as they may be shown.
func (b *Bridge) suppressedGroup(g audit.Comparison) map[string]any {
	return map[string]any{
		"key": g.Key, "sourceId": g.SourceID, "controller": g.Controller, "action": g.Action, "kind": g.Kind, "outcome": g.Outcome, "state": g.State,
		"currentEvents": b.count(g.CurrentEvents), "referenceEvents": b.count(g.ReferenceEvents), "suppressed": true,
	}
}

// groups returns the groups of a page as they may be shown: unchanged without suppression, with a small group reduced to its
// allow-listed view, and empty when the whole selection is small.
func (b *Bridge) groups(result *audit.Analysis, rows []audit.Comparison) any {
	if !b.suppressing() {
		return rows
	}
	if b.narrow(result) {
		return []any{}
	}
	out := make([]any, 0, len(rows))
	for _, g := range rows {
		if b.groupSmall(g) {
			out = append(out, b.suppressedGroup(g))
		} else {
			out = append(out, g)
		}
	}
	return out
}

// pageGroups applies groups to a page made by groupPage.
func (b *Bridge) pageGroups(result *audit.Analysis, page map[string]any) {
	if !b.suppressing() {
		return
	}
	rows, _ := page["groups"].([]audit.Comparison)
	page["groups"] = b.groups(result, rows)
	page["suppression"] = b.suppressionNote(result)
}

// suppressionNote tells the host what it is not shown, without any number that is itself small.
func (b *Bridge) suppressionNote(result *audit.Analysis) map[string]any {
	note := fmt.Sprintf("Counts below %d are shown as \"<%d\" and groups resting on them carry no timings, ratios or percentiles. Evidence access shows them.", b.minCohort, b.minCohort)
	narrow := b.narrow(result)
	if narrow {
		note = fmt.Sprintf("This selection holds fewer than %d events, so no group is listed. Widen the window or the filters.", b.minCohort)
	}
	return map[string]any{"minimum": b.minCohort, "narrow": narrow, "note": note}
}

// recorded is the "recordedEvents" block of a comparison. Under suppression it counts only the groups that are shown with figures:
// a total over all groups minus the listed groups would give the count of a hidden one.
func (b *Bridge) recorded(result *audit.Analysis, q audit.Query) map[string]any {
	var current, reference int64
	for _, g := range result.APIs {
		if b.suppressing() && b.groupSmall(g) {
			continue
		}
		current += g.CurrentEvents
		reference += g.ReferenceEvents
	}
	out := map[string]any{"currentPerSecond": float64(current) / q.End.Sub(q.Start).Seconds(), "referencePerSecond": float64(reference) / q.BaselineEnd.Sub(q.BaselineStart).Seconds()}
	if b.suppressing() && b.narrow(result) {
		current, reference = 0, 0
		out["currentPerSecond"], out["referencePerSecond"] = 0.0, 0.0
		out["current"], out["reference"] = fmt.Sprintf("<%d", b.minCohort), fmt.Sprintf("<%d", b.minCohort)
		return out
	}
	out["current"], out["reference"] = current, reference
	return out
}

// maskPlan says which slots of a timeline are not shown. total is the group's own count (shown next to the slots); counts are the slots'.
// A slot below the minimum is hidden. What the hidden slots hold together is total minus the shown ones, and the host can work that out;
// while it is a positive number below the minimum it would give the count of a single hidden slot, so the smallest shown slot is hidden
// too. A shown slot has at least the minimum, so one step is enough, and it stops when no slot is left.
func (b *Bridge) maskPlan(total int64, counts []int64) []bool {
	hide := make([]bool, len(counts))
	var shown int64
	for i, c := range counts {
		if b.small(c) {
			hide[i] = true
		} else {
			shown += c
		}
	}
	for rest := total - shown; rest > 0 && rest < int64(b.minCohort); {
		pick := -1
		for i, c := range counts {
			if !hide[i] && c > 0 && (pick < 0 || c < counts[pick]) {
				pick = i
			}
		}
		if pick < 0 {
			break
		}
		hide[pick] = true
		rest += counts[pick]
	}
	return hide
}

// points shows the slots of a group's timeline: a hidden slot is its time and "<n" only.
func (b *Bridge) points(g audit.Comparison, points []audit.Point) any {
	if !b.suppressing() {
		return points
	}
	counts := make([]int64, len(points))
	for i, p := range points {
		counts[i] = p.Count
	}
	hide := b.maskPlan(g.Current.Count, counts)
	out := make([]any, 0, len(points))
	for i, p := range points {
		if hide[i] {
			out = append(out, map[string]any{"at": p.At, "count": b.hiddenCount(), "suppressed": true})
		} else {
			out = append(out, p)
		}
	}
	return out
}

// hiddenCount is the text that stands for a count the host may not see.
func (b *Bridge) hiddenCount() string { return fmt.Sprintf("<%d", b.minCohort) }

// hiddenSum is the number of current-period events in the groups whose count is hidden. A breakdown reports the events of the whole
// selection, so the host could subtract the groups it was shown and read this number: when it is positive and below the minimum it is the
// count of a hidden group (or of a few), and the breakdown is refused.
func (b *Bridge) hiddenSum(result *audit.Analysis) int64 {
	var sum int64
	for _, g := range result.APIs {
		if b.small(g.CurrentEvents) {
			sum += g.CurrentEvents
		}
	}
	return sum
}

// shownBreakdown returns the rows of a breakdown as they may be shown. Rows below the minimum are folded into Other; while Other is itself
// small, the smallest listed row is folded in too (otherwise total minus the listed rows would give Other); a selection that is small
// altogether lists nothing. The result holds maps for rows that carry no timings.
func (b *Bridge) shownBreakdown(out audit.Breakdown) (rows []any, other any, total any) {
	if !b.suppressing() {
		list := make([]any, len(out.Rows))
		for i := range out.Rows {
			list[i] = out.Rows[i]
		}
		if out.Other == nil {
			return list, nil, out.Total
		}
		return list, *out.Other, out.Total
	}
	if b.small(out.Total) {
		return []any{}, nil, b.count(out.Total)
	}
	keep := append([]audit.BreakdownRow(nil), out.Rows...)
	var folded []audit.BreakdownRow
	kept := keep[:0]
	for _, r := range keep {
		if b.small(r.Count) {
			folded = append(folded, r)
		} else {
			kept = append(kept, r)
		}
	}
	keep = kept
	sum := func() int64 {
		var s int64
		if out.Other != nil {
			s = out.Other.Count
		}
		for _, r := range folded {
			s += r.Count
		}
		return s
	}
	for sum() > 0 && sum() < int64(b.minCohort) && len(keep) > 0 {
		smallest := 0
		for i, r := range keep {
			if r.Count < keep[smallest].Count {
				smallest = i
			}
		}
		folded = append(folded, keep[smallest])
		keep = append(keep[:smallest], keep[smallest+1:]...)
	}
	list := make([]any, 0, len(keep))
	for _, r := range keep {
		if b.small(r.Stats.Count) {
			r.Stats = audit.Stats{} // timings of fewer than the minimum completed requests are near-individual values
		}
		list = append(list, r)
	}
	if len(folded) == 0 {
		if out.Other == nil {
			return list, nil, out.Total
		}
		return list, *out.Other, out.Total
	}
	merged := audit.BreakdownRow{Value: audit.BreakdownOtherLabel}
	if out.Other != nil {
		merged = *out.Other
		merged.Value = audit.BreakdownOtherLabel
	}
	for _, r := range folded {
		merged.Count += r.Count
		merged.Complete += r.Complete
		merged.Incomplete += r.Incomplete
		merged.Pending += r.Pending
		merged.Count4xx += r.Count4xx
		merged.Count5xx += r.Count5xx
		merged.Values += max(r.Values, 1)
	}
	merged.Stats, merged.Filter = audit.Stats{}, nil
	return list, merged, out.Total
}

// groupHidden is the refusal for a timeline of a group that rests on a small count. The text repeats nothing the host sent.
func (b *Bridge) groupHidden() error {
	return codedWire(CodeInvalidArguments, WirePermissionRequired,
		fmt.Sprintf("That group has fewer than %d events in a period (or the selection does), so this host gets no timeline for it. Ask the person for evidence access, or run audit_compare on a wider window.", b.minCohort))
}

// breakdownHidden is the refusal for a breakdown over a comparison whose hidden groups together hold fewer than the minimum of events: the
// breakdown counts the whole selection, so the host could subtract the groups it was shown and read their count.
func (b *Bridge) breakdownHidden() error {
	return codedWire(CodeInvalidArguments, WirePermissionRequired,
		fmt.Sprintf("A breakdown of this comparison would reveal the size of a group of fewer than %d events. Run audit_compare on a wider window or with fewer filters, or ask the person for evidence access.", b.minCohort))
}

// compareTimelines is the free function of timelines.go with the suppression of this bridge: a hidden group refuses the whole call, and a
// slot that rests on fewer than the minimum shows "<n" and no timings.
func (b *Bridge) compareTimelines(result *audit.Analysis, id string, keys []string) (any, error) {
	if b.suppressing() {
		for _, key := range keys {
			if g, err := findGroup(result, key); err == nil && b.hiddenGroup(result, g) {
				return nil, b.groupHidden()
			}
		}
	}
	out, err := compareTimelines(result, id, keys)
	if err != nil || !b.suppressing() {
		return out, err
	}
	m, ok := out.(map[string]any)
	if !ok {
		return nil, coded(CodeInternal, "unexpected timeline shape")
	}
	slots, _ := m["slots"].([]map[string]any)
	groups, _ := m["groups"].([]audit.Comparison)
	for gi, g := range groups {
		var cells []map[string]any
		var counts []int64
		for _, slot := range slots {
			values, _ := slot["groups"].([]map[string]any)
			if gi < len(values) {
				n, _ := values[gi]["count"].(int64)
				cells, counts = append(cells, values[gi]), append(counts, n)
			}
		}
		for i, hide := range b.maskPlan(g.Current.Count, counts) {
			if hide {
				cells[i]["count"], cells[i]["suppressed"] = b.hiddenCount(), true
				cells[i]["currentP95Ms"], cells[i]["increaseMs"], cells[i]["increasePercent"] = nil, nil, nil
			}
		}
	}
	return m, nil
}
