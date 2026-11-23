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

func shownPoints(t *testing.T, b *Bridge, total int64, counts ...int64) (shown []int64, hidden int) {
	t.Helper()
	g := audit.Comparison{Current: audit.Stats{Count: total}}
	for _, p := range b.points(g, pts(counts...)).([]any) {
		switch v := p.(type) {
		case audit.Point:
			shown = append(shown, v.Count)
		case map[string]any:
			if v["count"] != "<5" || v["p95"] != nil {
				t.Errorf("a hidden slot carries %v", v)
			}
			hidden++
		}
	}
	return shown, hidden
}

// The slots of a timeline plus the total of the group must not give the count of one hidden slot.
func TestTimelineSlotsCannotBeSubtracted(t *testing.T) {
	b := newBridge(nil, nil, WithMinCohort(5))
	// one small slot among shown ones: total minus the shown slots would be 3, so a shown slot is hidden as well
	shown, hidden := shownPoints(t, b, 3+5+7+9, 5, 3, 7, 9)
	var sum int64
	for _, c := range shown {
		sum += c
	}
	if hidden != 2 || 24-sum < 5 {
		t.Errorf("hidden %d, the hidden slots hold %d: %v", hidden, 24-sum, shown)
	}
	// two small slots that already add to five or more are left alone
	if _, hidden := shownPoints(t, b, 3+4+8, 3, 4, 8); hidden != 2 {
		t.Errorf("hidden %d", hidden)
	}
	// nothing small: nothing hidden
	if _, hidden := shownPoints(t, b, 20, 10, 10); hidden != 0 {
		t.Errorf("hidden %d", hidden)
	}
	// every combination of up to six slots of 0 to 8 requests: the hidden slots always hold zero or at least five between them
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
			shown, _ := shownPoints(t, b, total, prefix...)
			var sum int64
			for _, c := range shown {
				sum += c
			}
			if rest := total - sum; rest != 0 && rest < 5 {
				t.Fatalf("slots %v: the hidden slots hold %d (shown %v)", prefix, rest, shown)
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

// The breakdown counts the whole selection: with the groups it was shown, the host could subtract and read a hidden group.
func TestBreakdownIsRefusedWhenItWouldGiveAHiddenGroupsCount(t *testing.T) {
	bd := audit.Breakdown{Dim: "statusCode", Total: 203, Rows: []audit.BreakdownRow{row("200", 203)}}
	big := audit.Comparison{Key: "big", CurrentEvents: 200}
	one := audit.Comparison{Key: "one", CurrentEvents: 3}
	two := audit.Comparison{Key: "two", CurrentEvents: 4}
	call := func(groups ...audit.Comparison) (map[string]any, error) {
		out, err := bridgeWithResult(t, bd, groups...).call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"r","dim":"statusCode"}`))
		if err != nil {
			return nil, err
		}
		return asJSON(t, out), nil
	}
	if _, err := call(big, one); err == nil || !strings.Contains(err.Error(), "fewer than 5") || strings.Contains(err.Error(), "203") {
		t.Errorf("a hidden group of 3: %v", err)
	}
	if _, err := call(big, one, two); err != nil { // together they hold 7: the host learns 7, not 3 or 4
		t.Errorf("two hidden groups of 3 and 4: %v", err)
	}
	if _, err := call(big); err != nil {
		t.Errorf("no hidden group: %v", err)
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
