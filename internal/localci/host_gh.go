package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// GHHost is the Host that asks the `gh` command line tool. It is the only place that runs gh.
type GHHost struct {
	// Env is the process environment gh runs with (it carries the gh token); nil means the current one.
	Env []string
	// BaseURL is the web address of the host, default https://github.com.
	BaseURL string
}

func (h *GHHost) gh(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // fixed binary, arguments built by this file
	if h.Env != nil {
		cmd.Env = h.Env
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

// Collaborators implements Host with the repository's collaborator list.
func (h *GHHost) Collaborators(ctx context.Context, repo string) ([]string, error) {
	out, err := h.gh(ctx, "api", "--paginate", "repos/"+repo+"/collaborators", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// CloneURL implements Host.
func (h *GHHost) CloneURL(repo string) string {
	base := strings.TrimSuffix(h.BaseURL, "/")
	if base == "" {
		base = "https://github.com"
	}
	return base + "/" + repo + ".git"
}
