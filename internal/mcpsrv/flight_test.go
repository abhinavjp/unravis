package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-350, dedupe of identical in-flight comparisons (docs/desktop/spikes/BL-350-dedupe.md). Real HTTP routes, SQLite ledger and the real
// dashboard handler over the demo repository; the comparison is held at the dashboard so two calls are really in flight together.

func (r *rig) startCompare(h host, callID string, args map[string]any) chan reply {
	r.t.Helper()
	out := make(chan reply, 1)
	go func() {
		out <- r.callRaw(h.header, map[string]any{"tool": "audit_compare", "args": args, "callId": callID})
	}()
	return out
}

func (r *rig) rows(profile, kind string) int {
	n := 0
	for _, e := range r.events() {
		if e.ProfileID == profile && e.Kind == kind {
			n++
		}
	}
	return n
}

func (r *rig) analyzeHits() int {
	n := 0
	for _, p := range r.dash.hits() {
		if p == "/api/analyze" {
			n++
		}
	}
	return n
}

func (r *rig) otherArgs() map[string]any {
	a := r.compareArgs()
	q := a["query"].(audit.Query)
	q.BucketSeconds = 120
	a["query"] = q
	return a
}

// noAnswerYet fails if the channel already has an answer.
func noAnswerYet(t *testing.T, out chan reply, what string) {
	t.Helper()
	select {
	case a := <-out:
		t.Fatalf("%s: answered early: %d %.200s", what, a.Status, a.Raw)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestIdenticalComparisonSharesTheRunningOne(t *testing.T) {
	r := newRig(t)
	h := r.enroll("dd1", r.approval(TierAggregate))
	release, lead := r.holdCompare(h, "d1000001-aaaa")
	follow := r.startCompare(h, "d1000002-aaaa", r.compareArgs())
	eventually(t, "the second call's admit row", func() bool { return r.rows("dd1", mcpaudit.KindAdmit) == 2 })
	noAnswerYet(t, follow, "the follower waits for the leader")
	close(release)
	a, b := waitReply(t, lead), waitReply(t, follow)
	if !a.OK || a.IsError || !b.OK || b.IsError {
		t.Fatalf("answers: %.200s | %.200s", a.Raw, b.Raw)
	}
	if id := a.env["resultId"]; id == nil || id != b.env["resultId"] {
		t.Errorf("one run, one result id: %v vs %v", a.env["resultId"], b.env["resultId"])
	}
	if n := r.analyzeHits(); n != 1 {
		t.Errorf("the dashboard ran the comparison %d times", n)
	}
	if r.rows("dd1", mcpaudit.KindAdmit) != 2 || r.rows("dd1", mcpaudit.KindEnd) != 2 {
		t.Errorf("each call has its own pair of rows: %+v", r.events())
	}
	for _, e := range r.events() {
		if e.ProfileID == "dd1" && e.Kind == mcpaudit.KindEnd && (e.Outcome != mcpaudit.OutcomeOK || e.Bytes == 0) {
			t.Errorf("closing row: %+v", e)
		}
	}
}

func TestADifferentComparisonIsStillRefusedWhileOneRuns(t *testing.T) {
	r := newRigBudgets(t, DefaultBudgets)
	h := r.enroll("dd2", r.approval(TierAggregate))
	release, lead := r.holdCompare(h, "d2000001-aaaa")
	if x := r.call(h, "audit_compare", r.otherArgs()); !overBudget(x) {
		t.Errorf("a different comparison at the same time: %d %.300s", x.Status, x.Raw)
	}
	close(release)
	if a := waitReply(t, lead); !a.OK {
		t.Errorf("leader: %.200s", a.Raw)
	}
}

func TestASharedRunIsCountedOnceInTheBudget(t *testing.T) {
	b := DefaultBudgets
	b.CompareRate = 2
	r := newRigBudgets(t, b)
	h := r.enroll("dd3", r.approval(TierAggregate))
	release, lead := r.holdCompare(h, "d3000001-aaaa")
	follow := r.startCompare(h, "d3000002-aaaa", r.compareArgs())
	eventually(t, "the follower to wait", func() bool { return r.rows("dd3", mcpaudit.KindAdmit) == 2 })
	close(release)
	waitReply(t, lead)
	waitReply(t, follow)
	if x := r.call(h, "audit_compare", r.otherArgs()); !x.OK || x.IsError {
		t.Errorf("the second place of the hour: %d %.300s", x.Status, x.Raw)
	}
	other := r.compareArgs()
	q := other["query"].(audit.Query)
	q.BucketSeconds = 300
	other["query"] = q
	if x := r.call(h, "audit_compare", other); !overBudget(x) {
		t.Errorf("the third comparison of the hour: %d %.300s", x.Status, x.Raw)
	}
}

func TestTwoHostsNeverShareARun(t *testing.T) {
	r := newRigBudgets(t, DefaultBudgets)
	a := r.enroll("dd4a", r.approval(TierAggregate))
	b := r.enroll("dd4b", r.approval(TierAggregate))
	release, outA := r.holdCompare(a, "d4000001-aaaa")
	outB := r.startCompare(b, "d4000002-aaaa", r.compareArgs())
	select {
	case <-r.dash.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the other host's comparison did not start its own run")
	}
	close(release)
	waitReply(t, outA)
	waitReply(t, outB)
	if n := r.analyzeHits(); n != 2 {
		t.Errorf("each host runs its own: %d runs", n)
	}
}

func TestAFollowerThatIsCancelledLeavesTheLeaderRunning(t *testing.T) {
	r := newRig(t)
	h := r.enroll("dd5", r.approval(TierAggregate))
	release, lead := r.holdCompare(h, "d5000001-aaaa")
	follow := r.startCompare(h, "d5000002-aaaa", r.compareArgs())
	eventually(t, "the follower to wait", func() bool { return r.rows("dd5", mcpaudit.KindAdmit) == 2 })
	if st, b := r.post("/mcp/v1/cancel", h.header, map[string]string{"callId": "d5000002-aaaa"}); st != 200 || !strings.Contains(string(b), "cancelling") {
		t.Fatalf("cancel: %d %s", st, b)
	}
	f := waitReply(t, follow)
	if !f.OK || !f.IsError || errCode(f.env) != "cancelled" {
		t.Errorf("cancelled follower: %.300s", f.Raw)
	}
	noAnswerYet(t, lead, "the leader goes on")
	close(release)
	if a := waitReply(t, lead); !a.OK || a.IsError {
		t.Errorf("leader: %.200s", a.Raw)
	}
}

func TestAFollowerRunsItselfWhenTheLeaderIsCancelled(t *testing.T) {
	r := newRig(t)
	h := r.enroll("dd6", r.approval(TierAggregate))
	release, lead := r.holdCompare(h, "d6000001-aaaa")
	follow := r.startCompare(h, "d6000002-aaaa", r.compareArgs())
	eventually(t, "the follower to wait", func() bool { return r.rows("dd6", mcpaudit.KindAdmit) == 2 })
	if st, b := r.post("/mcp/v1/cancel", h.header, map[string]string{"callId": "d6000001-aaaa"}); st != 200 || !strings.Contains(string(b), "cancelling") {
		t.Fatalf("cancel: %d %s", st, b)
	}
	if a := waitReply(t, lead); !a.OK || !a.IsError || errCode(a.env) != "cancelled" {
		t.Errorf("cancelled leader: %.300s", a.Raw)
	}
	select { // the follower took over and its own run reaches the dashboard
	case <-r.dash.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the follower did not run its own comparison after the leader was cancelled")
	}
	close(release)
	if f := waitReply(t, follow); !f.OK || f.IsError {
		t.Errorf("follower: %.300s", f.Raw)
	}
	if n := r.rows("dd6", mcpaudit.KindAdmit); n != 2 {
		t.Errorf("a call that took over writes no second admit row: %d", n)
	}
}

func TestARevokeEndsTheLeaderAndTheFollowerWithNoResult(t *testing.T) {
	r := newRig(t)
	h := r.enroll("dd7", r.approval(TierAggregate))
	_, lead := r.holdCompare(h, "d7000001-aaaa")
	follow := r.startCompare(h, "d7000002-aaaa", r.compareArgs())
	eventually(t, "the follower to wait", func() bool { return r.rows("dd7", mcpaudit.KindAdmit) == 2 })
	if _, err := r.g.Revoke(ctx, "dd7"); err != nil {
		t.Fatal(err)
	}
	for _, out := range []chan reply{lead, follow} {
		if a := waitReply(t, out); a.Status != 401 || a.OK || len(a.Result) != 0 {
			t.Errorf("after the revoke: %d %.300s", a.Status, a.Raw)
		}
	}
}

func TestAFollowerWithAShortDeadlineTimesOutAndTheLeaderGoesOn(t *testing.T) {
	r := newRig(t)
	h := r.enroll("dd8", r.approval(TierAggregate))
	release, lead := r.holdCompare(h, "d8000001-aaaa")
	f := r.callRaw(h.header, map[string]any{"tool": "audit_compare", "args": r.compareArgs(), "callId": "d8000002-aaaa", "deadlineMs": 150})
	if !f.OK || !f.IsError || errCode(f.env) != "deadline_exceeded" {
		t.Errorf("follower past its deadline: %.300s", f.Raw)
	}
	close(release)
	if a := waitReply(t, lead); !a.OK || a.IsError {
		t.Errorf("leader: %.200s", a.Raw)
	}
}
