package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/privacy"
	"api-audit-explorer/internal/codemode/wire"
	"api-audit-explorer/internal/jcs"
	"api-audit-explorer/internal/mcp"
)

// maxLogLines bounds the lines of console output the broker sweeps one by one (the 64 KiB byte cap applies as well).
const maxLogLines = 2000

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

var runtimePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// failure is the first thing that ended a run.
type failure struct {
	code   string
	detail string // text already safe to show (fixed text, or swept by the Guard); empty means the plain text of the code
}

// run is the state of one accepted run.
type run struct {
	b      *Broker
	id     string
	store  casestore.CodeRunStore
	req    RunRequest
	allow  bool
	host   Host
	start  time.Time
	worker Worker
	// ctx ends the run (cancel, cap, first failure); callsCtx is the context of host calls and ends with the run or when the script
	// has finished and its unfinished calls are abandoned.
	ctx         context.Context
	cancel      context.CancelFunc
	callsCtx    context.Context
	callsCancel context.CancelFunc
	parent      context.Context

	// serial orders the end of every call: done_seq, the Guard, the audit write and the answer to the worker all happen under it, so
	// the audit order is the order the script saw. It also makes the Guard (not safe for concurrent use) single-threaded.
	serial    sync.Mutex
	guard     *privacy.Guard
	doneSeq   int
	truncated bool // a call result was too large for the script (set under serial)

	mu       sync.Mutex
	cause    *failure
	finished bool
	over     bool // the script sent its last frame: calls it did not wait for are abandoned and get no answer
	seq      int
	inflight int
	usedIDs  map[int64]bool
	tools    []string // the tools this run may call: all ten for the owner, the tier's own for a grant (fixed before the run starts)
	timer    scriptTimer
	wg       sync.WaitGroup
}

// fail records the first failure and ends the run (the worker is killed). Later failures are ignored: the first cause is the one that
// is reported and recorded.
func (r *run) fail(code, detail string) {
	r.mu.Lock()
	if r.cause == nil && !r.finished {
		r.cause = &failure{code: code, detail: detail}
	}
	r.mu.Unlock()
	r.cancel()
}

// settleCause names the reason the run's context ended when nothing else did: the caller gave up, or the 280 second cap was reached. It is
// called by everything that notices the end of the context, so the cause is set before anyone reads it.
func (r *run) settleCause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cause == nil && !r.finished {
		if r.parent.Err() != nil {
			r.cause = &failure{code: CodeCancelled}
		} else {
			r.cause = &failure{code: CodeRunCap}
		}
	}
}

func (r *run) failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cause != nil
}

// scriptTimer counts the time the script itself runs: it stands still while any host call is in flight (spec D3 a).
type scriptTimer struct {
	kick    chan struct{} // signalled when the clock starts or stops, so the watcher recomputes its sleep
	mu      sync.Mutex
	now     func() time.Time
	acc     time.Duration
	since   time.Time
	started bool
	paused  int
}

func (t *scriptTimer) running() bool { return t.started && t.paused == 0 }

func (t *scriptTimer) wake() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

// left is the script time still allowed and whether the clock is running.
func (t *scriptTimer) left(limit time.Duration) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running() {
		return limit - t.acc, false
	}
	return limit - t.acc - t.now().Sub(t.since), true
}

func (t *scriptTimer) begin() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started, t.since = true, t.now()
	t.wake()
}

func (t *scriptTimer) pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running() {
		t.acc += t.now().Sub(t.since)
	}
	t.paused++
	t.wake()
}

func (t *scriptTimer) resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paused > 0 {
		t.paused--
	}
	if t.running() {
		t.since = t.now()
	}
	t.wake()
}

func (t *scriptTimer) elapsed() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running() {
		return t.acc + t.now().Sub(t.since)
	}
	return t.acc
}

