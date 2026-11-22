package releasedash

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/update"
)

const (
	apiBase     = "https://api.github.com/repos/abhinavjp/unravis"
	finishedTTL = 24 * time.Hour // a finished release does not change
	liveTTL     = 2 * time.Minute
	maxJSON     = 4 << 20
)

// Asset is one file attached to a GitHub release.
type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:<hex>" as GitHub computed it when the file was uploaded
	URL    string `json:"browser_download_url"`
}

func cleanAssets(in []Asset) []Asset {
	out := make([]Asset, 0, len(in))
	for _, a := range in {
		a.Name = clip(a.Name)
		if !strings.HasPrefix(a.URL, RepoURL) {
			a.URL = ""
		}
		a.Digest = strings.TrimPrefix(strings.ToLower(a.Digest), "sha256:")
		a.Digest = hexOnly(a.Digest, 64)
		out = append(out, a)
	}
	return out
}

type jsonEntry struct {
	body []byte
	etag string
	at   time.Time
}

var apiPathRE = regexp.MustCompile(`^/[A-Za-z0-9/._?=&+:-]{1,200}$`)

// GetJSON reads one GitHub API address of this repository into out. The answer is kept for ttl, and after that asked again with
// the saved ETag (a "not changed" answer costs nothing against GitHub's limit). When GitHub cannot be reached the last answer is
// used and stale is true. Nothing is sent but the address.
func (g *GH) GetJSON(ctx context.Context, path string, ttl time.Duration, out any) (stale bool, err error) {
	if !apiPathRE.MatchString(path) || dotSegment(path) {
		return false, errors.New("bad address")
	}
	g.jmu.Lock()
	defer g.jmu.Unlock()
	if g.jc == nil {
		g.jc = map[string]*jsonEntry{}
	}
	e := g.jc[path]
	if e != nil && time.Since(e.at) < ttl {
		return false, json.Unmarshal(e.body, out)
	}
	base := g.Base
	if base == "" {
		base = apiBase
	}
	cl := g.Client
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second}
	}
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if rerr != nil {
		return false, errors.New("bad address")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "unravis-releases")
	if e != nil && e.etag != "" {
		req.Header.Set("If-None-Match", e.etag)
	}
	fail := func(msg string) (bool, error) {
		if e != nil && json.Unmarshal(e.body, out) == nil {
			return true, nil
		}
		return false, errors.New(msg)
	}
	resp, derr := cl.Do(req)
	if derr != nil {
		return fail("could not reach GitHub")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && e != nil:
		e.at = time.Now()
		return false, json.Unmarshal(e.body, out)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return fail("GitHub asked us to slow down")
	case resp.StatusCode == http.StatusNotFound:
		return false, errors.New("GitHub does not have this")
	case resp.StatusCode != http.StatusOK:
		return fail("GitHub answered with an error")
	}
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, maxJSON+1))
	if rerr != nil || len(b) > maxJSON {
		return fail("the GitHub answer could not be read")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fail("the GitHub answer was not understood")
	}
	g.jc[path] = &jsonEntry{body: b, etag: resp.Header.Get("ETag"), at: time.Now()}
	if len(g.jc) > 200 {
		for k := range g.jc { // keep the cache small
			delete(g.jc, k)
			break
		}
	}
	return false, nil
}

// Check is one CI check run on the release commit.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress or completed
	Conclusion string `json:"conclusion"` // success, failure, cancelled, skipped ... (empty until completed)
	Start      string `json:"start,omitempty"`
	End        string `json:"end,omitempty"`
	Seconds    int    `json:"seconds,omitempty"`
	URL        string `json:"url,omitempty"`
}

