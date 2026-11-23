package codemode

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/privacy"
	"api-audit-explorer/internal/codemode/wire"
	"api-audit-explorer/internal/mcp"
)

// bigJSON is a JSON object of exactly n bytes.
func bigJSON(n int) []byte {
	head, tail := `{"x":"`, `"}`
	return []byte(head + strings.Repeat("a", n-len(head)-len(tail)) + tail)
}

func TestResultLimits(t *testing.T) {
	t.Run("final result at the limit and one over", func(t *testing.T) {
		at := newEnv(t, func(w *fakeWorker) { w.done(strings.Repeat("a", MaxResultBytes-2)) })
		res := at.run(`1`)
		at.wantStatus(res, casestore.RunOK, "")
		if len(res.Result) != MaxResultBytes {
			t.Errorf("result %d bytes", len(res.Result))
		}
		over := newEnv(t, func(w *fakeWorker) { w.done(strings.Repeat("a", MaxResultBytes-1)) })
		res = over.run(`1`)
		over.wantStatus(res, casestore.RunLimitExceeded, CodeOutputTooLarge)
		if string(res.Result) != "null" {
			t.Error("an over-size result was returned")
		}
	})
	t.Run("a call result at the limit and one over", func(t *testing.T) {
		var gotAt, gotOver json.RawMessage
		e := newEnv(t, func(w *fakeWorker) {
			gotAt, _ = w.Call("audit_get_guide", map[string]any{})
			gotOver, _ = w.Call("audit_get_slowest", map[string]any{})
			w.done(1)
		}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
			if tool == "audit_get_guide" {
				return bigJSON(MaxResultBytes), false
			}
			return bigJSON(MaxResultBytes + 1), false
		}))
		res := e.run(`1`)
		e.wantStatus(res, casestore.RunOK, "")
		if len(gotAt) != MaxResultBytes {
			t.Errorf("the script got %d bytes for an answer at the limit", len(gotAt))
		}
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		if json.Unmarshal(gotOver, &env) != nil || env.Error.Code != mcp.WireResultTooLarge {
			t.Errorf("over the limit: %.200s", gotOver)
		}
		if !res.Stats.Truncated {
			t.Error("stats do not say an answer was too large")
		}
		rows := e.calls(res.RunID)
		if rows[0].Status != casestore.CallOK || rows[1].Status != casestore.CallError {
			t.Errorf("rows %+v", rows)
		}
		e.verify()
	})
}

func TestLogCapAndLineCap(t *testing.T) {
	line := strings.Repeat("x", 1000)
	var logs []string
	for i := 0; i < 100; i++ {
		logs = append(logs, line)
	}
	e := newEnv(t, func(w *fakeWorker) { w.doneLogs(1, logs...) })
	res := e.run(`1`)
	e.wantStatus(res, casestore.RunOK, "")
	total := 0
	for _, l := range res.Logs {
		total += len(l) + 1
	}
	if total > MaxLogBytes || len(res.Logs) == 0 || len(res.Logs) >= 100 || !res.Stats.LogsTruncated {
		t.Errorf("%d lines, %d bytes, truncated %v", len(res.Logs), total, res.Stats.LogsTruncated)
	}
	many := make([]string, 5000)
	e2 := newEnv(t, func(w *fakeWorker) { w.doneLogs(1, many...) })
	res = e2.run(`1`)
	if len(res.Logs) > maxLogLines || !res.Stats.LogsTruncated {
		t.Errorf("%d lines", len(res.Logs))
	}
}

