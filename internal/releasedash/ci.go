package releasedash

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/localci"
)

// The CI side of the dashboard. It only reads the local CI store (the folder unravis-ci writes) with the same package
// functions the command line uses, and it never creates, changes or deletes anything in it. Every text that goes to the
// browser is passed through the Redactor first, like the release logs are.

// The words a person sees for the state of a run, a job or a step. The machine keys are the left side.
const (
	stPassed      = "passed"
	stFailed      = "failed"
	stRunning     = "running"
	stWaiting     = "waiting"
	stStopped     = "stopped"
	stNotRecorded = "not-recorded"
	stSkipped     = "skipped"
	stFinished    = "finished" // a step of a run that is still going: it ended, but its verdict is only known when the whole run ends
)

var statusWords = map[string]string{
	stPassed:      "Passed",
	stFailed:      "Failed",
	stRunning:     "Running",
	stWaiting:     "Waiting",
	stStopped:     "Stopped",
	stNotRecorded: "Not recorded here",
	stSkipped:     "Skipped",
	stFinished:    "Finished",
}

// ciAgentStale is how long an agent may be silent before the page says so. An agent writes its heartbeat every 10 seconds.
const ciAgentStale = 60 * time.Second

// ciDefaultLimit and ciMaxLimit bound a page of the run list.
const (
	ciDefaultLimit = 30
	ciMaxLimit     = 200
)

// ciLogGrab is how much of the end of one step log is read. A longer log is cut at the start and the page says so.
const ciLogGrab = 16 << 20

var (
	jobDirRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	stepLogRE = regexp.MustCompile(`^(\d{2,4})-(.*)\.log$`)
	reqIDRE   = regexp.MustCompile(`^q-[a-z0-9-]{1,60}$`)
	hexRE     = regexp.MustCompile(`^[0-9a-f]{4,64}$`)
)

// ciLogCache keeps the redacted lines of the step logs that were read recently, like logCache does for release logs.
var ciLogCache = memo[readLogResult]{maxItems: 12, maxWeight: 400000}

// CIRun is one row of the run list: a run that ended, one that is going, or a request that is still waiting (or was
// stopped before it started).
type CIRun struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // run: it has a folder in the store; request: only the queue knows it
	Status   string `json:"status"`
	Word     string `json:"word"`
	Workflow string `json:"workflow,omitempty"`
	Event    string `json:"event,omitempty"`
	Repo     string `json:"repo,omitempty"`
	PR       int    `json:"pr,omitempty"`
	Author   string `json:"author,omitempty"`
	Base     string `json:"base,omitempty"`   // a pull request: the branch it goes into
	Branch   string `json:"branch,omitempty"` // the branch of the pull request, or the one that was pushed
	SHA      string `json:"sha,omitempty"`    // the commit that ran
	HeadSHA  string `json:"headSha,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	Ms       int64  `json:"ms,omitempty"` // how long it took (so far, for a run that is going)
	Agent    string `json:"agent,omitempty"`
	Request  string `json:"request,omitempty"`
	Release  string `json:"release,omitempty"` // the release whose commit is this one
	Stale    bool   `json:"stale,omitempty"`   // running, but its agent has not been heard from
	Note     string `json:"note,omitempty"`

	at time.Time // sort key
}

// CIStep is one step of a job.
type CIStep struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Word   string `json:"word"`
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
	Ms     int64  `json:"ms,omitempty"`
	Err    string `json:"err,omitempty"`
	HasLog bool   `json:"hasLog"`
}

// CIJob is one job instance (one matrix combination) of a run.
type CIJob struct {
	Key    string   `json:"key"`
	ID     string   `json:"id,omitempty"`
	Label  string   `json:"label"`
	Status string   `json:"status"`
	Word   string   `json:"word"`
	Reason string   `json:"reason,omitempty"`
	Pseudo bool     `json:"pseudo,omitempty"` // the runner's own record of a stage a run stopped at, not a job of the workflow
	Start  string   `json:"start,omitempty"`
	End    string   `json:"end,omitempty"`
	Ms     int64    `json:"ms,omitempty"`
	Steps  []CIStep `json:"steps"`
}

// CIPosted is one result the agent sent to GitHub for a commit.
type CIPosted struct {
	Repo        string `json:"repo"`
	SHA         string `json:"sha"`
	Context     string `json:"context"`
	Status      string `json:"status"`
	Word        string `json:"word"`
	Description string `json:"description,omitempty"`
	At          string `json:"at,omitempty"`
}

// CIDetail is one run with its jobs and steps.
type CIDetail struct {
	Run        CIRun      `json:"run"`
	Jobs       []CIJob    `json:"jobs"`
	Live       bool       `json:"live"`     // the run is going: its page polls for more
	Checkout   bool       `json:"checkout"` // there is a log of the fetch and merge work before the jobs
	Planned    int        `json:"planned,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	SummaryCut bool       `json:"summaryCut,omitempty"`
	Posted     []CIPosted `json:"posted"`
}

