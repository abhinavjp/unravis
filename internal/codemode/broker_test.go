package codemode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/wire"
	"api-audit-explorer/internal/mcp"
)

var (
	ridPattern     = regexp.MustCompile(`"resultId":"[^"]*"`)
	elapsedPattern = regexp.MustCompile(`"elapsedMs":\d+`)
	ipPattern      = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
)

func normalize(b []byte) string {
	return elapsedPattern.ReplaceAllString(ridPattern.ReplaceAllString(string(b), `"resultId":"RID"`), `"elapsedMs":0`)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// A script that makes the two calls of the groom's live acceptance goes through the real tools and the real audit record, and what it
// is told is what a direct tool call is told (BL-363 acceptance 2, on the demo repository: the live check on the real source is the
// owner's).
func TestHostMatchesDirectTool(t *testing.T) {
	var sources, compare json.RawMessage
	e := newEnv(t, func(w *fakeWorker) {
		var err error
		if sources, err = w.Call("audit_list_sources", map[string]any{}); err != nil {
			t.Error(err)
		}
		if compare, err = w.Call("audit_compare", map[string]any{"query": demoQuery(), "limit": 5}); err != nil {
			t.Error(err)
		}
		w.done(map[string]any{"ok": true})
	})
	res := e.run(`const s = await unravis.audit_list_sources({}); return s`)
	e.wantStatus(res, casestore.RunOK, "")
	if res.Stats.Calls != 2 {
		t.Errorf("calls = %d", res.Stats.Calls)
	}

	direct := mcp.NewLocal(e.handler, testToken, mcp.WithIdentifiers(false))
	wantSources, isErr := direct.CallEnvelope(context.Background(), "audit_list_sources", json.RawMessage(`{}`))
	if isErr {
		t.Fatalf("direct call failed: %s", wantSources)
	}
	wantCompare, isErr := direct.CallEnvelope(context.Background(), "audit_compare", mustJSON(map[string]any{"query": demoQuery(), "limit": 5}))
	if isErr {
		t.Fatalf("direct compare failed: %s", wantCompare)
	}
	if normalize(sources) != normalize(wantSources) {
		t.Errorf("list_sources differs from the direct call:\n%.400s\n%.400s", sources, wantSources)
	}
	if normalize(compare) != normalize(wantCompare) {
		t.Errorf("compare differs from the direct call:\n%.400s\n%.400s", compare, wantCompare)
	}

	rows := e.calls(res.RunID)
	if len(rows) != 2 || rows[0].Tool != "audit_list_sources" || rows[1].Tool != "audit_compare" {
		t.Fatalf("audit rows: %+v", rows)
	}
	for i, got := range []json.RawMessage{sources, compare} {
		if rows[i].ResultHash != sum(got) || rows[i].Status != casestore.CallOK || rows[i].BlobState != casestore.BlobKept {
			t.Errorf("call %d row: %+v", i+1, rows[i])
		}
		blob, err := e.cas.CallResult(context.Background(), res.RunID, i+1)
		if err != nil || string(blob) != string(got) {
			t.Errorf("call %d: the kept copy is not what the script was told (%v)", i+1, err)
		}
	}
	run := e.getRun(res.RunID)
	if run.Source != `const s = await unravis.audit_list_sources({}); return s` || run.ToolSchemaHash != e.b.ToolSchemaHash() || len(run.Seed) != 32 ||
		run.StrippedJSHash == "" || run.SandboxMode != ModeLPAC || run.Runtime != "wazero-interp" || run.Surface != "cli" || run.AllowIdentifiers {
		t.Errorf("run row: %+v", run)
	}
	e.verify()
}

// The row of a call is on disk before the script gets its answer.
func TestCallIsAuditedBeforeTheAnswer(t *testing.T) {
	var e *env
	seen := make(chan int, 4)
	e = newEnv(t, func(w *fakeWorker) {
		for i := 1; i <= 3; i++ {
			if _, err := w.Call("audit_list_sources", map[string]any{}); err != nil {
				t.Error(err)
				return
			}
			runs, err := e.cas.ListRuns(context.Background(), casestore.RunQuery{})
			if err != nil || len(runs) != 1 {
				t.Errorf("runs: %v %v", runs, err)
				return
			}
			calls, _ := e.cas.RunCalls(context.Background(), runs[0].RunID)
			seen <- len(calls)
		}
		w.done(1)
	})
	e.run(`return 1`)
	for i := 1; i <= 3; i++ {
		if got := <-seen; got != i {
			t.Errorf("after answer %d the case held %d call rows", i, got)
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestRunRefusalsWriteNothing(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*env)
		req  RunRequest
		code string
	}{
		{"off", func(e *env) { e.set.Enabled = false }, RunRequest{Code: "return 1"}, CodeModeOff},
		{"no spawner", func(e *env) { e.cfg.Spawner = nil }, RunRequest{Code: "return 1"}, CodeSandboxUnavailable},
		{"spawner unavailable", func(e *env) { e.sp.unavailable = context.Canceled }, RunRequest{Code: "return 1"}, CodeSandboxUnavailable},
		{"no case", nil, RunRequest{Code: "return 1", CaseID: "other"}, CodeNoCase},
		{"source too large", nil, RunRequest{Code: strings.Repeat("a", MaxSource+1)}, CodeSourceTooLarge},
		{"bad utf8", nil, RunRequest{Code: "return '\xff'"}, CodeSyntaxError},
		{"nul", nil, RunRequest{Code: "return 1\x00"}, CodeSyntaxError},
		{"identifiers without the operator", nil, RunRequest{Code: "return 1", AllowIdentifiers: true}, CodeDenied},
		{"surface", nil, RunRequest{Code: "return 1", Surface: "web"}, CodeBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			if tc.mut != nil {
				tc.mut(e)
			}
			b := e.b
			if tc.name == "no spawner" {
				cfg := *e.cfg
				cfg.Spawner = nil
				var err error
				if b, err = New(cfg); err != nil {
					t.Fatal(err)
				}
			}
			req := tc.req
			if req.Surface == "" {
				req.Surface = "cli"
			}
			_, err := b.Run(context.Background(), req)
			ref, ok := err.(*Refusal)
			if !ok || ref.Code != tc.code {
				t.Fatalf("error %v, want refusal %s", err, tc.code)
			}
			if n := len(e.runs()); n != 0 {
				t.Errorf("%d rows were written for a refused request", n)
			}
			if e.sp.started() != 0 {
				t.Error("a worker was started for a refused request")
			}
		})
	}
}

func TestSourceLimitIsExactAndSeparateFromTypes(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) { w.done(1) })
	// 256 KiB of spaces after "return 1" is a script of exactly the limit.
	code := "return 1" + strings.Repeat(" ", MaxSource-len("return 1"))
	res := e.run(code)
	e.wantStatus(res, casestore.RunOK, "")
	if got := e.getRun(res.RunID).SourceBytes; got != MaxSource {
		t.Errorf("stored %d bytes", got)
	}
	if _, err := e.runReq(RunRequest{Code: code + " "}); err == nil {
		t.Error("a script over the limit was accepted")
	}
}

