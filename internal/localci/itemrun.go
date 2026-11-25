package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ItemRunner runs a queued request in this process: RunPR for a pull request, RunRef for a push or a schedule.
// Nothing here is project specific; the host, the protected paths and the environment are the caller's.
type ItemRunner struct {
	Store     *Store
	Host      Host
	OriginURL string
	Protect   []string
	// TrustedBases are the base branches whose content may supply the runner definition of a pull request that is
	// not all the owner's.
	TrustedBases []string
	Env          []string
	Vars         map[string]string
	Slots        int
	Keep         bool
	Redact       func(string) string
	Log          io.Writer
	Now          func() time.Time
}

// Run is the Runner of an in-process agent. A pull request whose head moved on, or that closed, is not run and
// is ended as superseded or cancelled (the poller has queued, or will queue, the newer head).
func (ir *ItemRunner) Run(ctx context.Context, r Request) (Outcome, error) {
	var res *PRResult
	var err error
	switch r.Kind {
	case KindPR:
		res, err = RunPR(ctx, PROptions{
			Store: ir.Store, Host: ir.Host, Repo: r.Repo, Number: r.Number, ExpectHead: r.SHA, OriginURL: ir.OriginURL,
			Workflow: r.Workflow, Protect: ir.Protect, TrustedBases: ir.TrustedBases, Keep: ir.Keep, Env: ir.Env, Vars: ir.Vars, Slots: ir.Slots,
			Redact: ir.Redact, Log: ir.Log, Now: ir.Now,
		})
	case KindPush, KindSchedule:
		event := "push"
		if r.Kind == KindSchedule {
			event = "schedule"
		}
		res, err = RunRef(ctx, RefOptions{
			Store: ir.Store, Host: ir.Host, Repo: r.Repo, Ref: r.Ref, SHA: r.SHA, Workflow: r.Workflow, Event: event, Cron: r.Cron,
			OriginURL: ir.OriginURL, Keep: ir.Keep, Env: ir.Env, Vars: ir.Vars, Slots: ir.Slots, Redact: ir.Redact, Log: ir.Log, Now: ir.Now,
		})
	default:
		return Outcome{}, fmt.Errorf("request kind %q is not known", r.Kind)
	}
	out := Outcome{}
	if res != nil {
		out.RunID = res.RunID
	}
	switch {
	case errors.Is(err, ErrHeadMoved):
		return Outcome{Conclusion: StateSuperseded, Reason: err.Error()}, nil
	case errors.Is(err, ErrPRNotOpen):
		return Outcome{Conclusion: StateCancelled, Reason: err.Error()}, nil
	case err != nil:
		out.Conclusion, out.Reason = "error", err.Error()
		return out, err
	}
	out.Conclusion, out.Reason = res.Run.Conclusion, res.Entry.Reason
	return out, nil
}

// OutcomePath is where a child process leaves its Outcome for the agent that spawned it.
func OutcomePath(stateDir, id string) string {
	return filepath.Join(QueueDir(stateDir), "outcome", id+".json")
}

// WriteOutcome records the outcome of a request for the agent that waits for the child process.
func WriteOutcome(stateDir, id string, out Outcome) error {
	if !validRequestID(id) {
		return fmt.Errorf("%q is not a request id", id)
	}
	if err := os.MkdirAll(filepath.Dir(OutcomePath(stateDir, id)), 0o750); err != nil {
		return err
	}
	return writeAtomic(OutcomePath(stateDir, id), out)
}

// ExecRunner runs each request in a child process, so that a request that wedges or crashes takes nothing else
// with it and can be stopped with its whole process tree. The child reads the request from the queue, runs it
// and leaves its Outcome in OutcomePath. It stops when its stdin closes: ExecRunner closes it when the request is
// cancelled or the agent stops (and the OS closes it if the agent dies), then kills the tree after Grace.
type ExecRunner struct {
	StateDir string
	// Argv builds the command line of the child for a request: the program first.
	Argv func(r Request) []string
	Env  []string
	Log  io.Writer
	// Grace is how long a cancelled child has to end by itself before its process tree is killed (default 30 s).
	Grace time.Duration

	logMu sync.Mutex
}

// Run is the Runner of an agent that spawns children.
func (e *ExecRunner) Run(ctx context.Context, r Request) (Outcome, error) {
	argv := e.Argv(r)
	if len(argv) == 0 {
		return Outcome{}, errors.New("no command line for the child process")
	}
	_ = os.Remove(OutcomePath(e.StateDir, r.ID))
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the program and its arguments are built by the serve command
	cmd.Env = e.Env
	cmd.WaitDelay = 5 * time.Second // a grandchild holding the output pipes must not hold the agent
	cmd.Stdout, cmd.Stderr = e.sink(), e.sink()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Outcome{}, err
	}
	grace := e.Grace
	if grace <= 0 {
		grace = 30 * time.Second
	}
	killCtx, kill := context.WithCancel(context.Background())
	defer kill()
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = stdin.Close()
			t := time.NewTimer(grace)
			defer t.Stop()
			select {
			case <-t.C:
				kill()
			case <-done:
			}
		case <-done:
		}
	}()
	started, werr := runTree(killCtx, cmd, "")
	close(done)
	<-watcherDone
	if !started {
		return Outcome{}, fmt.Errorf("cannot start the child process: %w", werr)
	}
	b, rerr := os.ReadFile(OutcomePath(e.StateDir, r.ID))
	_ = os.Remove(OutcomePath(e.StateDir, r.ID))
	if rerr != nil {
		why := "no outcome was left"
		if werr != nil {
			why = werr.Error()
		}
		return Outcome{}, fmt.Errorf("the child process ended without a result (%s)", why)
	}
	var out Outcome
	if err := json.Unmarshal(b, &out); err != nil {
		return Outcome{}, fmt.Errorf("the child's result is unreadable: %w", err)
	}
	return out, nil
}

// sink is the writer of a child's output: the agent's log, one write at a time.
func (e *ExecRunner) sink() io.Writer {
	if e.Log == nil {
		return io.Discard
	}
	return lockedWriter{mu: &e.logMu, w: e.Log}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// IndexRan returns the Ran function of the poller: a pull request head or a pushed commit counts as handled
// when the run index holds a success or failure for the same workflow. Cancelled and errored runs do not count
// (the next start looks again), and a schedule never counts: its time slot is kept by the poller.
func IndexRan(store *Store) func(Request) bool {
	var mu sync.Mutex
	var loaded time.Time
	var entries []IndexEntry
	return func(r Request) bool {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(loaded) > 5*time.Second {
			if es, err := store.ReadIndex(); err == nil {
				entries, loaded = es, time.Now()
			}
		}
		for _, e := range entries {
			if e.Repo != r.Repo || e.HeadSHA != r.SHA || e.Workflow != r.Workflow || (e.Conclusion != "success" && e.Conclusion != "failure") {
				continue
			}
			switch {
			case r.Kind == KindPR && e.Event == "pull_request" && e.PR == r.Number:
				return true
			case r.Kind == KindPush && e.Event == "push" && e.HeadRef == r.Ref:
				return true
			}
		}
		return false
	}
}