// Run records and runs one script. A Refusal is returned for a request that is turned away before anything is written; every other
// outcome (a script error, a limit, a crashed worker) is a RunResult, recorded in the case.
func (b *Broker) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	var set Settings
	if b.cfg.Settings != nil {
		set = b.cfg.Settings()
	}
	if !set.Enabled {
		return RunResult{}, refuse(CodeModeOff, plain[CodeModeOff])
	}
	if b.cfg.Authorize != nil {
		if err := b.cfg.Authorize(ctx, req); err != nil {
			var ref *Refusal
			if errors.As(err, &ref) {
				return RunResult{}, ref
			}
			return RunResult{}, refuse(CodeDenied, plain[CodeDenied])
		}
	}
	if req.AllowIdentifiers && !b.cfg.AllowIdentifiers {
		return RunResult{}, refuse(CodeDenied, "The operator has not allowed identifiers in code runs.")
	}
	if ref := b.cleanRequest(&req); ref != nil {
		return RunResult{}, ref
	}
	if b.cfg.Spawner == nil || b.cfg.Spawner.Available() != nil {
		return RunResult{}, refuse(CodeSandboxUnavailable, plain[CodeSandboxUnavailable])
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		return RunResult{}, refuse(CodeBusy, plain[CodeBusy])
	}
	store, caseID, err := b.cfg.Cases(ctx, req.CaseID)
	if err != nil || store == nil {
		return RunResult{}, refuse(CodeNoCase, plain[CodeNoCase])
	}
	b.sweepOrphans(ctx, caseID, store)

	seed, err := newSeed()
	if err != nil {
		return RunResult{}, refuse(CodeSandboxUnavailable, plain[CodeSandboxUnavailable])
	}
	now := b.cfg.Now()
	clock, err := privacy.NewScriptClock(privacy.ClockLive, now.UnixMilli(), b.cfg.Now)
	if err != nil {
		return RunResult{}, refuse(CodeBadRequest, plain[CodeBadRequest])
	}
	runID, err := casestore.NewRunID(now)
	if err != nil {
		return RunResult{}, refuse(CodeBadRequest, plain[CodeBadRequest])
	}

	// The tool host is made before the begin row, so a host that cannot be made leaves nothing open.
	host, err := safeHosts(b.cfg.Hosts, HostScope{AllowIdentifiers: req.AllowIdentifiers, Scoped: req.Scoped, Sources: slices.Clone(req.Sources), Tier: req.Tier})
	if err != nil {
		return RunResult{}, refuse(CodeInternal, plain[CodeInternal])
	}

	// Audit begin: nothing runs before this row and its chain record are on disk (spec 3.5 step 3).
	if _, err := store.BeginRun(ctx, casestore.RunStart{
		RunID: runID, Surface: req.Surface, CallerID: req.CallerID, CallerDeclared: req.CallerDeclared, AllowIdentifiers: req.AllowIdentifiers,
		KeepResults: set.KeepResults, ToolSchemaHash: b.schema, Reason: req.Reason, Source: req.Code, Seed: seed, ClockMS: clock.BaseMs(),
	}); err != nil {
		switch {
		case errors.Is(err, casestore.ErrCaseClosed), errors.Is(err, casestore.ErrNotFound), errors.Is(err, casestore.ErrHandleClosed):
			return RunResult{}, refuse(CodeNoCase, plain[CodeNoCase])
		case errors.Is(err, casestore.ErrImportRunning):
			return RunResult{}, refuse(CodeBusy, "An import is running on the case. Try again when it has finished.")
		}
		return RunResult{}, refuse(CodeAuditFailed, plain[CodeAuditFailed])
	}

	r := &run{b: b, id: runID, store: store, req: req, allow: req.AllowIdentifiers, start: now, parent: ctx, usedIDs: map[int64]bool{}, tools: b.toolsFor(&req),
		guard: privacy.NewGuard(req.AllowIdentifiers), host: host}
	r.timer.now = b.cfg.Now
	r.timer.kick = make(chan struct{}, 1)
	r.ctx, r.cancel = context.WithTimeout(ctx, b.runCap)
	r.callsCtx, r.callsCancel = context.WithCancel(r.ctx)
	b.mu.Lock()
	b.active[runID] = r
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.active, runID)
		b.mu.Unlock()
	}()
	return r.execute(ctx, set, seed, clock.BaseMs())
}

// guardedRun is runWorker under a recover: a panic anywhere in the run (a bug in the Guard on hostile text, say) ends this run as
// internal_error and the run is still sealed. It never reaches the app.
func (r *run) guardedRun(set Settings, seed string, baseMs int64) (out outcome) {
	defer func() {
		if p := recover(); p != nil {
			r.fail(CodeInternal, "")
			r.mu.Lock()
			out = outcome{fail: r.cause}
			r.mu.Unlock()
			r.killWorker()
		}
	}()
	return r.runWorker(set, seed, baseMs)
}

