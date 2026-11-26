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
		{"case of the sha does not matter", strings.ToUpper(head), []PushEvent{ev(head, "push", "acme")}, true, true, ""},
		{"a merge into the branch by somebody else is not a push", head, []PushEvent{ev(mid, "pr_merge", "alice"), ev(head, "push", "acme")}, true, true, ""},
		{"a deletion by somebody else is not a push", head, []PushEvent{ev(head, "push", "acme"), ev(other, "branch_deletion", "alice")}, true, true, ""},
		// F1: the owner pushing on top of a collaborator's push must not vouch for it
		{"an older push by a collaborator, the owner pushed the head on top", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "push", "alice"), ev(other, "branch_creation", "acme")}, true, false, "pushed to by alice"},
		{"the collaborator created the branch", head, []PushEvent{ev(head, "push", "acme"), ev(other, "branch_creation", "alice")}, true, false, "pushed to by alice"},
		{"a collaborator pushed the head", head, []PushEvent{ev(head, "push", "alice")}, true, false, "pushed to by alice"},
		{"a collaborator force pushed over the owner's work", head, []PushEvent{ev(head, "force_push", "alice"), ev(other, "push", "acme")}, true, false, "pushed to by alice"},
		{"a push without an account", head, []PushEvent{ev(head, "push", "acme"), ev(mid, "push", "")}, true, false, "no account"},
		{"look-alike login", head, []PushEvent{ev(head, "push", "acme-bot")}, true, false, "acme-bot"},
		{"web-flow is not the owner", head, []PushEvent{ev(head, "push", "web-flow")}, true, false, "web-flow"},
		// the history may be cut off: older pushes could be somebody else's
		{"the history may be cut off", head, []PushEvent{ev(head, "push", "acme")}, false, false, "may not reach back"},
		{"no record of the head", head, []PushEvent{ev(other, "push", "acme")}, true, false, "no record"},
		{"no records at all", head, nil, true, false, "no record"},
		{"only the merge names the head", head, []PushEvent{ev(head, "pr_merge", "acme")}, true, false, "no record"},
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
	full := func(oldest string) []PushEvent {
		evs := make([]PushEvent, 3)
		for i := range evs {
			evs[i] = ev("push")
		}
		evs[2] = ev(oldest)
		return evs
	}
	cases := []struct {
		name   string
		events []PushEvent
		want   bool
	}{
		{"short page", []PushEvent{ev("push")}, true},
		{"empty page", nil, true},
		{"full page ending at the creation", full("branch_creation"), true},
		{"full page ending at a push: older ones may be cut", full("push"), false},
		{"full page ending at a merge", full("pr_merge"), false},
	}
	for _, tc := range cases {
		if got := historyComplete(tc.events, 3); got != tc.want {
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
		{"a collaborator pushed owner-authored commits", "acme", &fakeHost{pusher: "alice", commits: []PRCommit{{AuthorLogin: "acme", CommitterLogin: "acme"}}}, false, "pushed to by alice"},
		{"the owner pushed on top of a collaborator's push", "acme", &fakeHost{pusher: "acme", older: older("alice")}, false, "pushed to by alice"},
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