func TestWallTimeCountsTheScriptNotTheWaitForTools(t *testing.T) {
	t.Run("a script that does not end", func(t *testing.T) {
		e := newEnv(t, func(w *fakeWorker) { <-w.killed })
		e.b.minTime = 10 * time.Millisecond
		res, err := e.runReq(RunRequest{Code: "for(;;){}", TimeoutMS: 100})
		if err != nil {
			t.Fatal(err)
		}
		e.wantStatus(res, casestore.RunTimeout, CodeWall)
		if !e.sp.worker(0).isKilled() {
			t.Error("the worker was not killed")
		}
		e.verify()
	})
	t.Run("a slow tool is not the script's time", func(t *testing.T) {
		e := newEnv(t, func(w *fakeWorker) {
			if _, err := w.Call("audit_compare", map[string]any{}); err != nil {
				t.Error(err)
			}
			w.done("fine")
		}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
			}
			return mustJSON(map[string]any{"schemaVersion": 1, "tool": tool}), false
		}))
		e.b.minTime = 10 * time.Millisecond
		res, err := e.runReq(RunRequest{Code: "1", TimeoutMS: 100})
		if err != nil {
			t.Fatal(err)
		}
		e.wantStatus(res, casestore.RunOK, "")
		if res.Stats.ScriptMS >= 200 || res.Stats.DurationMS < 500 {
			t.Errorf("script %d ms of %d ms in all", res.Stats.ScriptMS, res.Stats.DurationMS)
		}
	})
	t.Run("the clamps", func(t *testing.T) {
		b := &Broker{minTime: MinTimeout}
		for in, want := range map[int]time.Duration{0: DefaultTimeout, -5: DefaultTimeout, 10: MinTimeout, 5000: 5 * time.Second, 120000: MaxTimeout, 999999999: MaxTimeout} {
			if got := b.timeout(in); got != want {
				t.Errorf("timeout(%d) = %v, want %v", in, got, want)
			}
		}
	})
}

func TestRunCapEndsAStuckToolCall(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) { _, _ = w.Call("audit_compare", map[string]any{}) },
		withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
			<-ctx.Done()
			return mcp.ErrorEnvelope(tool, mcp.WireCancelled, "stopped", ""), true
		}))
	e.b.runCap = 300 * time.Millisecond
	res := e.run(`await unravis.audit_compare({})`)
	e.wantStatus(res, casestore.RunTimeout, CodeRunCap)
	rows := e.calls(res.RunID)
	if len(rows) != 1 || rows[0].Status != casestore.CallError {
		t.Errorf("the abandoned call must be on record: %+v", rows)
	}
	if !e.sp.worker(0).isKilled() {
		t.Error("worker alive")
	}
	e.verify()
}

func TestCancelKillsTheWorkerAndSealsTheRun(t *testing.T) {
	t.Run("Broker.Cancel", func(t *testing.T) {
		e := newEnv(t, func(w *fakeWorker) { <-w.killed })
		type out struct {
			res RunResult
			err error
		}
		ch := make(chan out, 1)
		go func() {
			res, err := e.runReq(RunRequest{Code: "for(;;){}"})
			ch <- out{res, err}
		}()
		var id string
		eventually(t, func() bool {
			runs := e.runs()
			if len(runs) == 1 {
				id = runs[0].RunID
			}
			return id != "" && e.sp.started() == 1
		})
		var ok bool
		eventually(t, func() bool { ok = e.b.Cancel(id); return ok })
		o := <-ch
		if o.err != nil {
			t.Fatal(o.err)
		}
		e.wantStatus(o.res, casestore.RunCancelled, CodeCancelled)
		if !e.sp.worker(0).isKilled() {
			t.Error("not killed")
		}
		if e.b.Cancel(id) {
			t.Error("a finished run can be cancelled")
		}
		e.verify()
	})
	t.Run("the caller's context", func(t *testing.T) {
		e := newEnv(t, func(w *fakeWorker) { <-w.killed })
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			eventually(t, func() bool { return e.sp.started() == 1 })
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		res, err := e.b.Run(ctx, RunRequest{Code: "for(;;){}", Surface: "mcp"})
		if err != nil {
			t.Fatal(err)
		}
		// The end record is written although the caller's context is gone.
		e.wantStatus(res, casestore.RunCancelled, CodeCancelled)
		e.verify()
	})
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestWorkerCrashIsRecorded(t *testing.T) {
	t.Run("pipe closes", func(t *testing.T) {
		e := newEnv(t, func(w *fakeWorker) { _ = w.outW.Close() })
		res := e.run(`1`)
		e.wantStatus(res, casestore.RunCrashed, CodeWorkerCrashed)
		e.verify()
	})
	t.Run("dies while a call is running", func(t *testing.T) {
		started := make(chan struct{})
		e := newEnv(t, func(w *fakeWorker) {
			_, _ = w.CallAsync("audit_compare", map[string]any{})
			<-started
			_ = w.outW.Close()
		}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
			close(started)
			<-ctx.Done()
			return mcp.ErrorEnvelope(tool, mcp.WireCancelled, "stopped", ""), true
		}))
		res := e.run(`1`)
		e.wantStatus(res, casestore.RunCrashed, CodeWorkerCrashed)
		if rows := e.calls(res.RunID); len(rows) != 1 || rows[0].Status != casestore.CallError {
			t.Errorf("rows %+v", rows)
		}
		e.verify()
	})
}

