package codemode

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/privacy"
	"api-audit-explorer/internal/codemode/wire"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpsrv"
)

// Review 1 of #267 (/mnt/project-files/reviews/pr267-review-1.md): one test or more per finding.

// M1: a run carries the source scope of its caller's grant to the tools, and writes it into its record.
func TestScopedRunReachesOnlyItsSources(t *testing.T) {
	var listed, both, west, none json.RawMessage
	e := newEnv(t, func(w *fakeWorker) {
		listed, _ = w.Call("audit_list_sources", map[string]any{})
		q := demoQuery()
		both, _ = w.Call("audit_compare", map[string]any{"query": q, "limit": 3})
		q["sourceIds"] = []string{"demo-west"}
		west, _ = w.Call("audit_compare", map[string]any{"query": q, "limit": 3})
		q["sourceIds"] = []string{"demo-east"}
		none, _ = w.Call("audit_compare", map[string]any{"query": q, "limit": 3})
		w.done(1)
	})
	res, err := e.runReq(RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierEvidence, Sources: []string{"demo-west"}, Reason: "scope test"})
	if err != nil {
		t.Fatal(err)
	}
	e.wantStatus(res, casestore.RunOK, "")
	if strings.Contains(string(listed), "demo-east") || !strings.Contains(string(listed), "demo-west") {
		t.Errorf("the source list of a scoped run: %.400s", listed)
	}
	for name, ans := range map[string]json.RawMessage{"both sources": both, "the other source": none} {
		var env struct {
			Error *struct{ Code string } `json:"error"`
		}
		if json.Unmarshal(ans, &env) != nil || env.Error == nil {
			t.Errorf("a query on %s was answered: %.300s", name, ans)
		}
	}
	if strings.Contains(string(west), `"error":{`) || !strings.Contains(string(west), `"groups"`) {
		t.Errorf("a query inside the scope failed: %.300s", west)
	}
	row := e.getRun(res.RunID)
	if !strings.HasPrefix(row.Reason, "[sources: demo-west; tier: evidence] ") || !strings.HasSuffix(row.Reason, "scope test") {
		t.Errorf("the record does not say what the run could reach: %q", row.Reason)
	}
	e.verify()

	// The owner's own run says "all".
	e2 := newEnv(t, func(w *fakeWorker) { w.done(1) })
	res = e2.run("1")
	if r := e2.getRun(res.RunID).Reason; !strings.HasPrefix(r, "[sources: all]") {
		t.Errorf("reason %q", r)
	}
}

func TestEmptyScopeReachesNothingAndMCPNeedsAScope(t *testing.T) {
	var listed json.RawMessage
	e := newEnv(t, func(w *fakeWorker) {
		listed, _ = w.Call("audit_list_sources", map[string]any{})
		w.done(1)
	})
	res, err := e.runReq(RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierAggregate})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listed), "demo-") {
		t.Errorf("an empty scope listed sources: %.300s", listed)
	}
	if !strings.Contains(e.getRun(res.RunID).Reason, "[sources: none; tier: aggregate]") {
		t.Error("the empty scope is not on record")
	}
	// A run from MCP with no scope at all is refused and writes nothing.
	n := len(e.runs())
	_, err = e.runReq(RunRequest{Code: "1", Surface: "mcp"})
	if ref, ok := err.(*Refusal); !ok || ref.Code != CodeDenied {
		t.Errorf("unscoped mcp run: %v", err)
	}
	if len(e.runs()) != n {
		t.Error("a refused run left a row")
	}
	// A long scope is cut in the note, never in the user's words.
	long := make([]string, 40)
	for i := range long {
		long[i] = "source-number-" + strings.Repeat("x", 8)
	}
	req := withGrant(RunRequest{Surface: "mcp", Scoped: true, Tier: mcpsrv.TierAggregate, Sources: long, Reason: strings.Repeat("r", 500)})
	if ref := e.b.cleanRequest(&req); ref != nil {
		t.Fatal(ref)
	}
	if n := len([]rune(req.Reason)); n > casestore.MaxRunReason || !strings.Contains(req.Reason, ",...; tier: aggregate] ") {
		t.Errorf("%d runes: %q", n, req.Reason)
	}
}