// killWorker kills the worker if there is one (after a panic the usual reaping did not run to its end).
func (r *run) killWorker() {
	if wk := r.worker; wk != nil {
		wk.Kill()
	}
}

// outcome is what the run ended with.
type outcome struct {
	fail     *failure
	result   []byte
	logs     []string
	logsCut  bool
	strip    string // hash of the stripped JS
	mode     string
	runtime  string
	workerSH string
	scriptMS int64
}

func (r *run) execute(ctx context.Context, set Settings, seed string, baseMs int64) (RunResult, error) {
	out := r.guardedRun(set, seed, baseMs)
	r.finishWatchers()

	// Every call is on disk (handlers finished) before the end record seals them.
	end := casestore.RunEnd{
		Status: statusFor(out.fail), SandboxMode: out.mode, Runtime: out.runtime, WorkerSHA256: out.workerSH, StrippedJSHash: out.strip,
		DurationMS: r.b.cfg.Now().Sub(r.start).Milliseconds(), ScriptMS: out.scriptMS, ConsoleLogs: strings.Join(out.logs, "\n"),
	}
	res := RunResult{V: 1, RunID: r.id, Logs: out.logs, Result: json.RawMessage("null"), Identifiers: "hidden",
		Sandbox: Sandbox{Mode: out.mode, Runtime: out.runtime, Worker: short(out.workerSH)}}
	if r.allow {
		res.Identifiers = "shown"
	}
	if res.Logs == nil {
		res.Logs = []string{}
	}
	if out.fail != nil {
		end.ErrorCode = out.fail.code
		msg := out.fail.detail
		if msg == "" {
			msg = plain[out.fail.code]
		}
		end.ErrorText = msg
		res.Error = &RunError{Code: out.fail.code, Message: msg}
	} else {
		end.Result = out.result
		res.Result = json.RawMessage(out.result)
	}
	r.mu.Lock()
	calls := r.seq
	r.mu.Unlock()
	r.serial.Lock()
	truncated := r.truncated
	r.serial.Unlock()
	res.Stats = Stats{Calls: calls, DurationMS: end.DurationMS, ScriptMS: out.scriptMS, Truncated: truncated, LogsTruncated: out.logsCut}
	res.Status = end.Status

	// The end record must be written even when the caller gave up: the context of the caller only decides whether the run was
	// cancelled, not whether it is sealed.
	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), casestore.InflightWait+10*time.Second)
	defer cancel()
	_, err := r.store.EndRun(endCtx, r.id, end)
	if errors.Is(err, casestore.ErrBadCodeRun) {
		// A field of the end record was refused. The run must not stay open for it: seal it with the outcome and nothing else.
		_, err = r.store.EndRun(endCtx, r.id, casestore.RunEnd{Status: casestore.RunError, ErrorCode: CodeAuditFailed, ErrorText: plain[CodeAuditFailed],
			DurationMS: end.DurationMS})
		if err == nil {
			// The answer says what the record says.
			res.Status, res.Result, res.Logs = casestore.RunError, json.RawMessage("null"), []string{}
			res.Error = &RunError{Code: CodeAuditFailed, Message: plain[CodeAuditFailed]}
		}
	}
	if err != nil {
		res.Status = casestore.RunError
		res.Result, res.Logs = json.RawMessage("null"), []string{}
		res.Error = &RunError{Code: CodeAuditFailed, Message: plain[CodeAuditFailed]}
		return res, fmt.Errorf("codemode: the run ended but its record could not be sealed: %w", err)
	}
	return res, nil
}

func short(h string) string {
	if len(h) >= 8 {
		return h[:8]
	}
	return h
}

func statusFor(f *failure) string {
	if f == nil {
		return casestore.RunOK
	}
	switch f.code {
	case CodeWall, CodeRunCap:
		return casestore.RunTimeout
	case CodeCancelled:
		return casestore.RunCancelled
	case CodeWorkerCrashed:
		return casestore.RunCrashed
	case CodeCallBudget, CodeTicks, CodeOOM, CodeStack, CodeOutputTooLarge, CodeResultTooLarge, CodeIdentifierLimit:
		return casestore.RunLimitExceeded
	}
	return casestore.RunError
}

// finishWatchers marks the run finished and waits for the call handlers. After it returns, nothing writes to the audit record but the
// end record.
func (r *run) finishWatchers() {
	r.callsCancel()
	r.wg.Wait()
	r.mu.Lock()
	r.finished = true
	r.mu.Unlock()
	r.cancel()
}