func TestHostileFramesEndTheRun(t *testing.T) {
	le := func(n uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, n); return b }
	cases := map[string]func(w *fakeWorker){
		"length over the maximum": func(w *fakeWorker) { _ = w.sendBytes(le(wire.MaxFrame + 1)) },
		"length zero":             func(w *fakeWorker) { _ = w.sendBytes(le(0)) },
		"not json":                func(w *fakeWorker) { _ = w.sendRaw([]byte("not json")) },
		"array":                   func(w *fakeWorker) { _ = w.sendRaw([]byte(`[1]`)) },
		"unknown type":            func(w *fakeWorker) { _ = w.sendRaw([]byte(`{"t":"boom"}`)) },
		"second hello":            func(w *fakeWorker) { _ = w.send(wire.Hello{T: wire.TypeHello, V: 1, Runtime: "x"}) },
		"ret from the worker":     func(w *fakeWorker) { _ = w.send(wire.Ret{T: wire.TypeRet, ID: 1, OK: true}) },
		"run from the worker":     func(w *fakeWorker) { _ = w.send(wire.Run{T: wire.TypeRun}) },
		"call id zero": func(w *fakeWorker) {
			_ = w.send(wire.Call{T: wire.TypeCall, ID: 0, Tool: "audit_get_guide", Args: json.RawMessage(`{}`)})
		},
		"negative call id": func(w *fakeWorker) {
			_ = w.send(wire.Call{T: wire.TypeCall, ID: -4, Tool: "audit_get_guide", Args: json.RawMessage(`{}`)})
		},
		"unknown field":             func(w *fakeWorker) { _ = w.sendRaw([]byte(`{"t":"done","ok":true,"extra":1}`)) },
		"repeated field":            func(w *fakeWorker) { _ = w.sendRaw([]byte(`{"t":"done","ok":true,"t":"call"}`)) },
		"repeated field other case": func(w *fakeWorker) { _ = w.sendRaw([]byte(`{"t":"done","ok":true,"OK":false}`)) },
		"text after the object":     func(w *fakeWorker) { _ = w.sendRaw([]byte(`{"t":"done","ok":true} {"t":"done"}`)) },
		"invalid utf8":              func(w *fakeWorker) { _ = w.sendRaw([]byte("{\"t\":\"done\",\"ok\":true,\"result\":\"\xff\"}")) },
		"ok with an error": func(w *fakeWorker) {
			_ = w.sendRaw([]byte(`{"t":"done","ok":true,"error":{"code":"script_error","message":"x"}}`))
		},
		"failed with a result": func(w *fakeWorker) {
			_ = w.sendRaw([]byte(`{"t":"done","ok":false,"result":1,"error":{"code":"script_error","message":"x"}}`))
		},
		"failed with no error": func(w *fakeWorker) { _ = w.sendRaw([]byte(`{"t":"done","ok":false}`)) },
		"duplicate call id": func(w *fakeWorker) {
			_ = w.send(wire.Call{T: wire.TypeCall, ID: 7, Tool: "audit_get_guide", Args: json.RawMessage(`{}`)})
			_ = w.send(wire.Call{T: wire.TypeCall, ID: 7, Tool: "audit_get_guide", Args: json.RawMessage(`{}`)})
		},
		"huge call id": func(w *fakeWorker) {
			_ = w.send(wire.Call{T: wire.TypeCall, ID: 1 << 40, Tool: "audit_get_guide", Args: json.RawMessage(`{}`)})
		},
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, func(w *fakeWorker) { script(w); <-w.killed })
			res := e.run(`1`)
			e.wantStatus(res, casestore.RunError, CodeBadFrame)
			if !e.sp.worker(0).isKilled() {
				t.Error("worker alive")
			}
			e.verify()
		})
	}
}

