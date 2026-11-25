package mcp

import (
	"fmt"
	"time"

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

// slotSmall says a timeline slot rests on a small count: its valid timings are 1 to 4, or it has none and the counters that cover every
// document of the slot (4xx, 5xx, incomplete) are 1 to 4.
func (b *Bridge) slotSmall(p audit.Point) bool {
	if b.small(p.Count) {
		return true
	}
	return p.Count == 0 && (b.small(p.Count4xx) || b.small(p.Count5xx) || b.small(p.Incomplete))
}

// slotGrid is the number of slot positions the host can count in the window of a query (the times it would see in a full timeline).
func slotGrid(q audit.Query) int {
	if q.BucketSeconds <= 0 {
		return 0
	}
	width := int64(q.BucketSeconds)
	first := q.Start.Unix() / width
	if q.Start.Unix()%width < 0 {
		first--
	}
	n := 0
	for at := first * width; at < q.End.Unix() && n < 100000; at += width {
		n++
	}
	return n
}

// maskPlan says which slots of a group's timeline are not shown. total is the group's own count (shown next to the slots), counts the
// slots' valid timings, small which slots rest on a small count, observed which have a point at all, n the number of slot positions. A hidden slot is not marked: it is left
// out of a timeline and shown as a missing slot in a comparison, so the host cannot count the hidden slots. What the hidden slots hold
// together is total minus the shown ones, and the host can work that out. Two things would still give a slot away:
//   - that sum is 1 to 4: it would be the count of a single slot, so the smallest shown slot is hidden too (a shown slot has at least
//     the minimum, so one step is enough);
//   - that sum is 4 times the positions the host cannot see a count for (the slots that look missing, hidden ones included): then each of
//     them would hold exactly 4. Hiding more shown slots only raises the sum, so nothing can be done: ok is false and no timeline is shown.
func (b *Bridge) maskPlan(total int64, counts []int64, small, observed []bool, n int) (hide []bool, ok bool) {
	hide = append([]bool(nil), small...)
	for {
		var shown int64
		seen := 0
		for i, c := range counts {
			if !hide[i] && c > 0 {
				shown += c
			}
			if !hide[i] && observed[i] {
				seen++ // a slot with data is a position the host knows: one with no timing (only failures) holds no hidden timings either
			}
		}
		rest := total - shown
		if rest <= 0 {
			return hide, true
		}
		if rest >= int64(b.minCohort) {
			return hide, rest < 4*int64(max(n-seen, 1))
		}
		pick := -1
		for i, c := range counts {
			if !hide[i] && c > 0 && (pick < 0 || c < counts[pick]) {
				pick = i
			}
		}
		if pick < 0 {
			return hide, false
		}
		hide[pick] = true
	}
}

// points shows the slots of a group's timeline: a hidden slot is not in the list (as a slot with no observation is not). ok is false
// when no safe list exists (see maskPlan).
func (b *Bridge) points(g audit.Comparison, q audit.Query, points []audit.Point) (list any, ok bool) {
	if !b.suppressing() {
		return points, true
	}
	counts := make([]int64, len(points))
	small, observed := make([]bool, len(points)), make([]bool, len(points))
	for i, p := range points {
		counts[i], small[i], observed[i] = p.Count, b.slotSmall(p), true
	}
	hide, ok := b.maskPlan(g.Current.Count, counts, small, observed, max(slotGrid(q), len(points)))
	if !ok {
		return nil, false
	}
	out := make([]audit.Point, 0, len(points))
	for i, p := range points {
		if !hide[i] {
			out = append(out, p)
		}
	}
	return out, true
}

// provenanceView is a provenance entry as a suppressing bridge shows it: the same keys, with the three record counts null (the shape of
// "not measured") and countsHidden set. The outer fields shadow the embedded ones in the JSON.
type provenanceView struct {
	audit.Provenance
	DocsReturned *int64 `json:"docsReturned"`
	DocsExamined *int64 `json:"docsExamined"`
	SampleSize   *int64 `json:"sampleSize"`
	CountsHidden bool   `json:"countsHidden"`
}

// provenance is the provenance of an answer as it may be shown. "sampleSize" is the number of records behind a source's figures in a
// window, "docsReturned" the number of groups it made and "docsExamined" the size of the scan: next to the listed groups, the first minus
// their counts is the count of a hidden group (and a small selection would show itself whole). They stay with the window, the mode,
// completeness and timing, which say how far to trust the answer and give no count.
func (b *Bridge) provenance(p []audit.Provenance) any {
	if !b.suppressing() {
		return p
	}
	out := make([]provenanceView, len(p))
	for i, e := range p {
		out[i] = provenanceView{Provenance: e, CountsHidden: true}
	}
	return out
}

// hiddenCount is the text that stands for a count the host may not see.
func (b *Bridge) hiddenCount() string { return fmt.Sprintf("<%d", b.minCohort) }

// hiddenGroups is how many groups have a hidden current count and what they hold together. A breakdown reports the events of the whole
// selection, so the host could subtract the groups it was shown and read this sum.
func (b *Bridge) hiddenGroups(result *audit.Analysis) (k int, sum int64) {
	for _, g := range result.APIs {
		if b.small(g.CurrentEvents) {
			k, sum = k+1, sum+g.CurrentEvents
		}
	}
	return k, sum
}

// valuesRange is what the host can tell about one of k hidden counts of 1 to 4 that add up to sum: the lowest and highest it can be.
func valuesRange(k int, sum int64) (lo, hi int64) {
	lo = max(1, sum-4*int64(k-1))
	hi = min(4, sum-int64(k-1))
	return lo, hi
}

// breakdownAllowed says whether a breakdown of the comparison may be shown. The hidden groups are named in the groups the host was
// shown, and the breakdown counts the whole selection, so total minus the shown groups is the sum of the hidden ones:
//   - 1 to 4 events: the count of a hidden group (or of a few);
//   - a sum that leaves a hidden group a single possible value (k groups holding k, or 4k): every one is known;
//   - the status code and the polling type split the groups by outcome and kind, so their rows give the sum of just those groups: no
//     hidden group is allowed at all. (hourOfDay is not offered to hosts; uiVersion, userId and userAgent cut across groups.)
func (b *Bridge) breakdownAllowed(result *audit.Analysis, dim string) bool {
	k, sum := b.hiddenGroups(result)
	if k == 0 {
		return true
	}
	if b.small(sum) || dim == audit.BreakdownStatusCode || dim == audit.BreakdownPollingType {
		return false
	}
	lo, hi := valuesRange(k, sum)
	return hi > lo
}

// breakdownHidden is the refusal for a breakdown that would give away the size of a hidden group.
func (b *Bridge) breakdownHidden() error {
	return codedWire(CodeInvalidArguments, WirePermissionRequired,
		fmt.Sprintf("A breakdown of this comparison would reveal the size of a group of fewer than %d events. Run audit_compare on a wider window or with fewer filters, or ask the person for evidence access.", b.minCohort))
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
		o := *out.Other
		o.Values = 0 // "values" equal to "count" would say every value in Other had one request
		return list, o, out.Total
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
	merged.Stats, merged.Filter, merged.Values = audit.Stats{}, nil, 0
	return list, merged, out.Total
}

// groupHidden is the refusal for a timeline of a group that rests on a small count. The text repeats nothing the host sent.
func (b *Bridge) groupHidden() error {
	return codedWire(CodeInvalidArguments, WirePermissionRequired,
		fmt.Sprintf("That group has fewer than %d events in a period (or the selection does), so this host gets no timeline for it. Ask the person for evidence access, or run audit_compare on a wider window.", b.minCohort))
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
		var small, observed []bool
		points := map[int64]audit.Point{}
		for _, series := range result.Timeline {
			if series.Key == g.Key {
				for _, p := range series.Points {
					points[p.At.UnixNano()] = p
				}
				break
			}
		}
		for _, slot := range slots {
			values, _ := slot["groups"].([]map[string]any)
			if gi >= len(values) {
				continue
			}
			at, _ := slot["at"].(time.Time)
			p, has := points[at.UnixNano()]
			n, _ := values[gi]["count"].(int64)
			cells, counts, small, observed = append(cells, values[gi]), append(counts, n), append(small, b.slotSmall(p)), append(observed, has)
		}
		hide, ok := b.maskPlan(g.Current.Count, counts, small, observed, len(cells))
		if !ok {
			return nil, b.groupHidden()
		}
		for i, h := range hide {
			if h {
				// the cell of a slot with no observation, whatever it held
				cells[i]["count"], cells[i]["sampleStatus"] = int64(0), "missing"
				cells[i]["currentP95Ms"], cells[i]["increaseMs"], cells[i]["increasePercent"] = nil, nil, nil
			}
		}
	}
	return m, nil
}