// runWorker is the part of the run from the strip to the last frame. It never returns before the worker is gone (or the kill wait is
// over) and every call handler is finished.
func (r *run) runWorker(set Settings, seed string, baseMs int64) (out outcome) {
	gotDone := false
	defer func() {
		out.scriptMS = r.timer.elapsed().Milliseconds()
		if out.fail == nil {
			r.mu.Lock()
			out.fail = r.cause
			r.mu.Unlock()
		}
		if out.fail == nil && !gotDone {
			// Whatever ended the loop, a run that never produced its last frame did not succeed.
			out.fail = &failure{code: CodeWorkerCrashed}
		}
	}()

	js, sf := strip(r.req.Code)
	if sf != nil {
		detail := r.g("strip", r.guard.ScriptError(sf.msg))
		if !r.allow && privacy.ContainsIP(detail) {
			detail = plain[sf.code] // the words of the parser held an address: say the fixed thing
			if detail == "" {
				detail = plain[CodeSyntaxError]
			}
		}
		out.fail = &failure{code: sf.code, detail: detail}
		return out
	}
	out.strip = hashHex(js)

	wk, err := r.b.cfg.Spawner.Start(r.ctx, SpawnOptions{AllowDegraded: set.AllowDegraded})
	if err != nil || wk == nil {
		code := CodeSandboxUnavailable
		if errors.Is(err, ErrDegradedRefused) {
			code = CodeSandboxDegradedNope
		}
		out.fail = &failure{code: code}
		return out
	}
	r.worker = wk
	defer r.reap(wk)
	// What the spawner says about the worker is recorded only when it has the shape the record accepts; a spawner that breaks its own
	// contract is a sandbox that cannot be trusted.
	switch {
	case wk.Mode() == ModeFallback && !set.AllowDegraded:
		out.mode = ModeFallback
		out.fail = &failure{code: CodeSandboxDegradedNope}
		return out
	case wk.Mode() != ModeLPAC && wk.Mode() != ModeFallback, !hex64.MatchString(wk.SHA256()):
		out.fail = &failure{code: CodeSandboxUnavailable}
		return out
	}
	out.mode, out.workerSH = wk.Mode(), wk.SHA256()
	// The killer: whatever ends the run (the caller, the cap, a failure) kills the worker, which also unblocks every pipe read and write.
	go func() {
		<-r.ctx.Done()
		r.settleCause()
		wk.Kill()
	}()

	frames := make(chan frameResult)
	go readFrames(r.ctx, wk, frames)

	// hello, before any script code.
	hello, ok := r.awaitHello(frames)
	if !ok {
		return out
	}
	out.runtime = hello.Runtime

	runFrame, err := wire.Marshal(wire.Run{T: wire.TypeRun, JS: js, Tools: r.tools, Limits: workerLimits(),
		Clock: wire.Clock{Mode: string(privacy.ClockLive), BaseMs: baseMs}, Seed: seed})
	if errors.Is(err, wire.ErrFrameTooLarge) {
		// The script is within the source limit, but its JavaScript does not fit one frame once it is written out.
		out.fail = &failure{code: CodeSourceTooLarge}
		return out
	}
	if err != nil || wire.WriteFrame(wk.Stdin(), runFrame) != nil {
		r.fail(CodeWorkerCrashed, "")
		return out
	}
	r.timer.begin()
	stopClock := r.watchScriptTime(r.b.timeout(r.req.TimeoutMS))
	defer stopClock()

	var done *wire.Done
	for done == nil {
		var fr frameResult
		select {
		case fr = <-frames:
		case <-r.ctx.Done():
			r.settleCause()
			return out
		}
		if fr.err != nil {
			switch {
			case errors.Is(fr.err, wire.ErrFrameTooLarge), errors.Is(fr.err, wire.ErrEmptyFrame):
				r.fail(CodeBadFrame, "")
			default:
				r.fail(CodeWorkerCrashed, "")
			}
			return out
		}
		typ, err := wire.PeekType(fr.data)
		if err != nil {
			r.fail(CodeBadFrame, "")
			return out
		}
		switch typ {
		case wire.TypeCall:
			var c wire.Call
			if wire.Decode(fr.data, &c) != nil {
				r.fail(CodeBadFrame, "")
				return out
			}
			if r.onCall(c) {
				// A call that ends the run: nothing more is read, so no later frame becomes a goroutine or a row. The denial's own row is
				// written and the run is failed by its handler.
				<-r.ctx.Done()
				r.settleCause()
				return out
			}
		case wire.TypeDone:
			var d wire.Done
			if wire.Decode(fr.data, &d) != nil {
				r.fail(CodeBadFrame, "")
				return out
			}
			done = &d
		default: // a second hello, a ret from the worker, anything else
			r.fail(CodeBadFrame, "")
			return out
		}
	}
	// The script is over. Calls it did not wait for are abandoned (and recorded as they end).
	r.mu.Lock()
	r.over = true
	r.mu.Unlock()
	r.timer.pause()
	r.callsCancel()
	r.wg.Wait()
	if r.failed() {
		return out
	}
	gotDone = true
	r.takeDone(done, &out)
	return out
}

