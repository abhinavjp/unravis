package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Defaults of the poller.
const (
	DefaultPollInterval = 30 * time.Second
	DefaultQuietPeriod  = 60 * time.Second
)

// Poller watches a repository through the Host and puts run requests in the Queue:
//
//   - a pull request into a tracked base branch with a head commit not seen before gets a pr request;
//   - a tracked branch whose tip moved gets a push request once it has stayed the same for the quiet period
//     (a series of pushes in a row becomes one run), if the workflow runs on push for that branch;
//   - every on.schedule entry of the configured workflows gets a schedule request when it falls due. The
//     cron entries are read from the schedule branch, in UTC. The time of the last firing of each entry is
//     kept in StatePath, so a restart neither fires an entry twice nor makes up for a long absence with more
//     than one run.
//
// The first look at a pull request or a branch (the start of the process) enqueues it only when Ran says no
// result is recorded for that head commit: that is the catch-up of what happened while the agent was off,
// newest pull request first.
type Poller struct {
	Host  Host
	Queue *Queue
	Repo  string // "owner/name"

	// PRWorkflows maps a base branch to the workflow that gates pull requests into it; its keys are the base
	// branches that are watched. PushWorkflows does the same for pushes (nil means PRWorkflows).
	PRWorkflows   map[string]string
	PushWorkflows map[string][]string
	// ScheduleWorkflows are the workflow files whose on.schedule entries run here. ScheduleBranch is where the
	// files are read from (the default branch). Nothing is scheduled when either is empty.
	ScheduleWorkflows []string
	ScheduleBranch    string
	// Labels go on every request the poller enqueues.
	Labels []string

	Interval time.Duration // between two looks, default 30 s
	Quiet    time.Duration // a push waits this long after the last change, default 60 s
	// Ran says whether a result is already recorded for the request (same kind, number or branch, commit and
	// workflow). nil means nothing ran before.
	Ran func(Request) bool
	// StatePath keeps the firing times of schedule entries between runs; empty keeps them in memory only.
	StatePath string
	Now       func() time.Time
	Log       io.Writer

	mu        sync.Mutex
	started   bool
	prETag    string
	open      map[int]bool // pull requests seen open at the last full list
	prSeen    map[int]string
	tipETag   map[string]string
	tipSeen   map[string]string // branch -> sha handled
	pending   map[string]pendingPush
	wfCache   map[string]scheduleCache
	fired     map[string]time.Time
	firedLoad bool
	lastErr   map[string]string
	pushNoted map[string]bool
	schedETag string
	schedTip  string
}

type pendingPush struct {
	sha   string
	since time.Time
}

type scheduleCache struct {
	sha   string
	crons []*Cron
	err   error
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Poller) interval() time.Duration {
	if p.Interval > 0 {
		return p.Interval
	}
	return DefaultPollInterval
}

func (p *Poller) quiet() time.Duration {
	if p.Quiet > 0 {
		return p.Quiet
	}
	return DefaultQuietPeriod
}

func (p *Poller) logf(format string, a ...any) {
	if p.Log != nil {
		_, _ = fmt.Fprintf(p.Log, "%s %s\n", p.now().Format("15:04:05"), fmt.Sprintf(format, a...))
	}
}

func (p *Poller) pushWorkflows() map[string][]string {
	if p.PushWorkflows != nil {
		return p.PushWorkflows
	}
	out := make(map[string][]string, len(p.PRWorkflows))
	for b, wf := range p.PRWorkflows {
		out[b] = []string{wf}
	}
	return out
}

