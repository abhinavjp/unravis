package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Background jobs of the case-file routes: an archive (write, read back, commit)
// and a verify (test restore). One runs at a time, because each holds the busy
// lock of caseFileState; the job object is what the page polls for percent and
// what Cancel acts on.

const (
	jobArchive = "archive"
	jobVerify  = "verify"

	fileJobRunning = "running"
	fileJobDone    = "done"
	fileJobFailed  = "failed"
	fileJobStopped = "stopped"
)

type fileJob struct {
	kind   string
	caseID casestore.CaseID
	cancel context.CancelFunc
	done   chan struct{} // closed when the job has ended and released everything

	total atomic.Int64
	read  atomic.Int64

	mu         sync.Mutex
	state      string
	phase      string
	reason     string // machine word of a failure or stop
	text       string // the fixed sentence for it
	name       string // the case file's name
	folder     string
	bytes      int64
	cancelled  bool // a person pressed Cancel
	committing bool // the commit point was taken: Cancel is too late
	keyLocked  bool // the master key was locked under it
}

func newFileJob(kind string, id casestore.CaseID, cancel context.CancelFunc) *fileJob {
	return &fileJob{kind: kind, caseID: id, cancel: cancel, done: make(chan struct{}), state: fileJobRunning}
}

func (j *fileJob) setPhase(p string) {
	j.mu.Lock()
	j.phase = p
	j.mu.Unlock()
}

// step is the progress callback handed to the writers and readers.
func (j *fileJob) step(n int64) { j.read.Add(n) }

// requestCancel is Cancel. It reports false when the job already took its commit
// point (or has ended): the commit won and Cancel is too late.
func (j *fileJob) requestCancel() bool {
	j.mu.Lock()
	if j.committing || j.state != fileJobRunning {
		j.mu.Unlock()
		return false
	}
	j.cancelled = true
	j.mu.Unlock()
	j.cancel()
	return true
}

// lockOut is the master key lock reaching a running job: it stops like Cancel
// does, but the reason is the key.
func (j *fileJob) lockOut() {
	j.mu.Lock()
	if j.state == fileJobRunning && !j.committing {
		j.keyLocked = true
	}
	j.mu.Unlock()
	j.cancel()
}

// takeCommit is the decision between Cancel and the commit: whichever gets here
// first wins. It reports true when the commit may go ahead.
func (j *fileJob) takeCommit() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancelled || j.keyLocked {
		return false
	}
	j.committing = true
	return true
}

func (j *fileJob) finish(state, reason, text string) {
	j.mu.Lock()
	j.state, j.reason, j.text = state, reason, text
	j.phase = ""
	j.mu.Unlock()
}

func (j *fileJob) wasCancelled() (cancelled, keyLocked bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cancelled, j.keyLocked
}

// fileJobDTO is the body of the 202 answer and of GET .../archive and .../verify.
type fileJobDTO struct {
	Kind    string `json:"kind"`
	State   string `json:"state"` // running, done, failed, stopped, or idle (no job)
	Phase   string `json:"phase,omitempty"`
	Percent int    `json:"percent"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
	Name    string `json:"name,omitempty"`
	Folder  string `json:"folder,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
}

func (j *fileJob) percent() int {
	j.mu.Lock()
	state := j.state
	j.mu.Unlock()
	if state == fileJobDone {
		return 100
	}
	total := j.total.Load()
	if total <= 0 {
		return 0
	}
	p := int(j.read.Load() * 100 / total)
	if p > 99 {
		p = 99
	}
	if p < 0 {
		p = 0
	}
	return p
}

func (j *fileJob) dto() fileJobDTO {
	p := j.percent()
	j.mu.Lock()
	defer j.mu.Unlock()
	return fileJobDTO{Kind: j.kind, State: j.state, Phase: j.phase, Percent: p, Reason: j.reason, Error: j.text,
		Name: j.name, Folder: j.folder, Bytes: j.bytes}
}

func jobKey(kind string, id casestore.CaseID) string { return kind + "|" + string(id) }

// fileJobs is the registry on caseFileState: the last job of each kind for each
// case. A new job of the same kind and case replaces the old one.
type fileJobs struct {
	mu sync.Mutex
	m  map[string]*fileJob
}

func (r *fileJobs) put(j *fileJob) {
	r.mu.Lock()
	if r.m == nil {
		r.m = map[string]*fileJob{}
	}
	r.m[jobKey(j.kind, j.caseID)] = j
	r.mu.Unlock()
}

func (r *fileJobs) get(kind string, id casestore.CaseID) *fileJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[jobKey(kind, id)]
}

// running returns the job of kind that is still running for the case, or nil.
func (r *fileJobs) running(kind string, id casestore.CaseID) *fileJob {
	j := r.get(kind, id)
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state != fileJobRunning {
		return nil
	}
	return j
}

// stopAll is the master key lock: every running job stops.
func (r *fileJobs) stopAll() {
	r.mu.Lock()
	all := make([]*fileJob, 0, len(r.m))
	for _, j := range r.m {
		all = append(all, j)
	}
	r.mu.Unlock()
	for _, j := range all {
		j.lockOut()
	}
}

// stopCase is a case key lock reaching that case: its running archive and verify
// stop with the key as the reason, and the call waits (bounded) until they have ended,
// so nothing is still writing from a key that was just forgotten.
func (r *fileJobs) stopCase(id casestore.CaseID) {
	r.mu.Lock()
	var hit []*fileJob
	for _, j := range r.m {
		if j.caseID == id {
			hit = append(hit, j)
		}
	}
	r.mu.Unlock()
	for _, j := range hit {
		j.lockOut()
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for _, j := range hit {
		select {
		case <-j.done:
		case <-timer.C:
			return
		}
	}
}

// waitAll waits for every running job to end (a test and shutdown helper).
func (r *fileJobs) waitAll() {
	r.mu.Lock()
	all := make([]*fileJob, 0, len(r.m))
	for _, j := range r.m {
		all = append(all, j)
	}
	r.mu.Unlock()
	for _, j := range all {
		<-j.done
	}
}

// activeImport reports whether an import job of the case has not finished (M1).
func (j *importJobs) activeImport(caseID casestore.CaseID) bool {
	j.mu.Lock()
	var hit []*importJob
	for _, job := range j.m {
		if job.caseID == caseID {
			hit = append(hit, job)
		}
	}
	j.mu.Unlock()
	for _, job := range hit {
		if job.active() {
			return true
		}
	}
	return false
}

// anyRunning reports whether a job of kind is running for any case.
func (r *fileJobs) anyRunning(kind string) bool {
	r.mu.Lock()
	all := make([]*fileJob, 0, len(r.m))
	for _, j := range r.m {
		if j.kind == kind {
			all = append(all, j)
		}
	}
	r.mu.Unlock()
	for _, j := range all {
		j.mu.Lock()
		running := j.state == fileJobRunning
		j.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}
