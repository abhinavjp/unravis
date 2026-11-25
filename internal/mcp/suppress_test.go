package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

func row(v string, count int64) audit.BreakdownRow {
	return audit.BreakdownRow{Value: v, Count: count, Complete: count, Stats: audit.Stats{Count: count}}
}

func asJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal(string(raw))
	}
	return m
}

// The rows of a breakdown: below the minimum goes into Other, Other is never itself small, and rows plus Other still add up to the total.
func TestBreakdownFoldsSmallRowsAndKeepsTheSum(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	for name, tc := range map[string]struct {
		rows      []audit.BreakdownRow
		other     *audit.BreakdownRow
		wantRows  int
		wantOther int64 // 0: no Other row
		wantTotal any
	}{
		"nothing small":            {[]audit.BreakdownRow{row("a", 50), row("b", 20)}, nil, 2, 0, int64(70)},
		"edge: exactly five stays": {[]audit.BreakdownRow{row("a", 50), row("b", 5)}, nil, 2, 0, int64(55)},
		"small rows join Other":    {[]audit.BreakdownRow{row("a", 50), row("b", 4), row("c", 3)}, nil, 1, 7, int64(57)},
		"Other small and alone":    {[]audit.BreakdownRow{row("a", 50), row("b", 40)}, &audit.BreakdownRow{Count: 2}, 1, 42, int64(92)},
		"small rows and Other":     {[]audit.BreakdownRow{row("a", 50), row("b", 30), row("c", 1)}, &audit.BreakdownRow{Count: 10}, 2, 11, int64(91)},
		"all small, total fine":    {[]audit.BreakdownRow{row("a", 3), row("b", 3)}, nil, 0, 6, int64(6)},
		"total small":              {[]audit.BreakdownRow{row("a", 3)}, nil, 0, 0, "<5"},
	} {
		var total int64
		for _, r := range tc.rows {
			total += r.Count
		}
		if tc.other != nil {
			total += tc.other.Count
		}
		rows, other, gotTotal := b.shownBreakdown(audit.Breakdown{Rows: tc.rows, Other: tc.other, Total: total})
		if len(rows) != tc.wantRows || gotTotal != tc.wantTotal {
			t.Errorf("%s: %d rows, total %v: %v", name, len(rows), gotTotal, rows)
		}
		var shown int64
		for _, r := range rows {
			c := r.(audit.BreakdownRow).Count
			if c < 5 {
				t.Errorf("%s: a row of %d is listed", name, c)
			}
			shown += c
		}
		switch o := other.(type) {
		case nil:
			if tc.wantOther != 0 {
				t.Errorf("%s: no Other row", name)
			}
		case audit.BreakdownRow:
			if o.Count != tc.wantOther || o.Count < 5 || o.Stats.Count != 0 || o.Filter != nil {
				t.Errorf("%s: other %+v", name, o)
			}
			shown += o.Count
		default:
			t.Errorf("%s: other %T", name, other)
		}
		if n, ok := gotTotal.(int64); ok && shown != n {
			t.Errorf("%s: rows and Other add up to %d, the total says %d", name, shown, n)
		}
	}
	// without suppression nothing changes
	rows, other, total := newBridge(nil, nil).shownBreakdown(audit.Breakdown{Rows: []audit.BreakdownRow{row("a", 1)}, Other: &audit.BreakdownRow{Count: 2}, Total: 3})
	if len(rows) != 1 || other.(audit.BreakdownRow).Count != 2 || total != int64(3) {
		t.Error("an unscoped bridge changed a breakdown")
	}
}

