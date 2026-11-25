package localci

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- naming

// GateRule names the status that gates one kind of request: a pull request into Branch, or a push to Branch.
// Context is the name the host requires (a branch protection rule asks for it). Workflow narrows the rule to one
// workflow file; empty matches any.
type GateRule struct {
	Kind     string `json:"kind"` // pr or push
	Branch   string `json:"branch"`
	Workflow string `json:"workflow,omitempty"`
	Context  string `json:"context"`
}

// StatusNaming decides the names of the statuses a request gets. In shadow mode (PostRequired false, the zero
// value) every name starts with Prefix, so nothing this program posts can satisfy or block a branch protection
// rule. With PostRequired the gate is posted under its real, required name (cutover phases 1 and 2).
type StatusNaming struct {
	Prefix       string // default DefaultStatusPrefix
	Gates        []GateRule
	PostRequired bool
}

func (n StatusNaming) prefix() string {
	if n.Prefix == "" {
		return DefaultStatusPrefix
	}
	return n.Prefix
}

func requestBranch(r Request) string {
	if r.Kind == KindPR {
		return r.Base
	}
	return r.Ref
}

func (g GateRule) matches(r Request) bool {
	return g.Kind == r.Kind && g.Branch == requestBranch(r) && (g.Workflow == "" || g.Workflow == r.Workflow)
}

// Rule is the gate rule of a request; ok is false when no rule names it (such a request gets no statuses).
func (n StatusNaming) Rule(r Request) (GateRule, bool) {
	for _, g := range n.Gates {
		if g.matches(r) {
			return g, true
		}
	}
	return GateRule{}, false
}

// GateName is the context of the whole-run status of a request: local/<context> in shadow mode, the required
// context itself with PostRequired.
func (n StatusNaming) GateName(r Request) (string, bool) {
	g, ok := n.Rule(r)
	if !ok {
		return "", false
	}
	if n.PostRequired {
		return g.Context, true
	}
	return n.prefix() + g.Context, true
}

// JobName is the context of one job's status: local/<job>. When more than one workflow runs for the same branch
// and kind (a push to develop runs two), the workflow's name is put in between, so that two jobs of the same
// name do not overwrite each other. It is empty for a job whose name is the gate's own (the gate status says it).
func (n StatusNaming) JobName(r Request, job string) string {
	g, ok := n.Rule(r)
	if !ok || strings.TrimSpace(job) == "" {
		return ""
	}
	same := 0
	for _, o := range n.Gates {
		if o.Kind == g.Kind && o.Branch == g.Branch {
			same++
		}
	}
	name := n.prefix() + job
	if same > 1 {
		name = n.prefix() + strings.TrimSuffix(strings.TrimSuffix(r.Workflow, ".yml"), ".yaml") + "/" + job
	}
	if gate, _ := n.GateName(r); name == gate || name == n.prefix()+g.Context {
		return ""
	}
	if len([]rune(name)) > statusNameMax {
		return ""
	}
	return name
}

// ---------------------------------------------------------------- the poster

