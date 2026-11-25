package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/codemode/wire"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/server"
)

// The tests give the broker a scripted worker: a goroutine that speaks the wire protocol and runs no JavaScript. Everything else is
// the real thing: the real read-only tools through the real dashboard handler on the demo repository, and a real sealed SQLite case.

const testToken = "test-session-token"

var demoNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// fakeWorker is one scripted worker process.
type fakeWorker struct {
	// broker side of the pipes
	stdinW  *io.PipeWriter // broker writes here
	stdoutR *io.PipeReader // broker reads here
	// worker side
	inR  *io.PipeReader
	outW *io.PipeWriter

	killed chan struct{}
	once   sync.Once
	mode   string
	sha    string

	mu       sync.Mutex
	pending  map[int64]chan wire.Ret
	nextID   int64
	runMsg   wire.Run
	wmu      sync.Mutex // serialises the worker's writes
	runReady chan struct{}
}

func (w *fakeWorker) Stdin() io.WriteCloser { return w.stdinW }
func (w *fakeWorker) Stdout() io.ReadCloser { return w.stdoutR }
func (w *fakeWorker) Mode() string          { return w.mode }
func (w *fakeWorker) SHA256() string        { return w.sha }
func (w *fakeWorker) Kill() {
	w.once.Do(func() {
		close(w.killed)
		_ = w.inR.CloseWithError(io.ErrClosedPipe)
		_ = w.outW.CloseWithError(io.EOF)
		_ = w.stdinW.CloseWithError(io.ErrClosedPipe)
		_ = w.stdoutR.CloseWithError(io.ErrClosedPipe)
	})
}
func (w *fakeWorker) Wait() error { <-w.killed; return nil }
func (w *fakeWorker) isKilled() bool {
	select {
	case <-w.killed:
		return true
	default:
		return false
	}
}

// send writes one frame as the worker.
func (w *fakeWorker) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.sendRaw(b)
}

func (w *fakeWorker) sendRaw(b []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return wire.WriteFrame(w.outW, b)
}

// sendBytes writes bytes with no framing at all (a hostile length prefix, say).
func (w *fakeWorker) sendBytes(b []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_, err := w.outW.Write(b)
	return err
}

// Call makes one host call and waits for the answer.
func (w *fakeWorker) Call(tool string, args any) (json.RawMessage, error) {
	ch, err := w.CallAsync(tool, args)
	if err != nil {
		return nil, err
	}
	ret, ok := <-ch
	if !ok {
		return nil, io.ErrClosedPipe
	}
	if !ret.OK {
		return nil, fmt.Errorf("ret error %v", ret.Error)
	}
	return ret.Result, nil
}

// CallAsync sends a call frame and returns the channel the answer will arrive on (closed when the worker is killed first).
func (w *fakeWorker) CallAsync(tool string, args any) (<-chan wire.Ret, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return w.callRaw(tool, raw)
}

func (w *fakeWorker) callRaw(tool string, args json.RawMessage) (<-chan wire.Ret, error) {
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	ch := make(chan wire.Ret, 1)
	w.pending[id] = ch
	w.mu.Unlock()
	return ch, w.send(wire.Call{T: wire.TypeCall, ID: id, Tool: tool, Args: args})
}

// pump reads the broker's frames: the run frame first, then ret frames.
func (w *fakeWorker) pump() {
	defer func() {
		w.mu.Lock()
		for id, ch := range w.pending {
			close(ch)
			delete(w.pending, id)
		}
		w.mu.Unlock()
	}()
	first := true
	for {
		data, err := wire.ReadFrame(w.inR, wire.MaxFrame)
		if err != nil {
			return
		}
		if first {
			first = false
			var r wire.Run
			if wire.Decode(data, &r) == nil {
				w.mu.Lock()
				w.runMsg = r
				w.mu.Unlock()
				w.runReady <- struct{}{}
			}
			continue
		}
		var ret wire.Ret
		if wire.Decode(data, &ret) != nil {
			continue
		}
		w.mu.Lock()
		ch := w.pending[ret.ID]
		delete(w.pending, ret.ID)
		w.mu.Unlock()
		if ch != nil {
			ch <- ret
		}
	}
}

// fakeScript is what a test says the worker does after the run frame arrived.
type fakeScript func(w *fakeWorker)