func TestUnknownToolIsRecordedAsDeniedAndEndsTheRun(t *testing.T) {
	for _, tool := range []string{"os_exec", "audit_compare ", strings.Repeat("t", 5000), "audit_get_guide\x00", ""} {
		e := newEnv(t, func(w *fakeWorker) {
			ch, err := w.CallAsync(tool, map[string]any{})
			if err == nil {
				<-ch
			}
		})
		res := e.run(`1`)
		e.wantStatus(res, casestore.RunError, CodeBadFrame)
		rows := e.calls(res.RunID)
		if len(rows) != 1 || rows[0].Status != casestore.CallDenied || rows[0].Tool != "unknown" {
			t.Errorf("tool %.20q: %+v", tool, rows)
		}
		e.verify()
	}
}

func TestBadArgumentsAreDeniedAndTheRunGoesOn(t *testing.T) {
	for name, raw := range map[string]string{
		"repeated key":   `{"a":1,"a":2}`,
		"key in 2 cases": `{"limit":1,"Limit":2}`,
		"too large":      `{"x":"` + strings.Repeat("a", casestore.MaxCallArgs) + `"}`,
		"lone surrogate": `{"x":"\ud800"}`,
		"deep":           strings.Repeat(`{"a":`, 70) + `1` + strings.Repeat(`}`, 70),
	} {
		t.Run(name, func(t *testing.T) {
			var got json.RawMessage
			e := newEnv(t, func(w *fakeWorker) {
				ch, err := w.callRaw("audit_get_guide", json.RawMessage(raw))
				if err != nil {
					return
				}
				if ret, ok := <-ch; ok {
					got = ret.Result
				}
				w.done("went on")
			})
			res := e.run(`1`)
			e.wantStatus(res, casestore.RunOK, "")
			var env struct {
				Error struct{ Code string } `json:"error"`
			}
			if json.Unmarshal(got, &env) != nil || env.Error.Code != mcp.WireInvalidArguments {
				t.Errorf("answer %s", got)
			}
			rows := e.calls(res.RunID)
			if len(rows) != 1 || rows[0].Status != casestore.CallDenied || !strings.Contains(rows[0].ArgsJSON, "argumentsOmitted") {
				t.Errorf("rows %+v", rows)
			}
			e.verify()
		})
	}
}

func TestHelloIsChecked(t *testing.T) {
	cases := map[string]func(*fakeSpawner){
		"no hello":           func(s *fakeSpawner) { s.noHello = true },
		"missing mitigation": func(s *fakeSpawner) { s.hello = func(h *wire.Hello) { h.Mitigations["win32kOff"] = false } },
		"no mitigations":     func(s *fakeSpawner) { s.hello = func(h *wire.Hello) { h.Mitigations = nil } },
		"wrong version":      func(s *fakeSpawner) { s.hello = func(h *wire.Hello) { h.V = 2 } },
		"odd runtime":        func(s *fakeSpawner) { s.hello = func(h *wire.Hello) { h.Runtime = "a b\n" } },
		"empty runtime":      func(s *fakeSpawner) { s.hello = func(h *wire.Hello) { h.Runtime = "" } },
		"call before hello":  func(s *fakeSpawner) { s.rawHello = []byte(`{"t":"call","id":1,"tool":"audit_get_guide","args":{}}`) },
		"hello with unknown key": func(s *fakeSpawner) {
			s.rawHello = []byte(`{"t":"hello","v":1,"runtime":"x","mitigations":{},"extra":1}`)
		},
		"spawn fails":         func(s *fakeSpawner) { s.startErr = errors.New("no") },
		"unknown mode":        func(s *fakeSpawner) { s.mode = "none" },
		"worker hash not hex": func(s *fakeSpawner) { s.sha = "xyz" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, func(w *fakeWorker) { t.Error("the script ran after a bad hello") })
			mut(e.sp)
			res := e.run(`1`)
			e.wantStatus(res, casestore.RunError, CodeSandboxUnavailable)
			e.verify()
		})
	}
}

