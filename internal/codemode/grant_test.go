package codemode

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpsrv"
)

// Review 5 of #267 (/mnt/project-files/reviews/pr267-review-5.md): a code run is limited by the grant's own tool list (M1) and charged to
// the host's budgets like the host's own tool calls (M2).

func grantRun(t *testing.T, tools []string, script fakeScript, opts ...envOpt) (*env, RunResult, **fakeWorker) {
	t.Helper()
	var worker *fakeWorker
	e := newEnv(t, func(w *fakeWorker) { worker = w; script(w) }, opts...)
	res, err := e.runReq(RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierEvidence, Sources: []string{"demo-west"},
		Tools: tools, ProfileID: "host-a", ProfileRev: 3, Reason: "grant test"})
	if err != nil {
		t.Fatal(err)
	}
	return e, res, &worker
}

// An evidence grant that names two tools cannot reach the others (raw rows included) through a script.
func TestGrantToolListLimitsTheRun(t *testing.T) {
	narrow := []string{"audit_compare", "audit_get_guide"}
	for _, tool := range []string{"audit_get_requests", "audit_get_slowest", "audit_get_activity", "audit_get_breakdown", "audit_list_sources"} {
		var answer json.RawMessage
		e, res, w := grantRun(t, narrow, func(w *fakeWorker) {
			answer, _ = w.Call(tool, map[string]any{"resultId": "x"})
			w.done(1)
		})
		e.wantStatus(res, casestore.RunError, CodeBadFrame)
		if len(answer) != 0 {
			t.Errorf("%s: a run was answered: %.200s", tool, answer)
		}
		rows := e.calls(res.RunID)
		if len(rows) != 1 || rows[0].Tool != tool || rows[0].Status != casestore.CallDenied {
			t.Errorf("%s: rows %+v", tool, rows)
		}
		if got := (*w).runMsg.Tools; !slices.Equal(got, mcpsrv.GrantTools(mcpsrv.TierEvidence, narrow)) || len(got) != 2 {
			t.Errorf("%s: the run frame names %v", tool, got)
		}
		e.verify()
	}
	// The tools of the list work.
	e, res, _ := grantRun(t, narrow, func(w *fakeWorker) {
		if a, _ := w.Call("audit_get_guide", map[string]any{}); len(a) == 0 {
			t.Error("no guide answer")
		}
		w.done(1)
	})
	e.wantStatus(res, casestore.RunOK, "")
}

