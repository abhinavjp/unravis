package localci

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Trust is the verdict of Trusted.
type Trust struct {
	// Run is true when the pull request may run on this PC.
	Run bool
	// FromBase is true when the runner definition (workflows, config, protected folders) must come from the
	// base branch, not from the pull request: the author is not the owner of the base repository.
	FromBase bool
	// Reason says why the pull request may not run (empty when it may).
	Reason string
}

var repoSlugRe = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

// splitRepo splits "owner/name". The slug ends up in host calls and in a clone URL, so it is strict: letters,
// digits and "-" for the owner, plus "." and "_" for the name ("." and ".." are not names).
func splitRepo(repo string) (owner, name string, ok bool) {
	if !repoSlugRe.MatchString(repo) {
		return "", "", false
	}
	owner, name, _ = strings.Cut(repo, "/")
	if name == "." || name == ".." {
		return "", "", false
	}
	return owner, name, true
}

// webFlowLogin is the committer GitHub records for a commit made in its web editor or by a squash merge
// button: the content is the author's, so it counts as the author's own commit.
const webFlowLogin = "web-flow"

// CommitsByOwner reports whether every commit of a pull request was written AND committed by owner. A commit
// whose login is unknown (empty) counts as not the owner's, and an empty list proves nothing.
func CommitsByOwner(owner string, commits []PRCommit) bool {
	owner = strings.TrimSpace(owner)
	if len(commits) == 0 || owner == "" {
		return false
	}
	for _, c := range commits {
		if !strings.EqualFold(strings.TrimSpace(c.AuthorLogin), owner) {
			return false
		}
		committer := strings.TrimSpace(c.CommitterLogin)
		if !strings.EqualFold(committer, owner) && !strings.EqualFold(committer, webFlowLogin) {
			return false
		}
	}
	return true
}

// PushEvent is one record of a push to a branch: the commit the branch pointed to afterwards, what kind of push
// it was (the host's word, such as "push" or "force_push"), and the account that pushed.
type PushEvent struct {
	After string
	Type  string
	Actor string
}

// pushKinds are the records that put a commit on a branch. Other records (a branch deleted, a pull request
// merged into it) say nothing about who pushed a branch's head.
var pushKinds = map[string]bool{"push": true, "force_push": true, "branch_creation": true}

// PusherOf returns the account that pushed headSHA, from push records listed newest first: the newest
// record of a push kind whose tip is headSHA. It returns "" when no record shows that push, or when the record
// names no account. It never guesses from an older or a different push.
func PusherOf(headSHA string, events []PushEvent) string {
	headSHA = strings.TrimSpace(headSHA)
	if headSHA == "" {
		return ""
	}
	for _, e := range events {
		if pushKinds[e.Type] && strings.EqualFold(strings.TrimSpace(e.After), headSHA) {
			return strings.TrimSpace(e.Actor)
		}
	}
	return ""
}

// HeadPushedByOwner reports whether the host's own record says that owner pushed the head commit of pr, as
// the second proof that a branch is the owner's, next to CommitsByOwner. The logins in a commit are text its
// pusher wrote, so a collaborator who forges the owner's identity passes CommitsByOwner; the pusher is the
// account that held the credentials. It fails closed: a lookup error, an empty answer or any other account
// is false, and the reason says which.
func HeadPushedByOwner(ctx context.Context, h Host, repo, owner string, pr PullRequest) (bool, string) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, "the owner of the repository is unknown"
	}
	pusher, err := h.HeadPusher(ctx, repo, pr.HeadRef, pr.HeadSHA)
	if err != nil {
		return false, fmt.Sprintf("cannot tell who pushed the head commit: %v", err)
	}
	if pusher == "" {
		return false, "the host has no record of who pushed the head commit"
	}
	if !strings.EqualFold(pusher, owner) {
		return false, fmt.Sprintf("the head commit was pushed by %s, not by %s", pusher, owner)
	}
	return true, ""
}

// TrustedBase reports whether base is one of the branches whose content the runner may take the runner
// definition from. A pull request of a non-owner may only be run into such a branch: any other branch can be
// created or rewritten by whoever has push access, so it proves nothing.
func TrustedBase(base string, trusted []string) bool {
	for _, t := range trusted {
		if t != "" && t == base {
			return true
		}
	}
	return false
}

// Trusted decides whether the code of a pull request may run on this PC. It is a pure function.
//
//   - A pull request whose head branch lives outside the base repository (a fork, or an unknown head owner)
//     never runs.
//   - The owner of the base repository runs, with the workflow files of the pull request.
//   - A collaborator (the allow-list) runs, but the runner definition is read from the base branch.
//   - Anybody else never runs.
func Trusted(prAuthor, headRepoOwner string, collaborators []string, baseRepo string) Trust {
	owner, _, ok := splitRepo(baseRepo)
	if !ok {
		return Trust{Reason: fmt.Sprintf("repository %q is not owner/name", baseRepo)}
	}
	author := strings.TrimSpace(prAuthor)
	if author == "" {
		return Trust{Reason: "the author of the pull request is unknown"}
	}
	if !strings.EqualFold(strings.TrimSpace(headRepoOwner), owner) {
		return Trust{Reason: "fork pull request: a maintainer pushes it to a branch of this repository"}
	}
	if strings.EqualFold(author, owner) {
		return Trust{Run: true}
	}
	for _, c := range collaborators {
		if strings.EqualFold(strings.TrimSpace(c), author) {
			return Trust{Run: true, FromBase: true}
		}
	}
	return Trust{Reason: fmt.Sprintf("%s is not in the allow-list of authors who may run code on this PC", author)}
}