// Run polls until ctx ends: once at once, then every Interval. A failing look is logged and tried again at the
// next interval (the network may be down); the loop only ends with the context.
func (p *Poller) Run(ctx context.Context) {
	for {
		if err := p.Poll(ctx); err != nil && ctx.Err() == nil {
			p.logf("poll: %v", err)
		}
		t := time.NewTimer(p.interval())
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// Poll makes one look at the host and enqueues what is new. The error joins the problems of the single parts
// (one part failing does not stop the others); a repeated identical problem is logged once.
func (p *Poller) Poll(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		p.started = true
		p.open, p.prSeen = map[int]bool{}, map[int]string{}
		p.tipETag, p.tipSeen, p.pending = map[string]string{}, map[string]string{}, map[string]pendingPush{}
		p.wfCache, p.fired, p.lastErr, p.pushNoted = map[string]scheduleCache{}, map[string]time.Time{}, map[string]string{}, map[string]bool{}
	}
	var errs []error
	for _, step := range []struct {
		name string
		do   func(context.Context) error
	}{{"pull requests", p.pollPRs}, {"pushes", p.pollPushes}, {"schedule", p.pollSchedule}} {
		if err := step.do(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s: %w", step.name, err))
		}
	}
	return p.dedupeErrors(errs)
}

func (p *Poller) dedupeErrors(errs []error) error {
	var out []error
	cur := map[string]string{}
	for _, e := range errs {
		k := strings.SplitN(e.Error(), ":", 2)[0]
		cur[k] = e.Error()
		if p.lastErr[k] != e.Error() {
			out = append(out, e)
		}
	}
	p.lastErr = cur
	return errors.Join(out...)
}

func (p *Poller) enqueue(r Request) {
	r.Repo, r.Labels = p.Repo, p.Labels
	got, added, err := p.Queue.Enqueue(r)
	switch {
	case err != nil:
		p.logf("cannot queue %s: %v", r.Label(), err)
	case added:
		p.logf("queued %s at %s as %s", r.Label(), shortSHA(r.SHA), got.ID)
	}
}

func (p *Poller) pollPRs(ctx context.Context) error {
	if len(p.PRWorkflows) == 0 {
		return nil
	}
	list, err := p.Host.OpenPullRequests(ctx, p.Repo, p.prETag)
	if err != nil {
		return err
	}
	if list.NotModified {
		return nil
	}
	p.prETag = list.ETag
	prs := append([]PullRequest(nil), list.PRs...)
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number > prs[j].Number }) // newest (highest number) first
	now := map[int]bool{}
	for _, pr := range prs {
		wf := p.PRWorkflows[pr.BaseRef]
		if wf == "" || (pr.State != "" && !strings.EqualFold(pr.State, "OPEN")) {
			continue
		}
		now[pr.Number] = true
		req := Request{Kind: KindPR, Repo: p.Repo, Ref: pr.HeadRef, Base: pr.BaseRef, Number: pr.Number, SHA: pr.HeadSHA, Workflow: wf}
		seen, known := p.prSeen[pr.Number]
		if known && seen == pr.HeadSHA+"@"+pr.BaseRef {
			continue
		}
		p.prSeen[pr.Number] = pr.HeadSHA + "@" + pr.BaseRef
		if !known && p.Ran != nil && p.Ran(req) {
			continue // the catch-up has nothing to do for this one
		}
		p.enqueue(req)
	}
	// a pull request that is no longer open: its waiting or running request is of no use
	for n := range p.open {
		if now[n] {
			continue
		}
		delete(p.prSeen, n)
		p.cancelPR(n)
	}
	p.open = now
	return nil
}

func (p *Poller) cancelPR(n int) {
	rs, err := p.Queue.List()
	if err != nil {
		return
	}
	for _, r := range rs {
		if r.Active() && r.Kind == KindPR && r.Number == n && r.Repo == p.Repo {
			if _, err := p.Queue.Cancel(r.ID, "the pull request is no longer open"); err == nil {
				p.logf("cancelled %s: the pull request is no longer open", r.Label())
			}
		}
	}
}

