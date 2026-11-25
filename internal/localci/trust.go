package localci

import (
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
