package releasedash

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/update"
)

// StageNames is the order Release.ps1 works in (Wait is shown as Build).
var StageNames = []string{"Preflight", "Tag", "Build", "Sign", "Publish", "Feed", "Verify"}

var eventStage = map[string]string{
	"preflight-ok": "Preflight", "tagged": "Tag", "built": "Build", "signed": "Sign",
	"published": "Publish", "feed-updated": "Feed", "verified": "Verify",
}

type Step struct {
	Name   string `json:"name"`
	State  string `json:"state"` // done, failed, running, pending or unknown (not recorded on this PC)
	At     string `json:"at,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Release struct {
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	Channel    string `json:"channel"`
	Published  string `json:"published,omitempty"`
	URL        string `json:"url,omitempty"`
	RunURL     string `json:"runUrl,omitempty"`
	OnGitHub   bool   `json:"onGitHub"`
	OnFeed     bool   `json:"onFeed"`
	Withdrawn  bool   `json:"withdrawn"`
	Verify     string `json:"verify"` // passed, failed, pending or unknown
	Steps      []Step `json:"steps"`
	Recorded   bool   `json:"recorded"` // this PC has the release manager's log for it
	LastReason string `json:"reason,omitempty"`
}

type Feed struct {
	Channel   string   `json:"channel"`
	Status    string   `json:"status"` // ok, unreachable, rejected, none
	Version   string   `json:"version,omitempty"`
	Published string   `json:"published,omitempty"`
	Expires   string   `json:"expires,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	Paused    bool     `json:"paused"`
	Blocked   []string `json:"blocked,omitempty"`
	Rollout   string   `json:"rollout,omitempty"`
	Note      string   `json:"note,omitempty"`
}

