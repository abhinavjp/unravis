package localci

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func claimReq(id string) Request {
	r := prReq(7, testSHA)
	r.ID = id
	return r
}

func newTestFleet(h ClaimHost, id string) *Fleet {
	return &Fleet{Host: h, Repo: "o/r", ID: id, Labels: []string{"win"}, TTL: 5 * time.Minute}
}

func mustAcquire(t *testing.T, f *Fleet, r Request) ClaimDecision {
	t.Helper()
	d, err := f.Acquire(context.Background(), r)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	return d
}

func TestClaimNameKeepsToSafeNames(t *testing.T) {
	good, err := ClaimName(claimReq("x"))
	if err != nil || good != testSHA+"-fast-gate.yml" {
		t.Fatalf("name %q, %v", good, err)
	}
	sched := claimReq("x")
	sched.Kind, sched.Slot = KindSchedule, "20260101T0900"
	if n, err := ClaimName(sched); err != nil || n != testSHA+"-fast-gate.yml-s20260101T0900" {
		t.Fatalf("schedule name %q, %v", n, err)
	}
	for _, bad := range []Request{
		{SHA: "main", Workflow: "a.yml"},
		{SHA: testSHA, Workflow: "../x.yml"},
		{SHA: testSHA, Workflow: "a/b.yml"},
		{SHA: testSHA, Workflow: "a.yml.lock"},
		{SHA: testSHA, Workflow: ""},
		{SHA: testSHA, Workflow: "a b.yml"},
		{Kind: KindSchedule, SHA: testSHA, Workflow: "a.yml", Slot: "../1"},
	} {
		if n, err := ClaimName(bad); err == nil {
			t.Errorf("%+v gave the name %q", bad, n)
		}
	}
}

func TestValidRefNameIsTheDeleteGuard(t *testing.T) {
	for _, ok := range []string{testSHA + "-fast-gate.yml", testSHA + "-ci.yml+2", "m-1a2b3c4d"} {
		if !ValidRefName(ok) {
			t.Errorf("%q should be a valid name", ok)
		}
	}
	for _, bad := range []string{"", "..", "a..b", "heads/main", "../heads/main", "a/b", "-x", ".x", "x.lock", "x.", "a b", "a~1", "a^", "a:b", "a\\b", "a?", "a*", "a[", strings.Repeat("a", 300)} {
		if ValidRefName(bad) {
			t.Errorf("%q must not be a valid name", bad)
		}
	}
	if !ValidRefNS(RefNSClaim) || !ValidRefNS(RefNSAgent) || ValidRefNS("heads") || ValidRefNS("ci-claim/x") || ValidRefNS("") {
		t.Error("only the two fleet namespaces are valid")
	}
	h := &MemClaimHost{}
	for _, ns := range []string{"heads", "tags", "ci-claim/x", ""} {
		if err := h.DeleteRef(context.Background(), "o/r", ns, "main"); err == nil {
			t.Errorf("a delete in the namespace %q went through", ns)
		}
	}
	if err := h.DeleteRef(context.Background(), "o/r", RefNSClaim, "../../heads/main"); err == nil {
		t.Error("a delete with a path in the name went through")
	}
}

func TestSecondAgentFindsTheClaimBusy(t *testing.T) {
	h := &MemClaimHost{}
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	if d := mustAcquire(t, a, claimReq("ra")); d.Outcome != ClaimWon {
		t.Fatalf("first: %+v", d)
	}
	d := mustAcquire(t, b, claimReq("rb"))
	if d.Outcome != ClaimBusy || d.Holder != "m-aaaa1111" || d.RetryAfter < 30*time.Second || d.RetryAfter > time.Minute {
		t.Fatalf("second: %+v", d)
	}
	if got := h.Names(RefNSClaim); len(got) != 1 || got[0] != testSHA+"-fast-gate.yml" {
		t.Fatalf("refs: %v", got)
	}
	rec, _ := h.Record(RefNSClaim, testSHA+"-fast-gate.yml")
	if rec.Agent != "m-aaaa1111" || rec.State != ClaimHeld || rec.SHA != testSHA || rec.Item != "pr 7 into develop" {
		t.Fatalf("record: %+v", rec)
	}
}