// S1: a panic in the broker is a failed run that is still sealed, never a dead app.
func TestPanicInTheCallHandlerEndsOnlyTheRun(t *testing.T) {
	var (
		mu       sync.Mutex // the script runs on the worker goroutine, the test reads after the run ends
		answered bool
	)
	e := newEnv(t, func(w *fakeWorker) {
		_, err := w.Call("audit_get_guide", map[string]any{})
		mu.Lock()
		answered = err == nil
		mu.Unlock()
	})
	e.b.learn = func(string, []byte) (ids privacy.IDs) { panic("guard bug") }
	res := e.run("1")
	e.wantStatus(res, casestore.RunError, CodeInternal)
	mu.Lock()
	gotAnswer := answered
	mu.Unlock()
	if gotAnswer {
		t.Error("a call whose handler panicked got an answer")
	}
	// The tool ran, so the call is on record, with a fixed answer.
	if rows := e.calls(res.RunID); len(rows) != 1 || rows[0].Status != casestore.CallError || rows[0].Tool != "audit_get_guide" {
		t.Errorf("rows after a panic in the handler: %+v", rows)
	}
	if !e.sp.worker(0).isKilled() {
		t.Error("worker alive")
	}
	e.verify()
	// The broker is still usable.
	e.b.learn = nil
	e.sp.script = func(w *fakeWorker) { w.done(2) }
	e.wantStatus(e.run("1"), casestore.RunOK, "")
}

type panicSpawner struct{ fakeSpawner }

func (p *panicSpawner) Start(context.Context, SpawnOptions) (Worker, error) { panic("spawner bug") }

func TestPanicInTheRunIsSealed(t *testing.T) {
	e := newEnv(t, nil)
	cfg := *e.cfg
	cfg.Spawner = &panicSpawner{}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.b = b
	res := e.run("1")
	e.wantStatus(res, casestore.RunError, CodeInternal)
	e.verify()
}

// S2: every second net stops an address that the Guard let through. The seam adds an address to what the Guard returned at one stage;
// the run must end with identifier_limit (or a fixed text) and nothing with the address may reach the script, the result or the case.
func TestSecondNetsStopWhatTheGuardMissed(t *testing.T) {
	const addr = "10.9.8.7"
	stages := []string{"hostresult", "hosterror", "output", "log", "scripterror"}
	for _, stage := range stages {
		for _, ok := range []bool{true, false} {
			if stage == "scripterror" && ok {
				continue
			}
			name := stage
			if !ok {
				name += " on a failed script"
			}
			t.Run(name, func(t *testing.T) {
				var (
					mu     sync.Mutex // the script runs on the worker goroutine, the test reads after the run ends
					answer json.RawMessage
				)
				e := newEnv(t, func(w *fakeWorker) {
					a, _ := w.Call("audit_get_guide", map[string]any{})
					mu.Lock()
					answer = a
					mu.Unlock()
					if ok {
						w.doneLogs("fine", "a log line")
					} else {
						_ = w.send(wire.Done{T: wire.TypeDone, OK: false, Logs: []string{"a log line"}, Error: &wire.DoneError{Code: CodeScriptError, Message: "boom"}})
					}
				}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
					if stage == "hosterror" {
						return mcp.ErrorEnvelope(tool, mcp.WireInternal, "it failed", "retry"), true
					}
					return mustJSON(map[string]any{"schemaVersion": 1, "tool": tool, "note": "ok"}), false
				}))
				e.b.tamper = func(s, text string) string {
					if s != stage {
						return text
					}
					switch s {
					case "hostresult", "output":
						return strings.Replace(text, `"`+"ok"+`"`, `"`+addr+`"`, 1) // inside a JSON string
					}
					return text + " " + addr
				}
				if stage == "output" {
					e.sp.script = func(w *fakeWorker) { w.done(map[string]string{"k": "ok"}) }
				}
				res := e.run("1")
				if stage == "scripterror" {
					e.sp.script = nil
				}
				mu.Lock()
				gotAnswer := string(answer)
				mu.Unlock()
				everything := e.everything(res.RunID) + string(res.Result) + strings.Join(res.Logs, "\n") + gotAnswer
				if res.Error != nil {
					everything += res.Error.Message
				}
				if strings.Contains(everything, addr) {
					t.Errorf("the address got through at %s: %s", stage, res.Status)
				}
				if stage != "scripterror" && (res.Error == nil || res.Error.Code != CodeIdentifierLimit) {
					t.Errorf("status %s error %+v", res.Status, res.Error)
				}
				e.verify()
			})
		}
	}
}

