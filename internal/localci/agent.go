package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults of the agent.
const (
	DefaultAgentSlots = 2
	DefaultClaimEvery = 2 * time.Second
	DefaultBeatEvery  = 10 * time.Second
	DefaultDoneKeep   = 14 * 24 * time.Hour
)

// Outcome is what a Runner reports for a request it ran to the end.
type Outcome struct {
	RunID      string `json:"runId,omitempty"`
	Conclusion string `json:"conclusion"` // success, failure, cancelled, superseded or error
	Reason     string `json:"reason,omitempty"`
}

// Runner runs one claimed request and returns when it ended. It must stop soon after ctx ends. A non-nil error is
// recorded as the conclusion "error" (the Outcome may still carry the run id).
type Runner func(ctx context.Context, r Request) (Outcome, error)

// AgentJob is a running request, as the heartbeat lists it.
type AgentJob struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Since time.Time `json:"since"`
}

// Heartbeat is <state>/agents/<name>.json: the proof of life of an agent.
type Heartbeat struct {
	Name     string     `json:"name"`
	PID      int        `json:"pid"`
	Slots    int        `json:"slots"`
	Labels   []string   `json:"labels,omitempty"`
	State    string     `json:"state"` // ready, paused or draining
	Started  time.Time  `json:"started"`
	LastSeen time.Time  `json:"lastSeen"`
	Jobs     []AgentJob `json:"jobs"`
	// Status is how posting commit statuses goes; nil when this agent posts none.
	Status *StatusHealth `json:"status,omitempty"`
	// FleetID is the opaque id this agent has on the other PCs' screens; empty when claims are off.
	FleetID string `json:"fleetId,omitempty"`
}

// AgentsDir is where heartbeats and drain marks live.
func AgentsDir(stateDir string) string { return filepath.Join(stateDir, "agents") }

var agentNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidAgentName says whether name is usable as an agent name (it becomes a file name).
func ValidAgentName(name string) bool { return agentNameRe.MatchString(name) }

func heartbeatPath(stateDir, name string) string {
	return filepath.Join(AgentsDir(stateDir), name+".json")
}

func drainPath(stateDir, name string) string {
	return filepath.Join(AgentsDir(stateDir), name+".drain")
}