func TestFinishedClaimStaysAsATombstone(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	mustAcquire(t, a, claimReq("ra"))
	a.Finish(ctx, claimReq("ra"), "failure")
	d := mustAcquire(t, b, claimReq("rb"))
	if d.Outcome != ClaimOver || d.Conclusion != "failure" || d.Holder != "m-aaaa1111" {
		t.Fatalf("after a finished run: %+v", d)
	}
	// a person asking for the run again starts it over
	again := claimReq("rb")
	again.Rerun = true
	if d := mustAcquire(t, b, again); d.Outcome != ClaimWon {
		t.Fatalf("rerun: %+v", d)
	}
	if got := h.Names(RefNSClaim); len(got) != 1 || !strings.HasSuffix(got[0], "+2") {
		t.Fatalf("the rerun is generation 2 and the old one is gone: %v", got)
	}
}

func TestClaimReleasedOnCancelAndError(t *testing.T) {
	for _, conclusion := range []string{"cancelled", "superseded", "error", ""} {
		t.Run("conclusion "+conclusion, func(t *testing.T) {
			h := &MemClaimHost{}
			ctx := context.Background()
			a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
			mustAcquire(t, a, claimReq("ra"))
			a.Finish(ctx, claimReq("ra"), conclusion)
			if got := h.Names(RefNSClaim); len(got) != 0 {
				t.Fatalf("the claim is still there: %v", got)
			}
			if d := mustAcquire(t, b, claimReq("rb")); d.Outcome != ClaimWon {
				t.Fatalf("another PC may run it now: %+v", d)
			}
		})
	}
}

func TestReleaseAndReleaseAll(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	a := newTestFleet(h, "m-aaaa1111")
	r1, r2 := claimReq("r1"), claimReq("r2")
	r2.Workflow = "other.yml"
	mustAcquire(t, a, r1)
	mustAcquire(t, a, r2)
	a.Release(ctx, r1)
	if got := h.Names(RefNSClaim); len(got) != 1 {
		t.Fatalf("after one release: %v", got)
	}
	a.ReleaseAll(ctx)
	if got := h.Names(RefNSClaim); len(got) != 0 {
		t.Fatalf("after releasing all: %v", got)
	}
	if a.Held("r1") || a.Held("r2") {
		t.Fatal("nothing is held any more")
	}
}

func TestStaleLeaseIsTakenOver(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	mustAcquire(t, a, claimReq("ra"))
	base := testSHA + "-fast-gate.yml"

	h.Backdate(RefNSClaim, base, 4*time.Minute) // within the five minutes: still A's
	if d := mustAcquire(t, b, claimReq("rb")); d.Outcome != ClaimBusy {
		t.Fatalf("a lease of 4 minutes is not stale: %+v", d)
	}
	h.Backdate(RefNSClaim, base, 6*time.Minute)
	if d := mustAcquire(t, b, claimReq("rb")); d.Outcome != ClaimWon {
		t.Fatalf("a lease of 6 minutes is: %+v", d)
	}
	if got := h.Names(RefNSClaim); len(got) != 1 || got[0] != base+"+2" {
		t.Fatalf("B holds generation 2 and generation 1 is gone: %v", got)
	}
	// A wakes up: its next refresh finds the claim is not its own, and says so
	if lost := a.Refresh(ctx); len(lost) != 1 || lost[0] != "ra" {
		t.Fatalf("A should find the claim lost: %v", lost)
	}
	if a.Held("ra") {
		t.Fatal("A still thinks it holds it")
	}
	// A finishing late must not touch B's claim
	a.Finish(ctx, claimReq("ra"), "success")
	if rec, ok := h.Record(RefNSClaim, base+"+2"); !ok || rec.Agent != "m-bbbb2222" || rec.State != ClaimHeld {
		t.Fatalf("B's claim was touched: %+v %v", rec, ok)
	}
	// B's refresh keeps it
	if lost := b.Refresh(ctx); len(lost) != 0 {
		t.Fatalf("B lost its own claim: %v", lost)
	}
}

