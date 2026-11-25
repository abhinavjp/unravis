package codeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/codemode"
)

// These tests drive the command line's entrance (CLI) over the real Broker, the real codeapi resolver and a real sealed case store, the way
// codeapi_test.go does for the HTTP routes. There is no JavaScript worker in this slice: an accepted run ends as sandbox_unavailable.

func cliOf(t *testing.T, r *rig) clisrv.Code {
	t.Helper()
	c := CLI(r.broker)
	if c == nil {
		t.Fatal("no entrance")
	}
	return c
}

func refusalOf(t *testing.T, err error) string {
	t.Helper()
	var ref *clisrv.CodeRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("not a refusal: %v", err)
	}
	return ref.Code
}

// The wired dependency really calls Broker.Run: the run is recorded in the open case with the surface "cli", it is not scoped, and the
// script and the reason are in the record.
func TestCLIRunsThroughTheBroker(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	id := r.newCase("From the console")
	c := cliOf(t, r)

	// Switched off (the default): a refusal, and nothing recorded.
	_, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1+1"})
	if got := refusalOf(t, err); got != codemode.CodeModeOff {
		t.Fatalf("code mode is off: %q", got)
	}
	if n := len(r.runsOf(id)); n != 0 {
		t.Fatalf("a refused run was recorded: %d", n)
	}

	r.enable()
	res, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1+1", Reason: "my reason", TimeoutMS: 5000})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := res.(codemode.RunResult)
	if !ok || out.RunID == "" || out.Status == "ok" || out.Error == nil || out.Error.Code != codemode.CodeSandboxUnavailable {
		t.Fatalf("result: %#v", res)
	}
	runs := r.runsOf(id)
	if len(runs) != 1 {
		t.Fatalf("runs in the case: %d, want 1", len(runs))
	}
	got := runs[0]
	if got.RunID != out.RunID || got.Surface != "cli" || got.CallerID != "cli" || got.AllowIdentifiers || got.EndedMS == 0 || got.Status == "running" ||
		!strings.Contains(got.Reason, "my reason") {
		t.Fatalf("recorded run: %+v", got)
	}
	if full, err := r.getRun(id, got.RunID); err != nil || full.Source != "1+1" {
		t.Fatalf("source not recorded: %q %v", full.Source, err)
	}
	// The result is what the HTTP route answers for the same run.
	b, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(b), `"status":"error"`) {
		t.Fatalf("encoded result: %s %v", b, err)
	}
}

func TestCLIRefusals(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	c := cliOf(t, r)
	r.enable()

	// No case open.
	_, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1"})
	if got := refusalOf(t, err); got != codemode.CodeNoCase {
		t.Fatalf("no case: %q", got)
	}
	// Two cases open and none named: the refusal the person can fix by naming one. Naming one runs.
	a := r.newCase("A")
	b := r.newCase("B")
	_, err = c.Run(context.Background(), clisrv.CodeRunInput{Code: "1"})
	if got := refusalOf(t, err); got != clicore.CodeModeManyCases {
		t.Fatalf("many cases: %q", got)
	}
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1", CaseID: b}); err != nil {
		t.Fatalf("named case: %v", err)
	}
	if len(r.runsOf(a)) != 0 || len(r.runsOf(b)) != 1 {
		t.Fatalf("the run went to the wrong case: a=%d b=%d", len(r.runsOf(a)), len(r.runsOf(b)))
	}
	// Identifiers need the operator's switch, which this app does not have set.
	_, err = c.Run(context.Background(), clisrv.CodeRunInput{Code: "1", CaseID: b, AllowIdentifiers: true})
	if got := refusalOf(t, err); got != codemode.CodeDenied {
		t.Fatalf("identifiers: %q", got)
	}
	// No safe runner on this computer.
	r2 := newRig(t, rigOpts{spawner: &spawn{avail: errors.New("not here")}})
	r2.newCase("C")
	r2.enable()
	_, err = cliOf(t, r2).Run(context.Background(), clisrv.CodeRunInput{Code: "1"})
	if got := refusalOf(t, err); got != codemode.CodeSandboxUnavailable {
		t.Fatalf("no sandbox: %q", got)
	}
}

