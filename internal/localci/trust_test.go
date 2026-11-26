package localci

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPusherOf(t *testing.T) {
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const other = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ev := func(after, kind, actor string) PushEvent { return PushEvent{After: after, Type: kind, Actor: actor} }
	cases := []struct {
		name   string
		head   string
		events []PushEvent
		want   string
	}{
		{"the newest push of the head", head, []PushEvent{ev(head, "push", "acme"), ev(other, "push", "alice")}, "acme"},
		{"a later push moved the branch on, the head's own push is older", head, []PushEvent{ev(other, "push", "alice"), ev(head, "push", "acme")}, "acme"},
		{"the newest record wins over an older one for the same tip", head, []PushEvent{ev(head, "force_push", "alice"), ev(head, "push", "acme")}, "alice"},
		{"force push counts", head, []PushEvent{ev(head, "force_push", "acme")}, "acme"},
		{"first push of a new branch counts", head, []PushEvent{ev(head, "branch_creation", "acme")}, "acme"},
		{"case of the sha does not matter", strings.ToUpper(head), []PushEvent{ev(head, "push", "acme")}, "acme"},
		{"a merge into the branch is not a push of its head", head, []PushEvent{ev(head, "pr_merge", "alice")}, ""},
		{"a deletion is not a push of its head", head, []PushEvent{ev(head, "branch_deletion", "alice")}, ""},
		{"no record of the head", head, []PushEvent{ev(other, "push", "acme")}, ""},
		{"no records at all", head, nil, ""},
		{"record names no account", head, []PushEvent{ev(head, "push", "")}, ""},
		{"empty head sha never matches an empty tip", "", []PushEvent{ev("", "push", "acme")}, ""},
		{"blank head sha", "  ", []PushEvent{ev("", "push", "acme")}, ""},
	}
	for _, tc := range cases {
		if got := PusherOf(tc.head, tc.events); got != tc.want {
			t.Errorf("%s: PusherOf = %q, want %q", tc.name, got, tc.want)
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
	cases := []struct {
		name  string
		owner string
		host  *fakeHost
		want  bool
		why   string // part of the reason when not trusted
	}{
		{"the owner pushed it", "acme", &fakeHost{pusher: "acme"}, true, ""},
		{"the owner pushed it, login in another case", "acme", &fakeHost{pusher: "ACME"}, true, ""},
		{"owner pusher, commits by somebody else (the commit check decides that, not this one)", "acme", &fakeHost{pusher: "acme", commits: []PRCommit{{AuthorLogin: "alice", CommitterLogin: "alice"}}}, true, ""},
		{"a collaborator pushed owner-authored commits", "acme", &fakeHost{pusher: "alice", commits: []PRCommit{{AuthorLogin: "acme", CommitterLogin: "acme"}}}, false, "pushed by alice"},
		{"the web-flow account is not the owner", "acme", &fakeHost{pusher: "web-flow"}, false, "pushed by web-flow"},
		{"a login that only starts like the owner", "acme", &fakeHost{pusher: "acme-bot"}, false, "pushed by acme-bot"},
		{"the host lookup failed", "acme", &fakeHost{pusher: "acme", pusherErr: errors.New("gh: HTTP 502")}, false, "cannot tell who pushed"},
		{"the host has no record of the push", "acme", &fakeHost{noPusher: true}, false, "no record"},
		{"the owner is unknown", "", &fakeHost{pusher: "acme"}, false, "owner of the repository is unknown"},
		{"the owner is blank", "  ", &fakeHost{pusher: ""}, false, "owner of the repository is unknown"},
	}
	for _, tc := range cases {
		tc.host.pr = pr
		got, why := HeadPushedByOwner(context.Background(), tc.host, "acme/widgets", tc.owner, pr)
		if got != tc.want || (!tc.want && !strings.Contains(why, tc.why)) || (tc.want && why != "") {
			t.Errorf("%s: got (%v, %q), want %v containing %q", tc.name, got, why, tc.want, tc.why)
		}
		if tc.owner != "" && strings.TrimSpace(tc.owner) != "" {
			if len(tc.host.pusherAsked) != 1 || tc.host.pusherAsked[0] != "feature/x@"+head {
				t.Errorf("%s: the host must be asked about the head branch and commit: %v", tc.name, tc.host.pusherAsked)
			}
		}
	}
}

func TestGHHeadPusherRefusesWhatItCannotLookUp(t *testing.T) {
	h := &GHHost{Env: []string{}} // gh must never run for these
	sha := strings.Repeat("a", 40)
	for name, c := range map[string][3]string{
		"not owner/name":    {"acme", "feature", sha},
		"path in the repo":  {"acme/widgets/../x", "feature", sha},
		"no head branch":    {"acme/widgets", "", sha},
		"no head commit":    {"acme/widgets", "feature", ""},
		"query in the repo": {"acme/widgets?x=1", "feature", sha},
	} {
		if got, err := h.HeadPusher(context.Background(), c[0], c[1], c[2]); err == nil || got != "" {
			t.Errorf("%s: (%q, %v), want an error", name, got, err)
		}
	}
}