// A group that rests on a count of 1 to 4 in either period shows names, state and counts only; zero is not small.
func TestSmallGroupsAreAllowListed(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	big := audit.Comparison{Key: "k1", Controller: "C", CurrentEvents: 100, ReferenceEvents: 90, Current: audit.Stats{Count: 100}, Reference: audit.Stats{Count: 90}}
	smallRef := audit.Comparison{Key: "k2", Controller: "C", State: "compared", CurrentEvents: 100, ReferenceEvents: 3, Current: audit.Stats{Count: 100}, Reference: audit.Stats{Count: 3},
		P95Ratio: ptr(1.5), P95DeltaMs: ptr(20), TotalTimeMs: 12345}
	fewTimings := audit.Comparison{Key: "k3", CurrentEvents: 100, ReferenceEvents: 100, Current: audit.Stats{Count: 2, MeanMs: ptr(5)}, Reference: audit.Stats{Count: 100}}
	zero := audit.Comparison{Key: "k4", State: "missing_current", CurrentEvents: 0, ReferenceEvents: 80, Reference: audit.Stats{Count: 80}}
	res := &audit.Analysis{APIs: []audit.Comparison{big, smallRef, fewTimings, zero}}
	got := b.groups(res, res.APIs).([]any)
	if len(got) != 4 {
		t.Fatalf("%d groups", len(got))
	}
	if _, ok := got[0].(audit.Comparison); !ok {
		t.Errorf("a big group was changed: %T", got[0])
	}
	if _, ok := got[3].(audit.Comparison); !ok {
		t.Errorf("a group with a zero count was treated as small: %T", got[3])
	}
	for _, i := range []int{1, 2} {
		m, ok := got[i].(map[string]any)
		if !ok || m["suppressed"] != true {
			t.Fatalf("group %d: %#v", i, got[i])
		}
		for k := range m {
			switch k {
			case "key", "sourceId", "controller", "action", "kind", "outcome", "state", "currentEvents", "referenceEvents", "suppressed":
			default:
				t.Errorf("group %d carries %q", i, k)
			}
		}
	}
	m := got[1].(map[string]any)
	if m["currentEvents"] != int64(100) || m["referenceEvents"] != "<5" {
		t.Errorf("counts: %v %v", m["currentEvents"], m["referenceEvents"])
	}
	if raw, _ := json.Marshal(got[1]); strings.Contains(string(raw), "12345") || strings.Contains(string(raw), "1.5") {
		t.Errorf("a figure built on the small count is shown: %s", raw)
	}
	// the selection as a whole is small: nothing is listed
	tiny := &audit.Analysis{APIs: []audit.Comparison{{Key: "only", CurrentEvents: 3, Current: audit.Stats{Count: 3}}}}
	if g := b.groups(tiny, tiny.APIs).([]any); len(g) != 0 {
		t.Errorf("a selection of 3 events lists %v", g)
	}
	if !b.hiddenGroup(tiny, tiny.APIs[0]) || b.hiddenGroup(res, big) || !b.hiddenGroup(res, smallRef) {
		t.Error("hiddenGroup")
	}
	// totals leave out the groups that are hidden, so total minus the listed groups cannot give a hidden group's count
	q := audit.Query{}
	if rec := b.recorded(res, q); rec["current"] != int64(100+0) || rec["reference"] != int64(90+80) {
		t.Errorf("recorded: %v", rec)
	}
	if rec := b.recorded(tiny, q); rec["current"] != "<5" {
		t.Errorf("recorded narrow: %v", rec)
	}
}

func ptr(v float64) *float64 { return &v }

// ---- differencing: what the host can work out by subtracting what it was shown ----

func pts(counts ...int64) []audit.Point {
	out := make([]audit.Point, len(counts))
	for i, c := range counts {
		out[i] = audit.Point{Count: c}
	}
	return out
}

func gridQuery(n int) audit.Query {
	t0 := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	return audit.Query{Start: t0, End: t0.Add(time.Duration(n) * 10 * time.Second), BucketSeconds: 10}
}

