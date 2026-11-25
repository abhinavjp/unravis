package clisrv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clicore"
)

// fakeCode is a broker that answers from a function. The real one is reached in internal/codeapi (TestCLIRunsThroughTheBroker).
type fakeCode struct {
	types func(ctx context.Context) (any, error)
	run   func(ctx context.Context, in CodeRunInput) (any, error)
}

func (f fakeCode) Types(ctx context.Context) (any, error)                { return f.types(ctx) }
func (f fakeCode) Run(ctx context.Context, in CodeRunInput) (any, error) { return f.run(ctx, in) }

func codeHandler(c Code) http.Handler {
	return Handler(Deps{Backend: echo(), Code: c, Info: func() Info { return Info{PID: 42, Gen: "133", AppVersion: "1.2.3", Owner: "app"} }, Logf: func(string, ...any) {}})
}

const goodCase = "abcdefghijklmnopqrstuvwxyz"

func TestCodeTypesAnswersWhatTheBrokerGives(t *testing.T) {
	h := codeHandler(fakeCode{types: func(context.Context) (any, error) {
		return map[string]any{"namespace": "unravis", "dts": "declare namespace unravis {}", "schemaSha256": "abc", "limits": map[string]any{"maxSourceBytes": 262144}}, nil
	}})
	r := op(t, h, "code.types", "")
	if !r.OK || r.Data["namespace"] != "unravis" || r.Data["schemaSha256"] != "abc" || r.Meta["coverage"] != "complete" {
		t.Fatalf("%+v", r)
	}
	if r := op(t, h, "code.types", `{"x":1}`); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
		t.Errorf("args are refused: %+v", r)
	}
}

func TestCodeWithoutABrokerIsUnavailable(t *testing.T) {
	h := newHandler(echo())
	for _, c := range []struct{ name, args string }{{"code.types", ""}, {"code.run", `{"code":"1"}`}} {
		r := op(t, h, c.name, c.args)
		if r.OK || r.Error.Code != "ENDPOINT_UNAVAILABLE" || r.Error.Message == "" || r.Error.Action == "" {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

func TestCodeTypesFailureIsInternalAndPlain(t *testing.T) {
	h := codeHandler(fakeCode{types: func(context.Context) (any, error) { return nil, errors.New("C:\\secret\\path failed") }})
	r := op(t, h, "code.types", "")
	if r.OK || r.Error.Code != "INTERNAL" || strings.Contains(r.Raw, "secret") {
		t.Errorf("%s", r.Raw)
	}
}

func TestCodeRunSuccessCarriesTheResult(t *testing.T) {
	var got CodeRunInput
	h := codeHandler(fakeCode{run: func(_ context.Context, in CodeRunInput) (any, error) {
		got = in
		return map[string]any{"v": 1, "runId": "r1", "status": "ok", "result": 42, "identifiers": "hidden", "stats": map[string]any{"calls": 0, "truncated": false}}, nil
	}})
	r := op(t, h, "code.run", `{"code":"return 42","caseId":"`+goodCase+`","timeoutMs":5000,"reason":"why","allowIdentifiers":false}`)
	if !r.OK || r.Data["runId"] != "r1" || r.Data["status"] != "ok" || r.Meta["identifiers"] != "hidden" || r.Meta["coverage"] != "complete" {
		t.Fatalf("%+v", r)
	}
	if got != (CodeRunInput{Code: "return 42", CaseID: goodCase, TimeoutMS: 5000, Reason: "why"}) {
		t.Errorf("the broker was asked for %+v", got)
	}
}

// A run whose result or logs were cut says so in meta, and a run that shows identifiers says so.
func TestCodeRunMetaFollowsTheRun(t *testing.T) {
	h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) {
		return map[string]any{"status": "ok", "identifiers": "shown", "stats": map[string]any{"truncated": true, "logsTruncated": true}}, nil
	}})
	r := op(t, h, "code.run", `{"code":"1","allowIdentifiers":true}`)
	if !r.OK || r.Meta["identifiers"] != "shown" || r.Meta["coverage"] != "partial" || r.Meta["truncated"] != "result" {
		t.Fatalf("%+v", r)
	}
	if w, _ := r.Meta["warnings"].([]any); len(w) != 2 {
		t.Errorf("warnings: %v", r.Meta["warnings"])
	}
}

// A script that ran and failed was accepted and recorded: the owner answers it as a result, and the command line reads the status.
func TestCodeRunFailedScriptIsStillAResult(t *testing.T) {
	h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) {
		return map[string]any{"status": "error", "error": map[string]any{"code": "script_error", "message": "boom"}}, nil
	}})
	r := op(t, h, "code.run", `{"code":"throw 1"}`)
	if !r.OK || r.Data["status"] != "error" {
		t.Fatalf("%+v", r)
	}
}

