package cicli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/localci"
)

func TestServeFlagsOfTheFleetAreChecked(t *testing.T) {
	withStatusFakes(t)
	for name, args := range map[string][]string{
		"short ttl":     {"serve", "--claim-ttl", "30s"},
		"negative cmp":  {"serve", "--compare-every", "-1m"},
		"too fast cmp":  {"serve", "--compare-every", "5s"},
		"claims no arg": {"claims", "extra"},
		"claims repo":   {"claims", "--repo", "norepo"},
	} {
		if code, _, _ := cli(t, nil, args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
}

// A token that may not write the repository must stop serve with a plain message, not let the PC run unclaimed.
func TestServeStopsWhenTheTokenCannotWriteClaims(t *testing.T) {
	f := withStatusFakes(t)
	f.claims.Fail(localci.ErrClaimDenied)
	state := filepath.Join(t.TempDir(), "store")
	code, _, e := cli(t, nil, "serve", "--repo", "acme/widgets", "--state", state, "--in-process", "--no-push", "--no-keep-awake", "--no-release-pause")
	if code != exitUsage {
		t.Fatalf("exit %d, want %d\n%s", code, exitUsage, e)
	}
	for _, want := range []string{"may not write claim records", "--no-claims"} {
		if !strings.Contains(e, want) {
			t.Fatalf("the message must say %q:\n%s", want, e)
		}
	}
}

// With --no-claims the host is never asked.
func TestServeNoClaimsNeverCallsTheHost(t *testing.T) {
	f := withStatusFakes(t)
	f.claims.Fail(localci.ErrClaimDenied)
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	pr := localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}
	withHost(t, &fakeHost{url: bare, pr: pr, list: []localci.PullRequest{pr}})
	_, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push", "--no-claims",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "t1"}, func(out string) bool { return strings.Contains(out, "queued pr 1 into develop") })
	waitFor(t, "the run to finish", 90*time.Second, func() bool {
		es, _ := (&localci.Store{Dir: state}).ReadIndex()
		return len(es) == 1 && es[0].Conclusion == "success"
	})
	if out, code := stop(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, op := range []string{"list", "create", "update", "delete"} {
		if n := f.claims.Calls(op); n != 0 {
			t.Fatalf("--no-claims still called the host: %s x%d", op, n)
		}
	}
}

// The normal path: serve claims the request, runs it, leaves a done claim, publishes the PC and says it stopped.
func TestServeClaimsRunsAndPublishes(t *testing.T) {
	f := withStatusFakes(t)
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	pr := localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}
	withHost(t, &fakeHost{url: bare, pr: pr, list: []localci.PullRequest{pr}})
	_, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "my-secret-box", "--label", "gpu"}, func(out string) bool { return strings.Contains(out, "queued pr 1 into develop") })
	waitFor(t, "the run to finish", 90*time.Second, func() bool {
		es, _ := (&localci.Store{Dir: state}).ReadIndex()
		return len(es) == 1 && es[0].Conclusion == "success"
	})
	waitFor(t, "the claim to be marked done", 30*time.Second, func() bool {
		for _, n := range f.claims.Names(localci.RefNSClaim) {
			if rec, _ := f.claims.Record(localci.RefNSClaim, n); rec.State == localci.ClaimDone && rec.Conclusion == "success" {
				return true
			}
		}
		return false
	})
	waitFor(t, "the PC to publish itself", 30*time.Second, func() bool { return len(f.claims.Names(localci.RefNSAgent)) == 1 })
	out, code := stop()
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	id, err := localci.LoadFleetID(state)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := f.claims.Record(localci.RefNSAgent, id)
	if !ok || rec.State != "stopped" {
		t.Fatalf("the PC should have said it stopped: %+v ok=%v", rec, ok)
	}
	// the public record carries the opaque id and the labels, never the name given with --name
	for _, ns := range []string{localci.RefNSClaim, localci.RefNSAgent} {
		for _, n := range f.claims.Names(ns) {
			r, _ := f.claims.Record(ns, n)
			if strings.Contains(n, "my-secret-box") || strings.Contains(r.Agent, "my-secret-box") || strings.Contains(r.Item, "my-secret-box") {
				t.Fatalf("the PC name leaked into %s/%s: %+v", ns, n, r)
			}
		}
	}
	view, err := localci.ReadFleetView(state)
	if err != nil || view == nil || view.Self != id {
		t.Fatalf("the agents file was not written: %+v %v", view, err)
	}
}

// serve saves the comparison on its own so the Overview headline stays fresh.
func TestServeSavesTheComparisonOnATimer(t *testing.T) {
	withStatusFakes(t)
	state := filepath.Join(t.TempDir(), "store")
	_, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--in-process", "--no-push", "--no-claims",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--compare-every", "1m"}, func(out string) bool { return strings.Contains(out, "watching acme/widgets") })
	waitFor(t, "the comparison file", 30*time.Second, func() bool {
		_, err := os.Stat(localci.ComparePath(state))
		return err == nil
	})
	if out, code := stop(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestServeCompareEveryZeroTurnsTheTimerOff(t *testing.T) {
	withStatusFakes(t)
	state := filepath.Join(t.TempDir(), "store")
	_, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--in-process", "--no-push", "--no-claims",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--compare-every", "0"}, func(out string) bool { return strings.Contains(out, "watching acme/widgets") })
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(localci.ComparePath(state)); err == nil {
		t.Fatal("--compare-every 0 still saved the comparison")
	}
	if out, code := stop(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestClaimsCommandListsAndCleans(t *testing.T) {
	f := withStatusFakes(t)
	ctx := context.Background()
	rec := localci.FleetRecord{V: 1, Agent: "m-aaaa1111", State: localci.ClaimDone, Conclusion: "success", Item: "pr 1 into develop"}
	if err := f.claims.CreateRef(ctx, "acme/widgets", localci.RefNSClaim, "abc-ci.yml", rec); err != nil {
		t.Fatal(err)
	}
	if err := f.claims.CreateRef(ctx, "acme/widgets", localci.RefNSAgent, "m-aaaa1111", localci.FleetRecord{V: 1, Agent: "m-aaaa1111", State: "ready"}); err != nil {
		t.Fatal(err)
	}
	code, out, e := cli(t, nil, "claims", "--repo", "acme/widgets")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, e)
	}
	for _, want := range []string{"1 PC(s)", "m-aaaa1111", "abc-ci.yml", "1 claim(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	f.claims.Backdate(localci.RefNSClaim, "abc-ci.yml", 8*24*time.Hour)
	if code, _, e := cli(t, nil, "claims", "--repo", "acme/widgets", "--clean"); code != exitOK {
		t.Fatalf("exit %d\n%s", code, e)
	}
	if n := f.claims.Names(localci.RefNSClaim); len(n) != 0 {
		t.Fatalf("--clean left %v", n)
	}
	if n := f.claims.Names(localci.RefNSAgent); len(n) != 1 {
		t.Fatalf("--clean must not touch the PCs: %v", n)
	}
}