func TestRefreshKeepsALeaseFresh(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	mustAcquire(t, a, claimReq("ra"))
	base := testSHA + "-fast-gate.yml"
	h.Backdate(RefNSClaim, base, 4*time.Minute)
	if lost := a.Refresh(ctx); len(lost) != 0 {
		t.Fatal(lost)
	}
	h.Backdate(RefNSClaim, base, 0)
	refs, _ := h.ListRefs(ctx, "o/r", RefNSClaim, "")
	if len(refs) != 1 || refs[0].Age > time.Second {
		t.Fatalf("the refresh should reset the age: %+v", refs)
	}
	// a refresh that is a few minutes late is no reason to be taken over while it keeps happening
	h.Backdate(RefNSClaim, base, 3*time.Minute)
	a.Refresh(ctx)
	if d := mustAcquire(t, b, claimReq("rb")); d.Outcome != ClaimBusy {
		t.Fatalf("a refreshed lease holds: %+v", d)
	}
}

func TestLostClaimWhenTheRefIsGone(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	a := newTestFleet(h, "m-aaaa1111")
	mustAcquire(t, a, claimReq("ra"))
	_ = h.DeleteRef(ctx, "o/r", RefNSClaim, testSHA+"-fast-gate.yml")
	if lost := a.Refresh(ctx); len(lost) != 1 {
		t.Fatalf("a deleted ref is a lost claim: %v", lost)
	}
}

func TestHostDownFailsClosed(t *testing.T) {
	h := &MemClaimHost{}
	a := newTestFleet(h, "m-aaaa1111")
	h.Fail(errors.New("no network"))
	if d, err := a.Acquire(context.Background(), claimReq("ra")); err == nil {
		t.Fatalf("an unreachable host must not hand out a claim: %+v", d)
	}
	h.Fail(nil)
	mustAcquire(t, a, claimReq("ra"))
	// a held claim survives a host that is down: the run goes on, the lease is simply not refreshed
	h.Fail(errors.New("no network"))
	if lost := a.Refresh(context.Background()); len(lost) != 0 {
		t.Fatalf("a failed refresh is not a lost claim: %v", lost)
	}
	if !a.Held("ra") {
		t.Fatal("the claim was dropped")
	}
}

func TestOwnClaimOfADeadProcessIsTakenUp(t *testing.T) {
	h := &MemClaimHost{}
	first := newTestFleet(h, "m-aaaa1111")
	mustAcquire(t, first, claimReq("old"))
	// the same state folder after a restart: same id, new process, the request is back in the queue under a new id
	second := newTestFleet(h, "m-aaaa1111")
	if d := mustAcquire(t, second, claimReq("new")); d.Outcome != ClaimWon {
		t.Fatalf("restart: %+v", d)
	}
}

func TestOneRequestPerClaimOnThisPC(t *testing.T) {
	h := &MemClaimHost{}
	a := newTestFleet(h, "m-aaaa1111")
	mustAcquire(t, a, claimReq("pr-request"))
	push := claimReq("push-request")
	push.Kind, push.Ref = KindPush, "main"
	if d := mustAcquire(t, a, push); d.Outcome != ClaimBusy || d.Holder != "m-aaaa1111" {
		t.Fatalf("the push of a commit that a pull request run holds must wait: %+v", d)
	}
}

func TestScheduleSlotsAreSeparateClaims(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	day1 := claimReq("d1")
	day1.Kind, day1.Ref, day1.Slot = KindSchedule, "main", "20260101T0900"
	mustAcquire(t, a, day1)
	a.Finish(ctx, day1, "success")
	day2 := claimReq("d2")
	day2.Kind, day2.Ref, day2.Slot = KindSchedule, "main", "20260102T0900"
	if d := mustAcquire(t, b, day2); d.Outcome != ClaimWon {
		t.Fatalf("the next day's scheduled run is a new claim: %+v", d)
	}
	same := day1
	same.ID = "d1b"
	if d := mustAcquire(t, b, same); d.Outcome != ClaimOver {
		t.Fatalf("the same slot already ran: %+v", d)
	}
}