func TestCodeRunRefusalsMapToCommandLineCodes(t *testing.T) {
	for code, want := range map[string]string{
		"code_mode_off": "ENDPOINT_UNAVAILABLE", "sandbox_unavailable": "ENDPOINT_UNAVAILABLE", "denied": "PERMISSION_DENIED", "no_case": "NOT_FOUND",
		clicore.CodeModeManyCases: "INVALID_ARGUMENT", "busy": "OWNER_BUSY", "source_too_large": "REQUEST_TOO_LARGE", "syntax_error": "INVALID_ARGUMENT",
		"bad_request": "INVALID_ARGUMENT", "audit_failed": "INTERNAL", "internal_error": "INTERNAL", "a_code_from_the_future": "INTERNAL",
	} {
		h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) { return nil, &CodeRefusal{Code: code} }})
		r := op(t, h, "code.run", `{"code":"1"}`)
		if r.OK || r.Error.Code != want || r.Error.Message == "" {
			t.Errorf("%s: %+v", code, r)
		}
	}
	// the refusals a person can fix say what to do next
	for _, code := range []string{"code_mode_off", "sandbox_unavailable", "denied", "no_case", clicore.CodeModeManyCases, "busy", "source_too_large", "syntax_error"} {
		h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) { return nil, &CodeRefusal{Code: code} }})
		if r := op(t, h, "code.run", `{"code":"1"}`); r.Error.Action == "" {
			t.Errorf("%s says nothing about what to do next: %+v", code, r)
		}
	}
	// busy is the one refusal that passes by itself
	h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) { return nil, &CodeRefusal{Code: "busy"} }})
	if r := op(t, h, "code.run", `{"code":"1"}`); !strings.Contains(r.Raw, `"retryable":true`) {
		t.Errorf("busy is retryable: %s", r.Raw)
	}
}

// An error of the broker that is not a refusal is internal, and its text never reaches the command line.
func TestCodeRunOtherErrorsAreInternalAndPlain(t *testing.T) {
	h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) {
		return nil, errors.New("open C:\\Users\\x\\case.db: boom")
	}})
	r := op(t, h, "code.run", `{"code":"1"}`)
	if r.OK || r.Error.Code != "INTERNAL" || strings.Contains(r.Raw, "case.db") {
		t.Errorf("%s", r.Raw)
	}
}

func TestCodeRunChecksTheRequestBeforeTheBroker(t *testing.T) {
	called := false
	h := codeHandler(fakeCode{run: func(context.Context, CodeRunInput) (any, error) { called = true; return map[string]any{}, nil }})
	big := strings.Repeat("a", clicore.MaxCodeSource+1)
	for name, c := range map[string]struct{ args, want string }{
		"no script":        {`{}`, "INVALID_ARGUMENT"},
		"empty script":     {`{"code":""}`, "INVALID_ARGUMENT"},
		"script too big":   {`{"code":"` + big + `"}`, "REQUEST_TOO_LARGE"},
		"bad case id":      {`{"code":"1","caseId":"../x"}`, "INVALID_ARGUMENT"},
		"negative timeout": {`{"code":"1","timeoutMs":-1}`, "INVALID_ARGUMENT"},
		"timeout too long": {`{"code":"1","timeoutMs":120001}`, "INVALID_ARGUMENT"},
		"long reason":      {`{"code":"1","reason":"` + strings.Repeat("r", 501) + `"}`, "INVALID_ARGUMENT"},
		"unknown field":    {`{"code":"1","surface":"mcp"}`, "INVALID_ARGUMENT"},
		"scoped":           {`{"code":"1","scoped":true}`, "INVALID_ARGUMENT"},
	} {
		r := op(t, h, "code.run", c.args)
		if r.OK || r.Error.Code != c.want {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if called {
		t.Error("a request that is not valid reached the broker")
	}
}

// The deadline of the call ends the run: the broker is given the call's own context, so it ends the script, and the answer is TIMEOUT. A
// cancel from the command line ends it the same way and the answer is CANCELED.
func TestCodeRunDeadlineAndCancel(t *testing.T) {
	started := make(chan struct{}, 2)
	h := codeHandler(fakeCode{run: func(ctx context.Context, _ CodeRunInput) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		return map[string]any{"status": "cancelled", "error": map[string]any{"code": "cancelled"}}, nil // the broker's answer to an ended context
	}})
	r := post(t, h, "/cli/v1/op", `{"op":"code.run","args":{"code":"while(1){}"},"requestId":"`+rid+`","deadlineMs":50}`)
	if r.OK || r.Error.Code != "TIMEOUT" {
		t.Errorf("deadline: %s", r.Raw)
	}
	<-started

	done := make(chan reply, 1)
	go func() {
		done <- post(t, h, "/cli/v1/op", `{"op":"code.run","args":{"code":"while(1){}"},"requestId":"`+rid+`","callId":"0123456789abcdef"}`)
	}()
	<-started
	if c := post(t, h, "/cli/v1/cancel", `{"callId":"0123456789abcdef"}`); c.Status != 200 || !strings.Contains(c.Raw, "cancelling") {
		t.Fatalf("cancel: %+v", c)
	}
	select {
	case r := <-done:
		if r.OK || r.Error.Code != "CANCELED" {
			t.Errorf("cancel mid-run: %s", r.Raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled run did not end")
	}
}

// code.run and code.types are for the human command line only: nothing in a request can name another principal, and the page of a browser
// is never ours. code.replay is not built.
func TestCodeIsOnlyForTheHumanCommandLine(t *testing.T) {
	called := false
	h := codeHandler(fakeCode{
		types: func(context.Context) (any, error) { called = true; return map[string]any{}, nil },
		run:   func(context.Context, CodeRunInput) (any, error) { called = true; return map[string]any{}, nil },
	})
	for _, body := range []string{
		`{"op":"code.run","args":{"code":"1"},"requestId":"` + rid + `","principal":"cli-ci"}`,
		`{"op":"code.types","args":{},"requestId":"` + rid + `","principal":"mcp"}`,
		`{"op":"code.replay","args":{},"requestId":"` + rid + `"}`,
	} {
		if r := post(t, h, "/cli/v1/op", body); r.OK {
			t.Errorf("%s: %+v", body, r)
		}
	}
	if called {
		t.Error("a refused request reached the broker")
	}
	r := httptest.NewRequest("POST", "/cli/v1/op", strings.NewReader(`{"op":"code.run","args":{"code":"1"},"requestId":"`+rid+`"}`))
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || called {
		t.Errorf("a page with an Origin: %d", w.Code)
	}
}