func TestDegradedModeNeedsTheSetting(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) { t.Error("ran in a degraded sandbox without permission") })
	e.sp.mode = ModeFallback
	res := e.run(`1`)
	e.wantStatus(res, casestore.RunError, CodeSandboxDegradedNope)

	e2 := newEnv(t, func(w *fakeWorker) { w.done(1) })
	e2.sp.mode = ModeFallback
	e2.set.AllowDegraded = true
	res = e2.run(`1`)
	e2.wantStatus(res, casestore.RunOK, "")
	if res.Sandbox.Mode != ModeFallback || e2.getRun(res.RunID).SandboxMode != ModeFallback {
		t.Errorf("the degraded mode is not on record: %+v", res.Sandbox)
	}
	if !e2.sp.opts[0].AllowDegraded {
		t.Error("the spawner was not told the setting")
	}

	e3 := newEnv(t, nil)
	e3.sp.startErr = ErrDegradedRefused
	e3.wantStatus(e3.run(`1`), casestore.RunError, CodeSandboxDegradedNope)
}

// A failed audit write is a failed run: the call that could not be recorded is not answered.
type flakyStore struct {
	casestore.CodeRunStore
	mu          sync.Mutex
	failRecord  error
	failEnd     error
	failBegin   error
	orphanCalls int
}

func (f *flakyStore) BeginRun(ctx context.Context, in casestore.RunStart) (casestore.CodeRun, error) {
	if f.failBegin != nil {
		return casestore.CodeRun{}, f.failBegin
	}
	return f.CodeRunStore.BeginRun(ctx, in)
}

func (f *flakyStore) RecordCall(ctx context.Context, id string, rec casestore.CallRecord) error {
	if f.failRecord != nil {
		return f.failRecord
	}
	return f.CodeRunStore.RecordCall(ctx, id, rec)
}

func (f *flakyStore) EndRun(ctx context.Context, id string, end casestore.RunEnd) (casestore.CodeRun, error) {
	if f.failEnd != nil {
		return casestore.CodeRun{}, f.failEnd
	}
	return f.CodeRunStore.EndRun(ctx, id, end)
}

func (f *flakyStore) CloseOrphanRuns(ctx context.Context) (int, error) {
	f.mu.Lock()
	f.orphanCalls++
	f.mu.Unlock()
	return f.CodeRunStore.CloseOrphanRuns(ctx)
}

func withFlaky(f *flakyStore) envOpt {
	return func(e *env) { f.CodeRunStore = e.inner; e.inner = f }
}

