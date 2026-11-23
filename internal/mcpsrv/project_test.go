package mcpsrv

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// BL-348 slice 2 tests: tier projection and small-count suppression on the real path (HTTP, SQLite, the real dashboard handler over the
// demo repository). The demo has the same number of events in each group, so these tests sweep the window and the filters to get every size,
// and compare an aggregate host with an evidence host (no suppression) on the same query: what the aggregate host sees is either the
// evidence host's figures, unchanged, or a suppressed view of them.

func (r *rig) pair(tag string) (agg, ev host) {
	return r.scoped("agg-"+tag, TierAggregate, "demo-west"), r.scoped("ev-"+tag, TierEvidence, "demo-west")
}

func (r *rig) queryFor(win time.Duration, uv []string, bucket int) map[string]any {
	return map[string]any{"query": audit.Query{SourceIDs: []string{"demo-west"}, UIVersions: uv, Start: r.now.Add(-win), End: r.now,
		BaselineStart: r.now.Add(-2 * win), BaselineEnd: r.now.Add(-win), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: bucket}}
}

var sweep = []struct {
	win time.Duration
	uv  []string
}{
	{10 * time.Second, nil}, {10 * time.Second, []string{"demo-1.1"}}, {15 * time.Second, nil}, {15 * time.Second, []string{"demo-1.0"}},
	{30 * time.Second, nil}, {30 * time.Second, []string{"demo-1.0"}}, {time.Minute, []string{"demo-2.0"}}, {time.Minute, nil},
	{2 * time.Minute, []string{"demo-1.0"}}, {2 * time.Minute, []string{"demo-1.1"}}, {2 * time.Minute, nil}, {5 * time.Minute, nil}, {time.Hour, nil},
}

func isSmall(v any) bool { n, ok := v.(float64); return ok && n > 0 && n < MinCohort }

// every number in the four count fields of a group, in any shape of the answer
func groupCountsOf(g map[string]any) (cur, ref, curStat, refStat any) {
	c, _ := g["current"].(map[string]any)
	rf, _ := g["reference"].(map[string]any)
	return g["currentEvents"], g["referenceEvents"], c["count"], rf["count"]
}