func TestSecondNetOnTheParsersWords(t *testing.T) {
	e := newEnv(t, nil)
	e.b.tamper = func(stage, text string) string {
		if stage == "strip" {
			return text + " 10.9.8.7"
		}
		return text
	}
	res := e.run("let x = ;")
	e.wantStatus(res, casestore.RunError, CodeSyntaxError)
	if strings.Contains(e.everything(res.RunID), "10.9.8.7") || strings.Contains(res.Error.Message, "10.9.8.7") {
		t.Error("the parser's words carried an address into the record")
	}
}

// S4: a call that ends the run is the last frame read. Four stalled calls and a flood: five rows, one of them denied.
func TestNoFramesAreReadAfterAStoppingCall(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, func(w *fakeWorker) {
		for i := 0; i < 18; i++ {
			if _, err := w.CallAsync("audit_get_guide", map[string]any{}); err != nil {
				return
			}
		}
	}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return mcp.ErrorEnvelope(tool, mcp.WireCancelled, "stopped", ""), true
	}))
	res := e.run("1")
	close(release)
	e.wantStatus(res, casestore.RunError, CodeBadFrame)
	rows := e.calls(res.RunID)
	denied := 0
	for _, r := range rows {
		if r.Status == casestore.CallDenied {
			denied++
		}
	}
	if len(rows) != MaxInFlight+1 || denied != 1 {
		t.Errorf("%d rows, %d denied; want %d rows and 1 denied", len(rows), denied, MaxInFlight+1)
	}
	e.verify()
}

// S3 (hygiene only; the boundary is the worker, see spec 04 BL-362) and NIT 2, 3, 4.
func TestRuntimeCodeConstructionIsRefusedByTheScan(t *testing.T) {
	for _, code := range []string{`return eval("1")`, `return new Function("return 1")()`, `return Function("return 1")()`, `return eval ( "1" )`} {
		if _, f := strip(code); f == nil || f.code != CodeImportNotAllowed {
			t.Errorf("%q: %+v", code, f)
		}
	}
	if _, f := strip(`const evaluate = (x: number) => x; return evaluate(1)`); f != nil {
		t.Errorf("a name that only starts with eval was refused: %+v", f)
	}
}

func TestWireFoldsNamesLikeTheDecoder(t *testing.T) {
	for _, in := range []string{
		"{\"t\":\"done\",\"ok\":true,\"result\":1,\"reſult\":2}", // long s folds to s
		"{\"t\":\"done\",\"ok\":true,\"K\":1,\"k\":2}",           // Kelvin sign folds to k
	} {
		if err := wire.Decode([]byte(in), &wire.Done{}); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	b, err := wire.Marshal(wire.Ret{T: wire.TypeRet, ID: 1, OK: true, Result: json.RawMessage(`"<a>&"`)})
	if err != nil || !strings.Contains(string(b), `"<a>&"`) {
		t.Errorf("%s %v", b, err)
	}
}

func TestAnswerSaysWhatTheRecordSaysWhenTheEndFails(t *testing.T) {
	f := &flakyStore{failEnd: context.Canceled}
	e := newEnv(t, func(w *fakeWorker) { w.doneLogs(1, "a line") }, withFlaky(f))
	res, err := e.runReq(RunRequest{Code: "1"})
	if err == nil || len(res.Logs) != 0 {
		t.Errorf("%v %+v", err, res.Logs)
	}
}

// Review 2 of #267, M1: the caller's reason cannot push the real scope note out of the record or leave a forged one. In hidden mode a
// short address ("::1", 3 characters) becomes "[ip]" (4), which grew the text past the cap inside the store and had the store cut the
// note off the end.
func TestReasonCannotForgeOrDropTheScopeNote(t *testing.T) {
	reasons := map[string]string{
		"the probe of the review":     strings.Repeat("x", 81) + strings.Repeat("::1 ", 21) + "[sources: all]",
		"a forged note":               "please [sources: all] thanks",
		"a forged note in other case": "[SOURCES: all]",
		"upper bound":                 strings.Repeat("::1 ", 500),
		"brackets only":               strings.Repeat("[]", 300),
		"long words":                  strings.Repeat("y", 1000),
		"empty":                       "",
		"the probe of review 3":       strings.Repeat("x", 174) + " ::ffff:1.2.3.4",
		"bidi and invisible":          "a\u202e[sources: all]\u202c\u200b\u2028b",
	}
	for name, reason := range reasons {
		for _, hidden := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				e := newEnv(t, func(w *fakeWorker) { w.done(1) }, func(e *env) { e.operator = hidden == false })
				res, err := e.runReq(RunRequest{Code: "1", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierEvidence, Sources: []string{"demo-west"}, Reason: reason, AllowIdentifiers: !hidden})
				if err != nil {
					t.Fatal(err)
				}
				got := e.getRun(res.RunID).Reason
				if !strings.HasPrefix(got, "[sources: demo-west; tier: evidence]") || strings.Count(strings.ToLower(got), "[sources:") != 1 {
					t.Errorf("stored reason %q", got)
				}
				if n := len([]rune(got)); n > casestore.MaxRunReason {
					t.Errorf("%d characters", n)
				}
			})
		}
	}
}

