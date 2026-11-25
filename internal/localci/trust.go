package localci

import (
	"fmt"
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

// splitRepo splits "owner/name".
func splitRepo(repo string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(strings.TrimSpace(repo), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
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
