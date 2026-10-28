package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// The setup window runs unelevated. When a run needs administrator rights it starts a second copy of setup with "run as
// administrator" and the arguments --worker --pipe <name>, and the two talk over that pipe in lines of JSON (one WireMsg per line).
//
// The window sends one "request". The worker answers with "event" and "log" lines while it works and one "result" line at the end;
// the window may send "cancel" at any time. The worker accepts only the operations in workerOps with typed options: it never runs a
// command it is sent, never opens a browser, never starts Unravis, and stops when the pipe closes (which also cancels its run, so the
// journal puts everything back).

// WorkRequest is what the window asks the worker (or silent mode asks the runner) to do.
type WorkRequest struct {
	Op             Op               `json:"op"`
	Options        Options          `json:"options"`
	Uninstall      UninstallOptions `json:"uninstall"`
	AllowDowngrade bool             `json:"allowDowngrade,omitempty"`
	Silent         bool             `json:"silent,omitempty"`
}

// Message types on the pipe.
const (
	MsgRequest = "request"
	MsgCancel  = "cancel"
	MsgEvent   = "event"
	MsgLog     = "log"
	MsgResult  = "result"
)

// WireMsg is one line on the pipe.
type WireMsg struct {
	Type    string       `json:"type"`
	Request *WorkRequest `json:"request,omitempty"`
	Event   *StepEvent   `json:"event,omitempty"`
	Line    string       `json:"line,omitempty"`
	Result  *Result      `json:"result,omitempty"`
}

// maxWireLine is the longest line either side accepts.
const maxWireLine = 1 << 20

var workerOps = map[Op]bool{OpInstall: true, OpUpgrade: true, OpRepair: true, OpUninstall: true}

// Perform runs one request from start to end: find the install, decide what the run really does, check the PC, plan, and execute.
// Nothing changes unless preflight passes and the plan is sound. The window, the worker and silent mode all use it.
func (r *Runner) Perform(ctx context.Context, req WorkRequest) Result {
	if !workerOps[req.Op] {
		return Result{Outcome: OutcomeInvalid, Op: req.Op, Message: fmt.Sprintf("Setup does not know the action %q. Nothing was changed.", req.Op)}
	}
	old, err := Detect(r.Sys.Reg)
	if err != nil {
		return Result{Outcome: OutcomePreflightFailed, Op: req.Op, Message: "Setup could not read what is installed on this PC: " + err.Error()}
	}
	version := ""
	if r.Payload != nil {
		version = r.Payload.Manifest.Version
	}
	if req.Op == OpInstall && strings.TrimSpace(req.Options.Location) == "" && req.Options.Scope != "" {
		req.Options.Location = DefaultLocation(req.Options.Scope, r.Dirs) // silent mode may leave the folder out
	}
	op := ResolveOp(req.Op, old, version)
	if op != req.Op {
		r.logf("asked to %s; Unravis %s is already installed, so this run is a %s", req.Op, old.Version, op)
	}
	if op == OpUninstall {
		if old == nil {
			return Result{Outcome: OutcomeInvalid, Op: op, Message: "Unravis is not installed on this PC, so there is nothing to remove."}
		}
	} else {
		checks := r.Preflight(PreflightInput{Op: op, Opts: req.Options, Old: old, AllowDowngrade: req.AllowDowngrade, Silent: req.Silent})
		for _, c := range checks {
			r.logf("preflight %s: %s: %s", c.ID, c.Status, c.Detail)
		}
		if Blocked(checks) {
			return Result{Outcome: OutcomePreflightFailed, Op: op, Version: version, Checks: checks,
				Message: "Setup found something to fix first. Nothing was changed."}
		}
	}
	p, err := r.NewPlan(op, req.Options, req.Uninstall, old)
	if err != nil {
		res := Result{Outcome: OutcomeInvalid, Op: op, Version: version, Message: plainError(err) + " Nothing was changed."}
		if f := ProblemField(err); f != "" {
			res.Failed = f
		}
		return res
	}
	return r.Execute(ctx, p)
}

// wireWriter writes whole lines from several goroutines.
type wireWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (ww *wireWriter) send(m WireMsg) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ww.mu.Lock()
	defer ww.mu.Unlock()
	_, err = ww.w.Write(append(data, '\n'))
	return err
}

func newWireScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxWireLine)
	return sc
}