// A cancel of the call ends a live run through the call's context: the broker stops waiting for the worker, the run is recorded as ended
// (not left running) and the answer comes back at once. With no worker in this build the run ends as a failed start; with one it would end as
// cancelled (the owner's CANCELED answer is tested in internal/clisrv).
func TestCLICancelEndsALiveRun(t *testing.T) {
	sp := &spawn{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	r := newRig(t, rigOpts{spawner: sp})
	id := r.newCase("Cancel me")
	r.enable()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := cliOf(t, r).Run(ctx, clisrv.CodeRunInput{Code: "while(true){}"})
		done <- outcome{res, err}
	}()
	select {
	case <-sp.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not start")
	}
	cancel()
	select {
	case o := <-done:
		res, ok := o.res.(codemode.RunResult)
		if o.err != nil || !ok || res.Status == "ok" || res.Status == "running" || res.Error == nil {
			t.Fatalf("%#v %v", o.res, o.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run was not ended")
	}
	if runs := r.runsOf(id); len(runs) != 1 || runs[0].Status == "running" || runs[0].Surface != "cli" {
		t.Fatalf("recorded: %+v", runs)
	}
}

// code.types is the document of the HTTP route, byte for byte as data.
func TestCLITypesAreTheRouteTypes(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	res := r.call(http.MethodGet, "/api/code/types", "")
	if res.Code != http.StatusOK {
		t.Fatalf("route = %d", res.Code)
	}
	got, err := cliOf(t, r).Types(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var viaCLI, viaRoute map[string]any
	b, _ := json.Marshal(got)
	if err := json.Unmarshal(b, &viaCLI); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(res.Body.Bytes(), &viaRoute); err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(viaCLI)
	b2, _ := json.Marshal(viaRoute)
	if string(b1) != string(b2) || viaCLI["namespace"] == "" || viaCLI["schemaSha256"] == "" || viaCLI["dts"] == "" {
		t.Fatalf("the command line's types differ from the route's:\n%s\n%s", b1, b2)
	}
}

// No broker (code mode is not built into this app) gives no entrance at all, as an untyped nil, so clisrv answers ENDPOINT_UNAVAILABLE.
func TestCLIWithoutABrokerIsNil(t *testing.T) {
	if c := CLI(nil); c != nil {
		t.Fatalf("got %#v", c)
	}
}

// The command line checks a script against its own copies of the limits, and has words for every code the broker can end a run with. A
// change in the broker that the command line has not followed fails here.
func TestCLILimitsAndTextsMatchTheBroker(t *testing.T) {
	if clicore.MaxCodeSource != codemode.MaxSource {
		t.Errorf("MaxCodeSource %d, broker %d", clicore.MaxCodeSource, codemode.MaxSource)
	}
	if int64(clicore.MaxCodeTimeoutMS) != codemode.MaxTimeout.Milliseconds() {
		t.Errorf("MaxCodeTimeoutMS %d, broker %d", clicore.MaxCodeTimeoutMS, codemode.MaxTimeout.Milliseconds())
	}
	unknown := clicore.CodeModeError("not_a_code").Message
	for _, code := range []string{
		codemode.CodeModeOff, codemode.CodeDenied, codemode.CodeNoCase, codemode.CodeBusy, codemode.CodeSourceTooLarge, codemode.CodeSyntaxError,
		codemode.CodeImportNotAllowed, codemode.CodeSandboxUnavailable, codemode.CodeSandboxDegradedNope, codemode.CodeCallBudget,
		codemode.CodeResultTooLarge, codemode.CodeOutputTooLarge, codemode.CodeScriptError, codemode.CodeTicks, codemode.CodeWall, codemode.CodeRunCap,
		codemode.CodeOOM, codemode.CodeStack, codemode.CodeWorkerCrashed, codemode.CodeBadFrame, codemode.CodeCancelled, codemode.CodeIdentifierLimit,
		codemode.CodeAuditFailed, codemode.CodeBadRequest, codemode.CodeInternal, clicore.CodeModeManyCases,
	} {
		e := clicore.CodeModeError(code)
		if e.Message == unknown || e.Message == "" {
			t.Errorf("%s has no words on the command line", code)
		}
	}
}