func TestStripAndSyntaxAreAuditedAndNeverReachTheWorker(t *testing.T) {
	cases := []struct {
		name, code, wantCode string
	}{
		{"syntax", `let x = ;`, CodeSyntaxError},
		{"import", `import x from "y"; return 1`, CodeImportNotAllowed},
		{"export", "const a = 1\nexport { a }", CodeImportNotAllowed},
		{"dynamic import", `const m = await import("fs"); return m`, CodeImportNotAllowed},
		{"dynamic import computed", `const n = "f" + "s"; return await import(n)`, CodeImportNotAllowed},
		{"import.meta", `return import.meta`, CodeImportNotAllowed},
		{"namespace", `namespace N { export const a = 1 }; return N.a`, CodeSyntaxError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(w *fakeWorker) { t.Error("the worker was started") })
			res := e.run(tc.code)
			e.wantStatus(res, casestore.RunError, tc.wantCode)
			if e.sp.started() != 0 {
				t.Error("a worker was started")
			}
			row := e.getRun(res.RunID)
			if row.Source != tc.code || row.StrippedJSHash != "" || row.ErrorText == "" {
				t.Errorf("row: %+v", row)
			}
			e.verify()
		})
	}
}

func TestTypeScriptIsStrippedAndLineNumbersMatch(t *testing.T) {
	var js string
	e := newEnv(t, func(w *fakeWorker) {
		w.mu.Lock()
		js = w.runMsg.JS
		w.mu.Unlock()
		w.done(map[string]int{"n": 3})
	})
	res := e.run("enum Color { Red, Green }\ninterface Row { a: number }\nconst r: Row = { a: Color.Green }\nreturn r.a as number")
	e.wantStatus(res, casestore.RunOK, "")
	if strings.Contains(js, "interface") || strings.Contains(js, ": Row") || !strings.HasPrefix(js, "(async") {
		t.Errorf("the worker was given %q", js)
	}
	if e.getRun(res.RunID).StrippedJSHash != sum([]byte(js)) {
		t.Error("the stored hash is not the hash of what the worker got")
	}
	if line := stripLine(t, "let ok = 1\nlet x = ;"); line != 2 {
		t.Errorf("syntax error reported on line %d", line)
	}
}