// ServeWorker is the elevated side: it reads one request, runs it, streams the progress back and ends with the result. A cancel
// message, or the window going away, cancels the run (which puts everything back). The runner's own Log still gets every line (the
// worker's copy of the log file).
func ServeWorker(ctx context.Context, rw io.ReadWriter, r *Runner) Result {
	ww := &wireWriter{w: rw}
	sc := newWireScanner(rw)
	finish := func(res Result) Result {
		_ = ww.send(WireMsg{Type: MsgResult, Result: &res})
		return res
	}
	if !sc.Scan() {
		return Result{Outcome: OutcomeInvalid, Message: "The setup window went away before it said what to do. Nothing was changed."}
	}
	var first WireMsg
	if err := json.Unmarshal(sc.Bytes(), &first); err != nil || first.Type != MsgRequest || first.Request == nil {
		return finish(Result{Outcome: OutcomeInvalid, Message: "The setup window sent something the worker does not accept. Nothing was changed."})
	}
	req := *first.Request
	if !workerOps[req.Op] {
		return finish(Result{Outcome: OutcomeInvalid, Op: req.Op, Message: fmt.Sprintf("The worker does not accept the action %q. Nothing was changed.", req.Op)})
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		// Anything after the request is a cancel, or the end of the pipe; both stop the run.
		for sc.Scan() {
			var m WireMsg
			if json.Unmarshal(sc.Bytes(), &m) == nil && m.Type == MsgCancel {
				break
			}
		}
		cancel()
	}()
	w := *r
	w.Log = func(s string) {
		if r.Log != nil {
			r.Log(s)
		}
		_ = ww.send(WireMsg{Type: MsgLog, Line: Redact(s)})
	}
	w.Events = func(e StepEvent) {
		if r.Events != nil {
			r.Events(e)
		}
		_ = ww.send(WireMsg{Type: MsgEvent, Event: &e})
	}
	return finish(w.Perform(ctx, req))
}

// ErrUACCancelled is the person saying No at the Windows approval prompt.
var ErrUACCancelled = errors.New("the administrator approval was cancelled")

// Elevator starts the elevated worker and returns the pipe to it. On Windows it is ShellExecuteW "runas" and a named pipe that only
// the current user and SYSTEM may open; tests use a fake over net.Pipe.
type Elevator interface {
	Start(ctx context.Context) (io.ReadWriteCloser, error)
}

// RunWorker is the window's side: it starts the worker, sends the request and passes each event and log line on until the result. A
// cancel of ctx is sent to the worker, which puts everything back and still answers with its result.
func RunWorker(ctx context.Context, el Elevator, req WorkRequest, onEvent func(StepEvent), onLog func(string)) Result {
	conn, err := el.Start(ctx)
	if errors.Is(err, ErrUACCancelled) || errors.Is(err, ErrCancelled) {
		return Result{Outcome: OutcomeCancelledUAC, Op: req.Op,
			Message: "The Windows approval was cancelled, so nothing was changed.", Fix: "Press Install again and choose Yes when Windows asks, or install just for you."}
	}
	if err != nil {
		return Result{Outcome: OutcomeRolledBack, Op: req.Op, Message: "Setup could not start its administrator part, so nothing was changed: " + err.Error()}
	}
	defer conn.Close()
	ww := &wireWriter{w: conn}
	if err := ww.send(WireMsg{Type: MsgRequest, Request: &req}); err != nil {
		return Result{Outcome: OutcomeRolledBack, Op: req.Op, Message: "Setup could not talk to its administrator part, so nothing was changed: " + err.Error()}
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = ww.send(WireMsg{Type: MsgCancel})
		case <-done:
		}
	}()
	sc := newWireScanner(conn)
	for sc.Scan() {
		var m WireMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch m.Type {
		case MsgEvent:
			if m.Event != nil && onEvent != nil {
				onEvent(*m.Event)
			}
		case MsgLog:
			if onLog != nil {
				onLog(Redact(m.Line))
			}
		case MsgResult:
			if m.Result != nil {
				return *m.Result
			}
		}
	}
	// The worker ended without a result: it may have been stopped in the middle. Its journal is still on disk and the next run puts
	// back what it changed before doing anything else.
	return Result{Outcome: OutcomeRollbackLeft, Op: req.Op,
		Message: "The administrator part of setup stopped before it finished.",
		Fix:     "Run setup again: it first puts back anything the stopped run changed.",
		Left:    []string{"Whatever the stopped run changed (setup puts it back on its next run)."}}
}
