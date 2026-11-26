package localci

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAllPushesByOwner(t *testing.T) {
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const mid = "cccccccccccccccccccccccccccccccccccccccc"
	const other = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ev := func(after, kind, actor string) PushEvent { return PushEvent{After: after, Type: kind, Actor: actor} }
	cases := []struct {
		name     string
		head     string
		events   []PushEvent
		complete bool
		want     bool
		why      string
	}{
		{"the owner pushed the head, and everything before", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "push", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"owner login in another case", head, []PushEvent{ev(head, "push", "ACME"), ev(other, "branch_creation", "Acme")}, true, true, ""},
		{"force push and creation count", head, []PushEvent{ev(head, "force_push", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"case of the sha does not matter", strings.ToUpper(head), []PushEvent{ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"the owner merged a pull request into the branch", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "pr_merge", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"the owner merged through the merge queue", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "merge_queue_merge", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"a deletion by somebody else puts no commit on the branch", head, []PushEvent{ev(mid, "branch_deletion", "alice"), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"a deleted and recreated branch: the owner made both lives", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "branch_creation", "acme"), ev(mid, "branch_deletion", "alice"), ev(other, "push", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"a deleted and recreated branch: a collaborator pushed in the earlier life", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "branch_creation", "acme"), ev(mid, "branch_deletion", "acme"), ev(other, "push", "alice"), ev(other, "branch_creation", "acme")}, true, false, "changed by alice"},
		// a pull request or a merge queue lands commits on the branch with web-flow as committer, so the commit
		// logins pass: the account that merged is the only proof
		{"somebody else merged a pull request into the branch", head, []PushEvent{ev(mid, "pr_merge", "alice"), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, false, "changed by alice"},
		{"somebody else merged through the merge queue", head, []PushEvent{ev(mid, "merge_queue_merge", "alice"), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, false, "changed by alice"},
		{"a merge without an account", head, []PushEvent{ev(mid, "pr_merge", ""), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, false, "no account"},
		{"a kind of record this code does not know", head, []PushEvent{ev(mid, "squash_something", "acme"), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, false, "not known"},
		{"the head is an older record, newer records of other kinds by somebody else", head, []PushEvent{ev(mid, "pr_merge", "alice"), ev(other, "merge_queue_merge", "alice"), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, false, "changed by alice"},
		{"the head is an older record, newer records of other kinds by the owner", head, []PushEvent{ev(mid, "pr_merge", "acme"), ev(head, "push", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		// F1: the owner pushing on top of a collaborator's push must not vouch for it
		{"an older push by a collaborator, the owner pushed the head on top", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "push", "alice"), ev(other, "branch_creation", "acme")}, true, false, "changed by alice"},
		{"the collaborator created the branch", head, []PushEvent{ev(head, "push", "acme"), ev(other, "branch_creation", "alice")}, true, false, "changed by alice"},
		{"a collaborator pushed the head", head, []PushEvent{ev(head, "push", "alice")}, true, false, "changed by alice"},
		{"a collaborator force pushed over the owner's work", head, []PushEvent{ev(head, "force_push", "alice"), ev(other, "push", "acme")}, true, false, "changed by alice"},
		{"a push without an account", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "push", ""), ev(other, "branch_creation", "acme")}, true, false, "no account"},
		{"look-alike login", head, []PushEvent{ev(head, "push", "acme-bot")}, true, false, "acme-bot"},
		{"web-flow is not the owner", head, []PushEvent{ev(head, "push", "web-flow")}, true, false, "web-flow"},
		// the history may be cut off: older records could be somebody else's. A short list is no proof either
		{"the history may be cut off", head, []PushEvent{ev(head, "push", "acme")}, false, false, "may not reach back"},
		{"a short history that does not start at the creation", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "push", "acme")}, true, false, "does not start"},
		{"only a single push, no creation", head, []PushEvent{ev(head, "push", "acme")}, true, false, "does not start"},
		{"no record of the head", head, []PushEvent{ev(other, "push", "acme"), ev(other, "branch_creation", "acme")}, true, false, "no record"},
		{"no records at all", head, nil, true, false, "no record"},
		{"only a merge names the head", head, []PushEvent{ev(head, "pr_merge", "acme"), ev(other, "branch_creation", "acme")}, true, true, ""},
		{"blank head sha", "  ", []PushEvent{ev("", "push", "acme")}, true, false, "no head commit"},
	}
	for _, tc := range cases {
		got, why := AllPushesByOwner("acme", tc.head, PushHistory{Events: tc.events, Complete: tc.complete})
		if got != tc.want || (!tc.want && !strings.Contains(why, tc.why)) || (tc.want && why != "") {
			t.Errorf("%s: got (%v, %q), want %v containing %q", tc.name, got, why, tc.want, tc.why)
		}
	}
	if ok, why := AllPushesByOwner(" ", "x", PushHistory{Complete: true}); ok || !strings.Contains(why, "owner") {
		t.Errorf("an unknown owner trusts nothing: %v %q", ok, why)
	}
}

func TestHistoryComplete(t *testing.T) {
	ev := func(kind string) PushEvent { return PushEvent{After: "x", Type: kind, Actor: "acme"} }
	cases := []struct {
		name   string
		events []PushEvent
		want   bool
	}{
		{"oldest is the creation", []PushEvent{ev("push"), ev("branch_creation")}, true},
		{"only the creation", []PushEvent{ev("branch_creation")}, true},
		{"short page that does not start at the creation", []PushEvent{ev("push")}, false},
		{"empty page", nil, false},
		{"full page ending at a push: older ones may be cut", []PushEvent{ev("push"), ev("push"), ev("push")}, false},
		{"page ending at a merge", []PushEvent{ev("push"), ev("pr_merge")}, false},
		{"page ending at a deletion", []PushEvent{ev("push"), ev("branch_deletion")}, false},
	}
	for _, tc := range cases {
		if got := historyComplete(tc.events); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParsePushEvents(t *testing.T) {
	got, err := parsePushEvents("aaa\tpush\tacme\r\nbbb\tbranch_creation\t\n\nccc\tforce_push\talice\n")
	want := []PushEvent{{"aaa", "push", "acme"}, {"bbb", "branch_creation", ""}, {"ccc", "force_push", "alice"}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("%+v %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if got, err := parsePushEvents(""); err != nil || len(got) != 0 {
		t.Fatalf("an empty answer is no records: %+v %v", got, err)
	}
	// a line that is not three fields could be the newer push of somebody else: the answer is unusable
	for _, bad := range []string{"aaa\tpush\n", "aaa\n", "aaa\tpush\tacme\textra\n", "aaa\tpush\tacme\nbroken\n"} {
		if got, err := parsePushEvents(bad); err == nil {
			t.Errorf("%q must be refused, got %+v", bad, got)
		}
	}
}

func TestHeadPushedByOwner(t *testing.T) {
	head := strings.Repeat("a", 40)
	pr := PullRequest{Number: 7, HeadRef: "feature/x", HeadSHA: head, Author: "acme", HeadRepoOwner: "acme"}
	older := func(actor string) []PushEvent {
		return []PushEvent{{After: strings.Repeat("c", 40), Type: "push", Actor: actor}}
	}
	cases := []struct {
		name  string
		owner string
		host  *fakeHost
		want  bool
		why   string // part of the reason when not trusted
	}{
		{"the owner pushed it", "acme", &fakeHost{pusher: "acme"}, true, ""},
		{"the owner pushed it, login in another case", "acme", &fakeHost{pusher: "ACME"}, true, ""},
		{"owner pusher, older owner pushes", "acme", &fakeHost{pusher: "acme", older: older("acme")}, true, ""},
		{"owner pusher, commits by somebody else (the commit check decides that, not this one)", "acme", &fakeHost{pusher: "acme", commits: []PRCommit{{AuthorLogin: "alice", CommitterLogin: "alice"}}}, true, ""},
		{"a collaborator pushed owner-authored commits", "acme", &fakeHost{pusher: "alice", commits: []PRCommit{{AuthorLogin: "acme", CommitterLogin: "acme"}}}, false, "changed by alice"},
		{"the owner pushed on top of a collaborator's push", "acme", &fakeHost{pusher: "acme", older: older("alice")}, false, "changed by alice"},
		{"the web-flow account is not the owner", "acme", &fakeHost{pusher: "web-flow"}, false, "web-flow"},
		{"a login that only starts like the owner", "acme", &fakeHost{pusher: "acme-bot"}, false, "acme-bot"},
		{"the host lookup failed", "acme", &fakeHost{pusher: "acme", pusherErr: errors.New("gh: HTTP 502")}, false, "cannot tell who pushed"},
		{"the host has no record of the push", "acme", &fakeHost{noPusher: true}, false, "no record"},
		{"the history may be cut off", "acme", &fakeHost{pusher: "acme", cut: true}, false, "may not reach back"},
		{"the owner is unknown", "", &fakeHost{pusher: "acme"}, false, "owner of the repository is unknown"},
		{"the owner is blank", "  ", &fakeHost{pusher: ""}, false, "owner of the repository is unknown"},
	}
	for _, tc := range cases {
		tc.host.pr = pr
		got, why := HeadPushedByOwner(context.Background(), tc.host, "acme/widgets", tc.owner, pr)
		if got != tc.want || (!tc.want && !strings.Contains(why, tc.why)) || (tc.want && why != "") {
			t.Errorf("%s: got (%v, %q), want %v containing %q", tc.name, got, why, tc.want, tc.why)
		}
		if strings.TrimSpace(tc.owner) != "" {
			if len(tc.host.pusherAsked) != 1 || tc.host.pusherAsked[0] != "feature/x" {
				t.Errorf("%s: the host must be asked about the head branch: %v", tc.name, tc.host.pusherAsked)
			}
		}
	}
}

func TestGHPushHistoryRefusesWhatItCannotLookUp(t *testing.T) {
	h := &GHHost{Env: []string{}} // gh must never run for these
	for name, c := range map[string][2]string{
		"not owner/name":    {"acme", "feature"},
		"path in the repo":  {"acme/widgets/../x", "feature"},
		"no head branch":    {"acme/widgets", ""},
		"query in the repo": {"acme/widgets?x=1", "feature"},
	} {
		if got, err := h.PushHistory(context.Background(), c[0], c[1]); err == nil || got.Complete || len(got.Events) != 0 {
			t.Errorf("%s: (%+v, %v), want an error", name, got, err)
		}
	}
}