// shownPoints runs the slots of a group of the given total through the bridge. ok false means no timeline is shown.
func shownPoints(t *testing.T, b *Bridge, total int64, counts ...int64) (shown []audit.Point, ok bool) {
	t.Helper()
	g := audit.Comparison{Current: audit.Stats{Count: total}}
	// a slot of 0 is a position with no observation: it is not in the list of points the dashboard gives, only in the grid
	var observed []audit.Point
	for _, p := range pts(counts...) {
		if p.Count > 0 {
			observed = append(observed, p)
		}
	}
	list, ok := b.points(g, gridQuery(len(counts)), observed)
	if !ok {
		return nil, false
	}
	p, isPoints := list.([]audit.Point)
	if !isPoints {
		t.Fatalf("points are %T: a hidden slot must not be a marker of another type", list)
	}
	return p, true
}

// What the host can work out from a timeline: the sum of the slots that are not shown (the group's own count minus the shown slots) and
// how many positions that sum can sit in (the slots it cannot see a count for). Review 1 of #266 recovered 36 exact counts on the demo
// from the number of "<5" marks and that sum; there are no marks now, so the number of hidden slots is unknown, and the sum must leave
// every position at least two possible values.
func TestTimelineHiddenSlotsCannotBeCountedOrSubtracted(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	// the reviewer's two cases: all ones, and all fours
	if shown, ok := shownPoints(t, b, 6, 1, 1, 1, 1, 1, 1); !ok || len(shown) != 0 {
		t.Errorf("six slots of 1: ok %v shown %v", ok, shown)
	}
	if _, ok := shownPoints(t, b, 4+4+100, 4, 4, 100); ok {
		t.Error("two slots of 4 beside one of 100: the two positions left would each hold exactly 4, no timeline may be shown")
	}
	if _, ok := shownPoints(t, b, 4*6, 4, 4, 4, 4, 4, 4); ok {
		t.Error("six slots of 4 and nothing else: every position would hold exactly 4")
	}
	// a hidden slot leaves no trace: the list holds only the slots that are shown, untouched
	shown, ok := shownPoints(t, b, 3+5+7+9, 5, 3, 7, 9, 0, 0)
	withData := 0
	for _, p := range shown {
		if p.Count > 0 {
			withData++
		}
	}
	if !ok || withData != 2 { // 3 is small; 5 is hidden with it, so the hidden slots hold 8, not 3
		t.Fatalf("ok %v shown %v", ok, shown)
	}
	// nothing small: nothing hidden
	if shown, ok := shownPoints(t, b, 20, 10, 10); !ok || len(shown) != 2 {
		t.Errorf("ok %v shown %v", ok, shown)
	}
	// every combination of up to five slots of 0 to 8 requests, with an 8-position window
	var try func(prefix []int64)
	try = func(prefix []int64) {
		if len(prefix) > 0 {
			var total int64
			for _, c := range prefix {
				total += c
			}
			if total < 5 {
				return // a group of fewer than five has no timeline at all (hiddenGroup)
			}
			counts := append(append([]int64(nil), prefix...), 0, 0, 0)[:len(prefix)+3]
			shown, ok := shownPoints(t, b, total, counts...)
			if !ok {
				return
			}
			var sum int64
			seen := 0
			for _, p := range shown {
				if p.Count == 0 {
					continue
				}
				seen++
				sum += p.Count
				if p.Count < 5 {
					t.Fatalf("slots %v: a slot of %d is shown", prefix, p.Count)
				}
			}
			rest := total - sum
			positions := int64(len(counts) - seen) // the slots the host sees no count for
			if rest != 0 && (rest < 5 || rest >= 4*positions) {
				t.Fatalf("slots %v: the hidden slots hold %d among %d positions: a position is known (shown %v)", prefix, rest, positions, shown)
			}
		}
		if len(prefix) < 5 {
			for c := int64(0); c <= 8; c++ {
				try(append(append([]int64(nil), prefix...), c))
			}
		}
	}
	try(nil)
}