// ReadAgents lists the heartbeat files, by name.
func ReadAgents(stateDir string) ([]Heartbeat, error) {
	ents, err := os.ReadDir(AgentsDir(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Heartbeat
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || len(n) < 6 || n[len(n)-5:] != ".json" || n[0] == '.' {
			continue
		}
		b, err := os.ReadFile(filepath.Join(AgentsDir(stateDir), n)) //nolint:gosec // folder of the store, listing is the point
		if err != nil {
			continue
		}
		var hb Heartbeat
		if json.Unmarshal(b, &hb) != nil || hb.Name == "" {
			continue
		}
		out = append(out, hb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Fresh says whether a heartbeat is recent enough to count as a live agent.
func (h Heartbeat) Fresh(now time.Time, stale time.Duration) bool {
	return now.Sub(h.LastSeen) <= stale
}

// RequestDrain asks the named agent to stop claiming and exit once its running requests end. It works across
// processes: the agent looks for the mark at every tick.
func RequestDrain(stateDir, name string) error {
	if !ValidAgentName(name) {
		return fmt.Errorf("%q is not an agent name", name)
	}
	if err := os.MkdirAll(AgentsDir(stateDir), 0o750); err != nil {
		return err
	}
	return os.WriteFile(drainPath(stateDir, name), []byte("drain\n"), 0o600)
}

// Agent claims requests from the queue and runs them, Slots at a time. It writes a heartbeat file, takes back
// the requests of agents that died, keeps the PC awake while there is work (KeepAwake), and stops claiming
// while Paused says so.
type Agent struct {
	Name   string
	Dir    string // state dir: agents/ lives under it
	Queue  *Queue
	Slots  int
	Labels []string
	Runner Runner
	// Status, when set, posts commit statuses for what the agent sees and does (shadow phase). A posting problem
	// never reaches a run.
	Status *StatusService
	// Fleet, when set, makes the agent claim every request on the host before it runs it, so that several PCs
	// that watch one repository run each (commit, workflow) once. Nil: this PC runs everything it is asked to.
	Fleet *Fleet
	// FleetEvery is how often the lease is refreshed and the agent record published; default a minute.
	FleetEvery time.Duration

	// Paused, when it says true, stops the agent from claiming; what runs keeps running.
	Paused func() bool
	// KeepAwake is told true when there is work (a request waits or runs) and false when there is not.
	KeepAwake func(on bool)
	// PIDAlive tells whether a process exists; it lets a restart replace the heartbeat of a dead process at once.
	PIDAlive func(pid int) bool

	ClaimEvery time.Duration // between two looks at the queue, default 2 s
	BeatEvery  time.Duration // between two heartbeats, default 10 s; an agent is dead after three missed ones
	DoneKeep   time.Duration // finished requests older than this are deleted, default 14 days
	// CacheLimit is the size the build cache is trimmed to (see Store.TrimCaches), once an hour and at start.
	// Zero: DefaultCacheLimit. Negative: never trim.
	CacheLimit int64
	Now        func() time.Time
	Log        io.Writer

	mu        sync.Mutex
	jobs      map[string]*agentJob
	draining  bool
	started   time.Time
	lastBeat  time.Time
	lastState string
	dirty     bool
	awake     bool
	lastPrune time.Time
	trimming  atomic.Bool
	wake      chan struct{}
	wg        sync.WaitGroup
	logMu     sync.Mutex
}

type agentJob struct {
	req    Request
	since  time.Time
	cancel context.CancelFunc
	asked  bool
	lost   bool // the claim went to another PC while this one ran
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Agent) slots() int {
	if a.Slots > 0 {
		return a.Slots
	}
	return DefaultAgentSlots
}

func (a *Agent) claimEvery() time.Duration {
	if a.ClaimEvery > 0 {
		return a.ClaimEvery
	}
	return DefaultClaimEvery
}

func (a *Agent) beatEvery() time.Duration {
	if a.BeatEvery > 0 {
		return a.BeatEvery
	}
	return DefaultBeatEvery
}

func (a *Agent) staleAfter() time.Duration { return 3 * a.beatEvery() }

func (a *Agent) doneKeep() time.Duration {
	if a.DoneKeep > 0 {
		return a.DoneKeep
	}
	return DefaultDoneKeep
}

func (a *Agent) logf(format string, a2 ...any) {
	if a.Log == nil {
		return
	}
	a.logMu.Lock()
	defer a.logMu.Unlock()
	_, _ = fmt.Fprintf(a.Log, "%s %s\n", a.now().Format("15:04:05"), fmt.Sprintf(format, a2...))
}

// Drain stops the agent claiming; Run returns when what is running has ended.
func (a *Agent) Drain() {
	a.mu.Lock()
	if !a.draining {
		a.draining, a.dirty = true, true
	}
	a.mu.Unlock()
	a.poke()
}

func (a *Agent) poke() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Running is the number of requests running now.
func (a *Agent) Running() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.jobs)
}

// Run works until it is drained (Drain, or a drain mark made with RequestDrain) or ctx ends. Draining lets the
// running requests finish. Ending ctx stops them too and puts them back in the queue, so that the next agent
// takes them up. The error is only for a setup problem (no name, a name in use, an unwritable state folder).
func (a *Agent) Run(ctx context.Context) error {
	if !ValidAgentName(a.Name) {
		return fmt.Errorf("agent name %q is not usable (letters, digits, dot, dash, underscore; at most 64)", a.Name)
	}
	if a.Queue == nil || a.Runner == nil || a.Dir == "" {
		return errors.New("agent: no queue, runner or state folder")
	}
	if err := os.MkdirAll(AgentsDir(a.Dir), 0o750); err != nil {
		return err
	}
	if err := a.takeName(); err != nil {
		return err
	}
	a.mu.Lock()
	a.jobs = map[string]*agentJob{}
	a.wake = make(chan struct{}, 1)
	a.started = a.now()
	a.mu.Unlock()
	fctx, fcancel := context.WithCancel(ctx)
	fleetDone := make(chan struct{})
	defer func() {
		a.setAwake(false)
		fcancel()
		<-fleetDone
		a.fleetStop()
		_ = os.Remove(heartbeatPath(a.Dir, a.Name))
		_ = os.Remove(drainPath(a.Dir, a.Name))
	}()
	go func() {
		defer close(fleetDone)
		a.fleetLoop(fctx)
	}()
	_ = os.Remove(drainPath(a.Dir, a.Name)) // a mark left from before this start is stale
	a.requeueStale(false)
	a.beat(true)
	a.logf("agent %s: %d slots%s", a.Name, a.slots(), labelNote(a.Labels))

	// idle-ok: runs only inside the separate CI agent process (not the desktop app); the period is the agent setting.
	t := time.NewTicker(a.claimEvery())
	defer t.Stop()
	for {
		a.tick(ctx)
		a.mu.Lock()
		done := a.draining && len(a.jobs) == 0
		a.mu.Unlock()
		if done {
			a.logf("agent %s: drained, nothing running", a.Name)
			return nil
		}
		select {
		case <-ctx.Done():
			a.logf("agent %s: stopping, putting the running requests back", a.Name)
			a.wg.Wait()
			return nil
		case <-t.C:
		case <-a.wake:
		}
	}
}

func labelNote(l []string) string {
	if len(l) == 0 {
		return ""
	}
	b, _ := json.Marshal(l)
	return ", labels " + string(b)
}

// takeName refuses to start when another live process already uses the name.
func (a *Agent) takeName() error {
	b, err := os.ReadFile(heartbeatPath(a.Dir, a.Name))
	if err != nil {
		return nil //nolint:nilerr // no heartbeat file: the name is free
	}
	var hb Heartbeat
	if json.Unmarshal(b, &hb) != nil || hb.PID == os.Getpid() {
		return nil //nolint:nilerr // an unreadable file or our own old one does not hold the name
	}
	if !hb.Fresh(a.now(), a.staleAfter()) {
		return nil
	}
	if a.PIDAlive != nil && !a.PIDAlive(hb.PID) {
		return nil
	}
	return fmt.Errorf("agent %q is already running (process %d, last seen %s ago); use another --name or stop it", a.Name, hb.PID, a.now().Sub(hb.LastSeen).Round(time.Second))
}

func (a *Agent) tick(ctx context.Context) {
	a.checkDrainMark()
	a.checkCancels()
	now := a.now()
	if now.Sub(a.lastBeat) >= a.beatEvery() {
		a.requeueStale(true)
		a.prune(ctx, now)
	}
	a.statusTick()
	a.claim(ctx)
	a.keepAwake()
	a.beat(false)
}

func (a *Agent) checkDrainMark() {
	if _, err := os.Stat(drainPath(a.Dir, a.Name)); err == nil {
		_ = os.Remove(drainPath(a.Dir, a.Name))
		a.logf("agent %s: drain requested", a.Name)
		a.Drain()
	}
}

// checkCancels stops the running requests that someone cancelled or that a newer head replaced.
func (a *Agent) checkCancels() {
	a.mu.Lock()
	var todo []*agentJob
	for _, j := range a.jobs {
		if !j.asked {
			todo = append(todo, j)
		}
	}
	a.mu.Unlock()
	for _, j := range todo {
		state, reason, ok := a.Queue.CancelRequested(j.req.ID)
		if !ok {
			continue
		}
		a.mu.Lock()
		j.asked = true
		a.mu.Unlock()
		a.logf("%s: %s (%s), stopping it", j.req.ID, state, reason)
		j.cancel()
	}
}

func (a *Agent) claim(ctx context.Context) {
	for {
		a.mu.Lock()
		free := a.slots() - len(a.jobs)
		draining := a.draining
		a.mu.Unlock()
		if free <= 0 || draining || ctx.Err() != nil {
			return
		}
		if a.Paused != nil && a.Paused() {
			return
		}
		r, err := a.Queue.Claim(a.Name, a.Labels)
		if err != nil {
			a.logf("claim: %v", err)
			return
		}
		if r == nil {
			return
		}
		if !a.acquire(ctx, *r) {
			continue
		}
		a.start(ctx, *r)
	}
}

// statusTick posts what changed since the last look: new requests in the queue, jobs that ended in the runs.
func (a *Agent) statusTick() {
	if a.Status == nil {
		return
	}
	a.Status.ObserveQueue(a.Queue)
	a.mu.Lock()
	reqs := make([]Request, 0, len(a.jobs))
	for _, j := range a.jobs {
		reqs = append(reqs, j.req)
	}
	a.mu.Unlock()
	for _, r := range reqs {
		a.Status.Progress(r)
	}
}

func (a *Agent) start(ctx context.Context, r Request) {
	jctx, cancel := context.WithCancel(ctx)
	j := &agentJob{req: r, since: a.now(), cancel: cancel}
	if a.Status != nil {
		a.Status.Started(r)
	}
	a.mu.Lock()
	a.jobs[r.ID] = j
	a.dirty = true
	a.mu.Unlock()
	a.logf("%s: running %s at %s", r.ID, r.Label(), shortSHA(r.SHA))
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		out, err := a.runSafely(jctx, r)
		a.finish(ctx, r, j.since, out, err)
		a.mu.Lock()
		delete(a.jobs, r.ID)
		a.dirty = true
		a.mu.Unlock()
		a.poke()
	}()
}

func (a *Agent) runSafely(ctx context.Context, r Request) (out Outcome, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the runner crashed: %v", p)
		}
	}()
	return a.Runner(ctx, r)
}