type Source struct {
	OK     bool   `json:"ok"`
	At     string `json:"at,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type Snapshot struct {
	At       string    `json:"at"`
	Run      *Run      `json:"run,omitempty"`
	Releases []Release `json:"releases"`
	Feeds    []Feed    `json:"feeds"`
	GitHub   Source    `json:"github"`
	Local    Source    `json:"local"`
	Feed     Source    `json:"feed"`
}

// Dash holds the three readers. It is safe for concurrent use.
type Dash struct {
	DataDir   string
	StatePath string // the dashboard's own update memory; empty keeps it in memory
	Keys      []update.Key
	GH        *GH
	FeedGap   time.Duration // zero: 5 minutes

	mu       sync.Mutex
	feeds    []Feed
	feedAt   time.Time
	feedErr  string
	fetchers map[string]*update.Fetcher
}

func New(dataDir, statePath string, keys []update.Key) *Dash {
	return &Dash{DataDir: dataDir, StatePath: statePath, Keys: keys, GH: &GH{}}
}

func (d *Dash) fetcher(ch string) *update.Fetcher {
	if d.fetchers == nil {
		d.fetchers = map[string]*update.Fetcher{}
	}
	if f := d.fetchers[ch]; f != nil {
		return f
	}
	cur := "0.0.0"
	if ch == update.ChannelRC {
		cur = "0.0.0-rc.0"
	}
	f := &update.Fetcher{Keys: d.Keys, Build: update.ChannelRC, Want: ch, Current: cur, StatePath: d.StatePath}
	d.fetchers[ch] = f
	return f
}

func (d *Dash) readFeeds(ctx context.Context, force bool) ([]Feed, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	gap := d.FeedGap
	if gap == 0 {
		gap = 5 * time.Minute
	}
	if force {
		gap = 30 * time.Second
	}
	if !d.feedAt.IsZero() && time.Since(d.feedAt) < gap {
		return d.feeds, d.feedErr
	}
	d.feedAt = time.Now()
	var out []Feed
	bad, unreachable := "", 0
	for _, ch := range []string{update.ChannelRC, update.ChannelStable} {
		fd := Feed{Channel: ch, Status: "none"}
		res := d.fetcher(ch).Check(ctx)
		switch res.Status {
		case update.StatusAvailable, update.StatusLatest:
			fd.Status = "ok"
			if res.Manifest != nil {
				fd.Version = res.Manifest.Version
				fd.Published = res.Manifest.Published.UTC().Format(time.RFC3339)
				fd.Expires = res.Manifest.Expires.UTC().Format(time.RFC3339)
			}
			if res.Held {
				fd.Detail = clip(res.Detail)
			}
		case update.StatusUnreachable:
			fd.Status = "unreachable"
			unreachable++
		case update.StatusRejected:
			fd.Status, bad = "rejected", "the feed answer failed its signature or trust check"
			fd.Detail = clip(res.Detail)
		}
		if d.StatePath != "" {
			if st, err := update.ReadState(d.StatePath); err == nil {
				if c := st.Control[ch]; c != nil && c.Expires.After(time.Now()) {
					fd.Paused, fd.Blocked, fd.Note = c.Pause, c.Blocked, clip(c.Note)
					if c.RolloutVersion != "" && c.RolloutPercent > 0 && c.RolloutPercent < 100 {
						fd.Rollout = c.RolloutVersion + " to " + strconv.Itoa(c.RolloutPercent) + "% of PCs"
					}
				}
			}
		}
		out = append(out, fd)
	}
	if ctx.Err() != nil { // the caller went away: keep the last answer and ask again next time
		d.feedAt = time.Time{}
		return d.feeds, d.feedErr
	}
	if unreachable == 2 { // one channel may simply have no release yet; both failing means the feed is not reachable
		bad = "could not reach the update feed"
	}
	d.feeds, d.feedErr = out, bad
	return out, bad
}

// Snapshot gathers everything. Network reads are cached (see GH and FeedGap); local reads happen every call.
func (d *Dash) Snapshot(ctx context.Context, force bool) Snapshot {
	now := time.Now().UTC()
	s := Snapshot{At: now.Format(time.RFC3339), Releases: []Release{}}
	loc := ReadLocal(d.DataDir)
	s.Run = loc.Run
	s.Local = Source{OK: loc.Found}
	if !loc.Found {
		s.Local.Detail = "No release log on this PC. Step by step status only shows on the PC that makes releases."
	}
	rels, okAt, ghErr := d.GH.Get(ctx, force)
	s.GitHub = Source{OK: ghErr == "" && !okAt.IsZero(), Detail: ghErr}
	if !okAt.IsZero() {
		s.GitHub.At = okAt.UTC().Format(time.RFC3339)
	}
	feeds, feedErr := d.readFeeds(ctx, force)
	s.Feeds = feeds
	s.Feed = Source{OK: feedErr == "", Detail: feedErr}
	s.Releases = merge(rels, feeds, loc)
	return s
}

func chanOf(v string) (string, bool) {
	p, err := update.ParseVersion(v)
	if err != nil {
		return "", false
	}
	return update.ChannelOf(p), true
}

func merge(gh []Rel, feeds []Feed, loc Local) []Release {
	by := map[string]*Release{}
	get := func(v string) *Release {
		if r := by[v]; r != nil {
			return r
		}
		ch, ok := chanOf(v)
		if !ok {
			return nil
		}
		r := &Release{Version: v, Tag: "v" + v, Channel: ch, Verify: "unknown"}
		by[v] = r
		return r
	}
	for _, g := range gh {
		if r := get(strings.TrimPrefix(g.Tag, "v")); r != nil {
			r.OnGitHub, r.Published, r.URL = !g.Draft, g.Published, g.URL
		}
	}
	events := map[string][]Event{}
	for _, e := range loc.Events {
		if e.Version != "" && get(e.Version) != nil {
			events[e.Version] = append(events[e.Version], e)
		}
	}
	blocked := map[string]bool{}
	for _, f := range feeds {
		for _, b := range f.Blocked {
			blocked[b] = true
		}
		if f.Version != "" {
			if r := get(f.Version); r != nil {
				r.OnFeed = true
			}
		}
	}
	if loc.Run != nil {
		get(loc.Run.Version)
	}
	out := make([]Release, 0, len(by))
	for v, r := range by {
		r.Withdrawn = blocked[v]
		fillSteps(r, events[v], loc.Run)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := update.ParseVersion(out[i].Version)
		b, _ := update.ParseVersion(out[j].Version)
		return update.Compare(a, b) > 0
	})
	return out
}

func fillSteps(r *Release, ev []Event, run *Run) {
	st := map[string]*Step{}
	for _, n := range StageNames {
		st[n] = &Step{Name: n, State: "unknown"}
	}
	if len(ev) > 0 {
		r.Recorded = true
		for _, n := range StageNames {
			st[n].State = "pending"
		}
	}
	for _, e := range ev {
		if n, ok := eventStage[e.Name]; ok {
			st[n].State, st[n].At = "done", e.At
			if e.Run != "" && (n == "Build" || n == "Verify") {
				r.RunURL = RepoURL + "actions/runs/" + e.Run
			}
		}
		switch e.Name {
		case "verify-failed":
			st["Verify"].State, st["Verify"].At, st["Verify"].Reason = "failed", e.At, e.Reason
			if e.Run != "" {
				r.RunURL = RepoURL + "actions/runs/" + e.Run
			}
		case "withdrawn":
			r.Withdrawn = true
		case "stopped":
			name := e.Stage
			if name == "Wait" {
				name = "Build"
			}
			if s := st[name]; s != nil && s.State != "done" {
				s.State, s.At, s.Reason = "failed", e.At, e.Reason
			}
			r.LastReason = e.Reason
		}
	}
	if !r.Recorded {
		if r.OnGitHub {
			st["Publish"].State = "done"
		}
		if r.OnFeed {
			st["Feed"].State = "done"
		}
	}
	if run != nil && run.Version == r.Version && run.Status == "running" {
		for _, n := range StageNames {
			if st[n].State == "pending" {
				st[n].State = "running"
				break
			}
		}
	}
	for _, n := range StageNames {
		r.Steps = append(r.Steps, *st[n])
	}
	switch v := st["Verify"].State; {
	case v == "done":
		r.Verify = "passed"
	case v == "failed":
		r.Verify = "failed"
	case r.Recorded:
		r.Verify = "pending"
	}
}