// Many agents ask for one claim at the same moment: exactly one wins, every other one is told it is busy.
func TestManyAgentsRaceForOneClaim(t *testing.T) {
	const agents = 24
	h := &MemClaimHost{}
	var wg sync.WaitGroup
	var won, busy, other atomic.Int32
	var winner atomic.Value
	start := make(chan struct{})
	for i := 0; i < agents; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := newTestFleet(h, fmt.Sprintf("m-race%04d", i))
			<-start
			d, err := f.Acquire(context.Background(), claimReq(fmt.Sprintf("r%d", i)))
			switch {
			case err != nil:
				other.Add(1)
			case d.Outcome == ClaimWon:
				won.Add(1)
				winner.Store(f.ID)
			case d.Outcome == ClaimBusy:
				busy.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if won.Load() != 1 || busy.Load() != agents-1 || other.Load() != 0 {
		t.Fatalf("won %d, busy %d, other %d", won.Load(), busy.Load(), other.Load())
	}
	rec, _ := h.Record(RefNSClaim, testSHA+"-fast-gate.yml")
	if rec.Agent != winner.Load().(string) {
		t.Fatalf("the claim names %s, the winner was %s", rec.Agent, winner.Load())
	}
}

// Many agents find the same stale lease at the same moment: exactly one takes it over, and the old generation goes.
func TestManyAgentsRaceForAStaleLease(t *testing.T) {
	const agents = 24
	h := &MemClaimHost{}
	dead := newTestFleet(h, "m-dead0000")
	mustAcquire(t, dead, claimReq("dead"))
	h.Backdate(RefNSClaim, testSHA+"-fast-gate.yml", 10*time.Minute)
	var wg sync.WaitGroup
	var won atomic.Int32
	start := make(chan struct{})
	for i := 0; i < agents; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := newTestFleet(h, fmt.Sprintf("m-race%04d", i))
			<-start
			if d, err := f.Acquire(context.Background(), claimReq(fmt.Sprintf("r%d", i))); err == nil && d.Outcome == ClaimWon {
				won.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("%d agents took the same stale lease", won.Load())
	}
	if got := h.Names(RefNSClaim); len(got) != 1 || got[0] != testSHA+"-fast-gate.yml+2" {
		t.Fatalf("refs: %v", got)
	}
}

// Agents that keep asking, finishing and re-asking never run one request twice at the same time.
func TestRacingAgentsNeverRunTheSameClaimTwiceAtOnce(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	var running, overlap, runs atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := newTestFleet(h, fmt.Sprintf("m-loop%04d", i))
			for n := 0; n < 20; n++ {
				r := claimReq(fmt.Sprintf("r%d-%d", i, n))
				r.Rerun = true // every round is a person asking for a run again
				d, err := f.Acquire(ctx, r)
				if err != nil || d.Outcome != ClaimWon {
					continue
				}
				if running.Add(1) > 1 {
					overlap.Add(1)
				}
				runs.Add(1)
				time.Sleep(time.Millisecond)
				running.Add(-1)
				f.Finish(ctx, r, "cancelled")
			}
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 || runs.Load() == 0 {
		t.Fatalf("overlapping runs %d of %d", overlap.Load(), runs.Load())
	}
}

func TestPublishAndSyncShowOtherAgentsByOpaqueId(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	dir := t.TempDir()
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	b.Labels = []string{"linux", "bad label!\x1b[31m"}
	hbA := Heartbeat{Name: "my-private-pc-name", Slots: 2, Started: time.Now(), Jobs: []AgentJob{{ID: "x", Label: "pr 7 into develop", Since: time.Now()}}}
	if err := a.Publish(ctx, hbA, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, Heartbeat{Name: "other", Slots: 1}, "draining"); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(ctx, hbA, "ready"); err != nil { // second publish updates
		t.Fatal(err)
	}
	if err := a.Sync(ctx, dir); err != nil {
		t.Fatal(err)
	}
	v, err := ReadFleetView(dir)
	if err != nil || v == nil {
		t.Fatalf("view: %v %v", v, err)
	}
	if v.Self != "m-aaaa1111" || len(v.Agents) != 2 || v.Agents[0].ID != "m-aaaa1111" || v.Agents[1].ID != "m-bbbb2222" {
		t.Fatalf("agents: %+v", v)
	}
	if v.Agents[0].Slots != 2 || len(v.Agents[0].Jobs) != 1 || v.Agents[0].Jobs[0].Item != "pr 7 into develop" || v.Agents[0].State != "ready" {
		t.Fatalf("agent a: %+v", v.Agents[0])
	}
	if v.Agents[1].State != "draining" || len(v.Agents[1].Labels) != 2 || v.Agents[1].Labels[1] != "badlabel31m" && strings.ContainsAny(v.Agents[1].Labels[1], " !\x1b") {
		t.Fatalf("agent b: %+v", v.Agents[1])
	}
	rec, _ := h.Record(RefNSAgent, "m-aaaa1111")
	if strings.Contains(fmt.Sprintf("%+v", rec), "my-private-pc-name") {
		t.Fatalf("the PC's own name reached the host: %+v", rec)
	}
	if _, err := ReadFleetView(filepath.Join(dir, "nowhere")); err != nil {
		t.Fatalf("a missing view is no error: %v", err)
	}
}

func TestSyncRemovesOldStoppedAgentsAndCleanClaimsOldTombstones(t *testing.T) {
	h := &MemClaimHost{}
	ctx := context.Background()
	dir := t.TempDir()
	a, old := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-oldd0000")
	if err := old.Publish(ctx, Heartbeat{}, "stopped"); err != nil {
		t.Fatal(err)
	}
	h.Backdate(RefNSAgent, "m-oldd0000", 25*time.Hour)
	if err := a.Publish(ctx, Heartbeat{}, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if got := h.Names(RefNSAgent); len(got) != 1 || got[0] != "m-aaaa1111" {
		t.Fatalf("agent refs: %v", got)
	}
	mustAcquire(t, a, claimReq("ra"))
	a.Finish(ctx, claimReq("ra"), "success")
	other := claimReq("rb")
	other.Workflow = "held.yml"
	mustAcquire(t, a, other)
	h.Backdate(RefNSClaim, testSHA+"-fast-gate.yml", 8*24*time.Hour)
	h.Backdate(RefNSClaim, testSHA+"-held.yml", 8*24*time.Hour)
	a.CleanClaims(ctx)
	if got := h.Names(RefNSClaim); len(got) != 1 || got[0] != testSHA+"-held.yml" {
		t.Fatalf("only the old finished claim goes, a held one stays: %v", got)
	}
}

func TestLoadFleetIDIsStablePerStateFolder(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	a, err := LoadFleetID(d1)
	if err != nil || !fleetIDRe.MatchString(a) {
		t.Fatalf("id %q, %v", a, err)
	}
	if again, _ := LoadFleetID(d1); again != a {
		t.Fatalf("the id changed: %s then %s", a, again)
	}
	if b, _ := LoadFleetID(d2); b == a {
		t.Fatal("two state folders must not share an id")
	}
}

func TestRemoteTextIsCleaned(t *testing.T) {
	if got := cleanText("pr 7\x1b[31m\nred<script>", 12); strings.ContainsAny(got, "\x1b\n") || len([]rune(got)) > 12 {
		t.Fatalf("%q", got)
	}
	if got := cleanLabels([]string{"ok", "a b", "", "x/../y", strings.Repeat("z", 99)}); len(got) != 4 || got[1] != "ab" || got[2] != "x..y" && got[2] != "xy" || len(got[3]) != 32 {
		t.Fatalf("%q", got)
	}
	if cleanState("hacked") != "ready" || cleanState("stopped") != "stopped" {
		t.Fatal("an unknown state is shown as ready")
	}
}