// A slot with no valid timing but 1 to 4 failed or unfinished requests is a small slot too.
func TestSlotWithOnlyFailuresIsSmall(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	list, ok := b.points(audit.Comparison{Current: audit.Stats{Count: 30}}, gridQuery(4), []audit.Point{{Count: 10}, {Count: 0, Count5xx: 2}, {Count: 0, Incomplete: 3}, {Count: 20, Count5xx: 1}})
	if !ok {
		t.Fatal("no timeline")
	}
	for _, p := range list.([]audit.Point) {
		if p.Count == 0 {
			t.Errorf("a slot with no timing but a small failure count is shown: %+v", p)
		}
	}
	if n := len(list.([]audit.Point)); n != 2 {
		t.Errorf("%d slots shown", n)
	}
}

// A fake dashboard for the breakdown route, to read the bridge's own answers over a cached result.
func bridgeWithResult(t *testing.T, bd audit.Breakdown, groups ...audit.Comparison) *Bridge {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/breakdown" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(bd)
	})
	b := NewLocal(h, "tok-0123456789abcdef0123456789abcdef", WithMinCohort(5), WithSources([]string{"s"}))
	b.result = &audit.Analysis{Query: audit.Query{SourceIDs: []string{"s"}}, APIs: groups}
	b.resultID, b.created = "r", time.Now()
	return b
}