func stripLine(t *testing.T, code string) int {
	t.Helper()
	_, f := strip(code)
	if f == nil {
		t.Fatal("no failure")
	}
	var line int
	if _, err := fmtSscanLine(f.msg, &line); err != nil {
		t.Fatalf("no line in %q", f.msg)
	}
	return line
}

func fmtSscanLine(msg string, line *int) (int, error) {
	m := regexp.MustCompile(`line (\d+), column (\d+)`).FindStringSubmatch(msg)
	if m == nil {
		return 0, context.Canceled
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	*line = n
	return 1, nil
}

func TestCallBudgetFiftyThenDenied(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) {
		for i := 0; i < MaxCalls; i++ {
			if _, err := w.Call("audit_get_guide", map[string]any{}); err != nil {
				t.Errorf("call %d: %v", i+1, err)
				return
			}
		}
		// The 51st call ends the run: it gets no answer.
		if ch, err := w.CallAsync("audit_get_guide", map[string]any{}); err == nil {
			if ret, ok := <-ch; ok {
				t.Errorf("the 51st call was answered: %+v", ret)
			}
		}
	})
	res := e.run(`for (;;) await unravis.audit_get_guide({})`)
	e.wantStatus(res, casestore.RunLimitExceeded, CodeCallBudget)
	rows := e.calls(res.RunID)
	if len(rows) != MaxCalls+1 {
		t.Fatalf("%d rows", len(rows))
	}
	for i, r := range rows {
		want := casestore.CallOK
		if i == MaxCalls {
			want = casestore.CallDenied
		}
		if r.Status != want {
			t.Errorf("row %d status %s", i+1, r.Status)
		}
	}
	e.verify()
}

func TestFourInFlightAreFineAndFiveIsAViolation(t *testing.T) {
	t.Run("four at once", func(t *testing.T) {
		e := newEnv(t, func(w *fakeWorker) {
			c := make([]<-chan wire.Ret, 0, 4)
			for i := 0; i < 4; i++ {
				ch, err := w.CallAsync("audit_get_guide", map[string]any{})
				if err != nil {
					t.Error(err)
					return
				}
				c = append(c, ch)
			}
			for _, ch := range c {
				if ret, ok := <-ch; !ok || !ret.OK {
					t.Errorf("answer %+v %v", ret, ok)
				}
			}
			// The fifth waits its turn in the worker, which sends it after an answer arrived: allowed.
			if _, err := w.Call("audit_get_guide", map[string]any{}); err != nil {
				t.Error(err)
			}
			w.done(5)
		})
		res := e.run(`return 5`)
		e.wantStatus(res, casestore.RunOK, "")
		if res.Stats.Calls != 5 {
			t.Errorf("calls %d", res.Stats.Calls)
		}
	})
	t.Run("five at once", func(t *testing.T) {
		// A host that holds every call lets five calls pile up.
		release := make(chan struct{})
		e := newEnv(t, func(w *fakeWorker) {
			for i := 0; i < 5; i++ {
				if _, err := w.CallAsync("audit_get_guide", map[string]any{}); err != nil {
					return
				}
			}
			time.Sleep(2 * time.Second)
		}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return mcp.ErrorEnvelope(tool, mcp.WireCancelled, "stopped", ""), true
		}))
		res := e.run(`x`)
		close(release)
		e.wantStatus(res, casestore.RunError, CodeBadFrame)
		var denied int
		for _, r := range e.calls(res.RunID) {
			if r.Status == casestore.CallDenied {
				denied++
			}
		}
		if denied != 1 {
			t.Errorf("%d denied calls", denied)
		}
		e.verify()
	})
}