type frameResult struct {
	data []byte
	err  error
}

// readFrames reads frames from the worker until the run ends. It gives each frame to the loop and stops on the first error.
func readFrames(ctx context.Context, wk Worker, out chan<- frameResult) {
	for {
		data, err := wire.ReadFrame(wk.Stdout(), wire.MaxFrame)
		select {
		case out <- frameResult{data: data, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// awaitHello reads the first frame and checks it. It gives the worker HelloTimeout.
func (r *run) awaitHello(frames <-chan frameResult) (wire.Hello, bool) {
	timer := time.NewTimer(r.b.helloWait)
	defer timer.Stop()
	var fr frameResult
	select {
	case fr = <-frames:
	case <-timer.C:
		r.fail(CodeSandboxUnavailable, "")
		return wire.Hello{}, false
	case <-r.ctx.Done():
		r.settleCause()
		return wire.Hello{}, false
	}
	var h wire.Hello
	if fr.err != nil || wire.Decode(fr.data, &h) != nil || h.T != wire.TypeHello || h.V != wire.Version || !runtimePattern.MatchString(h.Runtime) {
		r.fail(CodeSandboxUnavailable, "")
		return wire.Hello{}, false
	}
	for _, name := range r.b.cfg.RequiredMitigations {
		if !h.Mitigations[name] {
			r.fail(CodeSandboxUnavailable, "")
			return wire.Hello{}, false
		}
	}
	return h, true
}

// watchScriptTime ends the run with wall when the script itself (not its waits for host calls) has run for limit. It sleeps on one
// timer set to the time that is left and wakes early only when a call starts or ends (no polling: nothing runs while a call is in
// flight, and nothing at all runs when no run is live).
func (r *run) watchScriptTime(limit time.Duration) (stop func()) {
	quit := make(chan struct{})
	var once sync.Once
	go func() {
		timer := time.NewTimer(limit)
		defer timer.Stop()
		for {
			left, running := r.timer.left(limit)
			if running && left <= 0 {
				r.fail(CodeWall, "")
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if running {
				timer.Reset(left)
			}
			select {
			case <-quit:
				return
			case <-r.ctx.Done():
				return
			case <-r.timer.kick:
			case <-timer.C:
			}
		}
	}()
	return func() { once.Do(func() { close(quit) }) }
}

// reap kills the worker, closes its pipes and waits (bounded) for it to be gone.
func (r *run) reap(wk Worker) {
	wk.Kill()
	_ = wk.Stdin().Close()
	_ = wk.Stdout().Close()
	gone := make(chan struct{})
	go func() {
		_ = wk.Wait()
		close(gone)
	}()
	select {
	case <-gone:
	case <-time.After(KillWait):
	}
}

// takeDone turns the last frame into the outcome. The result, every log line and the error text pass the Guard before they are kept.
func (r *run) takeDone(d *wire.Done, out *outcome) {
	r.serial.Lock()
	defer r.serial.Unlock()
	logs, cut := r.sweepLogs(d.Logs)
	out.logs, out.logsCut = logs, cut || d.LogsTruncated
	if r.guard.Incomplete() || leaks(r.allow, nil, logs) {
		// Logs are returned and stored whether the script succeeded or not, so the last check on them is made on every path.
		out.logs = nil
		out.fail = &failure{code: CodeIdentifierLimit}
		return
	}
	if !d.OK {
		if d.Error == nil || d.Result != nil {
			out.fail = &failure{code: CodeBadFrame}
			return
		}
		switch d.Error.Code {
		case CodeScriptError, CodeTicks, CodeOOM, CodeStack, CodeOutputTooLarge:
		default:
			out.fail = &failure{code: CodeBadFrame}
			return
		}
		detail := ""
		if d.Error.Code == CodeScriptError {
			detail = r.g("scripterror", r.guard.ScriptError(d.Error.Message))
			if r.guard.Incomplete() {
				out.fail = &failure{code: CodeIdentifierLimit}
				return
			}
			if !r.allow && privacy.ContainsIP(detail) {
				out.fail = &failure{code: CodeIdentifierLimit}
				return
			}
		}
		out.fail = &failure{code: d.Error.Code, detail: detail}
		return
	}
	if d.Error != nil {
		out.fail = &failure{code: CodeBadFrame}
		return
	}
	raw := []byte(d.Result)
	if len(raw) == 0 {
		raw = []byte("null") // the script returned undefined
	}
	if len(raw) > MaxResultBytes {
		out.fail = &failure{code: CodeOutputTooLarge}
		return
	}
	swept, err := r.guard.Output(raw)
	swept = r.gb("output", swept)
	switch {
	case errors.Is(err, privacy.ErrIncomplete):
		out.fail = &failure{code: CodeIdentifierLimit}
		return
	case err != nil:
		out.fail = &failure{code: CodeScriptError, detail: "The result could not be returned: it is nested too deeply or is not valid JSON."}
		return
	case len(swept) > MaxResultBytes:
		out.fail = &failure{code: CodeOutputTooLarge}
		return
	}
	if leaks(r.allow, swept, logs) {
		// The Guard is the only net that was supposed to catch an address; a second net found one anyway. Nothing leaves.
		out.logs = nil
		out.fail = &failure{code: CodeIdentifierLimit}
		return
	}
	out.result = swept
}

// sweepLogs runs each console line through the Guard under the 64 KiB and line caps.
func (r *run) sweepLogs(lines []string) ([]string, bool) {
	var out []string
	size := 0
	cut := false
	for i, line := range lines {
		if i >= maxLogLines || size+len(line)+1 > MaxLogBytes+1024 {
			cut = true
			break
		}
		swept := r.g("log", r.guard.Log(line))
		if size+len(swept)+1 > MaxLogBytes {
			cut = true
			break
		}
		size += len(swept) + 1
		out = append(out, swept)
	}
	return out, cut
}

// leaks is the last check before a result and its logs are stored: in hidden mode none of them may hold an IP address.
// g and gb pass what a Guard call returned through the test seam (a no-op in production).
func (r *run) g(stage, text string) string {
	if r.b.tamper != nil {
		return r.b.tamper(stage, text)
	}
	return text
}

func (r *run) gb(stage string, b []byte) []byte {
	if r.b.tamper != nil && b != nil {
		return []byte(r.b.tamper(stage, string(b)))
	}
	return b
}

func leaks(allow bool, result []byte, logs []string) bool {
	if allow {
		return false
	}
	if privacy.ContainsIP(string(result)) {
		return true
	}
	for _, l := range logs {
		if privacy.ContainsIP(l) {
			return true
		}
	}
	return false
}

// call frames ---------------------------------------------------------------------------------------------------------------------

// denial is a call the broker does not run.
type denial struct {
	status string // always casestore.CallDenied
	wire   string // the code of the error answer the script gets
	msg    string
	stop   string // when set, the run ends with this code after the call is recorded
}

// onCall takes one call frame. It runs on the read loop, so the counters it checks are exact. It either starts a handler or, for a call
// it will not run, a handler that only records the denial.
func (r *run) onCall(c wire.Call) (stops bool) {
	r.mu.Lock()
	if c.ID <= 0 || c.ID > 1<<31 || r.usedIDs[c.ID] {
		r.mu.Unlock()
		r.fail(CodeBadFrame, "")
		return true
	}
	r.usedIDs[c.ID] = true
	r.seq++
	seq := r.seq
	r.inflight++
	inflight := r.inflight
	r.mu.Unlock()
	r.timer.pause()

	tool := c.Tool
	var den *denial
	switch {
	case seq > MaxCalls:
		den = &denial{status: casestore.CallDenied, wire: mcp.WireInvalidArguments, msg: plain[CodeCallBudget], stop: CodeCallBudget}
	case inflight > MaxInFlight:
		den = &denial{status: casestore.CallDenied, wire: mcp.WireInvalidArguments, msg: "Too many calls at the same time.", stop: CodeBadFrame}
	case len(tool) <= maxToolNameBytes && slices.Contains(r.b.tools, tool) && !r.known(tool):
		// A real tool the grant's tier does not include. The run frame never named it, so a worker that calls it broke the protocol: the
		// call is recorded under its own name, as denied, and the run ends. Nothing reaches the host.
		den = &denial{status: casestore.CallDenied, wire: mcp.WireInvalidArguments, msg: "That tool is outside the tier of the grant.", stop: CodeBadFrame}
	case len(tool) > maxToolNameBytes || !r.known(tool):
		tool = "unknown"
		den = &denial{status: casestore.CallDenied, wire: mcp.WireInvalidArguments, msg: "Unknown tool.", stop: CodeBadFrame}
	}
	args, argsBad := cleanArgs(c.Args)
	if den == nil && argsBad != "" {
		den = &denial{status: casestore.CallDenied, wire: mcp.WireInvalidArguments, msg: argsBad}
	}
	r.wg.Add(1)
	go r.handleCall(c.ID, seq, tool, args, den)
	return den != nil && den.stop != ""
}

func (r *run) idsOf(tool string, answer []byte) privacy.IDs {
	if r.b.learn == nil {
		return privacy.IDs{}
	}
	return r.b.learn(tool, answer)
}

func (r *run) known(tool string) bool {
	return slices.Contains(r.tools, tool)
}

// cleanArgs returns the argument document to record and a reason when the arguments are not acceptable. The audit record keeps a short
// marker instead of arguments it cannot hold (too large, not canonical JSON).
func cleanArgs(raw json.RawMessage) ([]byte, string) {
	if len(raw) == 0 {
		return []byte("{}"), ""
	}
	if len(raw) > casestore.MaxCallArgs {
		return []byte(fmt.Sprintf(`{"argumentsOmitted":"too large","bytes":%d}`, len(raw))), "The arguments are larger than 64 KB."
	}
	if _, err := jcs.Canonicalize(raw); err != nil || !mcp.StrictJSON(raw) {
		return []byte(`{"argumentsOmitted":"not valid JSON"}`), "The arguments are not valid JSON."
	}
	return raw, ""
}

// handleCall runs one host call (or only records its denial) and answers the worker. The audit row is written before the answer is sent.
func (r *run) handleCall(id int64, seq int, tool string, args []byte, den *denial) {
	defer r.wg.Done()
	recorded := false
	defer func() {
		// A panic here (the Guard reading hostile text) ends this run, not the app; the call gets no answer. The call is on record
		// all the same: the tool may have run.
		if p := recover(); p != nil {
			r.fail(CodeInternal, "")
			if !recorded {
				r.recordPanicked(seq, tool, args)
			}
		}
	}()
	started := time.Now()
	var envelope []byte
	var isErr bool
	if den == nil {
		limit := MaxCallTime
		if dl, ok := r.ctx.Deadline(); ok {
			limit = min(limit, time.Until(dl))
		}
		callCtx, cancel := context.WithTimeout(r.callsCtx, max(limit, time.Millisecond))
		envelope, isErr = r.safeHostCall(callCtx, tool, args)
		cancel()
	}
	dur := time.Since(started).Milliseconds()

	r.serial.Lock()
	defer r.serial.Unlock()
	// The call stays "in flight" until its answer has been sent, so a worker cannot get past the limit by racing the answer.
	defer func() {
		r.mu.Lock()
		r.inflight--
		r.mu.Unlock()
	}()
	r.doneSeq++
	reply, status, stop := r.answer(tool, envelope, isErr, den)
	rec := casestore.CallRecord{Seq: seq, DoneSeq: r.doneSeq, Tool: tool, Args: args, Result: reply, Status: status, DurationMS: dur}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(r.parent), AuditWait)
	err := r.store.RecordCall(auditCtx, r.id, rec)
	recorded = true
	cancel()
	if err != nil {
		// Fail closed: a call that is not on record does not get its answer.
		r.fail(CodeAuditFailed, "")
		return
	}
	if stop != "" {
		r.fail(stop, "")
		return
	}
	if den != nil && den.stop != "" {
		r.fail(den.stop, "")
		return
	}
	r.mu.Lock()
	skip := r.cause != nil || r.over
	r.mu.Unlock()
	if skip {
		return
	}
	frame, err := wire.Marshal(wire.Ret{T: wire.TypeRet, ID: id, OK: true, Result: reply})
	if err != nil || wire.WriteFrame(r.worker.Stdin(), frame) != nil {
		r.fail(CodeWorkerCrashed, "")
		return
	}
	r.timer.resume()
}

// recordPanicked is the best-effort row of a call whose handler panicked before its row was written: a fixed error answer, nothing the
// panic touched. It runs after the handler's own defers, so the lock is free; a panic inside it is swallowed.
func (r *run) recordPanicked(seq int, tool string, args []byte) {
	defer func() { _ = recover() }()
	r.serial.Lock()
	defer r.serial.Unlock()
	r.doneSeq++
	rec := casestore.CallRecord{Seq: seq, DoneSeq: r.doneSeq, Tool: tool, Args: args, Status: casestore.CallError,
		Result: mcp.ErrorEnvelope(tool, mcp.WireInternal, "The call failed inside the broker.", "")}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.parent), AuditWait)
	defer cancel()
	_ = r.store.RecordCall(ctx, r.id, rec)
}

// safeHostCall runs the tool and turns a panic of the host into an internal error answer, never into a dead broker.
func (r *run) safeHostCall(ctx context.Context, tool string, args []byte) (body []byte, isErr bool) {
	defer func() {
		if p := recover(); p != nil {
			body, isErr = mcp.ErrorEnvelope(tool, mcp.WireInternal, "The tool failed.", ""), true
		}
	}()
	return r.host.Call(ctx, tool, args)
}

// answer builds what the script is told for a finished call and the status that is recorded. Called with serial held. stop is set when
// the Guard could not keep track of every identifier: the call is recorded with a fixed answer and the run ends.
func (r *run) answer(tool string, envelope []byte, isErr bool, den *denial) (reply []byte, status string, stop string) {
	fixed := func(code, msg string) []byte { return mcp.ErrorEnvelope(tool, code, msg, "") }
	if den != nil {
		return fixed(den.wire, den.msg), casestore.CallDenied, ""
	}
	if isErr {
		var e struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Action  string `json:"action"`
			} `json:"error"`
		}
		if json.Unmarshal(envelope, &e) != nil || e.Error == nil {
			return fixed(mcp.WireInternal, "The tool failed."), casestore.CallError, ""
		}
		msg := r.g("hosterror", r.guard.HostError(e.Error.Message, privacy.IDs{}))
		action := r.g("hosterror", r.guard.HostError(e.Error.Action, privacy.IDs{}))
		rebuilt := mcp.ErrorEnvelope(tool, e.Error.Code, msg, action)
		if r.guard.Incomplete() || (!r.allow && privacy.ContainsIP(string(rebuilt))) {
			return fixed(mcp.WireInternal, "The answer was withheld."), casestore.CallError, CodeIdentifierLimit
		}
		return rebuilt, casestore.CallError, ""
	}
	if len(envelope) > MaxResultBytes {
		r.truncated = true
		return fixed(mcp.WireResultTooLarge, plain[CodeResultTooLarge]), casestore.CallError, ""
	}
	swept, err := r.guard.HostResult(envelope, r.idsOf(tool, envelope))
	swept = r.gb("hostresult", swept)
	switch {
	case errors.Is(err, privacy.ErrIncomplete) || r.guard.Incomplete():
		return fixed(mcp.WireInternal, "The answer was withheld."), casestore.CallError, CodeIdentifierLimit
	case err != nil:
		return fixed(mcp.WireInternal, "The answer could not be passed on."), casestore.CallError, ""
	case len(swept) > MaxResultBytes:
		r.truncated = true
		return fixed(mcp.WireResultTooLarge, plain[CodeResultTooLarge]), casestore.CallError, ""
	case !r.allow && privacy.ContainsIP(string(swept)):
		return fixed(mcp.WireInternal, "The answer was withheld."), casestore.CallError, CodeIdentifierLimit
	}
	return swept, casestore.CallOK, ""
}

// safeHosts makes the run's host and turns a panic of the factory into an error.
func safeHosts(f HostFactory, scope HostScope) (h Host, err error) {
	defer func() {
		if p := recover(); p != nil {
			h, err = nil, errors.New("codemode: host factory failed")
		}
	}()
	h = f(scope)
	if h == nil {
		err = errors.New("codemode: no host")
	}
	return h, err
}