// The tool list can only narrow the tier, never widen it, and a list or profile that is missing is refused with nothing written.
func TestGrantNeedsItsToolListAndProfile(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) { w.done(1) })
	n := len(e.runs())
	base := RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierAggregate, Sources: []string{"demo-west"}, ProfileID: "p", Tools: []string{"audit_compare"}}
	for name, mut := range map[string]func(*RunRequest){
		"no tool list":                func(r *RunRequest) { r.Tools = nil },
		"empty tool list":             func(r *RunRequest) { r.Tools = []string{} },
		"only tools outside the tier": func(r *RunRequest) { r.Tools = []string{"audit_get_requests"} },
		"no profile":                  func(r *RunRequest) { r.ProfileID = "" },
	} {
		req := base
		mut(&req)
		if _, err := e.b.Run(context.Background(), req); err == nil || err.(*Refusal).Code != CodeDenied {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, mut := range map[string]func(*RunRequest){
		"tools without scope":   func(r *RunRequest) { *r = RunRequest{Code: "1", Surface: "cli", Tools: []string{"audit_compare"}} },
		"profile without scope": func(r *RunRequest) { *r = RunRequest{Code: "1", Surface: "cli", ProfileID: "p"} },
	} {
		req := base
		mut(&req)
		if _, err := e.b.Run(context.Background(), req); err == nil || err.(*Refusal).Code != CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(e.runs()) != n {
		t.Error("a refused run left a row")
	}
	// A broker with no call admitter refuses a scoped run: the budgets cannot be skipped by leaving them out.
	e.cfg.Admit = nil
	b, err := New(*e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Run(context.Background(), base); err == nil || err.(*Refusal).Code != CodeDenied {
		t.Errorf("scoped run without an admitter: %v", err)
	}
}

type admitted struct {
	pr   mcpsrv.Principal
	tool string
}

// Every call of a scoped run goes through the host's budgets before it runs; a refusal is a denied row and an error answer, and the run
// goes on; a call that ran is closed with the bytes it returned.
func TestScopedCallsAreChargedToTheHostBudgets(t *testing.T) {
	var mu sync.Mutex
	var seen []admitted
	var ended [][]byte
	compares := 0
	admit := func(ctx context.Context, pr mcpsrv.Principal, tool string, _ []byte, _ bool) (context.Context, func([]byte, bool) *mcpsrv.CodeCallRefusal, *mcpsrv.CodeCallRefusal) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, admitted{pr, tool})
		if tool == "audit_compare" {
			if compares++; compares > 2 {
				return nil, nil, &mcpsrv.CodeCallRefusal{Code: mcp.WireBudgetExceeded, Message: "This host reached its limit for comparisons in the last hour."}
			}
		}
		var once sync.Once // like the real end: the broker also defers it, for a panic
		return ctx, func(result []byte, _ bool) *mcpsrv.CodeCallRefusal {
			once.Do(func() {
				mu.Lock()
				ended = append(ended, result)
				mu.Unlock()
			})
			return nil
		}, nil
	}
	var answers []json.RawMessage
	e, res, _ := grantRun(t, mcpsrv.ToolsForTier(mcpsrv.TierEvidence), func(w *fakeWorker) {
		for i := 0; i < 4; i++ {
			a, _ := w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 1})
			answers = append(answers, a)
		}
		_, _ = w.Call("audit_get_guide", map[string]any{})
		w.done(1)
	}, func(e *env) { e.admit = admit })
	e.wantStatus(res, casestore.RunOK, "") // a refused call does not end the run
	rows := e.calls(res.RunID)
	if len(rows) != 5 {
		t.Fatalf("rows %+v", rows)
	}
	for i, row := range rows[:4] {
		want := casestore.CallOK
		if i >= 2 {
			want = casestore.CallDenied
		}
		if row.Status != want {
			t.Errorf("call %d: %s, want %s", i+1, row.Status, want)
		}
	}
	for _, a := range answers[2:] {
		var env struct {
			Error struct{ Code string }
		}
		if json.Unmarshal(a, &env) != nil || env.Error.Code != mcp.WireBudgetExceeded {
			t.Errorf("a refused comparison answered %.200s", a)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 5 || len(ended) != 3 {
		t.Fatalf("admitted %d calls, ended %d (want 5 and 3)", len(seen), len(ended))
	}
	for _, a := range seen {
		if a.pr != (mcpsrv.Principal{ProfileID: "host-a", Rev: 3}) {
			t.Errorf("charged to %+v", a.pr)
		}
	}
	if len(ended[0]) == 0 {
		t.Error("the end of a call carried no result bytes")
	}
	e.verify()
}

// The owner's own run (cli, ui) has no grant and is not charged to a host.
func TestOwnersRunIsNotCharged(t *testing.T) {
	called := false
	e := newEnv(t, func(w *fakeWorker) { _, _ = w.Call("audit_get_guide", map[string]any{}); w.done(1) }, func(e *env) {
		e.admit = func(ctx context.Context, _ mcpsrv.Principal, _ string, _ []byte, _ bool) (context.Context, func([]byte, bool) *mcpsrv.CodeCallRefusal, *mcpsrv.CodeCallRefusal) {
			called = true
			return ctx, func([]byte, bool) *mcpsrv.CodeCallRefusal { return nil }, nil
		}
	})
	e.wantStatus(e.run("1"), casestore.RunOK, "")
	if called {
		t.Error("an unscoped run was charged to a host")
	}
}

// The host call of a scoped run runs under the context the admitter hands back (it ends on a revoke), not under the run's own (review 6
// of #267, M1).
func TestScopedCallRunsUnderTheAdmittersContext(t *testing.T) {
	type key struct{}
	var seen atomic.Bool
	admit := func(ctx context.Context, _ mcpsrv.Principal, _ string, _ []byte, _ bool) (context.Context, func([]byte, bool) *mcpsrv.CodeCallRefusal, *mcpsrv.CodeCallRefusal) {
		return context.WithValue(ctx, key{}, "admitted"), func([]byte, bool) *mcpsrv.CodeCallRefusal { return nil }, nil
	}
	host := func(ctx context.Context, _ string, _ json.RawMessage) ([]byte, bool) {
		seen.Store(ctx.Value(key{}) == "admitted")
		return []byte(`{"ok":true}`), false
	}
	e, res, _ := grantRun(t, mcpsrv.ToolsForTier(mcpsrv.TierEvidence), func(w *fakeWorker) {
		_, _ = w.Call("audit_get_guide", map[string]any{})
		w.done(1)
	}, withHost(host), func(e *env) { e.admit = admit })
	e.wantStatus(res, casestore.RunOK, "")
	if !seen.Load() {
		t.Error("the host call did not run under the context of the admitter")
	}
}

// When the admitter's end refuses (the grant changed while the call ran), the script gets that refusal and never the host's answer, and
// the call is a denied row.
func TestScopedCallAnswerIsWithheldWhenEndRefuses(t *testing.T) {
	var ends atomic.Int32
	admit := func(ctx context.Context, _ mcpsrv.Principal, _ string, _ []byte, _ bool) (context.Context, func([]byte, bool) *mcpsrv.CodeCallRefusal, *mcpsrv.CodeCallRefusal) {
		return ctx, func([]byte, bool) *mcpsrv.CodeCallRefusal {
			ends.Add(1)
			return &mcpsrv.CodeCallRefusal{Code: mcp.WirePermissionRequired, Message: "This host's access changed while the call ran."}
		}, nil
	}
	host := func(context.Context, string, json.RawMessage) ([]byte, bool) {
		return []byte(`{"ok":true,"result":"HOST-SECRET"}`), false
	}
	var answer json.RawMessage
	e, res, _ := grantRun(t, mcpsrv.ToolsForTier(mcpsrv.TierEvidence), func(w *fakeWorker) {
		answer, _ = w.Call("audit_get_guide", map[string]any{})
		w.done(1)
	}, withHost(host), func(e *env) { e.admit = admit })
	e.wantStatus(res, casestore.RunOK, "")
	if strings.Contains(string(answer), "HOST-SECRET") {
		t.Errorf("the script got the host's answer: %.200s", answer)
	}
	var env struct{ Error struct{ Code string } }
	if json.Unmarshal(answer, &env) != nil || env.Error.Code != mcp.WirePermissionRequired {
		t.Errorf("the script's answer: %.200s", answer)
	}
	rows := e.calls(res.RunID)
	if len(rows) != 1 || rows[0].Status != casestore.CallDenied {
		t.Errorf("rows %+v", rows)
	}
	if n := ends.Load(); n < 1 {
		t.Errorf("end called %d times", n)
	}
}