func (a *Agent) finish(agentCtx context.Context, r Request, since time.Time, out Outcome, err error) {
	_, _, marked := a.Queue.CancelRequested(r.ID)
	a.mu.Lock()
	lost := a.jobs[r.ID] != nil && a.jobs[r.ID].lost
	a.mu.Unlock()
	if !marked && lost {
		// Another PC took the claim over while this one ran (this PC was asleep or offline): it stopped the run and
		// leaves the result to the other PC. The request goes back to the queue and is looked at again later.
		if a.Status != nil {
			a.Status.Forget(r)
		}
		if rerr := a.Queue.Requeue(r.ID); rerr != nil {
			a.logf("%s: could not put it back: %v", r.ID, rerr)
			return
		}
		a.logf("%s: stopped, another PC holds the claim now", r.ID)
		return
	}
	if !marked && agentCtx.Err() != nil {
		a.fleetRelease(r)
		if a.Status != nil {
			a.Status.Forget(r)
		}
		if rerr := a.Queue.Requeue(r.ID); rerr != nil {
			a.logf("%s: could not put it back: %v", r.ID, rerr)
			return
		}
		a.logf("%s: put back in the queue (the agent stopped)", r.ID)
		return
	}
	conclusion, reason := out.Conclusion, out.Reason
	switch {
	case err != nil:
		conclusion, reason = "error", err.Error()
	case conclusion == "":
		conclusion, reason = "error", "the runner gave no result"
	}
	done, cerr := a.Queue.Complete(r.ID, out.RunID, conclusion, reason)
	if cerr != nil {
		a.logf("%s: could not record the end: %v", r.ID, cerr)
		return
	}
	a.fleetFinish(r, done)
	if a.Status != nil {
		a.Status.Finished(r, since, err, done)
	}
	a.logf("%s: %s%s%s", r.ID, done.State, noteIf(done.Conclusion != "" && done.Conclusion != done.State, " ("+done.Conclusion+")"), noteIf(done.Reason != "", ": "+done.Reason))
}

