package mcpsrv

import (
	"api-audit-explorer/internal/mcp"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// BL-350 budget tests, on the real path: HTTP routes, SQLite, the real dashboard handler over the demo repository.

func overBudget(a reply) bool {
	return a.Status == 429 && !a.OK && a.Error != nil && a.Error.Code == "query_budget_exceeded" && a.Error.Action != "" && len(a.Result) == 0
}

func TestCompareRateIsPerHostAndPerHour(t *testing.T) {
	r := newRigBudgets(t, DefaultBudgets)
	a := r.enroll("rate-a", r.approval(TierAggregate))
	b := r.enroll("rate-b", r.approval(TierAggregate))
	for i := 0; i < DefaultBudgets.CompareRate; i++ {
		if c := r.call(a, "audit_compare", r.compareArgs()); !c.OK || c.IsError {
			t.Fatalf("compare %d: %d %.300s", i+1, c.Status, c.Raw)
		}
		r.clk.Add(time.Minute) // twelve in twelve minutes
	}
	n := len(r.dash.hits())
	c := r.call(a, "audit_compare", r.compareArgs())
	if !overBudget(c) || strings.Contains(string(c.Raw), "12") {
		t.Fatalf("the 13th comparison in an hour: %d %s", c.Status, c.Raw)
	}
	if len(r.dash.hits()) != n {
		t.Error("a refused comparison reached the dashboard")
	}
	// cheap tools and other hosts are not affected
	if g := r.call(a, "audit_get_guide", map[string]any{}); !g.OK {
		t.Errorf("guide after the compare limit: %.200s", g.Raw)
	}
	if c := r.call(b, "audit_compare", r.compareArgs()); !c.OK || c.IsError {
		t.Errorf("another host: %.300s", c.Raw)
	}
	// the hour passes: the oldest calls leave the window
	r.clk.Add(time.Hour)
	if c := r.call(a, "audit_compare", r.compareArgs()); !c.OK || c.IsError {
		t.Errorf("after the hour: %d %.300s", c.Status, c.Raw)
	}
	// a new grant of the same host does not reset the rate
	rows := 0
	for _, e := range r.events() {
		if e.Kind == "denied" && e.Code == "query_budget_exceeded" && e.ProfileID == "rate-a" {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("%d denied rows for the refused comparison", rows)
	}
}

func TestLiveRateAndCumulativeBytes(t *testing.T) {
	small := DefaultBudgets
	small.LiveRate, small.CumulativeBytes = 2, 40<<10
	r := newRigBudgets(t, small)
	h := r.enroll("live", r.approval(TierEvidence))
	c := r.call(h, "audit_compare", r.compareArgs())
	id := c.env["resultId"].(string)
	for i := 0; i < 2; i++ {
		if a := r.call(h, "audit_get_slowest", map[string]any{"resultId": id, "limit": 1}); !a.OK || a.IsError {
			t.Fatalf("slowest %d: %.300s", i, a.Raw)
		}
	}
	if a := r.call(h, "audit_get_slowest", map[string]any{"resultId": id, "limit": 1}); !overBudget(a) {
		t.Errorf("third live read: %d %.300s", a.Status, a.Raw)
	}
	// cached tools are not live reads
	if a := r.call(h, "audit_get_groups", map[string]any{"resultId": id}); !a.OK {
		t.Errorf("groups: %.200s", a.Raw)
	}
	// bytes: pull answers until the 40 KiB of the window is used; then every tool is refused until the window passes
	r.clk.Add(2 * time.Hour) // the rate window is over, the byte window too
	var refused bool
	for i := 0; i < 400 && !refused; i++ {
		a := r.call(h, "audit_get_groups", map[string]any{"resultId": id, "limit": 100})
		if a.IsError { // the handle expired with the clock
			c = r.call(h, "audit_compare", r.compareArgs())
			id = c.env["resultId"].(string)
			r.clk.Add(time.Minute)
			continue
		}
		refused = overBudget(a)
	}
	if !refused {
		t.Fatal("the byte budget never ran out")
	}
	if g := r.call(h, "audit_get_guide", map[string]any{}); !overBudget(g) {
		t.Errorf("guide after the byte budget: %.200s", g.Raw)
	}
	r.clk.Add(11 * time.Minute)
	if g := r.call(h, "audit_get_guide", map[string]any{}); !g.OK {
		t.Errorf("guide after the byte window: %.200s", g.Raw)
	}
}

// One comparison at a time per host and two in all.
func TestHeavyCallsAtOnce(t *testing.T) {
	r := newRigBudgets(t, DefaultBudgets)
	a := r.enroll("heavy-a", r.approval(TierAggregate))
	b := r.enroll("heavy-b", r.approval(TierAggregate))
	c := r.enroll("heavy-c", r.approval(TierAggregate))
	releaseA, outA := r.holdCompare(a, "aaaaaaaa-1")
	if x := r.call(a, "audit_compare", r.compareArgs()); !overBudget(x) {
		t.Errorf("a second comparison of the same host: %d %.300s", x.Status, x.Raw)
	}
	// another host may run one; the third host is over the global limit while both run
	releaseB := r.dash.holdPath("/api/analyze") // replaces the hold on the path: A's request is already inside
	outB := make(chan reply, 1)
	go func() {
		outB <- r.callRaw(b.header, map[string]any{"tool": "audit_compare", "args": r.compareArgs(), "callId": "bbbbbbbb-1"})
	}()
	select {
	case <-r.dash.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the second host's comparison never started")
	}
	third := make(chan reply, 1)
	go func() { third <- r.callRaw(c.header, map[string]any{"tool": "audit_compare", "args": r.compareArgs()}) }()
	select {
	case x := <-third:
		if !overBudget(x) {
			t.Errorf("a third comparison in all: %d %.300s", x.Status, x.Raw)
		}
	case <-time.After(3 * time.Second):
		t.Error("a third comparison in all was not refused (it is waiting on the dashboard)")
	}
	close(releaseA)
	close(releaseB)
	if got := waitReply(t, outA); !got.OK {
		t.Errorf("first: %.200s", got.Raw)
	}
	if got := waitReply(t, outB); !got.OK {
		t.Errorf("second: %.200s", got.Raw)
	}
	if x := r.call(c, "audit_compare", r.compareArgs()); !x.OK || x.IsError {
		t.Errorf("after they ended: %.300s", x.Raw)
	}
}

// The byte counter keeps one number per ten seconds, so a host cannot grow it by calling often.
func TestByteCounterIsBounded(t *testing.T) {
	l := newLimiter(DefaultBudgets)
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 20000; i++ {
		done, over := l.admit("p", ClassGuide, now.Add(time.Duration(i)*200*time.Millisecond))
		if over != nil {
			t.Fatalf("call %d refused: %v", i, over)
		}
		done(1, now.Add(time.Duration(i)*200*time.Millisecond))
	}
	if n := len(l.per["p"].bytes); n > 62 {
		t.Errorf("%d byte buckets", n)
	}
}

// A reissue (and a revoke, and Remove with a new enrolment of the same id) does not give the host a fresh budget: the key is the profile id.
func TestReissueKeepsTheRateCounters(t *testing.T) {
	r := newRigBudgets(t, DefaultBudgets)
	h := r.enroll("again", r.approval(TierAggregate))
	for i := 0; i < DefaultBudgets.CompareRate; i++ {
		if c := r.call(h, "audit_compare", r.compareArgs()); !c.OK || c.IsError {
			t.Fatalf("compare %d: %.300s", i+1, c.Raw)
		}
	}
	if c := r.call(h, "audit_compare", r.compareArgs()); !overBudget(c) {
		t.Fatalf("the 13th: %d %.200s", c.Status, c.Raw)
	}
	if _, err := r.g.Reissue(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	h2 := r.enroll("again", r.approval(TierAggregate)) // enroll again, approve, claim: a new credential and revision
	n := len(r.dash.hits())
	if c := r.call(h2, "audit_compare", r.compareArgs()); !overBudget(c) {
		t.Errorf("after the reissue: %d %.300s", c.Status, c.Raw)
	}
	if len(r.dash.hits()) != n {
		t.Error("a refused comparison reached the dashboard")
	}
	// a revoke and a new approval of the same id too
	if _, err := r.g.Revoke(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	if err := r.g.Remove(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	h3 := r.enroll("again", r.approval(TierAggregate))
	if c := r.call(h3, "audit_compare", r.compareArgs()); !overBudget(c) {
		t.Errorf("after revoke, remove and a new enrolment: %d %.300s", c.Status, c.Raw)
	}
}

// The bytes of a long call are charged when it ends. Ten minutes after the start, nine of them spent running, the answer still counts.
func TestBytesAreChargedWhereTheyAreDelivered(t *testing.T) {
	small := DefaultBudgets
	small.CumulativeBytes = 1 << 10
	r := newRigBudgets(t, small)
	h := r.enroll("slow", r.approval(TierAggregate))
	release, out := r.holdCompare(h, "cccccccc-1")
	r.clk.Add(9 * time.Minute) // the call runs for nine minutes (the clock is the test's own)
	close(release)
	if got := waitReply(t, out); !got.OK || len(got.Result) < 1<<10 {
		t.Fatalf("the comparison: %d %.200s", got.Status, got.Raw)
	}
	r.clk.Add(5 * time.Minute) // 14 minutes after the start, 5 after the end
	if g := r.call(h, "audit_get_guide", map[string]any{}); !overBudget(g) {
		t.Errorf("5 minutes after the answer was delivered: %d %.200s", g.Status, g.Raw)
	}
	r.clk.Add(6 * time.Minute)
	if g := r.call(h, "audit_get_guide", map[string]any{}); !g.OK {
		t.Errorf("11 minutes after the answer: %.200s", g.Raw)
	}
}

// Review 5 of #267, M2: the calls of a code run are charged to the same budgets and the same ledger as the host's own calls.
func TestCodeRunCallsShareTheHostBudgets(t *testing.T) {
	r := newRigBudgets(t, DefaultBudgets)
	h := r.enroll("code-a", r.approval(TierAggregate))
	other := r.enroll("code-b", r.approval(TierAggregate))
	pr := Principal{ProfileID: "code-a", Rev: 1}
	args, _ := json.Marshal(r.compareArgs())

	// Ten direct comparisons and two from a script are the host's twelve of the hour.
	for i := 0; i < 10; i++ {
		if c := r.call(h, "audit_compare", r.compareArgs()); !c.OK || c.IsError {
			t.Fatalf("direct %d: %d %.300s", i+1, c.Status, c.Raw)
		}
		r.clk.Add(time.Minute)
	}
	for i := 0; i < 2; i++ {
		end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_compare", args)
		if refused != nil {
			t.Fatalf("script comparison %d refused: %v", i+1, refused)
		}
		end([]byte(`{"ok":1}`), false)
		r.clk.Add(time.Minute)
	}
	// The 13th is refused, from either door, and neither reaches the dashboard.
	n := len(r.dash.hits())
	if c := r.call(h, "audit_compare", r.compareArgs()); !overBudget(c) {
		t.Errorf("13th direct comparison: %d %.300s", c.Status, c.Raw)
	}
	end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_compare", args)
	if refused == nil || end != nil || refused.Code != mcp.WireBudgetExceeded || refused.Message == "" {
		t.Fatalf("13th script comparison: %v", refused)
	}
	if len(r.dash.hits()) != n {
		t.Error("a refused comparison reached the dashboard")
	}
	// Other hosts and cheap tools are not affected; the refusal leaves a denied row, like a direct one.
	if end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`)); refused != nil {
		t.Errorf("guide: %v", refused)
	} else {
		end([]byte(`{}`), false)
	}
	if c := r.call(other, "audit_compare", r.compareArgs()); !c.OK || c.IsError {
		t.Errorf("another host: %.300s", c.Raw)
	}
	var admits, ends, denied int
	for _, e := range r.events() {
		if e.ProfileID != "code-a" || e.Tool != "audit_compare" {
			continue
		}
		switch e.Kind {
		case "admit":
			admits++
		case "end":
			ends++
		case "denied":
			denied++
		}
	}
	if admits != 12 || ends != 12 || denied != 2 {
		t.Errorf("ledger rows of the host's comparisons: %d admit, %d end, %d denied (want 12, 12, 2)", admits, ends, denied)
	}
	// An hour later the oldest leave the window.
	r.clk.Add(time.Hour)
	if end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_compare", args); refused != nil {
		t.Errorf("after the hour: %v", refused)
	} else {
		end([]byte(`{}`), false)
	}
}

// A script cannot run comparisons at once, nor read past the byte budget, nor use a tool that is not in the catalog.
func TestCodeRunCallsHonourAtOnceAndBytes(t *testing.T) {
	small := DefaultBudgets
	small.CumulativeBytes = 1000
	r := newRigBudgets(t, small)
	r.enroll("code-c", r.approval(TierAggregate))
	pr := Principal{ProfileID: "code-c", Rev: 1}
	end1, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_compare", []byte(`{}`))
	if refused != nil {
		t.Fatal(refused)
	}
	if _, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_compare", []byte(`{}`)); refused == nil || refused.Code != mcp.WireBudgetExceeded {
		t.Errorf("a second comparison at once: %v", refused)
	}
	end1(make([]byte, 2000), false) // the answer was bigger than the 10-minute budget
	if _, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`)); refused == nil || refused.Code != mcp.WireBudgetExceeded {
		t.Errorf("after the byte budget: %v", refused)
	}
	if _, refused := r.disp.AdmitCodeCall(ctx, pr, "not_a_tool", []byte(`{}`)); refused == nil || refused.Code != mcp.WirePermissionRequired {
		t.Errorf("a tool outside the catalog: %v", refused)
	}
	// In-flight slots are given back: after the end of the call another may start (once the byte window is over).
	r.clk.Add(11 * time.Minute)
	if end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_compare", []byte(`{}`)); refused != nil {
		t.Errorf("after the window: %v", refused)
	} else {
		end(nil, true)
	}
}

func TestToolAllowedIsTheGrantsListWithinItsTier(t *testing.T) {
	list := []string{"audit_get_guide", "audit_get_requests", "audit_compare"}
	if !ToolAllowed(TierEvidence, list, "audit_get_requests") || ToolAllowed(TierEvidence, list, "audit_get_slowest") {
		t.Error("evidence list")
	}
	if ToolAllowed(TierAggregate, list, "audit_get_requests") || ToolAllowed("root", list, "audit_compare") || ToolAllowed(TierAggregate, nil, "audit_compare") {
		t.Error("the tier is the ceiling, and an empty list allows nothing")
	}
	if got := GrantTools(TierAggregate, list); len(got) != 2 || got[0] != "audit_get_guide" || got[1] != "audit_compare" {
		t.Errorf("grant tools %v", got)
	}
	if GrantTools("root", list) != nil || GrantTools(TierEvidence, nil) != nil {
		t.Error("unknown tier or empty list")
	}
}