// The breakdown counts the whole selection: with the groups it was shown, the host could subtract and read the hidden groups.
func TestBreakdownIsRefusedWhenItWouldGiveHiddenGroupsAway(t *testing.T) {
	big := audit.Comparison{Key: "big", CurrentEvents: 200}
	hidden := func(counts ...int64) []audit.Comparison {
		out := []audit.Comparison{big}
		for i, c := range counts {
			out = append(out, audit.Comparison{Key: fmt.Sprint("h", i), CurrentEvents: c})
		}
		return out
	}
	ones := func(k int) []int64 {
		out := make([]int64, k)
		for i := range out {
			out[i] = 1
		}
		return out
	}
	fours := func(k int) []int64 {
		out := ones(k)
		for i := range out {
			out[i] = 4
		}
		return out
	}
	sum := func(c []int64) (n int64) {
		for _, x := range c {
			n += x
		}
		return n
	}
	for name, tc := range map[string]struct {
		hidden  []int64
		dim     string
		allowed bool
	}{
		"nothing hidden":                   {nil, "statusCode", true},
		"one group of 3":                   {[]int64{3}, "uiVersion", false},
		"two groups holding 7 (3 and 4)":   {[]int64{3, 4}, "uiVersion", true},
		"two groups holding 3 (1 and 2)":   {[]int64{1, 2}, "uiVersion", false}, // the sum is 1 to 4
		"five groups of 1 (the reviewer)":  {ones(5), "uiVersion", false},
		"five groups of 4":                 {fours(5), "userId", false},
		"six groups holding 9":             {[]int64{1, 2, 1, 2, 2, 1}, "userAgent", true},
		"three groups holding 7":           {[]int64{2, 2, 3}, "uiVersion", true},
		"hidden groups, status code rows":  {[]int64{3, 4}, "statusCode", false},
		"hidden groups, polling type rows": {[]int64{3, 4}, "pollingType", false},
		"hidden groups, user id rows":      {[]int64{3, 4}, "userId", true},
	} {
		groups := hidden(tc.hidden...)
		bd := audit.Breakdown{Dim: tc.dim, Total: 200 + sum(tc.hidden), Rows: []audit.BreakdownRow{row("x", 200+sum(tc.hidden))}}
		_, err := bridgeWithResult(t, bd, groups...).call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"r","dim":"`+tc.dim+`"}`))
		if (err == nil) != tc.allowed {
			t.Errorf("%s: allowed should be %v, got %v", name, tc.allowed, err)
		}
		if err != nil && (!strings.Contains(err.Error(), "fewer than 5") || strings.Contains(err.Error(), "200")) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Whatever the host can subtract, a hidden group must keep at least two possible values.
func TestHiddenGroupsKeepTwoPossibleValues(t *testing.T) {
	for k := 1; k <= 8; k++ {
		for sum := int64(k); sum <= int64(4*k); sum++ {
			lo, hi := valuesRange(k, sum)
			if lo < 1 || hi > 4 || lo > hi {
				t.Fatalf("k %d sum %d: range %d to %d", k, sum, lo, hi)
			}
			exact := hi == lo
			if exact != (k == 1 || sum == int64(k) || sum == int64(4*k)) {
				t.Errorf("k %d sum %d: range %d to %d", k, sum, lo, hi)
			}
		}
	}
}

// The timings of a shown row of fewer than 5 completed requests are dropped, and Other does not say how many values it folded.
func TestBreakdownRowTimingsAndOtherValues(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	r := row("a", 50)
	r.Stats = audit.Stats{Count: 3, MeanMs: ptr(77)}
	other := row("Other", 12)
	other.Values = 12
	rows, o, _ := b.shownBreakdown(audit.Breakdown{Rows: []audit.BreakdownRow{r}, Other: &other, Total: 62})
	if got := rows[0].(audit.BreakdownRow); got.Stats.Count != 0 || got.Stats.MeanMs != nil {
		t.Errorf("timings of 3 completed requests are shown: %+v", got.Stats)
	}
	if o.(audit.BreakdownRow).Values != 0 {
		t.Errorf("Other says how many values it holds: %+v", o)
	}
	// folded rows too
	o2 := row("Other", 2)
	o2.Values = 2
	_, merged, _ := b.shownBreakdown(audit.Breakdown{Rows: []audit.BreakdownRow{row("a", 50), row("b", 4)}, Other: &o2, Total: 56})
	if merged.(audit.BreakdownRow).Values != 0 {
		t.Errorf("a merged Other says how many values it holds: %+v", merged)
	}
}

// Whatever "top" the host asks for, no row or Other below the minimum is shown, rows and Other add up to the total, and the Other of one
// top differs from the Other of another by whole rows of at least the minimum: no pair of answers gives a small count by subtraction.
func TestBreakdownTopsCannotBeSubtracted(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // a fixed seed: the same distributions every run, not a secret
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(9)
		counts := make([]int64, n)
		var total int64
		for i := range counts {
			counts[i] = int64(1 + rng.Intn(12))
			if rng.Intn(3) == 0 {
				counts[i] += int64(rng.Intn(60))
			}
			total += counts[i]
		}
		sort.Slice(counts, func(i, j int) bool { return counts[i] > counts[j] })
		var others []int64
		for top := 0; top <= n; top++ {
			bd := audit.Breakdown{Total: total}
			var rest int64
			for i, c := range counts {
				if i < top {
					bd.Rows = append(bd.Rows, row(fmt.Sprint(i), c))
				} else {
					rest += c
				}
			}
			if rest > 0 {
				o := row("Other", rest)
				bd.Other = &o
			}
			rows, other, got := b.shownBreakdown(bd)
			if got != any(total) && (total >= 5 || got != any("<5")) {
				t.Fatalf("counts %v top %d: total %v", counts, top, got)
			}
			var sum int64
			var r []int64
			for _, x := range rows {
				c := x.(audit.BreakdownRow).Count
				if c < 5 {
					t.Fatalf("counts %v top %d: a row of %d", counts, top, c)
				}
				sum, r = sum+c, append(r, c)
			}
			var oc int64
			if o, ok := other.(audit.BreakdownRow); ok {
				oc = o.Count
				if oc < 5 {
					t.Fatalf("counts %v top %d: Other is %d", counts, top, oc)
				}
			}
			if total >= 5 && sum+oc != total {
				t.Fatalf("counts %v top %d: rows %v and Other %d do not add to %d", counts, top, r, oc, total)
			}
			others = append(others, oc)
		}
		for i := range others {
			for j := range others {
				if d := others[i] - others[j]; d > 0 && d < 5 {
					t.Fatalf("counts %v: the Other rows of two tops differ by %d (%v)", counts, d, others)
				}
			}
		}
	}
}

// Review 2 of #266, M1': a slot with no valid timing but 5 or more failures is shown (count 0), so the host knows that position holds no
// hidden timings. If it were counted as a position the hidden timings could be in, the check would be one position too lenient.
func TestShownSlotWithOnlyFailuresIsAKnownPosition(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	failOnly := audit.Point{Count: 0, Count5xx: 8}
	// grid of 3: [failures only (shown), 4, 4], group count 8: the 8 left over sit in the 2 positions the host cannot see, 4 each
	g := audit.Comparison{Current: audit.Stats{Count: 8}}
	if list, ok := b.points(g, gridQuery(3), []audit.Point{failOnly, {Count: 4}, {Count: 4}}); ok {
		t.Errorf("each hidden slot would be exactly 4, yet a timeline is shown: %v", list)
	}
	// grid of 5: two failure-only slots shown, three slots of 4, group count 12
	g = audit.Comparison{Current: audit.Stats{Count: 12}}
	if list, ok := b.points(g, gridQuery(5), []audit.Point{failOnly, {Count: 4}, failOnly, {Count: 4}, {Count: 4}}); ok {
		t.Errorf("12 over 3 positions: %v", list)
	}
	// one more position that could hold the timings: two values are possible again
	if _, ok := b.points(audit.Comparison{Current: audit.Stats{Count: 8}}, gridQuery(4), []audit.Point{failOnly, {Count: 4}, {Count: 4}}); !ok {
		t.Error("a position the host cannot see anything about was treated as known")
	}
}

// The same rule in a comparison of timelines: when no safe set of cells exists, the whole call is refused, for every key asked about.
func TestCompareTimelinesRefusedWhenNoSafeCellsExist(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	q := gridQuery(3)
	at := func(i int) time.Time { return q.Start.Add(time.Duration(i) * 10 * time.Second) }
	mk := func(total int64, pts ...audit.Point) *audit.Analysis {
		for i := range pts {
			pts[i].At = at(i)
		}
		return &audit.Analysis{
			Query:    q,
			APIs:     []audit.Comparison{{Key: "k", State: "compared", CurrentEvents: total, Current: audit.Stats{Count: total}}},
			Timeline: []audit.Series{{Key: "k", Points: pts}},
		}
	}
	// two hidden slots of exactly 4 beside one with no timing: refused
	if _, err := b.compareTimelines(mk(8, audit.Point{Count5xx: 8}, audit.Point{Count: 4}, audit.Point{Count: 4}), "r", []string{"k"}); err == nil || !strings.Contains(err.Error(), "fewer than 5") {
		t.Errorf("no safe cells: %v", err)
	}
	// plain: three slots of 4 and nothing else, 12 over 3 positions
	if _, err := b.compareTimelines(mk(12, audit.Point{Count: 4}, audit.Point{Count: 4}, audit.Point{Count: 4}), "r", []string{"k"}); err == nil {
		t.Error("12 over 3 positions: every cell would be exactly 4")
	}
	// a safe case is answered, and the hidden cell is a cell of no observation
	wide := mk(60, audit.Point{Count: 25}, audit.Point{Count: 3}, audit.Point{Count: 32})
	wide.Query = gridQuery(10)
	out, err := b.compareTimelines(wide, "r", []string{"k"})
	if err != nil {
		t.Fatal(err)
	}
	cells := out.(map[string]any)["slots"].([]map[string]any)
	hidden := 0
	for _, s := range cells {
		for _, v := range s["groups"].([]map[string]any) {
			if v["count"] == int64(0) && v["sampleStatus"] == "missing" {
				hidden++
			}
		}
	}
	if hidden == 0 {
		t.Errorf("the slot of 3 is still in the comparison: %v", cells)
	}
}
