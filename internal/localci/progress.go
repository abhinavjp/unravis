package localci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Kinds of RunEvent.
const (
	EventPlan     = "plan"      // the number of job instances is known (Total)
	EventJobStart = "job-start" // a job instance got a slot and began
	EventJobEnd   = "job-end"   // a job instance ended (Status says how)
)

// RunEvent is what Run tells RunOptions.Progress while a workflow runs.
type RunEvent struct {
	Kind  string
	Total int // plan: how many job instances the run has
	Key   string
	JobID string
	Label string
	// Status of a job-end event: success, failure, cancelled, skipped or not_run_locally.
	Status string
	Reason string
	// FailedStep is "step 3 go vet" for a failed job, when a step is to blame.
	FailedStep string
	Start, End time.Time
}

// emit hands one event to Progress, one at a time.
func (r *runState) emit(e RunEvent) {
	if r.o.Progress == nil {
		return
	}
	r.progMu.Lock()
	defer r.progMu.Unlock()
	r.o.Progress(e)
}

// emitEnd reports the end of a job instance. Texts are masked like every other sink.
func (r *runState) emitEnd(jr *JobResult) {
	if r.o.Progress == nil {
		return
	}
	e := RunEvent{Kind: EventJobEnd, Key: jr.Key, JobID: jr.ID, Label: jr.Label, Status: jr.Status, Reason: r.mask(jr.Reason), Start: jr.Start, End: jr.End}
	for _, s := range jr.Steps {
		if s.Conclusion == "failure" {
			e.FailedStep = r.mask(fmt.Sprintf("step %d %s", s.Index, s.Name))
			break
		}
	}
	r.emit(e)
}

// JobProgress is one job instance in a RunProgress.
type JobProgress struct {
	Key        string    `json:"key"`
	Label      string    `json:"label"`
	Status     string    `json:"status"` // running, or how it ended
	Reason     string    `json:"reason,omitempty"`
	FailedStep string    `json:"failedStep,omitempty"`
	Start      time.Time `json:"start,omitempty"`
	End        time.Time `json:"end,omitempty"`
}

// Ended says whether the job instance is over.
func (j JobProgress) Ended() bool { return j.Status != "" && j.Status != "running" }

// RunProgress is the snapshot a run leaves for the agent that watches it: <state>/queue/progress/<id>.json.
type RunProgress struct {
	Request string        `json:"request"`
	Total   int           `json:"total"` // 0 until the plan is known
	Jobs    []JobProgress `json:"jobs"`
	Updated time.Time     `json:"updated"`
}

// ProgressPath is where the run of request id leaves its RunProgress.
func ProgressPath(stateDir, id string) string {
	return filepath.Join(QueueDir(stateDir), "progress", id+".json")
}

// ReadProgress reads the snapshot of a request; a missing one is an empty snapshot.
func ReadProgress(stateDir, id string) (RunProgress, error) {
	if !validRequestID(id) {
		return RunProgress{}, fmt.Errorf("%q is not a request id", id)
	}
	b, err := os.ReadFile(ProgressPath(stateDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return RunProgress{Request: id}, nil
	}
	if err != nil {
		return RunProgress{}, err
	}
	var p RunProgress
	if err := json.Unmarshal(b, &p); err != nil {
		return RunProgress{}, fmt.Errorf("progress of %s is unreadable: %w", id, err)
	}
	return p, nil
}

// RemoveProgress deletes the snapshot of a request (a missing one is fine).
func RemoveProgress(stateDir, id string) {
	if validRequestID(id) {
		_ = os.Remove(ProgressPath(stateDir, id))
	}
}

// ProgressWriter keeps the snapshot of one request up to date from RunEvents. It is the Progress callback of the
// run, in whatever process the run is in; the agent reads the file.
type ProgressWriter struct {
	StateDir string
	ID       string
	Now      func() time.Time

	mu sync.Mutex
	p  RunProgress
}

// Reset removes a snapshot left by an earlier attempt and starts an empty one.
func (w *ProgressWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.p = RunProgress{Request: w.ID}
	RemoveProgress(w.StateDir, w.ID)
}

// Event applies one event and rewrites the file. A write problem is not the run's problem: the next event tries again.
func (w *ProgressWriter) Event(e RunEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !validRequestID(w.ID) {
		return
	}
	w.p.Request = w.ID
	switch e.Kind {
	case EventPlan:
		w.p.Total = e.Total
	case EventJobStart, EventJobEnd:
		jp := JobProgress{Key: e.Key, Label: e.Label, Status: "running", Start: e.Start}
		if e.Kind == EventJobEnd {
			jp.Status, jp.Reason, jp.FailedStep, jp.End = e.Status, e.Reason, e.FailedStep, e.End
		}
		found := false
		for i := range w.p.Jobs {
			if w.p.Jobs[i].Key == e.Key {
				if jp.Start.IsZero() {
					jp.Start = w.p.Jobs[i].Start
				}
				w.p.Jobs[i], found = jp, true
				break
			}
		}
		if !found {
			w.p.Jobs = append(w.p.Jobs, jp)
		}
	}
	if w.Now != nil {
		w.p.Updated = w.Now()
	} else {
		w.p.Updated = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(ProgressPath(w.StateDir, w.ID)), 0o750); err == nil {
		_ = writeAtomic(ProgressPath(w.StateDir, w.ID), w.p)
	}
}