// CILog is a window of one step log. Lines are the same shape the release log viewer reads.
type CILog struct {
	Title string    `json:"title"`
	Lines []LogLine `json:"lines"`
	From  int       `json:"from"`
	Total int       `json:"total"`
	Cut   bool      `json:"cut"`
	Live  bool      `json:"live"`
	Bytes int64     `json:"bytes"`
}

// ciView is one project's CI store, read at one moment.
type ciView struct {
	d     *Dash
	p     Project
	dir   string
	st    *localci.Store
	found bool
	names []string // machine and user names to hide
	rels  []relCommit
	now   time.Time
}

type relCommit struct{ version, commit string }

// ci opens the view of a project. ok is false for an id the project list does not have.
func (d *Dash) ci(projectID string) (*ciView, bool) {
	p, ok := d.project(projectID)
	if !ok {
		return nil, false
	}
	v := &ciView{d: d, p: p, dir: p.CIStore, now: time.Now()}
	if v.dir != "" {
		if st, err := os.Stat(v.dir); err == nil && st.IsDir() {
			v.found = true
			v.st = &localci.Store{Dir: v.dir}
		}
	}
	if p.HasReleases() && d.DataDir != "" {
		loc := ReadLocal(d.DataDir)
		v.names = loc.Names
		last := map[string]string{}
		var order []string
		for _, e := range loc.Events {
			if e.Version == "" || e.Commit == "" {
				continue
			}
			if _, seen := last[e.Version]; !seen {
				order = append(order, e.Version)
			}
			last[e.Version] = e.Commit
		}
		for _, ver := range order {
			v.rels = append(v.rels, relCommit{ver, strings.ToLower(last[ver])})
		}
	}
	return v, true
}

// rd hides what must not reach the screen in one text.
func (v *ciView) rd(s string) string {
	if s == "" {
		return ""
	}
	return NewRedactor(v.names...).Line(s)
}