func TestAuditFailureIsFailClosed(t *testing.T) {
	t.Run("a call that cannot be recorded gets no answer", func(t *testing.T) {
		f := &flakyStore{failRecord: errors.New("disk full")}
		var answered bool
		e := newEnv(t, func(w *fakeWorker) {
			_, err := w.Call("audit_get_guide", map[string]any{})
			answered = err == nil
		}, withFlaky(f))
		res := e.run(`1`)
		e.wantStatus(res, casestore.RunError, CodeAuditFailed)
		if answered {
			t.Error("the script got an answer for a call that is not on record")
		}
		if !e.sp.worker(0).isKilled() {
			t.Error("worker alive")
		}
	})
	t.Run("begin refused", func(t *testing.T) {
		for err, code := range map[error]string{casestore.ErrImportRunning: CodeBusy, casestore.ErrCaseClosed: CodeNoCase, errors.New("x"): CodeAuditFailed} {
			f := &flakyStore{failBegin: err}
			e := newEnv(t, func(w *fakeWorker) { t.Error("a run started without a begin record") }, withFlaky(f))
			_, got := e.runReq(RunRequest{Code: "1"})
			if ref, ok := got.(*Refusal); !ok || ref.Code != code {
				t.Errorf("%v: %v", err, got)
			}
			if e.sp.started() != 0 {
				t.Error("worker started")
			}
		}
	})
	t.Run("end cannot be sealed", func(t *testing.T) {
		f := &flakyStore{failEnd: errors.New("disk full")}
		e := newEnv(t, func(w *fakeWorker) { w.done(1) }, withFlaky(f))
		res, err := e.runReq(RunRequest{Code: "1"})
		if err == nil || res.Status != casestore.RunError || res.Error == nil || res.Error.Code != CodeAuditFailed || string(res.Result) != "null" {
			t.Errorf("%v %+v", err, res)
		}
	})
}

// BL-369: nothing called CloseOrphanRuns. The broker does, once per case before the first run, and on demand at case open.
func TestOrphanRunsAreClosedAtCaseOpen(t *testing.T) {
	f := &flakyStore{}
	e := newEnv(t, func(w *fakeWorker) { w.done(1) }, withFlaky(f))
	e.run(`1`)
	e.run(`1`)
	if f.orphanCalls != 1 {
		t.Errorf("CloseOrphanRuns called %d times for two runs", f.orphanCalls)
	}
	if _, err := e.b.OnCaseOpen(context.Background(), f); err != nil || f.orphanCalls != 2 {
		t.Errorf("OnCaseOpen: %v, %d calls", err, f.orphanCalls)
	}
}

func TestOrphanSweepRetriesAfterAnImportRefusal(t *testing.T) {
	f := &flakyStore{}
	calls := 0
	e := newEnv(t, func(w *fakeWorker) { w.done(1) }, func(e *env) { f.CodeRunStore = e.inner; e.inner = &orphanFail{flakyStore: f, n: &calls} })
	e.run(`1`)
	e.run(`1`)
	e.run(`1`)
	if calls != 2 { // refused once, then done, then not again
		t.Errorf("sweep attempts: %d", calls)
	}
}

type orphanFail struct {
	*flakyStore
	n *int
}

func (o *orphanFail) CloseOrphanRuns(ctx context.Context) (int, error) {
	*o.n++
	if *o.n == 1 {
		return 0, casestore.ErrImportRunning
	}
	return 0, nil
}

func TestTwoRunsAtOnceThenBusy(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, func(w *fakeWorker) {
		select {
		case <-release:
			w.done(1)
		case <-w.killed:
		}
	})
	var wg sync.WaitGroup
	results := make([]RunResult, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = e.runReq(RunRequest{Code: "1"})
		}()
	}
	eventually(t, func() bool { return e.sp.started() == 2 })
	_, err := e.runReq(RunRequest{Code: "1"})
	if ref, ok := err.(*Refusal); !ok || ref.Code != CodeBusy {
		t.Errorf("third run: %v", err)
	}
	if n := len(e.runs()); n != 2 {
		t.Errorf("%d runs on record, want 2", n)
	}
	close(release)
	wg.Wait()
	for _, r := range results {
		if r.Status != casestore.RunOK {
			t.Errorf("%+v", r)
		}
	}
	// The slot is free again.
	e.sp.script = func(w *fakeWorker) { w.done(1) }
	e.wantStatus(e.run(`1`), casestore.RunOK, "")
	e.verify()
}