type fakeSpawner struct {
	t           *testing.T
	script      fakeScript
	mode        string
	startErr    error
	unavailable error
	noHello     bool
	hello       func(*wire.Hello)
	rawHello    []byte
	sha         string

	mu      sync.Mutex
	workers []*fakeWorker
	opts    []SpawnOptions
}

func (s *fakeSpawner) Available() error { return s.unavailable }

func (s *fakeSpawner) Start(ctx context.Context, opts SpawnOptions) (Worker, error) {
	s.mu.Lock()
	s.opts = append(s.opts, opts)
	s.mu.Unlock()
	if s.startErr != nil {
		return nil, s.startErr
	}
	inR, stdinW := io.Pipe()
	stdoutR, outW := io.Pipe()
	mode := s.mode
	if mode == "" {
		mode = ModeLPAC
	}
	w := &fakeWorker{stdinW: stdinW, stdoutR: stdoutR, inR: inR, outW: outW, killed: make(chan struct{}), mode: mode,
		sha: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", pending: map[int64]chan wire.Ret{}, runReady: make(chan struct{}, 1)}
	if s.sha != "" {
		w.sha = s.sha
	}
	s.mu.Lock()
	s.workers = append(s.workers, w)
	s.mu.Unlock()
	go func() {
		go w.pump()
		if !s.noHello {
			h := wire.Hello{T: wire.TypeHello, V: wire.Version, Runtime: "wazero-interp", Mitigations: map[string]bool{"win32kOff": true, "childRestricted": true}}
			if s.hello != nil {
				s.hello(&h)
			}
			if s.rawHello != nil {
				_ = w.sendRaw(s.rawHello)
			} else if w.send(h) != nil {
				return
			}
		}
		select {
		case <-w.runReady:
		case <-w.killed:
			return
		}
		if s.script != nil {
			s.script(w)
		}
	}()
	return w, nil
}

func (s *fakeSpawner) worker(i int) *fakeWorker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workers[i]
}

func (s *fakeSpawner) started() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workers)
}

// finish helpers a script ends with.
func (w *fakeWorker) done(result any) {
	raw, _ := json.Marshal(result)
	_ = w.send(wire.Done{T: wire.TypeDone, OK: true, Result: raw})
	w.exit()
}

func (w *fakeWorker) doneLogs(result any, logs ...string) {
	raw, _ := json.Marshal(result)
	_ = w.send(wire.Done{T: wire.TypeDone, OK: true, Result: raw, Logs: logs})
	w.exit()
}

func (w *fakeWorker) doneErr(code, msg string) {
	_ = w.send(wire.Done{T: wire.TypeDone, OK: false, Error: &wire.DoneError{Code: code, Message: msg}})
	w.exit()
}

// exit is the worker process ending after its last frame: nothing reads the broker's pipe any more.
func (w *fakeWorker) exit() { _ = w.inR.CloseWithError(io.ErrClosedPipe) }

// env is a broker over a real sealed case and the demo dashboard.
type env struct {
	t       *testing.T
	b       *Broker
	sp      *fakeSpawner
	store   *sqlite.Store
	cas     *sqlite.Case
	caseID  string
	handler http.Handler
	set     Settings
	cfg     *Config
	// inner is the case the resolver hands out; tests may wrap it.
	inner    casestore.CodeRunStore
	hosts    HostFactory
	operator bool
	// admit is the call admitter of scoped runs (Config.Admit); nil admits every call.
	admit CallAdmitter
}

type envOpt func(*env)