func tstr(t time.Time) string {
	if t.IsZero() || t.Year() < 2000 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func dur(start, end time.Time) int64 {
	if start.IsZero() || start.Year() < 2000 || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

// shaOnly returns s (lower case) when it is 4 to 64 hex characters, else "".
func shaOnly(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !hexRE.MatchString(s) {
		return ""
	}
	return s
}

// conclusionStatus turns what the store calls an ending (a run's conclusion, a job's status, a step's conclusion) into a status key.
func conclusionStatus(c string) string {
	switch c {
	case "success":
		return stPassed
	case "failure", "error":
		return stFailed
	case "cancelled", "superseded":
		return stStopped
	case "skipped", "not_run_locally":
		return stSkipped
	case "running":
		return stRunning
	default:
		return stNotRecorded
	}
}

func setStatus(r *CIRun, st string) {
	r.Status, r.Word = st, statusWords[st]
}

func prefixMatch(a, b string) bool {
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// releaseOf finds the release made from a commit. Only an exact commit (a prefix of 7 or more characters either way) counts.
func (v *ciView) releaseOf(shas ...string) string {
	for _, s := range shas {
		if s == "" {
			continue
		}
		for _, r := range v.rels {
			if prefixMatch(s, r.commit) {
				return v.rd(r.version)
			}
		}
	}
	return ""
}

// commitsOf lists the commits of a release version (empty for one this PC has no record of).
func (v *ciView) commitsOf(version string) []string {
	var out []string
	for _, r := range v.rels {
		if r.version == version {
			out = append(out, r.commit)
		}
	}
	return out
}

func (v *ciView) agentMap() map[string]localci.Heartbeat {
	m := map[string]localci.Heartbeat{}
	if !v.found {
		return m
	}
	hbs, _ := localci.ReadAgents(v.dir)
	for _, h := range hbs {
		m[h.Name] = h
	}
	return m
}

// staleNote says why a running request looks abandoned, or "" when its agent is alive.
func (v *ciView) staleNote(agent string, agents map[string]localci.Heartbeat) string {
	h, ok := agents[agent]
	if !ok {
		return "The agent that took this run is not running any more."
	}
	if age := v.now.Sub(h.LastSeen); age > ciAgentStale {
		return "The agent that took this run has not been heard from for " + ago(age) + "."
	}
	return ""
}

func ago(d time.Duration) string {
	switch {
	case d < 90*time.Second:
		return "a minute"
	case d < 90*time.Minute:
		return strconv.Itoa(int(d.Minutes()+0.5)) + " minutes"
	case d < 36*time.Hour:
		return strconv.Itoa(int(d.Hours()+0.5)) + " hours"
	default:
		return strconv.Itoa(int(d.Hours()/24+0.5)) + " days"
	}
}

func eventOf(kind string) string {
	switch kind {
	case localci.KindPR:
		return "pull_request"
	case localci.KindPush:
		return "push"
	case "":
		return ""
	default:
		return kind
	}
}

func (v *ciView) fromIndex(e localci.IndexEntry) (CIRun, bool) {
	if !localci.ValidRunID(e.RunID) {
		return CIRun{}, false
	}
	r := CIRun{ID: e.RunID, Kind: "run", Workflow: v.rd(e.Workflow), Event: v.rd(e.Event), Repo: v.rd(e.Repo), PR: e.PR, Author: v.rd(e.Author),
		Base: v.rd(e.BaseRef), Branch: v.rd(e.HeadRef), SHA: shaOnly(e.CommitSHA), HeadSHA: shaOnly(e.HeadSHA), Reason: v.rd(e.Reason),
		Start: tstr(e.Start), End: tstr(e.End), Ms: dur(e.Start, e.End), at: e.Start}
	if r.SHA == "" {
		r.SHA = r.HeadSHA
	}
	setStatus(&r, conclusionStatus(e.Conclusion))
	return r, true
}

func (v *ciView) fromRequest(q localci.Request, agents map[string]localci.Heartbeat) CIRun {
	r := CIRun{ID: q.ID, Kind: "request", Workflow: v.rd(q.Workflow), Event: eventOf(q.Kind), Repo: v.rd(q.Repo), PR: q.Number, Base: v.rd(q.Base), Branch: v.rd(q.Ref),
		SHA: shaOnly(q.SHA), HeadSHA: shaOnly(q.SHA), Request: q.ID, Agent: v.rd(q.ClaimedBy), Reason: v.rd(q.Reason)}
	switch q.State {
	case localci.StateQueued:
		setStatus(&r, stWaiting)
		r.Start, r.at = tstr(q.EnqueuedAt), q.EnqueuedAt
	case localci.StateRunning:
		setStatus(&r, stRunning)
		r.Start, r.at = tstr(q.ClaimedAt), q.ClaimedAt
		r.Ms = dur(q.ClaimedAt, v.now)
		if n := v.staleNote(q.ClaimedBy, agents); n != "" {
			r.Stale, r.Note = true, n
		}
	default:
		setStatus(&r, conclusionStatus(q.Conclusion))
		if q.Conclusion == "" || q.State == localci.StateCancelled || q.State == localci.StateSuperseded {
			setStatus(&r, stStopped)
		}
		r.Start, r.End, r.at = tstr(q.EnqueuedAt), tstr(q.FinishedAt), q.FinishedAt
		if r.at.IsZero() {
			r.at = q.EnqueuedAt
		}
		r.Ms = dur(q.ClaimedAt, q.FinishedAt)
	}
	return r
}

// requests lists the queue, newest first by the time that matters for its state.
func (v *ciView) requests() []localci.Request {
	if !v.found {
		return nil
	}
	rs, _ := localci.ListRequests(v.dir)
	return rs
}

// liveRuns maps the ids of the run folders that are going right now to the request that makes each.
func (v *ciView) liveRuns(reqs []localci.Request) map[string]localci.Request {
	m := map[string]localci.Request{}
	if !v.found {
		return m
	}
	for _, q := range reqs {
		if q.State != localci.StateRunning {
			continue
		}
		if id := v.st.LiveRunID(q); id != "" {
			m[id] = q
		}
	}
	return m
}

// rows builds every row the store knows about, newest first: runs that ended, runs that are going, requests that wait,
// and requests that ended without a run of their own.
func (v *ciView) rows() []CIRun {
	if !v.found {
		return nil
	}
	reqs := v.requests()
	agents := v.agentMap()
	entries, _ := v.st.ReadIndex()
	byRun := map[string]localci.Request{}
	for _, q := range reqs {
		if q.RunID != "" {
			byRun[q.RunID] = q
		}
	}
	live := v.liveRuns(reqs)
	seen := map[string]bool{}
	var out []CIRun
	for i := len(entries) - 1; i >= 0; i-- {
		r, ok := v.fromIndex(entries[i])
		if !ok || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		if q, ok := byRun[r.ID]; ok {
			r.Agent, r.Request = v.rd(q.ClaimedBy), q.ID
		}
		out = append(out, r)
	}
	usedReq := map[string]bool{}
	for runID, q := range live {
		if seen[runID] {
			continue
		}
		seen[runID] = true
		usedReq[q.ID] = true
		r := v.fromRequest(q, agents)
		r.ID, r.Kind = runID, "run"
		out = append(out, r)
	}
	for _, q := range reqs {
		if usedReq[q.ID] || (q.RunID != "" && seen[q.RunID]) {
			continue
		}
		r := v.fromRequest(q, agents)
		if q.State != localci.StateQueued && q.State != localci.StateRunning && q.RunID != "" {
			// It ran, but this store has no record of the run.
			setStatus(&r, stNotRecorded)
		}
		out = append(out, r)
	}
	for i := range out {
		out[i].Release = v.releaseOf(out[i].SHA, out[i].HeadSHA)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.After(out[j].at)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// CIFilter narrows the run list. Zero values filter nothing.
type CIFilter struct {
	Status  string
	Branch  string
	SHA     string // a prefix of at least 4 hex characters
	Release string // a release version: only runs of its commit
	PR      int
}

func (f CIFilter) match(r CIRun, relCommits []string) bool {
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if f.PR != 0 && r.PR != f.PR {
		return false
	}
	if f.Branch != "" && r.Branch != f.Branch && r.Base != f.Branch {
		return false
	}
	if f.SHA != "" && !strings.HasPrefix(r.SHA, f.SHA) && !strings.HasPrefix(r.HeadSHA, f.SHA) {
		return false
	}
	if f.Release != "" {
		hit := false
		for _, c := range relCommits {
			if prefixMatch(r.SHA, c) || prefixMatch(r.HeadSHA, c) {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// CIPage is one page of the run list.
type CIPage struct {
	CIFound bool    `json:"ciFound"`
	Runs    []CIRun `json:"runs"`
	Total   int     `json:"total"` // how many rows match the filters, over all pages
	Next    string  `json:"next,omitempty"`
}

// cursorOf names a row: "<unix nanoseconds>~<id>". The next page holds the rows that sort after it.
func cursorOf(r CIRun) string { return strconv.FormatInt(r.at.UnixNano(), 10) + "~" + r.ID }

func after(r CIRun, cursor string) bool {
	ns, id, ok := strings.Cut(cursor, "~")
	if !ok {
		return true
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return true
	}
	rn := r.at.UnixNano()
	if rn != n {
		return rn < n
	}
	return r.ID < id
}

// Page filters, sorts and cuts the run list. A cursor that does not parse starts from the top.
func (v *ciView) Page(f CIFilter, limit int, cursor string) CIPage {
	if limit <= 0 {
		limit = ciDefaultLimit
	}
	if limit > ciMaxLimit {
		limit = ciMaxLimit
	}
	page := CIPage{CIFound: v.found, Runs: []CIRun{}}
	var relCommits []string
	if f.Release != "" {
		relCommits = v.commitsOf(f.Release)
	}
	matched := 0
	var kept []CIRun
	for _, r := range v.rows() {
		if !f.match(r, relCommits) {
			continue
		}
		matched++
		if cursor != "" && !after(r, cursor) {
			continue
		}
		kept = append(kept, r)
	}
	page.Total = matched
	if len(kept) > limit {
		page.Next = cursorOf(kept[limit-1])
		kept = kept[:limit]
	}
	if kept != nil {
		page.Runs = kept
	}
	return page
}

// ---------------------------------------------------------------- one run

// errNoRun is what Detail and StepLog answer for a run the store does not have.
var errNoRun = errors.New("That run is not in this CI store.")

// errBadID is what they answer for text that cannot be a run id.
var errBadID = errors.New("That is not a run id.")

// Detail reads one run. id is a run id or a request id (the id of a run that has not started, or that ended without one).
func (v *ciView) Detail(id string) (*CIDetail, error) {
	if !v.found {
		return nil, errNoRun
	}
	if reqIDRE.MatchString(id) {
		return v.requestDetail(id)
	}
	if !localci.ValidRunID(id) {
		return nil, errBadID
	}
	dir, err := v.st.RunDir(id)
	if err != nil {
		return nil, errNoRun
	}
	reqs := v.requests()
	live := v.liveRuns(reqs)
	d := &CIDetail{Jobs: []CIJob{}, Posted: []CIPosted{}}
	if rec, rerr := v.st.ReadRunRecord(id); rerr == nil {
		v.fillRecorded(d, id, dir, rec, reqs)
	} else if q, isLive := live[id]; isLive {
		v.fillLive(d, id, dir, q)
	} else {
		v.fillUnfinished(d, id, dir)
	}
	d.Checkout = fileExists(filepath.Join(dir, "checkout.log"))
	d.Summary, d.SummaryCut = v.summaryOf(dir)
	d.Run.Release = v.releaseOf(d.Run.SHA, d.Run.HeadSHA)
	d.Posted = v.postedFor(d.Run.SHA, d.Run.HeadSHA)
	return d, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func (v *ciView) fillRecorded(d *CIDetail, id, dir string, rec *localci.RunResult, reqs []localci.Request) {
	var run CIRun
	entries, _ := v.st.ReadIndex()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].RunID == id {
			run, _ = v.fromIndex(entries[i])
			break
		}
	}
	if run.ID == "" { // the index has no line for it: build the row from the record
		run = CIRun{ID: id, Kind: "run", Workflow: v.rd(rec.Workflow), Start: tstr(rec.Start), End: tstr(rec.End), Ms: dur(rec.Start, rec.End), at: rec.Start}
		setStatus(&run, conclusionStatus(rec.Conclusion))
	}
	for _, q := range reqs {
		if q.RunID == id {
			run.Agent, run.Request = v.rd(q.ClaimedBy), q.ID
		}
	}
	d.Run = run
	for _, j := range rec.Jobs {
		d.Jobs = append(d.Jobs, v.jobOf(dir, j))
	}
}

func (v *ciView) jobOf(runDir string, j *localci.JobResult) CIJob {
	out := CIJob{Key: v.rd(j.Key), ID: v.rd(j.ID), Label: v.rd(j.Label), Reason: v.rd(j.Reason), Pseudo: localci.IsPseudoJob(j),
		Start: tstr(j.Start), End: tstr(j.End), Ms: dur(j.Start, j.End), Steps: []CIStep{}}
	st := conclusionStatus(j.Status)
	out.Status, out.Word = st, statusWords[st]
	logs := map[int]string{}
	if jobDirRE.MatchString(j.Key) {
		logs = stepLogFiles(filepath.Join(runDir, "logs", j.Key))
	}
	for _, s := range j.Steps {
		cs := conclusionStatus(s.Conclusion)
		if s.Conclusion == "" {
			cs = conclusionStatus(s.Outcome)
		}
		_, has := logs[s.Index]
		out.Steps = append(out.Steps, CIStep{Index: s.Index, Name: v.rd(s.Name), Status: cs, Word: statusWords[cs], Start: tstr(s.Start), End: tstr(s.End),
			Ms: dur(s.Start, s.End), Err: v.rd(s.Err), HasLog: has})
	}
	return out
}

// stepLogFiles lists the log files of one job folder by step number. A missing folder is an empty map.
func stepLogFiles(dir string) map[int]string {
	out := map[int]string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		m := stepLogRE.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if _, dup := out[n]; !dup {
			out[n] = e.Name()
		}
	}
	return out
}

// fillLive builds a run that has no record yet from the queue entry, the progress snapshot and the log files written so far.
func (v *ciView) fillLive(d *CIDetail, id, dir string, q localci.Request) {
	agents := v.agentMap()
	run := v.fromRequest(q, agents)
	run.ID, run.Kind = id, "run"
	d.Run, d.Live = run, true
	prog, _ := localci.ReadProgress(v.dir, q.ID)
	d.Planned = prog.Total
	byKey := map[string]localci.JobProgress{}
	var keys []string
	for _, j := range prog.Jobs {
		if jobDirRE.MatchString(j.Key) {
			byKey[j.Key] = j
			keys = append(keys, j.Key)
		}
	}
	if ents, err := os.ReadDir(filepath.Join(dir, "logs")); err == nil {
		for _, e := range ents {
			if _, ok := byKey[e.Name()]; !ok && e.IsDir() && jobDirRE.MatchString(e.Name()) {
				byKey[e.Name()] = localci.JobProgress{Key: e.Name(), Label: e.Name(), Status: "running"}
				keys = append(keys, e.Name())
			}
		}
	}
	for _, k := range keys {
		jp := byKey[k]
		st := conclusionStatus(jp.Status)
		if jp.Status == "" || jp.Status == "running" {
			st = stRunning
		}
		job := CIJob{Key: k, Label: v.rd(jp.Label), Status: st, Word: statusWords[st], Reason: v.rd(jp.Reason), Start: tstr(jp.Start), End: tstr(jp.End), Steps: []CIStep{}}
		if jp.Ended() {
			job.Ms = dur(jp.Start, jp.End)
		} else {
			job.Ms = dur(jp.Start, v.now)
		}
		files := stepLogFiles(filepath.Join(dir, "logs", k))
		idx := make([]int, 0, len(files))
		for n := range files {
			idx = append(idx, n)
		}
		sort.Ints(idx)
		for i, n := range idx {
			name := files[n]
			if m := stepLogRE.FindStringSubmatch(name); m != nil {
				name = strings.ReplaceAll(m[2], "-", " ")
			}
			s := CIStep{Index: n, Name: v.rd(name), HasLog: true}
			switch {
			case i == len(idx)-1 && !jp.Ended():
				s.Status = stRunning
			case jp.FailedStep != "" && strings.HasPrefix(jp.FailedStep, "step "+strconv.Itoa(n)+" "):
				s.Status = stFailed
			default:
				s.Status = stFinished
			}
			s.Word = statusWords[s.Status]
			job.Steps = append(job.Steps, s)
		}
		d.Jobs = append(d.Jobs, job)
	}
}

// fillUnfinished describes a run folder that has no record and no live request: the agent stopped before the run ended.
func (v *ciView) fillUnfinished(d *CIDetail, id, dir string) {
	t, _ := localci.IDTime(id)
	d.Run = CIRun{ID: id, Kind: "run", Start: tstr(t), at: t, Reason: "This run never wrote its result. The agent that was running it probably stopped."}
	setStatus(&d.Run, stStopped)
	if ents, err := os.ReadDir(filepath.Join(dir, "logs")); err == nil {
		for _, e := range ents {
			if !e.IsDir() || !jobDirRE.MatchString(e.Name()) {
				continue
			}
			job := CIJob{Key: e.Name(), Label: v.rd(e.Name()), Status: stStopped, Word: statusWords[stStopped], Steps: []CIStep{}}
			files := stepLogFiles(filepath.Join(dir, "logs", e.Name()))
			idx := make([]int, 0, len(files))
			for n := range files {
				idx = append(idx, n)
			}
			sort.Ints(idx)
			for _, n := range idx {
				name := files[n]
				if m := stepLogRE.FindStringSubmatch(name); m != nil {
					name = strings.ReplaceAll(m[2], "-", " ")
				}
				job.Steps = append(job.Steps, CIStep{Index: n, Name: v.rd(name), Status: stFinished, Word: statusWords[stFinished], HasLog: true})
			}
			d.Jobs = append(d.Jobs, job)
		}
	}
}

// requestDetail answers for a request id: the run it made when there is one, else just what the queue says.
func (v *ciView) requestDetail(id string) (*CIDetail, error) {
	q, err := (&localci.Queue{Dir: localci.QueueDir(v.dir)}).Get(id)
	if err != nil {
		return nil, errNoRun
	}
	if q.RunID != "" && localci.ValidRunID(q.RunID) {
		if d, err := v.Detail(q.RunID); err == nil {
			return d, nil
		}
	}
	if q.State == localci.StateRunning {
		if rid := v.st.LiveRunID(q); rid != "" {
			if d, err := v.Detail(rid); err == nil {
				return d, nil
			}
		}
	}
	agents := v.agentMap()
	run := v.fromRequest(q, agents)
	if q.State != localci.StateQueued && q.State != localci.StateRunning && q.RunID != "" {
		setStatus(&run, stNotRecorded)
	}
	run.Release = v.releaseOf(run.SHA, run.HeadSHA)
	return &CIDetail{Run: run, Jobs: []CIJob{}, Live: q.State == localci.StateRunning, Posted: v.postedFor(run.SHA, run.HeadSHA)}, nil
}

const maxSummaryBytes = 64 << 10

// summaryOf reads runs/<id>/summary.md (the step summaries the run collected), redacted.
func (v *ciView) summaryOf(runDir string) (string, bool) {
	f, err := openShared(filepath.Join(runDir, "summary.md"))
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxSummaryBytes+1))
	if err != nil {
		return "", false
	}
	cut := len(b) > maxSummaryBytes
	if cut {
		b = b[:maxSummaryBytes]
	}
	red := NewRedactor(v.names...)
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = red.Line(l)
	}
	return strings.Join(lines, "\n"), cut
}

// postedFor lists what the agent sent to GitHub for these commits, newest first.
func (v *ciView) postedFor(shas ...string) []CIPosted {
	out := []CIPosted{}
	rows, _ := localci.ReadLedger(v.dir)
	for _, r := range rows {
		hit := false
		for _, s := range shas {
			if s != "" && strings.EqualFold(r.SHA, s) {
				hit = true
			}
		}
		if hit {
			out = append(out, v.postedRow(r))
		}
	}
	return out
}

func (v *ciView) postedRow(r localci.LedgerRow) CIPosted {
	st := stNotRecorded
	switch r.State {
	case "success":
		st = stPassed
	case "failure", "error":
		st = stFailed
	case "pending":
		st = stRunning
	}
	return CIPosted{Repo: v.rd(r.Repo), SHA: shaOnly(r.SHA), Context: v.rd(r.Context), Status: st, Word: statusWords[st], Description: v.rd(r.Description), At: tstr(r.At)}
}

// ---------------------------------------------------------------- logs

// StepLog reads a window of one step log (job "_checkout" is the checkout log). tail asks for the last n lines; otherwise
// the window starts at line from.
func (v *ciView) StepLog(id, job string, step, from, n int, tail bool) (*CILog, error) {
	if !v.found {
		return nil, errNoRun
	}
	if !localci.ValidRunID(id) {
		return nil, errBadID
	}
	dir, err := v.st.RunDir(id)
	if err != nil {
		return nil, errNoRun
	}
	var path, title string
	if job == localci.CheckoutLogName {
		path, title = filepath.Join(dir, "checkout.log"), "Before the jobs: fetching and merging the code"
	} else {
		if !jobDirRE.MatchString(job) || step < 1 || step > 9999 {
			return nil, errBadID
		}
		// The folder and file names come from listing the folders, never from the request text.
		jobDir := ""
		if ents, derr := os.ReadDir(filepath.Join(dir, "logs")); derr == nil {
			for _, e := range ents {
				if e.IsDir() && e.Name() == job {
					jobDir = e.Name()
				}
			}
		}
		if jobDir == "" {
			return nil, errNoRun
		}
		name, ok := stepLogFiles(filepath.Join(dir, "logs", jobDir))[step]
		if !ok {
			return nil, errNoRun
		}
		path, title = filepath.Join(dir, "logs", jobDir, name), v.rd(job)+" / step "+strconv.Itoa(step)
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, errNoRun
	}
	_, isRec := v.recorded(id)
	live := !isRec && v.isLive(id)
	nameKey := strings.Join(v.names, "\x00")
	if live {
		nameKey += "|live"
	}
	key := logKey{path: path, names: nameKey, size: fi.Size(), mod: fi.ModTime().UnixNano()}
	res, err := ciLogCache.get(key, false, func(r readLogResult) int { return len(r.lines) + 1 }, func() (readLogResult, error) {
		return readCILog(path, v.names, live)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errNoRun
		}
		return nil, errors.New("The log could not be read.")
	}
	total := len(res.lines)
	if n <= 0 || n > maxLogPage {
		n = maxLogPage
	}
	if tail {
		from = total - n
	}
	if from < 0 {
		from = 0
	}
	if from > total {
		from = total
	}
	end := from + n
	if end > total {
		end = total
	}
	return &CILog{Title: title, Lines: res.lines[from:end], From: from, Total: total, Cut: res.cut, Live: live, Bytes: fi.Size()}, nil
}

func (v *ciView) recorded(id string) (struct{}, bool) {
	return struct{}{}, fileExists(filepath.Join(v.st.RunsPath(), id, "run.json"))
}

func (v *ciView) isLive(id string) bool {
	_, ok := v.liveRuns(v.requests())[id]
	return ok
}

// readCILog reads the end of a step log, redacts every line from the first one (a private key block can start above the
// part the page asks for), and drops the line still being written when the run is going.
func readCILog(path string, names []string, live bool) (readLogResult, error) {
	f, err := openShared(path)
	if err != nil {
		return readLogResult{}, err
	}
	defer func() { _ = f.Close() }()
	var cut bool
	if fi, err := f.Stat(); err == nil && fi.Size() > ciLogGrab {
		if _, err := f.Seek(fi.Size()-ciLogGrab, io.SeekStart); err == nil {
			cut = true
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, ciLogGrab+(1<<20)))
	if err != nil {
		return readLogResult{}, err
	}
	lines := strings.Split(string(b), "\n")
	if cut && len(lines) > 0 {
		lines = lines[1:] // it starts in the middle of a line
	}
	if n := len(lines); n > 0 {
		if lines[n-1] == "" || live {
			lines = lines[:n-1] // the empty piece after the last newline, or a line still being written
		}
	}
	red := NewRedactor(names...)
	out := make([]LogLine, 0, len(lines))
	for _, raw := range lines {
		out = append(out, LogLine{Text: red.Line(strings.TrimSuffix(raw, "\r"))})
	}
	return readLogResult{lines: out, cut: cut}, nil
}

// ---------------------------------------------------------------- overview

// CIAgent is one agent that has a heartbeat file in the store.
type CIAgent struct {
	Name       string       `json:"name"`
	State      string       `json:"state"`
	Fresh      bool         `json:"fresh"`
	LastSeen   string       `json:"lastSeen,omitempty"`
	Silent     string       `json:"silent,omitempty"` // how long it has been silent, in words, when it is not fresh
	Slots      int          `json:"slots"`
	Jobs       []CIAgentJob `json:"jobs"`
	Labels     []string     `json:"labels,omitempty"`
	StatusMode string       `json:"statusMode,omitempty"`
	Posted     int          `json:"posted"`
	PostFailed int          `json:"postFailed"`
	PostWait   int          `json:"postWaiting"`
	LastPost   string       `json:"lastPost,omitempty"`
	LastError  string       `json:"lastError,omitempty"`
}

// CIAgentJob is what an agent is working on.
type CIAgentJob struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Since string `json:"since,omitempty"`
}

// CIBranch is the newest run of a branch people merge into.
type CIBranch struct {
	Branch string `json:"branch"`
	Run    *CIRun `json:"run"`
}

// CIShadow is the headline of the comparison with GitHub.
type CIShadow struct {
	Available   bool     `json:"available"`
	Generated   string   `json:"generated,omitempty"`
	Streak      int      `json:"streak"`
	Gate        int      `json:"gate"`
	GateMet     bool     `json:"gateMet"`
	Agree       int      `json:"agree"`
	Disagree    int      `json:"disagree"`
	LocalOnly   int      `json:"localOnly"`
	GitHubOnly  int      `json:"githubOnly"`
	Disagreeing []string `json:"disagreeing"`
	Warnings    []string `json:"warnings"`
}

// CIOverview is what the Overview tab shows.
type CIOverview struct {
	CIFound  bool       `json:"ciFound"`
	Now      string     `json:"now"`
	Branches []CIBranch `json:"branches"`
	Pulls    []CIRun    `json:"pulls"`
	Running  []CIRun    `json:"running"`
	Waiting  []CIRun    `json:"waiting"`
	Agents   []CIAgent  `json:"agents"`
	Posted   []CIPosted `json:"posted"`
	Shadow   CIShadow   `json:"shadow"`
}

const (
	maxOverviewPulls  = 6
	maxOverviewPosted = 8
	maxOverviewLines  = 10
)

// Overview gathers the Overview tab.
func (v *ciView) Overview() CIOverview {
	o := CIOverview{CIFound: v.found, Now: tstr(v.now), Branches: []CIBranch{}, Pulls: []CIRun{}, Running: []CIRun{}, Waiting: []CIRun{}, Agents: []CIAgent{}, Posted: []CIPosted{},
		Shadow: CIShadow{Disagreeing: []string{}, Warnings: []string{}, Gate: v.p.ShadowGate}}
	if !v.found {
		return o
	}
	rows := v.rows()
	pullSeen := map[int]bool{}
	for _, r := range rows {
		switch r.Status {
		case stRunning:
			o.Running = append(o.Running, r)
		case stWaiting:
			o.Waiting = append(o.Waiting, r)
		}
		if r.Event == "pull_request" && r.PR != 0 && !pullSeen[r.PR] && len(o.Pulls) < maxOverviewPulls {
			pullSeen[r.PR] = true
			o.Pulls = append(o.Pulls, r)
		}
	}
	for _, b := range v.p.Branches {
		cb := CIBranch{Branch: v.rd(b)}
		for i := range rows {
			if rows[i].Event == "push" && rows[i].Branch == b && rows[i].Status != stWaiting {
				r := rows[i]
				cb.Run = &r
				break
			}
		}
		o.Branches = append(o.Branches, cb)
	}
	hbs, _ := localci.ReadAgents(v.dir)
	for _, h := range hbs {
		o.Agents = append(o.Agents, v.agentOf(h))
	}
	if led, _ := localci.ReadLedger(v.dir); len(led) > 0 {
		for i, r := range led {
			if i >= maxOverviewPosted {
				break
			}
			o.Posted = append(o.Posted, v.postedRow(r))
		}
	}
	if rep, err := localci.ReadCompare(v.dir); err == nil && rep != nil {
		s := &o.Shadow
		s.Available, s.Generated = true, tstr(rep.Generated)
		s.Streak, s.GateMet = rep.Streak, rep.GateMet
		if rep.Gate > 0 {
			s.Gate = rep.Gate
		}
		s.Agree, s.Disagree, s.LocalOnly, s.GitHubOnly = rep.Agree, rep.Disagree, rep.LocalOnly, rep.GitHubOnly
		for i, t := range rep.Disagreeing {
			if i < maxOverviewLines {
				s.Disagreeing = append(s.Disagreeing, v.rd(t))
			}
		}
		for i, t := range rep.Warnings {
			if i < maxOverviewLines {
				s.Warnings = append(s.Warnings, v.rd(t))
			}
		}
	}
	return o
}

func (v *ciView) agentOf(h localci.Heartbeat) CIAgent {
	a := CIAgent{Name: v.rd(h.Name), State: v.rd(h.State), Fresh: h.Fresh(v.now, ciAgentStale), LastSeen: tstr(h.LastSeen), Slots: h.Slots, Jobs: []CIAgentJob{}}
	if !a.Fresh && !h.LastSeen.IsZero() {
		a.Silent = ago(v.now.Sub(h.LastSeen))
	}
	for _, l := range h.Labels {
		a.Labels = append(a.Labels, v.rd(l))
	}
	for _, j := range h.Jobs {
		a.Jobs = append(a.Jobs, CIAgentJob{ID: v.rd(j.ID), Label: v.rd(j.Label), Since: tstr(j.Since)})
	}
	if s := h.Status; s != nil {
		a.StatusMode, a.Posted, a.PostFailed, a.PostWait = v.rd(s.Mode), s.Posted, s.Failed, s.Waiting
		a.LastPost, a.LastError = tstr(s.LastPost), v.rd(s.LastError)
	}
	return a
}

// ---------------------------------------------------------------- projects

// CIProject is a project as the page sees it.
type CIProject struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Repo       string   `json:"repo,omitempty"`
	HasCI      bool     `json:"hasCi"`    // a CI store folder is set
	CIFound    bool     `json:"ciFound"`  // and it exists on this PC
	Releases   bool     `json:"releases"` // the project makes releases
	Channels   []string `json:"channels"`
	Steps      []string `json:"steps"`
	Branches   []string `json:"branches"`
	ShadowGate int      `json:"shadowGate"`
}

// Projects lists the project records. A store folder's path is not sent: it can hold a user name.
func (d *Dash) Projects2() []CIProject {
	var out []CIProject
	for _, p := range d.projects() {
		cp := CIProject{ID: p.ID, Name: p.Name, Repo: p.Repo, HasCI: p.CIStore != "", Releases: p.HasReleases(), Channels: p.Channels, Steps: p.Steps, Branches: p.Branches, ShadowGate: p.ShadowGate}
		if p.CIStore != "" {
			if st, err := os.Stat(p.CIStore); err == nil && st.IsDir() {
				cp.CIFound = true
			}
		}
		if cp.Branches == nil {
			cp.Branches = []string{}
		}
		out = append(out, cp)
	}
	return out
}
