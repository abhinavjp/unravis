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

// Kinds of record in the host's activity feed of a branch. Every kind except a deletion can put a commit on the
// branch (a pull request merged into it, or a merge queue, lands the merger's chosen commits there with the
// merging service as committer, so the commit logins cannot show who did it), so every one of them must name the
// owner. A kind this code does not know is not trusted: a new kind of change must not default to harmless.
const (
	kindBranchCreation = "branch_creation"
	kindBranchDeletion = "branch_deletion"
)

var ownerOnlyKinds = map[string]bool{
	"push": true, "force_push": true, kindBranchCreation: true, "pr_merge": true, "merge_queue_merge": true,
}

// PushHistory is the answer of Host.PushHistory: the push records of one branch, newest first.
type PushHistory struct {
	Events []PushEvent
	// Complete is true when the list reaches back to the creation of the branch, so no older record is missing.
	Complete bool
}

// AllPushesByOwner reports whether every record of the branch except a deletion names owner as the actor, the
// oldest record is the owner creating the branch, and the head commit headSHA is one of the records. It fails
// closed, with a reason: a history that may be cut off or does not start at a branch creation, any record by
// anybody else (an empty actor too), a kind of record this code does not know, or no record of the head. Looking
// only at who pushed the head would let the owner vouch for a collaborator's earlier push by pushing on top of
// it, and looking only at pushes would let a collaborator's pull request merge (web-flow is its committer) pass.
//
// A branch that was deleted and made again keeps the records of its earlier life in the feed, and they must name
// the owner too. The new branch starts at a commit the owner chose when creating it: that creation record names
// the owner, who vouches for the commit it started from, like any commit the owner builds on.
func AllPushesByOwner(owner, headSHA string, hist PushHistory) (bool, string) {
	owner, headSHA = strings.TrimSpace(owner), strings.TrimSpace(headSHA)
	if owner == "" {
		return false, "the owner of the repository is unknown"
	}
	if headSHA == "" {
		return false, "the pull request has no head commit to look up"
	}
	if !hist.Complete {
		return false, "the host's push record may not reach back to the start of the branch"
	}
	sawHead := false
	for _, e := range hist.Events {
		if e.Type == kindBranchDeletion {
			continue // removes the branch, puts no commit on it; a later creation record is judged on its own
		}
		if !ownerOnlyKinds[e.Type] {
			return false, fmt.Sprintf("the host's record of the branch has a kind of change %q that is not known", e.Type)
		}
		if actor := strings.TrimSpace(e.Actor); !strings.EqualFold(actor, owner) {
			if actor == "" {
				return false, "a change of the branch names no account"
			}
			return false, fmt.Sprintf("the branch was changed by %s, not only by %s", actor, owner)
		}
		if strings.EqualFold(strings.TrimSpace(e.After), headSHA) {
			sawHead = true
		}
	}
	if !sawHead {
		return false, "the host has no record of who pushed the head commit"
	}
	if oldest := hist.Events[len(hist.Events)-1]; oldest.Type != kindBranchCreation {
		return false, "the host's record of the branch does not start at its creation"
	}
	return true, ""
}

// HeadPushedByOwner reports whether the host's own push record says that only owner ever pushed to the head
// branch of pr, and pushed its head commit: the second proof that a branch is the owner's, next to
// CommitsByOwner. The logins in a commit are text its pusher wrote, so a collaborator who forges the owner's
// identity passes CommitsByOwner; the pusher is the account that held the credentials. It fails closed: a
// lookup error, an answer that may be cut off or any other account is false, and the reason says which.
func HeadPushedByOwner(ctx context.Context, h Host, repo, owner string, pr PullRequest) (bool, string) {
	if strings.TrimSpace(owner) == "" {
		return false, "the owner of the repository is unknown"
	}
	hist, err := h.PushHistory(ctx, repo, pr.HeadRef)
	if err != nil {
		return false, fmt.Sprintf("cannot tell who pushed to the branch: %v", err)
	}
	return AllPushesByOwner(owner, pr.HeadSHA, hist)
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

// IsTrustRefusal reports whether reason is one of the refusals of Trusted, or of the base-branch check that
// follows it: the pull request was kept off this PC on purpose, so it has no verdict to compare with GitHub's.
func IsTrustRefusal(reason string) bool {
	return strings.HasPrefix(reason, "fork pull request:") ||
		strings.HasSuffix(reason, "is not in the allow-list of authors who may run code on this PC") ||
		strings.HasPrefix(reason, "the code is not all the owner's and ") ||
		strings.HasPrefix(reason, "the author of the pull request is unknown")
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
