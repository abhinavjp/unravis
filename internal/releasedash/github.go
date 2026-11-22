package releasedash

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	releasesURL = "https://api.github.com/repos/abhinavjp/unravis/releases?per_page=40"
	RepoURL     = "https://github.com/abhinavjp/unravis/"
	maxBody     = 2 << 20
)

// Rel is one public GitHub release. This list is information only: it is not signature-checked.
type Rel struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Published  string  `json:"published_at"`
	URL        string  `json:"html_url"`
	Target     string  `json:"target_commitish"`
	Assets     []Asset `json:"assets"`
}

// GH reads the release list with a conditional request and a minimum gap between network calls.
type GH struct {
	Client *http.Client
	URL    string // tests only
	MinGap time.Duration
	Base   string // API address of the repository, tests only

	jmu sync.Mutex
	jc  map[string]*jsonEntry

	mu   sync.Mutex
	etag string
	rels []Rel
	at   time.Time // last network try
	okAt time.Time
	err  string
}

func (g *GH) Get(ctx context.Context, force bool) (rels []Rel, okAt time.Time, errMsg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gap := g.MinGap
	if gap == 0 {
		gap = 5 * time.Minute
	}
	if force {
		gap = 30 * time.Second
	}
	if !g.at.IsZero() && time.Since(g.at) < gap {
		return g.rels, g.okAt, g.err
	}
	g.at = time.Now()
	g.err = ""
	if err := g.fetch(ctx); err != nil {
		if ctx.Err() != nil { // the caller went away: that says nothing about GitHub, so do not remember it
			g.at = time.Time{}
			return g.rels, g.okAt, g.err
		}
		g.err = err.Error()
	}
	return g.rels, g.okAt, g.err
}

func (g *GH) fetch(ctx context.Context) error {
	u := g.URL
	if u == "" {
		u = releasesURL
	}
	cl := g.Client
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errors.New("bad address")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "unravis-releases")
	if g.etag != "" {
		req.Header.Set("If-None-Match", g.etag)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return errors.New("could not reach GitHub")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		g.okAt = time.Now()
		return nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("GitHub asked us to slow down; showing the last list")
	case resp.StatusCode != http.StatusOK:
		return errors.New("GitHub answered with an error")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		return errors.New("the GitHub answer could not be read")
	}
	var rels []Rel
	if json.Unmarshal(b, &rels) != nil {
		return errors.New("the GitHub answer was not understood")
	}
	for i := range rels {
		rels[i].Tag, rels[i].Name = clip(rels[i].Tag), clip(rels[i].Name)
		rels[i].Target = hexOnly(rels[i].Target, 40)
		rels[i].Assets = cleanAssets(rels[i].Assets)
		if !strings.HasPrefix(rels[i].URL, RepoURL) {
			rels[i].URL = ""
		}
	}
	g.rels, g.etag, g.okAt = rels, resp.Header.Get("ETag"), time.Now()
	return nil
}