func newEnv(t *testing.T, script fakeScript, opts ...envOpt) *env {
	t.Helper()
	root := t.TempDir() + "/cases"
	s, err := sqlite.New(sqlite.Options{Root: root, Protector: storetest.RandomProtector(), MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Create(context.Background(), "Brain Sandbox Slowdown (test)")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	cas := c.(*sqlite.Case)
	handler := server.New(audit.NewDemo(demoNow), nil, testToken, true)
	e := &env{t: t, sp: &fakeSpawner{t: t, script: script}, store: s, cas: cas, caseID: string(info.ID), handler: handler,
		set: Settings{Enabled: true, KeepResults: true}}
	e.inner = cas
	for _, o := range opts {
		o(e)
	}
	cfg := Config{
		Spawner: e.sp,
		Hosts: func(scope HostScope) Host {
			if e.hosts != nil {
				return e.hosts(scope)
			}
			return DashboardHosts(handler, testToken)(scope)
		},
		Admit: func(ctx context.Context, pr mcpsrv.Principal, tool string, args []byte) (func([]byte, bool), *mcpsrv.CodeCallRefusal) {
			if e.admit != nil {
				return e.admit(ctx, pr, tool, args)
			}
			return func([]byte, bool) {}, nil
		},
		Settings:            func() Settings { return e.set },
		AllowIdentifiers:    e.operator,
		RequiredMitigations: []string{"win32kOff", "childRestricted"},
		Cases: func(ctx context.Context, id string) (casestore.CodeRunStore, string, error) {
			if id != "" && id != e.caseID {
				return nil, "", ErrNoCase
			}
			return e.inner, e.caseID, nil
		},
	}
	e.cfg = &cfg
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.b = b
	b.helloWait = 300 * time.Millisecond
	return e
}

func (e *env) run(code string) RunResult {
	e.t.Helper()
	res, err := e.b.Run(context.Background(), RunRequest{Code: code, Surface: "cli", CallerID: "test", Reason: "unit test"})
	if err != nil {
		e.t.Fatalf("Run: %v", err)
	}
	return res
}

func (e *env) runReq(req RunRequest) (RunResult, error) {
	if req.Surface == "" {
		req.Surface = "cli"
	}
	return e.b.Run(context.Background(), withGrant(req))
}

// withGrant fills the profile and the tool list a scoped run carries (the whole tier) when the test did not set them.
func withGrant(req RunRequest) RunRequest {
	if req.Scoped {
		if req.ProfileID == "" {
			req.ProfileID = "p1"
		}
		if req.Tools == nil {
			req.Tools = mcpsrv.ToolsForTier(req.Tier)
		}
	}
	return req
}

func (e *env) runs() []casestore.CodeRun {
	e.t.Helper()
	runs, err := e.cas.ListRuns(context.Background(), casestore.RunQuery{Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	return runs
}

func (e *env) calls(runID string) []casestore.CodeCall {
	e.t.Helper()
	calls, err := e.cas.RunCalls(context.Background(), runID)
	if err != nil {
		e.t.Fatal(err)
	}
	return calls
}

func (e *env) getRun(runID string) casestore.CodeRun {
	e.t.Helper()
	r, err := e.cas.GetRun(context.Background(), runID)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// verify checks the chain of the case and fails the test when it is not intact.
func (e *env) verify() casestore.ChainReport {
	e.t.Helper()
	rep, err := e.cas.VerifyCodeChain(context.Background(), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if !rep.OK || len(rep.Problems) > 0 {
		e.t.Fatalf("the audit chain does not verify: %+v", rep.Problems)
	}
	return rep
}

func (e *env) wantStatus(res RunResult, status, code string) {
	e.t.Helper()
	got := ""
	if res.Error != nil {
		got = res.Error.Code
	}
	if res.Status != status || got != code {
		e.t.Fatalf("status %q code %q, want %q %q (error %+v)", res.Status, got, status, code, res.Error)
	}
	row := e.getRun(res.RunID)
	if row.Status != status || row.ErrorCode != code {
		e.t.Fatalf("stored row says %q %q, want %q %q", row.Status, row.ErrorCode, status, code)
	}
}

// demoQuery is the comparison window of the demo repository (the one the MCP tests use).
func demoQuery() map[string]any {
	return map[string]any{
		"sourceIds": []string{"demo-west", "demo-east"},
		"start":     demoNow.Add(-15 * time.Minute).Format(time.RFC3339), "end": demoNow.Format(time.RFC3339),
		"baselineStart": demoNow.Add(-30 * time.Minute).Format(time.RFC3339), "baselineEnd": demoNow.Add(-15 * time.Minute).Format(time.RFC3339),
		"kind": "all", "outcome": "all", "metric": "total", "bucketSeconds": 60,
	}
}

// goroutines counts live goroutines after a short settle, to catch leaks of a run.
func settleGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n := runtime.NumGoroutine(); n <= before+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines: %d before the run, %d after", before, runtime.NumGoroutine())
}

var _ = errors.New
var _ atomic.Int32

type hostFunc func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool)

func (f hostFunc) Call(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
	return f(ctx, tool, args)
}

// withHost replaces the tool host (for answers the demo repository cannot give: a huge answer, a hostile text, a stall).
func withHost(fn func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool)) envOpt {
	return func(e *env) { e.hosts = func(HostScope) Host { return hostFunc(fn) } }
}

// withOperatorIdentifiers is the operator's switch (--mcp-allow-identifiers).
func withOperatorIdentifiers(e *env) { e.operator = true }