func noteIf(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

// requeueStale takes back the requests of agents whose heartbeat stopped. At the start, a request under this
// agent's own name belongs to an earlier process of it.
func (a *Agent) requeueStale(self bool) {
	alive := map[string]bool{}
	if hbs, err := ReadAgents(a.Dir); err == nil {
		for _, h := range hbs {
			alive[h.Name] = h.Fresh(a.now(), a.staleAfter()) && (a.PIDAlive == nil || a.PIDAlive(h.PID))
		}
	}
	moved, err := a.Queue.RequeueStale(func(name string) bool {
		if name == a.Name {
			return self
		}
		return alive[name]
	})
	if err != nil {
		a.logf("requeue: %v", err)
	}
	for _, r := range moved {
		a.logf("%s: taken back from %s, which is gone", r.ID, r.ClaimedBy)
	}
}

func (a *Agent) prune(ctx context.Context, now time.Time) {
	if now.Sub(a.lastPrune) < time.Hour {
		return
	}
	a.lastPrune = now
	if n, err := a.Queue.Prune(now.Add(-a.doneKeep())); err == nil && n > 0 {
		a.logf("removed %d old finished requests", n)
	}
	a.trimCaches(ctx, now)
}

// trimCaches cuts the build caches down to the limit in the background: walking tens of thousands of files must
// not hold up the claiming of requests.
func (a *Agent) trimCaches(ctx context.Context, now time.Time) {
	limit := a.CacheLimit
	if limit < 0 || a.Dir == "" || !a.trimming.CompareAndSwap(false, true) {
		return
	}
	if limit == 0 {
		limit = DefaultCacheLimit
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.trimming.Store(false)
		freed, err := (&Store{Dir: a.Dir}).TrimCaches(ctx, limit, now)
		if freed > 0 {
			a.logf("build cache trimmed: freed %d MB (limit %d MB)", freed>>20, limit>>20)
		}
		if err != nil && ctx.Err() == nil {
			a.logf("build cache trim: %v", err)
		}
	}()
}

func (a *Agent) keepAwake() {
	queued, running := a.Queue.Depth()
	a.setAwake(queued > 0 || running > 0)
}

func (a *Agent) setAwake(on bool) {
	if a.KeepAwake == nil || a.awake == on {
		return
	}
	a.awake = on
	a.KeepAwake(on)
}

func (a *Agent) state() string {
	a.mu.Lock()
	draining := a.draining
	a.mu.Unlock()
	switch {
	case draining:
		return "draining"
	case a.Paused != nil && a.Paused():
		return "paused"
	}
	return "ready"
}

// Snapshot is the heartbeat as it would be written now.
func (a *Agent) Snapshot() Heartbeat {
	st := a.state()
	a.mu.Lock()
	defer a.mu.Unlock()
	hb := Heartbeat{Name: a.Name, PID: os.Getpid(), Slots: a.slots(), Labels: a.Labels, State: st, Started: a.started, LastSeen: a.now(), Jobs: []AgentJob{}, Status: a.Status.Health()}
	if a.Fleet != nil {
		hb.FleetID = a.Fleet.ID
	}
	for _, j := range a.jobs {
		hb.Jobs = append(hb.Jobs, AgentJob{ID: j.req.ID, Label: j.req.Label(), Since: j.since})
	}
	sort.Slice(hb.Jobs, func(i, k int) bool { return hb.Jobs[i].Since.Before(hb.Jobs[k].Since) })
	return hb
}

// beat writes the heartbeat when it is due, when what it says changed, or when forced.
func (a *Agent) beat(force bool) {
	st := a.state()
	a.mu.Lock()
	due := force || a.dirty || st != a.lastState || a.now().Sub(a.lastBeat) >= a.beatEvery()
	a.mu.Unlock()
	if !due {
		return
	}
	hb := a.Snapshot()
	if err := writeAtomic(heartbeatPath(a.Dir, a.Name), hb); err != nil {
		a.logf("heartbeat: %v", err)
		return
	}
	a.mu.Lock()
	a.lastBeat, a.lastState, a.dirty = hb.LastSeen, st, false
	a.mu.Unlock()
}