// Checks reads the check runs GitHub holds for the release tag's commit.
func (g *GH) Checks(ctx context.Context, tag string, ttl time.Duration) ([]Check, bool, error) {
	var raw struct {
		Runs []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Start      string `json:"started_at"`
			End        string `json:"completed_at"`
			URL        string `json:"html_url"`
		} `json:"check_runs"`
	}
	stale, err := g.GetJSON(ctx, "/commits/"+tag+"/check-runs?per_page=100", ttl, &raw)
	if err != nil {
		return nil, false, err
	}
	out := make([]Check, 0, len(raw.Runs))
	for _, r := range raw.Runs {
		c := Check{Name: clip(r.Name), Status: clip(r.Status), Conclusion: clip(r.Conclusion), Start: clip(r.Start), End: clip(r.End)}
		if strings.HasPrefix(r.URL, RepoURL) {
			c.URL = r.URL
		}
		c.Seconds = secondsBetween(r.Start, r.End)
		out = append(out, c)
	}
	return out, stale, nil
}

func secondsBetween(a, b string) int {
	t1, e1 := time.Parse(time.RFC3339Nano, a)
	t2, e2 := time.Parse(time.RFC3339Nano, b)
	if e1 != nil || e2 != nil || t2.Before(t1) {
		return 0
	}
	return int(t2.Sub(t1).Seconds() + 0.5)
}

// Change is one commit between the previous release and this one.
type Change struct {
	SHA   string `json:"sha"` // first 7 characters
	Title string `json:"title"`
	PR    int    `json:"pr,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Changes is what went into a release since the one before it in the same channel.
type Changes struct {
	Previous string   `json:"previous"` // tag, empty when there is none
	Total    int      `json:"total"`
	Commits  []Change `json:"commits"`
	More     bool     `json:"more,omitempty"`
}

var prRE = regexp.MustCompile(`\(#(\d{1,7})\)\s*$`)

// Changes compares the tag with the previous tag.
func (g *GH) Changes(ctx context.Context, prev, tag string, ttl time.Duration) (Changes, bool, error) {
	res := Changes{Previous: prev, Commits: []Change{}}
	if prev == "" {
		return res, false, nil
	}
	var raw struct {
		Total   int `json:"total_commits"`
		Commits []struct {
			SHA    string `json:"sha"`
			URL    string `json:"html_url"`
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"commits"`
	}
	stale, err := g.GetJSON(ctx, "/compare/"+prev+"..."+tag+"?per_page=100", ttl, &raw)
	if err != nil {
		return res, false, err
	}
	res.Total = raw.Total
	for i := len(raw.Commits) - 1; i >= 0; i-- { // newest first
		c := raw.Commits[i]
		title := strings.SplitN(strings.TrimSpace(c.Commit.Message), "\n", 2)[0]
		ch := Change{SHA: hexOnly(c.SHA, 40), Title: clip(title)}
		if len(ch.SHA) > 7 {
			ch.SHA = ch.SHA[:7]
		}
		if m := prRE.FindStringSubmatch(title); m != nil {
			ch.PR, _ = strconv.Atoi(m[1])
		}
		if strings.HasPrefix(c.URL, RepoURL) {
			ch.URL = c.URL
		}
		res.Commits = append(res.Commits, ch)
	}
	res.More = res.Total > len(res.Commits)
	return res, stale, nil
}

// previousTag finds the release just before v in the same channel among the published ones.
func previousTag(rels []Rel, v string) string {
	cur, err := update.ParseVersion(v)
	if err != nil {
		return ""
	}
	ch := update.ChannelOf(cur)
	best, bestTag := update.Version{}, ""
	for _, r := range rels {
		if r.Draft {
			continue
		}
		p, err := update.ParseVersion(strings.TrimPrefix(r.Tag, "v"))
		if err != nil || update.ChannelOf(p) != ch || update.Compare(p, cur) >= 0 {
			continue
		}
		if bestTag == "" || update.Compare(p, best) > 0 {
			best, bestTag = p, r.Tag
		}
	}
	return bestTag
}

// dotSegment reports a "." or ".." path segment ("a...b" in a compare address is not one).
func dotSegment(p string) bool {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return true
		}
	}
	return strings.Contains(p, "//")
}
