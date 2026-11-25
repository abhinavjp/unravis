package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// ghResponse is what `gh api -i` printed: the status line and headers, then the body.
type ghResponse struct {
	Status int
	Header map[string]string // lower-case names
	Body   []byte
}

// ghAPI runs `gh api -i` so the status and the ETag are visible. gh exits 1 for any status of 300 and up, a 304
// included, but still prints the headers, so the exit code is only an error when there is no response at all.
func (h *GHHost) ghAPI(ctx context.Context, etag, endpoint string) (*ghResponse, error) {
	args := []string{"api", "-i"}
	if etag != "" {
		args = append(args, "-H", "If-None-Match: "+etag)
	}
	args = append(args, endpoint)
	cmd := exec.CommandContext(ctx, "gh", args...)
	if h.Env != nil {
		cmd.Env = h.Env
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	runErr := cmd.Run()
	resp := parseGHResponse(out.Bytes())
	if resp == nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		return nil, fmt.Errorf("gh api %s: %s", endpointHead(endpoint), firstLine(msg))
	}
	return resp, nil
}

func endpointHead(e string) string {
	if i := strings.IndexByte(e, '?'); i >= 0 {
		return e[:i]
	}
	return e
}

func parseGHResponse(b []byte) *ghResponse {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	head, body, _ := bytes.Cut(b, []byte("\n\n"))
	lines := strings.Split(string(head), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "HTTP/") {
		return nil
	}
	f := strings.Fields(lines[0])
	if len(f) < 2 {
		return nil
	}
	code, err := strconv.Atoi(f[1])
	if err != nil {
		return nil
	}
	r := &ghResponse{Status: code, Header: map[string]string{}, Body: body}
	for _, l := range lines[1:] {
		if k, v, ok := strings.Cut(l, ":"); ok {
			r.Header[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return r
}

func (r *ghResponse) failed(what string) error {
	var m struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(r.Body, &m)
	if m.Message == "" {
		m.Message = "HTTP " + strconv.Itoa(r.Status)
	}
	return fmt.Errorf("%s: %s (HTTP %d)", what, m.Message, r.Status)
}

// OpenPullRequests implements Host with the pull request list of the REST API, 100 to a page, the first page
// conditional on the etag. A list with more than one page is read in full when the first page changed.
func (h *GHHost) OpenPullRequests(ctx context.Context, repo, etag string) (PullList, error) {
	var list PullList
	for page := 1; page <= 10; page++ {
		ep := fmt.Sprintf("repos/%s/pulls?state=open&per_page=100&page=%d", repo, page)
		tag := ""
		if page == 1 {
			tag = etag
		}
		resp, err := h.ghAPI(ctx, tag, ep)
		if err != nil {
			return PullList{}, err
		}
		if resp.Status == 304 {
			return PullList{ETag: etag, NotModified: true}, nil
		}
		if resp.Status != 200 {
			return PullList{}, resp.failed("list pull requests of " + repo)
		}
		if page == 1 {
			list.ETag = resp.Header["etag"]
		}
		var v []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
			Draft  bool   `json:"draft"`
			User   struct{ Login string }
			Base   struct {
				Ref  string
				Repo struct {
					FullName string `json:"full_name"`
				}
			}
			Head struct {
				Ref  string
				SHA  string
				Repo *struct {
					FullName string `json:"full_name"`
					Owner    struct{ Login string }
				}
			}
		}
		if err := json.Unmarshal(resp.Body, &v); err != nil {
			return PullList{}, fmt.Errorf("list pull requests of %s: %w", repo, err)
		}
		for _, p := range v {
			pr := PullRequest{Number: p.Number, State: strings.ToUpper(p.State), BaseRef: p.Base.Ref, HeadRef: p.Head.Ref, HeadSHA: p.Head.SHA, Author: p.User.Login}
			if p.Head.Repo != nil {
				pr.HeadRepoOwner = p.Head.Repo.Owner.Login
				pr.IsFork = !strings.EqualFold(p.Head.Repo.FullName, p.Base.Repo.FullName)
			} else {
				pr.IsFork = true // the head repository was deleted
			}
			list.PRs = append(list.PRs, pr)
		}
		if !strings.Contains(resp.Header["link"], `rel="next"`) {
			break
		}
	}
	return list, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// BranchTip implements Host with the ref of the branch.
func (h *GHHost) BranchTip(ctx context.Context, repo, branch, etag string) (BranchInfo, error) {
	resp, err := h.ghAPI(ctx, etag, "repos/"+repo+"/git/ref/heads/"+escapePath(branch))
	if err != nil {
		return BranchInfo{}, err
	}
	switch resp.Status {
	case 304:
		return BranchInfo{ETag: etag, NotModified: true}, nil
	case 404:
		return BranchInfo{}, fmt.Errorf("%w: %s in %s", ErrNoBranch, branch, repo)
	case 200:
	default:
		return BranchInfo{}, resp.failed("read branch " + branch + " of " + repo)
	}
	var v struct {
		Object struct{ SHA string }
	}
	if err := json.Unmarshal(resp.Body, &v); err != nil || !shaRe.MatchString(v.Object.SHA) {
		return BranchInfo{}, fmt.Errorf("read branch %s of %s: unexpected answer", branch, repo)
	}
	return BranchInfo{SHA: v.Object.SHA, ETag: resp.Header["etag"]}, nil
}

// FileAt implements Host with the raw contents endpoint.
func (h *GHHost) FileAt(ctx context.Context, repo, ref, path string) ([]byte, error) {
	return h.gh(ctx, "api", "-H", "Accept: application/vnd.github.raw+json",
		"repos/"+repo+"/contents/"+escapePath(path)+"?ref="+url.QueryEscape(ref))
}

// ListDir implements Host with the contents endpoint of a folder.
func (h *GHHost) ListDir(ctx context.Context, repo, ref, dir string) ([]string, error) {
	out, err := h.gh(ctx, "api", "repos/"+repo+"/contents/"+escapePath(dir)+"?ref="+url.QueryEscape(ref), "--jq", `.[] | select(.type=="file") | .name`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}