func (p *Poller) pollPushes(ctx context.Context) error {
	branches := make([]string, 0, len(p.pushWorkflows()))
	for b := range p.pushWorkflows() {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	var errs []error
	for _, b := range branches {
		wfs := p.pushWorkflows()[b]
		info, err := p.Host.BranchTip(ctx, p.Repo, b, p.tipETag[b])
		if err != nil {
			errs = append(errs, fmt.Errorf("branch %s: %w", b, err))
			continue
		}
		if !info.NotModified {
			p.tipETag[b] = info.ETag
			tip, seen := info.SHA, p.tipSeen[b]
			switch {
			case seen == "":
				// first look at this branch since the start: enqueue at once unless a result exists
				p.tipSeen[b] = tip
				delete(p.pending, b)
				for _, wf := range wfs {
					p.maybePush(ctx, b, wf, tip, true)
				}
			case tip != seen:
				if pe, ok := p.pending[b]; !ok || pe.sha != tip {
					p.pending[b] = pendingPush{sha: tip, since: p.now()}
					p.logf("push to %s: now %s, waiting %s for more", b, shortSHA(tip), p.quiet())
				}
			default:
				delete(p.pending, b) // moved and moved back
			}
		}
		if pe, ok := p.pending[b]; ok && p.now().Sub(pe.since) >= p.quiet() {
			delete(p.pending, b)
			p.tipSeen[b] = pe.sha
			for _, wf := range wfs {
				p.maybePush(ctx, b, wf, pe.sha, false)
			}
		}
	}
	return errors.Join(errs...)
}

// maybePush queues the push run of a branch tip when the workflow runs on push for that branch.
func (p *Poller) maybePush(ctx context.Context, branch, wf, sha string, catchUp bool) {
	req := Request{Kind: KindPush, Repo: p.Repo, Ref: branch, SHA: sha, Workflow: wf}
	if catchUp && p.Ran != nil && p.Ran(req) {
		return
	}
	data, err := p.Host.FileAt(ctx, p.Repo, sha, ".github/workflows/"+wf)
	if err != nil {
		p.logf("push to %s: cannot read %s at %s: %v", branch, wf, shortSHA(sha), err)
		return
	}
	w, _, perr := ParseWorkflow(".github/workflows/"+wf, data)
	if perr != nil {
		p.logf("push to %s: %s: %v", branch, wf, perr)
		return
	}
	trig, ok := w.On["push"]
	if !ok {
		if !p.pushNoted[branch+wf] {
			p.pushNoted[branch+wf] = true
			p.logf("push to %s: %s does not run on push, nothing queued", branch, wf)
		}
		return
	}
	if len(trig.Branches) > 0 {
		match := false
		for _, g := range trig.Branches {
			if matchGlob(g, branch) {
				match = true
			}
		}
		if !match {
			p.logf("push to %s: %s runs on push only for %s, nothing queued", branch, wf, strings.Join(trig.Branches, ", "))
			return
		}
	}
	if len(trig.Paths) > 0 && !p.pushNoted[branch+wf+"paths"] {
		p.pushNoted[branch+wf+"paths"] = true
		p.logf("push to %s: %s has a paths filter; it is not evaluated here, every push runs", branch, wf)
	}
	p.enqueue(req)
}

type pollerState struct {
	Fired map[string]time.Time `json:"fired"`
}

func (p *Poller) loadFired() {
	if p.firedLoad {
		return
	}
	p.firedLoad = true
	if p.StatePath == "" {
		return
	}
	b, err := os.ReadFile(p.StatePath)
	if err != nil {
		return
	}
	var st pollerState
	if json.Unmarshal(b, &st) == nil && st.Fired != nil {
		p.fired = st.Fired
	}
}

func (p *Poller) saveFired() {
	if p.StatePath == "" {
		return
	}
	if err := writeAtomic(p.StatePath, pollerState{Fired: p.fired}); err != nil {
		p.logf("cannot save %s: %v", p.StatePath, err)
	}
}

// ScheduleEntry is one on.schedule entry of a workflow, with its next firing time.
type ScheduleEntry struct {
	Workflow string
	Cron     string
	Next     time.Time
}

// loadSchedules reads the cron entries of every schedule workflow at the schedule branch tip. The result is
// cached for a tip commit. An entry that cannot be understood is an error (it is never skipped quietly).
func (p *Poller) loadSchedules(ctx context.Context) (tip string, entries map[string][]*Cron, err error) {
	info, terr := p.Host.BranchTip(ctx, p.Repo, p.ScheduleBranch, p.schedETag)
	if terr != nil {
		return "", nil, terr
	}
	if info.NotModified && p.schedTip != "" {
		info.SHA = p.schedTip
	} else {
		p.schedETag, p.schedTip = info.ETag, info.SHA
	}
	entries = map[string][]*Cron{}
	var errs []error
	for _, wf := range p.ScheduleWorkflows {
		c, ok := p.wfCache[wf]
		if !ok || c.sha != info.SHA {
			c = scheduleCache{sha: info.SHA}
			data, ferr := p.Host.FileAt(ctx, p.Repo, info.SHA, ".github/workflows/"+wf)
			if ferr != nil {
				c.err = fmt.Errorf("%s: %w", wf, ferr)
			} else if c.crons, c.err = cronsOf(wf, data); c.err != nil {
				c.err = fmt.Errorf("%s: %w", wf, c.err)
			}
			p.wfCache[wf] = c
		}
		if c.err != nil {
			errs = append(errs, c.err)
			continue
		}
		entries[wf] = c.crons
	}
	return info.SHA, entries, errors.Join(errs...)
}

func cronsOf(file string, data []byte) ([]*Cron, error) {
	w, _, err := ParseWorkflow(".github/workflows/"+file, data)
	if err != nil {
		return nil, err
	}
	trig, ok := w.On["schedule"]
	if !ok || len(trig.Cron) == 0 {
		return nil, errors.New("has no on.schedule entry")
	}
	var out []*Cron
	for _, spec := range trig.Cron {
		c, err := ParseCron(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Schedules reads the schedule entries and when each is next due, for status output.
func (p *Poller) Schedules(ctx context.Context) ([]ScheduleEntry, error) {
	if p.ScheduleBranch == "" || len(p.ScheduleWorkflows) == 0 {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wfCache == nil {
		p.wfCache = map[string]scheduleCache{}
	}
	_, entries, err := p.loadSchedules(ctx)
	var out []ScheduleEntry
	for _, wf := range p.ScheduleWorkflows {
		for _, c := range entries[wf] {
			out = append(out, ScheduleEntry{Workflow: wf, Cron: c.Spec, Next: c.Next(p.now())})
		}
	}
	return out, err
}

func (p *Poller) pollSchedule(ctx context.Context) error {
	if p.ScheduleBranch == "" || len(p.ScheduleWorkflows) == 0 {
		return nil
	}
	tip, entries, err := p.loadSchedules(ctx)
	if tip == "" {
		return err
	}
	p.loadFired()
	now := p.now().UTC()
	changed := false
	for _, wf := range p.ScheduleWorkflows {
		for _, c := range entries[wf] {
			key := wf + "|" + c.Spec
			last, ok := p.fired[key]
			if !ok {
				p.fired[key] = now // first time seen: from now on, no catch-up of the past
				changed = true
				continue
			}
			due := c.LastBetween(last, now)
			if due.IsZero() {
				continue
			}
			p.fired[key] = due
			changed = true
			p.logf("schedule %s (%s) fell due at %s", wf, c.Spec, due.Format("2006-01-02 15:04 UTC"))
			p.enqueue(Request{Kind: KindSchedule, Ref: p.ScheduleBranch, SHA: tip, Workflow: wf, Cron: c.Spec, Slot: due.UTC().Format("20060102T1504")})
		}
	}
	if changed {
		p.saveFired()
	}
	return err
}