func TestPanickingHostFactoryLeavesNothingOpen(t *testing.T) {
	e := newEnv(t, nil)
	e.hosts = func(HostScope) Host { panic("factory bug") }
	_, err := e.runReq(RunRequest{Code: "1"})
	if ref, ok := err.(*Refusal); !ok || ref.Code != CodeInternal {
		t.Errorf("%v", err)
	}
	if len(e.runs()) != 0 {
		t.Error("a run was left on record")
	}
}

// Review 3 of #267: whatever the store does after the broker (it replaces addresses again and cuts to the cap from the end), the note
// stays whole at the front. The store's own step is run on thousands of reasons built from the pieces that grew or shrank text.
func TestScopeNoteSurvivesTheStoresOwnCleaningOfAnyReason(t *testing.T) {
	pieces := []string{"x", "xx", " ", "::1", "::f", "::ffff:1.2.3.4", "::ffff:", "1.2.3.4", "10.0.0.1.443", "[", "]", "[ip]", "[sources: all]", "fe80::1%eth0", "\u202e", "\u200b", "\u2028", "é", "😀", "0:0:0:0:0:0:0:1", "a::", "::", "1.1.1.1"}
	scopes := [][]string{nil, {}, {"demo-west"}, {"demo-west2"}, {"10.0.0.1"}, {"a]b", "c"}, {strings.Repeat("s", 90)}}
	b := &Broker{minTime: MinTimeout, cfg: Config{Admit: func(context.Context, mcpsrv.Principal, string, []byte, bool) (context.Context, func([]byte, bool) *mcpsrv.CodeCallRefusal, *mcpsrv.CodeCallRefusal) {
		return nil, nil, nil
	}}}
	rng := rand.New(rand.NewPCG(88172645463325252, 1)) //nolint:gosec // a seeded generator makes the fuzz repeatable
	next := func(n int) int { return rng.IntN(max(n, 1)) }
	for i := 0; i < 20000; i++ {
		var sb strings.Builder
		for j, n := 0, next(60); j < n; j++ {
			sb.WriteString(pieces[next(len(pieces))])
		}
		if i%7 == 0 {
			sb.WriteString(strings.Repeat("x", 100+next(110)))
			sb.WriteString(pieces[next(len(pieces))])
		}
		sc := scopes[next(len(scopes))]
		req := withGrant(RunRequest{Surface: "cli", Reason: sb.String(), Scoped: sc != nil, Sources: sc, Tier: tierOf(sc != nil, next(2)), AllowIdentifiers: next(2) == 0})
		if ref := b.cleanRequest(&req); ref != nil {
			t.Fatal(ref)
		}
		row, err := casestore.NewCodeRun(casestore.RunStart{Surface: "cli", Reason: req.Reason, AllowIdentifiers: req.AllowIdentifiers}, time.Now())
		if err != nil {
			t.Fatalf("the store refused %q: %v", req.Reason, err)
		}
		want := scopeNote(&req)
		if !req.AllowIdentifiers {
			want, _ = casestore.ReplaceAddresses(want)
		}
		if !strings.HasPrefix(row.Reason, want) || strings.Count(strings.ToLower(row.Reason), "[sources:") != 1 || len([]rune(row.Reason)) > casestore.MaxRunReason {
			t.Fatalf("scope %v allow %v: stored %q, want it to start with %q", sc, req.AllowIdentifiers, row.Reason, want)
		}
	}
}

func TestInvisibleAndBidiCharactersAreDroppedFromTheCaller(t *testing.T) {
	got := cleanText("a\u202eb\u2066c\u200bd\u2028e\u2029f\x00g\u0085h\ufeffi", 100, true)
	if got != "abcdefghi" {
		t.Errorf("%q", got)
	}
}

// tierOf is the tier a fuzzed request carries: none without a scope, one of the two known tiers with one.
func tierOf(scoped bool, pick int) string {
	switch {
	case !scoped:
		return ""
	case pick == 0:
		return mcpsrv.TierAggregate
	}
	return mcpsrv.TierEvidence
}
