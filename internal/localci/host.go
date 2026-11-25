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

// PRCommit is one commit of a pull request and who wrote it. A login the host cannot map to an account is
// empty, and an empty login never counts as the owner.
type PRCommit struct {
	SHA            string
	AuthorLogin    string
	CommitterLogin string
}

// Host is everything the runner asks of the git host (GitHub, a self-hosted server, a test fake). The store and
// checkout code take it as a parameter and never name a host themselves.
type Host interface {
	// PullRequest reads one pull request of repo ("owner/name").
	PullRequest(ctx context.Context, repo string, number int) (PullRequest, error)
	// Collaborators lists the logins allowed to run code on this PC for repo: the ones who can push to it.
	Collaborators(ctx context.Context, repo string) ([]string, error)
	// Commits lists every commit of the pull request with the logins of its author and committer. When the
	// host cannot list them all, the list ends with a commit that has no logins.
	Commits(ctx context.Context, repo string, number int) ([]PRCommit, error)
	// CloneURL is where the bare mirror fetches repo from.
	CloneURL(repo string) string
}
