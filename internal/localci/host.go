package localci

import "context"

// PullRequest is what the checkout and trust code need to know about a pull request, whatever hosts it.
type PullRequest struct {
	Number        int
	State         string // "OPEN" for a pull request that can run
	BaseRef       string // branch the pull request merges into
	HeadRef       string // branch the pull request comes from
	HeadSHA       string // tip of the head branch
	Author        string // login of the author
	HeadRepoOwner string // login of the owner of the repository the head branch lives in
	IsFork        bool   // the head branch lives in another repository than the base
}

// Host is everything the runner asks of the git host (GitHub, a self-hosted server, a test fake). The store and
// checkout code take it as a parameter and never name a host themselves.
type Host interface {
	// PullRequest reads one pull request of repo ("owner/name").
	PullRequest(ctx context.Context, repo string, number int) (PullRequest, error)
	// Collaborators lists the logins allowed to run code on this PC for repo.
	Collaborators(ctx context.Context, repo string) ([]string, error)
	// CloneURL is where the bare mirror fetches repo from.
	CloneURL(repo string) string
}
