package localci

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// A check against a real repository, off unless LOCALCI_LIVE_REPO=owner/name is set (and gh is logged in with a token
// that may write the repository). It makes refs whose names start with "test-" or with a random commit id, and
// removes them at the end, also when a step fails. Run it with: go test ./internal/localci/ -run LiveClaims -v

func liveSetup(t *testing.T) (*GHClaimHost, string) {
	t.Helper()
	repo := os.Getenv("LOCALCI_LIVE_REPO")
	if repo == "" {
		t.Skip("LOCALCI_LIVE_REPO is not set")
	}
	return NewGHClaimHost(&GHHost{Env: os.Environ()}), repo
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// liveClean removes every ref of both namespaces whose name starts with prefix.
func liveClean(h *GHClaimHost, repo, prefix string) {
	ctx := context.Background()
	for _, ns := range []string{RefNSClaim, RefNSAgent} {
		refs, err := h.ListRefs(ctx, repo, ns, prefix)
		if err != nil {
			continue
		}
		for _, r := range refs {
			_ = h.DeleteRef(ctx, repo, ns, r.Name)
		}
	}
}

func TestLiveClaimsHostCreateUpdateListDelete(t *testing.T) {
	h, repo := liveSetup(t)
	ctx := context.Background()
	name := "test-" + randHex(6)
	t.Cleanup(func() { liveClean(h, repo, name) })
	rec := FleetRecord{V: 1, Agent: "m-test0001", State: ClaimHeld, Item: "a live test"}
	if err := h.CreateRef(ctx, repo, RefNSClaim, name, rec); err != nil {
		t.Fatal("create:", err)
	}
	if err := h.CreateRef(ctx, repo, RefNSClaim, name, rec); !errors.Is(err, ErrRefExists) {
		t.Fatalf("a second create must say the ref exists, got %v", err)
	}
	refs, err := h.ListRefs(ctx, repo, RefNSClaim, name)
	if err != nil || len(refs) != 1 || refs[0].Rec.Agent != "m-test0001" || refs[0].Rec.Item != "a live test" {
		t.Fatalf("list: %+v %v", refs, err)
	}
	if refs[0].Age < 0 || refs[0].Age > time.Minute {
		t.Fatalf("the age by the host's clock should be a few seconds, got %s", refs[0].Age)
	}
	rec.State = ClaimDone
	rec.Conclusion = "success"
	if err := h.UpdateRef(ctx, repo, RefNSClaim, name, rec); err != nil {
		t.Fatal("update:", err)
	}
	refs, _ = h.ListRefs(ctx, repo, RefNSClaim, name)
	if len(refs) != 1 || refs[0].Rec.State != ClaimDone || refs[0].Rec.Conclusion != "success" {
		t.Fatalf("the update did not show: %+v", refs)
	}
	// a generation name with a plus sign has to work in the address of the real host
	plus := name + "+2"
	if err := h.CreateRef(ctx, repo, RefNSClaim, plus, rec); err != nil {
		t.Fatal("create with +:", err)
	}
	if refs, _ = h.ListRefs(ctx, repo, RefNSClaim, name); len(refs) != 2 {
		t.Fatalf("list after the + generation: %+v", refs)
	}
	if err := h.DeleteRef(ctx, repo, RefNSClaim, plus); err != nil {
		t.Fatal("delete +:", err)
	}
	if err := h.DeleteRef(ctx, repo, RefNSClaim, name); err != nil {
		t.Fatal("delete:", err)
	}
	if err := h.DeleteRef(ctx, repo, RefNSClaim, name); err != nil {
		t.Fatalf("deleting a ref that is gone is not an error: %v", err)
	}
	if refs, _ = h.ListRefs(ctx, repo, RefNSClaim, name); len(refs) != 0 {
		t.Fatalf("left over: %+v", refs)
	}
}

// Many agents ask for the same request at the same moment on the real host: exactly one wins.
func TestLiveClaimsRace(t *testing.T) {
	h, repo := liveSetup(t)
	ctx := context.Background()
	sha := randHex(20)
	req := Request{ID: "live-1", Kind: KindPR, SHA: sha, Workflow: "live-test.yml", Number: 1, Base: "develop"}
	t.Cleanup(func() { liveClean(h, repo, sha) })
	const n = 6
	var wg sync.WaitGroup
	out := make([]ClaimDecision, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := &Fleet{Host: h, Repo: repo, ID: "m-live000" + strconv.Itoa(i)}
			r := req
			r.ID = "live-" + f.ID
			d, err := f.Acquire(ctx, r)
			if err != nil {
				t.Errorf("agent %d: %v", i, err)
			}
			out[i] = d
		}()
	}
	wg.Wait()
	won := 0
	for _, d := range out {
		if d.Outcome == ClaimWon {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d agents won the claim, want exactly 1: %+v", won, out)
	}
}

// The lease is 30 s because the real host answers in a few seconds; a shorter one would end before the first call returns.
// A lease nobody refreshes is taken over by another agent, judged by the host's clock; the old holder then sees it lost.
func TestLiveClaimsStaleLeaseIsTakenOver(t *testing.T) {
	h, repo := liveSetup(t)
	ctx := context.Background()
	sha := randHex(20)
	req := Request{ID: "live-a", Kind: KindPR, SHA: sha, Workflow: "live-test.yml", Number: 1, Base: "develop"}
	t.Cleanup(func() { liveClean(h, repo, sha) })
	a := &Fleet{Host: h, Repo: repo, ID: "m-live000a", TTL: 30 * time.Second, Log: t.Logf}
	b := &Fleet{Host: h, Repo: repo, ID: "m-live000b", TTL: 30 * time.Second, Log: t.Logf}
	if d, err := a.Acquire(ctx, req); err != nil || d.Outcome != ClaimWon {
		t.Fatalf("a: %+v %v", d, err)
	}
	rb := req
	rb.ID = "live-b"
	if d, err := b.Acquire(ctx, rb); err != nil || d.Outcome != ClaimBusy {
		t.Fatalf("b must be told it is busy while the lease is fresh: %+v %v", d, err)
	}
	time.Sleep(35 * time.Second) // a "crashed": it refreshes nothing
	d, err := b.Acquire(ctx, rb)
	if err != nil || d.Outcome != ClaimWon {
		t.Fatalf("b must take the stale lease over: %+v %v", d, err)
	}
	if lost := a.Refresh(ctx); len(lost) != 1 {
		t.Fatalf("a must find out it lost the claim: %v", lost)
	}
	b.Finish(ctx, rb, "success")
	if d, err := a.Acquire(ctx, req); err != nil || d.Outcome != ClaimOver || d.Conclusion != "success" {
		t.Fatalf("after b finished, the request is over: %+v %v", d, err)
	}
}

// Release on cancel removes the ref on the real host.
func TestLiveClaimsReleaseOnCancel(t *testing.T) {
	h, repo := liveSetup(t)
	ctx := context.Background()
	sha := randHex(20)
	req := Request{ID: "live-a", Kind: KindPR, SHA: sha, Workflow: "live-test.yml", Number: 1, Base: "develop"}
	t.Cleanup(func() { liveClean(h, repo, sha) })
	a := &Fleet{Host: h, Repo: repo, ID: "m-live000a"}
	if d, err := a.Acquire(ctx, req); err != nil || d.Outcome != ClaimWon {
		t.Fatalf("%+v %v", d, err)
	}
	a.Release(ctx, req)
	if refs, err := h.ListRefs(ctx, repo, RefNSClaim, sha); err != nil || len(refs) != 0 {
		t.Fatalf("the claim should be gone: %+v %v", refs, err)
	}
	// the agent record too
	if err := a.Publish(ctx, Heartbeat{}, "ready"); err != nil {
		t.Fatal("publish:", err)
	}
	if refs, _ := h.ListRefs(ctx, repo, RefNSAgent, "m-live000a"); len(refs) != 1 {
		t.Fatalf("the agent record: %+v", refs)
	}
	_ = h.DeleteRef(ctx, repo, RefNSAgent, "m-live000a")
}