// StatusHealth is what `status` shows about posting.
type StatusHealth struct {
	Posted      int       `json:"posted"`
	Failed      int       `json:"failed"`
	Waiting     int       `json:"waiting"`
	LastPost    time.Time `json:"lastPost,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
	LastErrorAt time.Time `json:"lastErrorAt,omitempty"`
	Mode        string    `json:"mode,omitempty"` // shadow or required
}

type postItem struct {
	repo, sha, name, state, desc string
}

func (p postItem) key() string { return p.repo + "@" + p.sha + " " + p.name }

// StatusPoster posts in the background, one at a time and in order, so that a slow host never slows a run and
// an old state can never land after a newer one. A newer state for the same commit and context replaces one
// that has not gone out yet. A failure never reaches the run: it is logged and shown in Health.
type StatusPoster struct {
	Reporter StatusReporter
	// Log gets one line per failure (already redacted by the caller's Reporter errors; add Redact for more).
	Log func(format string, a ...any)
	// Timeout bounds one post with all its retries; default 2 minutes.
	Timeout time.Duration
	Now     func() time.Time
	// syncPosts makes Post wait until the poster is idle, so a test sees every state go out and none is replaced by a newer one.
	syncPosts bool

	once   sync.Once
	mu     sync.Mutex
	cond   *sync.Cond
	order  []string
	items  map[string]postItem
	busy   bool
	closed bool
	health StatusHealth
	done   chan struct{}
	stop   context.CancelFunc
	base   context.Context
}

func (p *StatusPoster) init() {
	p.once.Do(func() {
		p.cond = sync.NewCond(&p.mu)
		p.items = map[string]postItem{}
		p.done = make(chan struct{})
		p.base, p.stop = context.WithCancel(context.Background())
		go p.loop()
	})
}

func (p *StatusPoster) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *StatusPoster) logf(format string, a ...any) {
	if p.Log != nil {
		p.Log(format, a...)
	}
}

// Post queues one status and returns at once.
func (p *StatusPoster) Post(repo, sha, name, state, desc string) {
	p.init()
	it := postItem{repo, sha, name, state, desc}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	k := it.key()
	if _, queued := p.items[k]; !queued {
		p.order = append(p.order, k)
	}
	p.items[k] = it
	p.cond.Broadcast()
	p.mu.Unlock()
	if p.syncPosts {
		p.Flush(10 * time.Second)
	}
}

func (p *StatusPoster) loop() {
	defer close(p.done)
	for {
		p.mu.Lock()
		for len(p.order) == 0 && !p.closed {
			p.cond.Wait()
		}
		if len(p.order) == 0 {
			p.mu.Unlock()
			return
		}
		k := p.order[0]
		p.order = p.order[1:]
		it := p.items[k]
		delete(p.items, k)
		p.busy = true
		p.mu.Unlock()

		timeout := p.Timeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(p.base, timeout)
		err := p.Reporter.Report(ctx, it.repo, it.sha, it.name, it.state, it.desc)
		cancel()

		p.mu.Lock()
		p.busy = false
		if err != nil {
			p.health.Failed++
			p.health.LastError = fmt.Sprintf("%s %s: %v", it.name, it.state, err)
			p.health.LastErrorAt = p.now()
		} else {
			p.health.Posted++
			p.health.LastPost = p.now()
		}
		p.cond.Broadcast()
		p.mu.Unlock()
		if err != nil {
			p.logf("status %s on %s: %v", it.name, shortSHA(it.sha), err)
		}
	}
}

// Health is a copy of the counters.
func (p *StatusPoster) Health() StatusHealth {
	p.init()
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.health
	h.Waiting = len(p.order)
	if p.busy {
		h.Waiting++
	}
	return h
}

// Flush waits until everything posted so far has gone out (or failed for good), at most for wait. It says
// whether the poster is idle.
func (p *StatusPoster) Flush(wait time.Duration) bool {
	p.init()
	deadline := time.Now().Add(wait)
	for {
		p.mu.Lock()
		idle := len(p.order) == 0 && !p.busy
		p.mu.Unlock()
		if idle {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Close flushes for at most wait, then stops the worker (what is still in flight is cancelled).
func (p *StatusPoster) Close(wait time.Duration) {
	p.init()
	p.Flush(wait)
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	select {
	case <-p.done:
	case <-time.After(time.Second):
		p.stop()
		<-p.done
	}
	p.stop()
}

// ---------------------------------------------------------------- the service the agent calls

// StatusService turns what the agent sees (a request queued, started, progressing, ended) into statuses. All
// posting happens in the agent's process, with its token: children only leave progress files.
type StatusService struct {
	Poster *StatusPoster
	Naming StatusNaming
	Dir    string // the state folder, where progress files live
	Now    func() time.Time

	mu     sync.Mutex
	queued map[string]Request
	runs   map[string]*runStatus
}

type runStatus struct {
	jobs    map[string]bool
	running string
}

func (s *StatusService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Health is the poster's counters with the mode.
func (s *StatusService) Health() *StatusHealth {
	if s == nil || s.Poster == nil {
		return nil
	}
	h := s.Poster.Health()
	h.Mode = "shadow"
	if s.Naming.PostRequired {
		h.Mode = "required"
	}
	return &h
}

func (s *StatusService) post(r Request, name, state, desc string) {
	if s.Poster == nil || name == "" {
		return
	}
	s.Poster.Post(r.Repo, r.SHA, name, state, desc)
}

func (s *StatusService) gate(r Request, state, desc string) {
	if name, ok := s.Naming.GateName(r); ok {
		s.post(r, name, state, desc)
	}
}

// ObserveQueue posts "queued" for every request that is new in the queue (the poller's, `rerun`'s, a catch-up's),
// and "cancelled" for one that was cancelled before it ran. A superseded request gets nothing: its newer head has
// its own statuses.
func (s *StatusService) ObserveQueue(q *Queue) {
	rs, err := q.folderOf("queued")
	if err != nil {
		return
	}
	present := map[string]bool{}
	s.mu.Lock()
	if s.queued == nil {
		s.queued = map[string]Request{}
	}
	var fresh []Request
	for _, r := range rs {
		present[r.ID] = true
		if _, seen := s.queued[r.ID]; !seen {
			s.queued[r.ID] = r
			fresh = append(fresh, r)
		}
	}
	var gone []Request
	for id, r := range s.queued {
		if !present[id] {
			gone = append(gone, r)
			delete(s.queued, id)
		}
	}
	s.mu.Unlock()
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].ID < fresh[j].ID })
	for _, r := range fresh {
		s.gate(r, StatusPending, "queued")
	}
	for _, r := range gone {
		if got, err := q.Get(r.ID); err == nil && got.State == StateCancelled {
			s.gate(r, StatusError, "cancelled")
		}
	}
}

// Started posts "running" for a request an agent just took, and clears what an earlier attempt left.
func (s *StatusService) Started(r Request) {
	RemoveProgress(s.Dir, r.ID)
	s.mu.Lock()
	delete(s.queued, r.ID)
	if s.runs == nil {
		s.runs = map[string]*runStatus{}
	}
	s.runs[r.ID] = &runStatus{jobs: map[string]bool{}, running: "running"}
	s.mu.Unlock()
	s.gate(r, StatusPending, "running")
}

// Forget drops what is known about a request without posting (the agent stopped and puts it back).
func (s *StatusService) Forget(r Request) {
	RemoveProgress(s.Dir, r.ID)
	s.mu.Lock()
	delete(s.runs, r.ID)
	delete(s.queued, r.ID)
	s.mu.Unlock()
}

// Progress reads the snapshot of a running request: it posts the jobs that ended since the last look and the
// gate's "running, job k of N" when k changed.
func (s *StatusService) Progress(r Request) { s.progress(r, true) }

func (s *StatusService) progress(r Request, gate bool) {
	p, err := ReadProgress(s.Dir, r.ID)
	if err != nil {
		return
	}
	s.mu.Lock()
	st := s.runs[r.ID]
	if st == nil {
		st = &runStatus{jobs: map[string]bool{}}
		if s.runs == nil {
			s.runs = map[string]*runStatus{}
		}
		s.runs[r.ID] = st
	}
	var ended []JobProgress
	for _, j := range p.Jobs {
		if j.Ended() && !st.jobs[j.Key] {
			st.jobs[j.Key] = true
			ended = append(ended, j)
		}
	}
	desc := "running"
	if p.Total > 0 && len(p.Jobs) > 0 {
		desc = fmt.Sprintf("running, job %d of %d", min(len(p.Jobs), p.Total), p.Total)
	}
	changed := desc != st.running
	st.running = desc
	s.mu.Unlock()
	for _, j := range ended {
		s.job(r, j)
	}
	if changed && gate {
		s.gate(r, StatusPending, desc)
	}
}

// job posts the end of one job under its own context. A job that did not run (skipped, not run locally) gets nothing.
func (s *StatusService) job(r Request, j JobProgress) {
	name := s.Naming.JobName(r, j.Label)
	took := ""
	if !j.Start.IsZero() && !j.End.IsZero() {
		took = " in " + fmtDur(j.End.Sub(j.Start))
	}
	switch j.Status {
	case "success":
		s.post(r, name, StatusSuccess, "passed"+took)
	case "failure":
		why := firstNonEmpty(j.FailedStep, j.Reason)
		s.post(r, name, StatusFailure, "failed"+took+noteIf(why != "", ": "+why))
	case "cancelled":
		s.post(r, name, StatusError, "cancelled")
	}
}

// Finished posts the end of a request once the queue has recorded it (done is what Queue.Complete returned).
func (s *StatusService) Finished(r Request, since time.Time, runErr error, done Request) {
	s.progress(r, false) // job ends that were not seen yet
	p, _ := ReadProgress(s.Dir, r.ID)
	RemoveProgress(s.Dir, r.ID)
	s.mu.Lock()
	delete(s.runs, r.ID)
	s.mu.Unlock()
	took := fmtDur(s.now().Sub(since))
	switch {
	case done.State == StateSuperseded:
		return
	case done.State == StateCancelled || done.Conclusion == StateCancelled:
		s.gate(r, StatusError, "cancelled")
	case runErr == nil && done.Conclusion == "success":
		n := 0
		for _, j := range p.Jobs {
			if j.Status == "success" {
				n++
			}
		}
		s.gate(r, StatusSuccess, "passed in "+took+noteIf(n > 0, fmt.Sprintf(" (%d jobs)", n)))
	case runErr == nil && done.Conclusion == "failure":
		why := done.Reason
		if why == "" {
			for _, j := range p.Jobs {
				if j.Status == "failure" {
					why = strings.TrimSpace("job " + j.Label + ", " + j.FailedStep)
					break
				}
			}
		}
		s.gate(r, StatusFailure, "failed in "+took+noteIf(why != "", ": "+why))
	default:
		why := done.Reason
		if runErr != nil {
			why = runErr.Error()
		}
		s.gate(r, StatusError, "error after "+took+noteIf(why != "", ": "+why))
	}
}

// MirrorDone is for a request this PC did not run because another PC did: it posts the result of that run on the
// gate, so a "queued" this PC posted earlier cannot stay on top of the other PC's result. The other PC posts the
// same state, so the two never disagree.
func (s *StatusService) MirrorDone(r Request, conclusion string) {
	s.Forget(r)
	switch conclusion {
	case "success":
		s.gate(r, StatusSuccess, "passed, run on another PC")
	case "failure":
		s.gate(r, StatusFailure, "failed, run on another PC")
	}
}

// WorkflowStem is the workflow file name without folder and extension.
func WorkflowStem(file string) string {
	return strings.TrimSuffix(strings.TrimSuffix(path.Base(file), ".yml"), ".yaml")
}