func TestIdentifierLimitEndsTheRun(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) {
		_, _ = w.Call("audit_get_guide", map[string]any{})
		w.done(1)
	})
	e.b.learn = func(tool string, answer []byte) privacy.IDs {
		var ids privacy.IDs
		for i := 0; i < 20000; i++ { // more than the Guard keeps
			ids.Users = append(ids.Users, "user-"+strconv.Itoa(i)+"-padding")
		}
		return ids
	}
	res := e.run(`1`)
	e.wantStatus(res, casestore.RunLimitExceeded, CodeIdentifierLimit)
	rows := e.calls(res.RunID)
	if len(rows) != 1 || rows[0].Status != casestore.CallError {
		t.Errorf("rows %+v", rows)
	}
	e.verify()
}

func TestNoGoroutineOrWorkerLeakAfterRuns(t *testing.T) {
	for i, script := range []fakeScript{
		func(w *fakeWorker) { w.done(1) },
		func(w *fakeWorker) { _ = w.outW.Close() },
		func(w *fakeWorker) { _ = w.sendRaw([]byte("junk")) },
		func(w *fakeWorker) {
			_, _ = w.Call("audit_get_guide", map[string]any{})
			w.doneErr(CodeScriptError, "x")
		},
		func(w *fakeWorker) { <-w.killed },
	} {
		e := newEnv(t, script)
		e.b.minTime = 10 * time.Millisecond
		before := runtime.NumGoroutine()
		_, _ = e.runReq(RunRequest{Code: "1", TimeoutMS: 100})
		if !e.sp.worker(0).isKilled() {
			t.Errorf("script %d: worker was not reaped", i)
		}
		settleGoroutines(t, before)
	}
}

// The broker has no way to start a process or reach the machine on its own: the only thing that can run a script is a Spawner it is
// given, and the production Spawner is the confined one of BL-366 and BL-367.
func TestBrokerPackageStartsNoProcess(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := map[string]bool{"os/exec": true, "syscall": true, "plugin": true, "unsafe": true, "net": true, "os": true, "golang.org/x/sys/windows": true}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); banned[p] {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
	_ = fmt.Sprint
}

// A script that ends without waiting for a call is not a crashed worker; the call is on record as abandoned and gets no answer.
func TestUnawaitedCallIsAbandonedAndRecorded(t *testing.T) {
	started := make(chan struct{})
	e := newEnv(t, func(w *fakeWorker) {
		_, _ = w.CallAsync("audit_compare", map[string]any{})
		<-started
		w.done("did not wait")
	}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
		close(started)
		<-ctx.Done()
		return mcp.ErrorEnvelope(tool, mcp.WireCancelled, "abandoned", ""), true
	}))
	res := e.run(`unravis.audit_compare({}); return "did not wait"`)
	e.wantStatus(res, casestore.RunOK, "")
	rows := e.calls(res.RunID)
	if len(rows) != 1 || rows[0].Status != casestore.CallError {
		t.Errorf("rows %+v", rows)
	}
	if string(res.Result) != `"did not wait"` {
		t.Errorf("result %s", res.Result)
	}
	e.verify()
}

// A field the record refuses must not leave the run open.
func TestRefusedEndFieldStillSealsTheRun(t *testing.T) {
	f := &flakyStore{}
	e := newEnv(t, func(w *fakeWorker) { w.done(1) }, func(e *env) { f.CodeRunStore = e.inner; e.inner = &badFirstEnd{flakyStore: f} })
	res, err := e.runReq(RunRequest{Code: "1"})
	if err != nil {
		t.Fatal(err)
	}
	row := e.getRun(res.RunID)
	if row.Status != casestore.RunError || row.ErrorCode != CodeAuditFailed || res.Status != row.Status || res.Error == nil || res.Error.Code != CodeAuditFailed {
		t.Errorf("the run was left as %s/%s, answered %+v", row.Status, row.ErrorCode, res)
	}
	e.verify()
}

type badFirstEnd struct {
	*flakyStore
	n int
}

func (b *badFirstEnd) EndRun(ctx context.Context, id string, end casestore.RunEnd) (casestore.CodeRun, error) {
	b.n++
	if b.n == 1 {
		return casestore.CodeRun{}, fmt.Errorf("%w: test", casestore.ErrBadCodeRun)
	}
	return b.flakyStore.EndRun(ctx, id, end)
}
