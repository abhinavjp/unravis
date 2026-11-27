package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// GHHost is the Host that asks the `gh` command line tool. ghExec is the only place that runs gh.
type GHHost struct {
	// Env is the process environment gh runs with (it carries the gh token); nil means the current one.
	Env []string
	// BaseURL is the web address of the host, default https://github.com.
	BaseURL string
}

func (h *GHHost) gh(ctx context.Context, args ...string) ([]byte, error) {
	return ghExec(ctx, h.Env, args...)
}

// ghExec runs the gh command line tool with env (nil: the current environment) and returns its output.
func ghExec(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // fixed binary, arguments built by this package
	if env != nil {
		cmd.Env = env
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args[:min(2, len(args))], " "), firstLine(msg))
	}
	return out.Bytes(), nil
}

// PullRequest implements Host with `gh pr view`.
func (h *GHHost) PullRequest(ctx context.Context, repo string, number int) (PullRequest, error) {
	out, err := h.gh(ctx, "pr", "view", fmt.Sprint(number), "--repo", repo, "--json",
		"number,state,baseRefName,headRefName,headRefOid,author,headRepositoryOwner,isCrossRepository")
	if err != nil {
		return PullRequest{}, err
	}
	var v struct {
		Number              int    `json:"number"`
		State               string `json:"state"`
		BaseRefName         string `json:"baseRefName"`
		HeadRefName         string `json:"headRefName"`
		HeadRefOid          string `json:"headRefOid"`
		IsCrossRepository   bool   `json:"isCrossRepository"`
		Author              struct{ Login string }
		HeadRepositoryOwner struct{ Login string }
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return PullRequest{}, fmt.Errorf("gh pr view: %w", err)
	}
	return PullRequest{Number: v.Number, State: strings.ToUpper(v.State), BaseRef: v.BaseRefName, HeadRef: v.HeadRefName,
		HeadSHA: v.HeadRefOid, Author: v.Author.Login, HeadRepoOwner: v.HeadRepositoryOwner.Login, IsFork: v.IsCrossRepository}, nil
}

// Collaborators implements Host with the repository's collaborators who can push (read-only and triage
// collaborators never run code here).
func (h *GHHost) Collaborators(ctx context.Context, repo string) ([]string, error) {
	out, err := h.gh(ctx, "api", "--paginate", "repos/"+repo+"/collaborators", "--jq", ".[] | select(.permissions.push == true) | .login")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// ghCommitListMax is the most commits GitHub lists for one pull request.
const ghCommitListMax = 250

// Commits implements Host with repos/{repo}/pulls/{n}/commits (paginated). GitHub lists at most 250 commits
// of a pull request: when the list is that long, a commit without logins ends it, so the caller cannot
// believe it has seen everything.
func (h *GHHost) Commits(ctx context.Context, repo string, number int) ([]PRCommit, error) {
	out, err := h.gh(ctx, "api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/commits", repo, number),
		"--jq", `.[] | [.sha, (.author.login // ""), (.committer.login // "")] | @tsv`)
	if err != nil {
		return nil, err
	}
	return parseCommitList(string(out)), nil
}

// parseCommitList reads the tab separated lines Commits asks gh for.
func parseCommitList(out string) []PRCommit {
	var commits []PRCommit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		c := PRCommit{SHA: f[0]}
		if len(f) > 1 {
			c.AuthorLogin = f[1]
		}
		if len(f) > 2 {
			c.CommitterLogin = f[2]
		}
		commits = append(commits, c)
	}
	if len(commits) >= ghCommitListMax {
		commits = append(commits, PRCommit{SHA: "(more commits than the host lists)"})
	}
	return commits
}

// ghPushListMax is how many push records of one branch PushHistory asks for (one page).
const ghPushListMax = 100

// PushHistory implements Host with the repository activity feed (repos/{repo}/activity), which records, for each
// push to a branch, the new tip and the account that pushed it. The history is Complete when the page is not
// full, or when its oldest record is the creation of the branch: otherwise older pushes may be cut off.
func (h *GHHost) PushHistory(ctx context.Context, repo, headRef string) (PushHistory, error) {
	if _, _, ok := splitRepo(repo); !ok {
		return PushHistory{}, fmt.Errorf("repository %q is not owner/name", repo)
	}
	if headRef == "" {
		return PushHistory{}, errors.New("the pull request has no head branch to look up")
	}
	// The branch name is outside the owner's control (a collaborator picks it), so it is escaped as one query
	// value: an "&" or "#" in a name must not add or cut a parameter. The jq program is fixed.
	path := fmt.Sprintf("repos/%s/activity?ref=%s&direction=desc&per_page=%d", repo, url.QueryEscape("refs/heads/"+headRef), ghPushListMax)
	out, err := h.gh(ctx, "api", path, "--jq", `.[] | [.after, .activity_type, (.actor.login // "")] | @tsv`)
	if err != nil {
		return PushHistory{}, err
	}
	events, err := parsePushEvents(string(out))
	if err != nil {
		return PushHistory{}, err
	}
	return PushHistory{Events: events, Complete: historyComplete(events)}, nil
}

// historyComplete reports whether events (newest first) show the whole life of the branch: the oldest record is
// the branch's creation. A short page is no proof (the feed may hold less than it has, or a branch may predate
// it), and a full page that has not reached the creation may be cut off, so both are incomplete.
func historyComplete(events []PushEvent) bool {
	return len(events) > 0 && events[len(events)-1].Type == kindBranchCreation
}

// parsePushEvents reads the tab separated lines HeadPusher asks gh for, newest first. A line that is not three
// fields is an error, not a line to skip: a skipped line could be the newer push of somebody else.
func parsePushEvents(out string) ([]PushEvent, error) {
	var events []PushEvent
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("the push record has an unreadable line (%d fields)", len(f))
		}
		events = append(events, PushEvent{After: f[0], Type: f[1], Actor: f[2]})
	}
	return events, nil
}

// CloneURL implements Host.
func (h *GHHost) CloneURL(repo string) string {
	base := strings.TrimSuffix(h.BaseURL, "/")
	if base == "" {
		base = "https://github.com"
	}
	return base + "/" + repo + ".git"
}