func TestAggregateGroupsAreEitherTheEvidenceFiguresOrSuppressed(t *testing.T) {
	r := newRig(t)
	agg, ev := r.pair("g")
	var sawSuppressed, sawShown, sawNarrow bool
	for _, s := range sweep {
		args := r.queryFor(s.win, s.uv, 60)
		a, e := r.call(agg, "audit_compare", args), r.call(ev, "audit_compare", args)
		if !a.OK || a.IsError || !e.OK || e.IsError {
			t.Fatalf("%v %v: %.300s | %.300s", s.win, s.uv, a.Raw, e.Raw)
		}
		truth := map[string]map[string]any{}
		var total float64
		for _, g := range e.env["groups"].([]any) {
			m := g.(map[string]any)
			truth[m["key"].(string)] = m
			total += m["currentEvents"].(float64)
		}
		narrow := total > 0 && total < MinCohort
		sup, _ := a.env["suppression"].(map[string]any)
		if sup == nil || sup["narrow"] != narrow {
			t.Errorf("%v %v: suppression %v, narrow should be %v", s.win, s.uv, sup, narrow)
		}
		shown := a.env["groups"].([]any)
		if narrow {
			sawNarrow = true
			if len(shown) != 0 {
				t.Errorf("%v %v: a selection of %v events lists %d groups", s.win, s.uv, total, len(shown))
			}
			if rec := a.env["recordedEvents"].(map[string]any); rec["current"] != "<5" {
				t.Errorf("recordedEvents of a narrow selection: %v", rec)
			}
			continue
		}
		for _, g := range shown {
			m := g.(map[string]any)
			want := truth[m["key"].(string)]
			c1, c2, c3, c4 := groupCountsOf(want)
			small := isSmall(c1) || isSmall(c2) || isSmall(c3) || isSmall(c4)
			if small {
				sawSuppressed = true
				if m["suppressed"] != true || len(m) != 10 {
					t.Errorf("%v %v: a small group is not reduced: %v", s.win, s.uv, m)
				}
				for _, f := range []string{"currentEvents", "referenceEvents"} {
					if isSmall(m[f]) {
						t.Errorf("%v %v: %s is the number %v", s.win, s.uv, f, m[f])
					}
					if w := want[f].(float64); w > 0 && w < MinCohort && m[f] != "<5" {
						t.Errorf("%v %v: %s is %v, want \"<5\"", s.win, s.uv, f, m[f])
					}
				}
				continue
			}
			sawShown = true
			if m["suppressed"] != nil || fmt.Sprint(m) != fmt.Sprint(want) {
				t.Errorf("%v %v: a group of enough events differs from the evidence view:\n%v\n%v", s.win, s.uv, m, want)
			}
		}
		// the paged read (audit_get_groups) is projected the same way
		id := a.env["resultId"].(string)
		pg := r.call(agg, "audit_get_groups", map[string]any{"resultId": id})
		if string(mustJSON(pg.env["groups"])) != string(mustJSON(a.env["groups"])) {
			t.Errorf("%v %v: audit_get_groups differs from audit_compare", s.win, s.uv)
		}
	}
	if !sawSuppressed || !sawShown || !sawNarrow {
		t.Errorf("the sweep did not reach every case: suppressed %v shown %v narrow %v", sawSuppressed, sawShown, sawNarrow)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// The totals leave out the groups that are suppressed, and nothing the aggregate host can add up gives a hidden group's count.
func TestAggregateTotalsLeaveOutHiddenGroups(t *testing.T) {
	r := newRig(t)
	agg, _ := r.pair("t")
	a := r.call(agg, "audit_compare", r.queryFor(15*time.Second, nil, 60)) // every group has 2 events in each period
	rec := a.env["recordedEvents"].(map[string]any)
	if rec["current"] != float64(0) || rec["reference"] != float64(0) {
		t.Errorf("hidden groups are in the totals: %v", rec)
	}
	if strings.Contains(string(a.Raw), `"p95Ratio"`) || strings.Contains(string(a.Raw), `"histogram"`) {
		t.Errorf("a figure of a hidden group is in the answer: %.600s", a.Raw)
	}
}

// A group that is hidden has no timeline, and the refusal repeats nothing the host sent.
func TestHiddenGroupsHaveNoTimeline(t *testing.T) {
	r := newRig(t)
	agg, ev := r.pair("tl")
	args := r.queryFor(15*time.Second, nil, 10)
	a := r.call(agg, "audit_compare", args)
	id := a.env["resultId"].(string)
	key := a.env["groups"].([]any)[0].(map[string]any)["key"].(string)
	for tool, in := range map[string]map[string]any{
		"audit_get_timeline": {"resultId": id, "apiKey": key}, "audit_compare_timelines": {"resultId": id, "apiKeys": []string{key}},
	} {
		got := r.call(agg, tool, in)
		if !got.OK || !got.IsError || errCode(got.env) != "permission_required" || strings.Contains(string(got.Raw), key) {
			t.Errorf("%s: %.300s", tool, got.Raw)
		}
	}
	// the evidence host still reads it
	e := r.call(ev, "audit_compare", args)
	ek := e.env["groups"].([]any)[0].(map[string]any)["key"].(string)
	if got := r.call(ev, "audit_get_timeline", map[string]any{"resultId": e.env["resultId"], "apiKey": ek}); !got.OK || got.IsError {
		t.Errorf("evidence timeline: %.300s", got.Raw)
	}
}

// Slots of a timeline that rest on fewer than five requests show "<5" and no timings; slots of enough requests are the evidence figures.
func TestTimelineSlotsBelowTheMinimumAreSuppressed(t *testing.T) {
	r := newRig(t)
	agg, ev := r.pair("pt")
	args := r.queryFor(time.Hour, nil, 30) // 250 events per group in 60 slots of 30 s: slots hold 4 or 5 (demo events come every 14.4 s)
	a, e := r.call(agg, "audit_compare", args), r.call(ev, "audit_compare", args)
	ak, ek := a.env["groups"].([]any)[0].(map[string]any), e.env["groups"].([]any)[0].(map[string]any)
	if ak["suppressed"] != nil {
		t.Fatalf("the group is itself small: %v", ak)
	}
	at := r.call(agg, "audit_get_timeline", map[string]any{"resultId": a.env["resultId"], "apiKey": ak["key"]})
	et := r.call(ev, "audit_get_timeline", map[string]any{"resultId": e.env["resultId"], "apiKey": ek["key"]})
	if !at.OK || at.IsError || !et.OK || et.IsError {
		t.Fatalf("%.300s | %.300s", at.Raw, et.Raw)
	}
	truth := map[string]map[string]any{}
	for _, p := range et.env["points"].([]any) {
		truth[p.(map[string]any)["at"].(string)] = p.(map[string]any)
	}
	var small, kept int
	for _, p := range at.env["points"].([]any) {
		m := p.(map[string]any)
		want := truth[m["at"].(string)]
		if isSmall(want["count"]) {
			small++
			if m["count"] != "<5" || m["suppressed"] != true || m["p95"] != nil {
				t.Errorf("a slot of %v requests: %v", want["count"], m)
			}
		} else {
			kept++
			if m["count"] != want["count"] {
				t.Errorf("a slot of enough requests changed: %v vs %v", m, want)
			}
		}
	}
	if small == 0 || kept == 0 {
		t.Errorf("the window holds no mix of slots: %d small, %d kept", small, kept)
	}
	// the same in the comparison of several groups
	ct := r.call(agg, "audit_compare_timelines", map[string]any{"resultId": a.env["resultId"], "apiKeys": []string{ak["key"].(string)}})
	if !ct.OK || ct.IsError {
		t.Fatalf("%.300s", ct.Raw)
	}
	for _, s := range ct.env["slots"].([]any) {
		for _, v := range s.(map[string]any)["groups"].([]any) {
			if isSmall(v.(map[string]any)["count"]) {
				t.Errorf("a slot count of 1 to 4 is shown: %v", v)
			}
		}
	}
}

// Breakdown: rows and Other are never small, and the rows still add up to the total the host is shown.
func TestBreakdownRowsBelowTheMinimumAreFolded(t *testing.T) {
	r := newRig(t)
	agg, ev := r.pair("bd")
	var sawFold, sawNarrow bool
	for _, s := range sweep {
		args := r.queryFor(s.win, s.uv, 60)
		a, e := r.call(agg, "audit_compare", args), r.call(ev, "audit_compare", args)
		if len(e.env["groups"].([]any)) == 0 {
			continue
		}
		for _, dim := range []string{"statusCode", "uiVersion", "pollingType", "userId", "userAgent"} {
			ab := r.call(agg, "audit_get_breakdown", map[string]any{"resultId": a.env["resultId"], "dim": dim})
			eb := r.call(ev, "audit_get_breakdown", map[string]any{"resultId": e.env["resultId"], "dim": dim})
			if !eb.OK || eb.IsError {
				t.Fatalf("%v %s: %.300s", s.win, dim, eb.Raw)
			}
			etotal := eb.env["total"].(float64)
			if etotal > 0 && etotal < MinCohort {
				sawNarrow = true
				// the whole selection is small: the breakdown would give the size of the hidden groups, so it is refused
				if !ab.IsError || errCode(ab.env) != "permission_required" || strings.Contains(string(ab.Raw), `"rows"`) {
					t.Errorf("%v %s: a selection of %v requests: %.400s", s.win, dim, etotal, ab.Raw)
				}
				continue
			}
			if !ab.OK || ab.IsError {
				t.Fatalf("%v %s: %.300s", s.win, dim, ab.Raw)
			}
			var sum float64
			for _, row := range ab.env["rows"].([]any) {
				c := row.(map[string]any)["count"].(float64)
				if isSmall(c) {
					t.Errorf("%v %s: a row of %v is listed", s.win, dim, c)
				}
				sum += c
			}
			if o, ok := ab.env["other"].(map[string]any); ok {
				c := o["count"].(float64)
				if isSmall(c) {
					t.Errorf("%v %s: Other is %v", s.win, dim, c)
				}
				sum += c
				if len(ab.env["rows"].([]any)) < len(eb.env["rows"].([]any)) {
					sawFold = true
				}
			}
			if sum != ab.env["total"].(float64) || ab.env["total"] != eb.env["total"] {
				t.Errorf("%v %s: rows and Other add up to %v, total %v (evidence %v)", s.win, dim, sum, ab.env["total"], eb.env["total"])
			}
		}
	}
	if !sawFold || !sawNarrow {
		t.Errorf("the sweep did not reach every case: fold %v narrow %v", sawFold, sawNarrow)
	}
}

// Without the identifiers grant no row carries the audit record id; with it, it does. The evidence tools themselves are the same.
func TestRecordIDsFollowTheIdentifiersGrant(t *testing.T) {
	r := newRig(t)
	without := r.scoped("rid-no", TierEvidence, "demo-west")
	ap := r.approval(TierEvidence)
	ap.Sources, ap.Identifiers = []string{"demo-west"}, true
	with := r.enroll("rid-yes", ap)
	for name, h := range map[string]host{"without": without, "with": with} {
		c := r.call(h, "audit_compare", r.queryFor(5*time.Minute, nil, 60))
		id := c.env["resultId"].(string)
		key := c.env["groups"].([]any)[0].(map[string]any)["key"].(string)
		for tool, args := range map[string]map[string]any{
			"audit_get_requests": {"resultId": id, "apiKey": key, "limit": 5}, "audit_get_slowest": {"resultId": id, "limit": 5},
		} {
			a := r.call(h, tool, args)
			rows, _ := a.env["rows"].([]any)
			if !a.OK || a.IsError || len(rows) == 0 {
				t.Fatalf("%s %s: %.300s", name, tool, a.Raw)
			}
			for _, row := range rows {
				_, has := row.(map[string]any)["id"]
				if has != (name == "with") {
					t.Errorf("%s %s: id present %v", name, tool, has)
				}
			}
		}
	}
}

// An evidence host is not suppressed: the same small selection gives it every figure.
func TestEvidenceTierIsNotSuppressed(t *testing.T) {
	r := newRig(t)
	_, ev := r.pair("ne")
	a := r.call(ev, "audit_compare", r.queryFor(10*time.Second, nil, 60))
	gs := a.env["groups"].([]any)
	if len(gs) == 0 || a.env["suppression"] != nil || gs[0].(map[string]any)["suppressed"] != nil || gs[0].(map[string]any)["currentEvents"] == "<5" {
		t.Errorf("%.400s", a.Raw)
	}
}
