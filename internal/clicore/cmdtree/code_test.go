package cmdtree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/clisrv"
)

// stubCode is the code-mode broker of an owner in these tests: it answers from functions and keeps what it was asked. The real broker is
// reached in internal/codeapi (TestCLIRunsThroughTheBroker) and in cmd/audit-explorer.
type stubCode struct {
	mu    sync.Mutex
	got   []clisrv.CodeRunInput
	types func(ctx context.Context) (any, error)
	run   func(ctx context.Context, in clisrv.CodeRunInput) (any, error)
}

func (s *stubCode) Types(ctx context.Context) (any, error) { return s.types(ctx) }
func (s *stubCode) Run(ctx context.Context, in clisrv.CodeRunInput) (any, error) {
	s.mu.Lock()
	s.got = append(s.got, in)
	s.mu.Unlock()
	return s.run(ctx, in)
}
func (s *stubCode) asked() []clisrv.CodeRunInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]clisrv.CodeRunInput(nil), s.got...)
}

const aCaseID = "abcdefghijklmnopqrstuvwxyz"

func okRun(result any) func(context.Context, clisrv.CodeRunInput) (any, error) {
	return func(context.Context, clisrv.CodeRunInput) (any, error) {
		return map[string]any{"v": 1, "runId": "run-1", "status": "ok", "result": result, "logs": []string{"hello from the script"}, "identifiers": "hidden",
			"stats": map[string]any{"calls": 2, "durationMs": 40, "scriptMs": 12}}, nil
	}
}

func typesDoc() map[string]any {
	return map[string]any{"namespace": "unravis", "dts": "declare namespace unravis {\n  function audit(): void;\n}\n", "schemaSha256": strings.Repeat("ab", 32),
		"limits": map[string]any{"maxSourceBytes": 262144, "defaultTimeoutMs": 30000, "minTimeoutMs": 1000, "maxTimeoutMs": 120000, "runCapSeconds": 150,
			"maxCalls": 200, "maxInFlight": 4, "maxResultBytes": 1048576, "maxLogBytes": 65536, "maxConcurrentRuns": 1}}
}

func TestCodeTypesTableAndJSON(t *testing.T) {
	b := &stubCode{types: func(context.Context) (any, error) { return typesDoc(), nil }}
	o := newOwner(t, nil, withCode(b))
	d := data(t, o.run("code", "types", "-o", "json"))
	if d["namespace"] != "unravis" || d["schemaSha256"] != strings.Repeat("ab", 32) || !strings.Contains(d["dts"].(string), "function audit") ||
		d["limits"].(map[string]any)["maxSourceBytes"] != float64(262144) {
		t.Errorf("json: %v", d)
	}
	if seen := o.opsSeen(); seen[len(seen)-1]["op"] != "code.types" {
		t.Errorf("op: %v", seen[len(seen)-1])
	}
	tb := o.run("code", "types")
	for _, want := range []string{"tool list hash (sha256)", strings.Repeat("ab", 32), "largest script (bytes)", "262144", "script time limit, longest (ms)", "120000",
		"Typings (TypeScript):", "  declare namespace unravis {", "    function audit(): void;"} {
		if !strings.Contains(tb.out, want) {
			t.Errorf("the table lacks %q:\n%s", want, tb.out)
		}
	}
	if tb.code != 0 || tb.err != "" {
		t.Errorf("table: %+v", tb)
	}
	// arguments are not taken
	if r := o.run("code", "types", "extra", "--json"); r.code != 2 {
		t.Errorf("extra argument: %+v", r)
	}
}

func TestCodeWithoutABrokerIsUnavailable(t *testing.T) {
	o := newOwner(t, nil)
	for _, args := range [][]string{{"code", "types", "--json"}, {"code", "run", "--code", "1", "--json"}} {
		r := o.run(args...)
		if r.code != clicore.ExitDependency || errCode(t, r) != "ENDPOINT_UNAVAILABLE" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestCodeRunTakesAFileCodeOrTheInput(t *testing.T) {
	b := &stubCode{run: okRun(map[string]any{"rows": 3})}
	o := newOwner(t, nil, withCode(b))
	file := filepath.Join(t.TempDir(), "script.ts")
	if err := os.WriteFile(file, []byte("return { rows: 3 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := data(t, o.run("code", "run", file, "-o", "json"))
	if d["status"] != "ok" || d["runId"] != "run-1" || d["result"].(map[string]any)["rows"] != float64(3) {
		t.Errorf("file: %v", d)
	}
	if a := lastArgs(o); len(a) != 1 || a["code"] != "return { rows: 3 }\n" {
		t.Errorf("only the script is sent when nothing else was asked: %v", a)
	}
	data(t, o.run("code", "run", "--code", "return 1", "--json"))
	r := runOn(t, o.attacher(), "return 2 // from the input", nil, "code", "run", "-", "--json")
	data(t, r)
	got := b.asked()
	if len(got) != 3 || got[0].Code != "return { rows: 3 }\n" || got[1].Code != "return 1" || got[2].Code != "return 2 // from the input" {
		t.Errorf("the broker was asked for %+v", got)
	}
	// a script in UTF-16 with a byte order mark (what PowerShell 5.1 writes with ">") is read
	u16 := []byte{0xFF, 0xFE}
	for _, c := range "return 3" {
		u16 = append(u16, byte(c), 0)
	}
	f16 := filepath.Join(t.TempDir(), "u16.ts")
	if err := os.WriteFile(f16, u16, 0o600); err != nil {
		t.Fatal(err)
	}
	data(t, o.run("code", "run", f16, "--json"))
	if got := b.asked(); got[len(got)-1].Code != "return 3" {
		t.Errorf("utf-16: %+v", got[len(got)-1])
	}
}

func TestCodeRunTable(t *testing.T) {
	b := &stubCode{run: okRun([]any{1, 2})}
	o := newOwner(t, nil, withCode(b))
	r := o.run("code", "run", "--code", "return [1,2]")
	if r.code != 0 || r.err != "" {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"run-1", "tool calls", "time (ms)", "40 (script 12)", "identifiers", "hidden", "Log:", "  hello from the script", "Result:"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("the table lacks %q:\n%s", want, r.out)
		}
	}
	nothing := &stubCode{run: okRun(nil)}
	o2 := newOwner(t, nil, withCode(nothing))
	if r := o2.run("code", "run", "--code", "1"); r.code != 0 || !strings.Contains(r.out, "The script returned no value.") {
		t.Errorf("no value: %+v", r)
	}
}

func TestCodeRunOptionsBecomeArguments(t *testing.T) {
	b := &stubCode{run: okRun(1)}
	o := newOwner(t, nil, withCode(b))
	data(t, o.run("code", "run", "--code", "1", "--case", aCaseID, "--timeout-ms", "5000", "--reason", "why I ran it", "--allow-identifiers", "--json"))
	if got := b.asked(); len(got) != 1 || got[0] != (clisrv.CodeRunInput{Code: "1", CaseID: aCaseID, TimeoutMS: 5000, Reason: "why I ran it", AllowIdentifiers: true}) {
		t.Errorf("the broker was asked for %+v", got)
	}
	// the same as one JSON object
	r := runOn(t, o.attacher(), `{"code":"1","caseId":"`+aCaseID+`","timeoutMs":5000,"reason":"why I ran it","allowIdentifiers":true}`, nil, "code", "run", "--request", "-", "--json")
	data(t, r)
	if got := b.asked(); len(got) != 2 || got[0] != got[1] {
		t.Errorf("--request asked for something else: %+v", got)
	}
}

// What can be seen to be wrong on this side is a usage error at once, and nothing is sent.
func TestCodeRunChecksBeforeSending(t *testing.T) {
	b := &stubCode{run: okRun(1)}
	o := newOwner(t, nil, withCode(b))
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.ts")
	if err := os.WriteFile(bad, []byte{'a', 0xff, 0xfe, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.ts")
	if err := os.WriteFile(empty, []byte("  \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(dir, "huge.ts")
	if err := os.WriteFile(huge, []byte(strings.Repeat("a", clicore.MaxCodeSource+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	exact := filepath.Join(dir, "exact.ts")
	if err := os.WriteFile(exact, []byte(strings.Repeat("a", clicore.MaxCodeSource)), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(o.opsSeen())
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no script":         {[]string{"code", "run"}, "INVALID_ARGUMENT"},
		"file and code":     {[]string{"code", "run", empty, "--code", "1"}, "INVALID_ARGUMENT"},
		"two files":         {[]string{"code", "run", empty, empty}, "INVALID_ARGUMENT"},
		"missing file":      {[]string{"code", "run", filepath.Join(dir, "none.ts")}, "INVALID_ARGUMENT"},
		"a folder":          {[]string{"code", "run", dir}, "INVALID_ARGUMENT"},
		"not text":          {[]string{"code", "run", bad}, "INVALID_ARGUMENT"},
		"empty file":        {[]string{"code", "run", empty}, "INVALID_ARGUMENT"},
		"empty code":        {[]string{"code", "run", "--code", " "}, "INVALID_ARGUMENT"},
		"too large":         {[]string{"code", "run", huge}, "REQUEST_TOO_LARGE"},
		"bad case id":       {[]string{"code", "run", "--code", "1", "--case", "../x"}, "INVALID_ARGUMENT"},
		"empty case id":     {[]string{"code", "run", "--code", "1", "--case", ""}, "INVALID_ARGUMENT"},
		"timeout too short": {[]string{"code", "run", "--code", "1", "--timeout-ms", "999"}, "INVALID_ARGUMENT"},
		"timeout too long":  {[]string{"code", "run", "--code", "1", "--timeout-ms", "120001"}, "INVALID_ARGUMENT"},
		"timeout zero":      {[]string{"code", "run", "--code", "1", "--timeout-ms", "0"}, "INVALID_ARGUMENT"},
		"long reason":       {[]string{"code", "run", "--code", "1", "--reason", strings.Repeat("r", 501)}, "INVALID_ARGUMENT"},
		"file and request":  {[]string{"code", "run", empty, "--request", "-"}, "INVALID_ARGUMENT"},
		"request and flag":  {[]string{"code", "run", "--request", "-", "--case", aCaseID}, "INVALID_ARGUMENT"},
	} {
		r := runOn(t, o.attacher(), `{"code":"1"}`, nil, append(c.args, "--json")...)
		if r.code != clicore.ExitUsage || errCode(t, r) != c.want {
			t.Errorf("%s: %+v", name, r)
		}
		if strings.Contains(r.err, "../x") {
			t.Errorf("%s: what the person typed is echoed: %s", name, r.err)
		}
	}
	if n := len(o.opsSeen()); n != before || len(b.asked()) != 0 {
		t.Errorf("a request that is not valid was sent (%d ops, %d runs)", n-before, len(b.asked()))
	}
	// the largest script is allowed
	data(t, o.run("code", "run", exact, "--json"))
	// the empty-script message says what to do
	r := o.run("code", "run")
	if !strings.Contains(r.err, "file") || !strings.Contains(r.err, "--code") {
		t.Errorf("no next step: %s", r.err)
	}
}

func TestCodeRunHasNoJSONLines(t *testing.T) {
	o := newOwner(t, nil, withCode(&stubCode{run: okRun(1), types: func(context.Context) (any, error) { return typesDoc(), nil }}))
	for _, args := range [][]string{{"code", "run", "--code", "1"}, {"code", "types"}} {
		r := o.run(append(args, "-o", "jsonl")...)
		if r.code != clicore.ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

// A refusal turns the run away before anything is written: nothing is printed, the error says what happened and what to do, and the exit
// code is the one of the code (3 for what must be switched on, 4 for a denied option, 5 for a case that is not there, 2 for a usage error).
func TestCodeRunRefusalsGiveExitCodes(t *testing.T) {
	for refusal, want := range map[string]struct {
		code string
		exit int
	}{
		"code_mode_off":           {"ENDPOINT_UNAVAILABLE", clicore.ExitDependency},
		"sandbox_unavailable":     {"ENDPOINT_UNAVAILABLE", clicore.ExitDependency},
		"denied":                  {"PERMISSION_DENIED", clicore.ExitDenied},
		"no_case":                 {"NOT_FOUND", clicore.ExitGone},
		clicore.CodeModeManyCases: {"INVALID_ARGUMENT", clicore.ExitUsage},
		"busy":                    {"OWNER_BUSY", clicore.ExitDependency},
		"source_too_large":        {"REQUEST_TOO_LARGE", clicore.ExitUsage},
		"syntax_error":            {"INVALID_ARGUMENT", clicore.ExitUsage},
		"audit_failed":            {"INTERNAL", clicore.ExitInternal},
	} {
		ref := refusal
		o := newOwner(t, nil, withCode(&stubCode{run: func(context.Context, clisrv.CodeRunInput) (any, error) { return nil, &clisrv.CodeRefusal{Code: ref} }}))
		r := o.run("code", "run", "--code", "1", "--json")
		if r.code != want.exit || errCode(t, r) != want.code || r.out != "" {
			t.Errorf("%s: %+v", refusal, r)
			continue
		}
		e := decode(t, r.err)["error"].(map[string]any)
		if e["message"] == "" || e["message"] == nil {
			t.Errorf("%s: no message: %v", refusal, e)
		}
		if refusal != "audit_failed" && (e["action"] == nil || e["action"] == "") {
			t.Errorf("%s: no next step: %v", refusal, e)
		}
		if tb := o.run("code", "run", "--code", "1"); tb.code != want.exit || tb.out != "" || !strings.Contains(tb.err, want.code) {
			t.Errorf("%s: table: %+v", refusal, tb)
		}
	}
}

// A script that was accepted and failed is an answer: its result is printed, and the exit code then says how it ended.
func TestCodeRunFailedScriptPrintsAndExitsNonzero(t *testing.T) {
	failed := func(status, code string) *stubCode {
		return &stubCode{run: func(context.Context, clisrv.CodeRunInput) (any, error) {
			return map[string]any{"v": 1, "runId": "run-9", "status": status, "logs": []string{"got this far"}, "identifiers": "hidden",
				"stats": map[string]any{"calls": 1}, "error": map[string]any{"code": code, "message": "boom"}}, nil
		}}
	}
	for _, c := range []struct {
		status, code, want string
		exit               int
	}{
		{"error", "script_error", "INVALID_ARGUMENT", clicore.ExitUsage},
		{"timeout", "wall", "TIMEOUT", clicore.ExitStopped},
		{"cancelled", "cancelled", "CANCELED", clicore.ExitStopped},
		{"crashed", "worker_crashed", "INTERNAL", clicore.ExitInternal},
		{"limit_exceeded", "call_budget", "BUDGET_EXCEEDED", clicore.ExitDependency},
		{"error", "a_code_from_the_future", "INTERNAL", clicore.ExitInternal},
	} {
		o := newOwner(t, nil, withCode(failed(c.status, c.code)))
		r := o.run("code", "run", "--code", "1", "--json")
		// unlike a refusal, the answer is on stdout (errCode wants it empty); the error is on stderr
		if e, _ := decode(t, r.err)["error"].(map[string]any); r.code != c.exit || e == nil || e["code"] != c.want {
			t.Errorf("%s/%s: %+v", c.status, c.code, r)
			continue
		}
		if d := decode(t, r.out)["data"].(map[string]any); d["status"] != c.status || d["runId"] != "run-9" {
			t.Errorf("%s/%s: the result was not printed: %s", c.status, c.code, r.out)
		}
		tb := o.run("code", "run", "--code", "1")
		if tb.code != c.exit || !strings.Contains(tb.out, "run-9") || !strings.Contains(tb.out, "did not finish well") || !strings.Contains(tb.out, "got this far") {
			t.Errorf("%s/%s: table: %+v", c.status, c.code, tb)
		}
	}
	// a failure with no error object still never exits 0
	odd := &stubCode{run: func(context.Context, clisrv.CodeRunInput) (any, error) { return map[string]any{"status": "error"}, nil }}
	if r := newOwner(t, nil, withCode(odd)).run("code", "run", "--code", "1", "--json"); r.code != clicore.ExitInternal {
		t.Errorf("no error object: %+v", r)
	}
}

// Ctrl+C ends the run: the owner is told to cancel, the broker's context ends, and the command exits 6 with CANCELED.
func TestCodeRunCancelEndsTheRun(t *testing.T) {
	started := make(chan struct{})
	b := &stubCode{run: func(ctx context.Context, _ clisrv.CodeRunInput) (any, error) {
		close(started)
		<-ctx.Done()
		return map[string]any{"status": "cancelled", "error": map[string]any{"code": "cancelled", "message": "stopped"}}, nil
	}}
	o := newOwner(t, nil, withCode(b))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan result, 1)
	go func() { done <- runCtx(t, ctx, o.attacher(), "", nil, "code", "run", "--code", "while(1){}", "--json") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not start")
	}
	cancel()
	select {
	case r := <-done:
		if r.code != clicore.ExitStopped || errCode(t, r) != "CANCELED" {
			t.Errorf("cancel: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled run did not end")
	}
}

// --timeout ends the command, and the run with it.
func TestCodeRunTimeoutEndsTheRun(t *testing.T) {
	b := &stubCode{run: func(ctx context.Context, _ clisrv.CodeRunInput) (any, error) {
		<-ctx.Done()
		return map[string]any{"status": "cancelled", "error": map[string]any{"code": "cancelled"}}, nil
	}}
	o := newOwner(t, nil, withCode(b))
	r := o.run("code", "run", "--code", "while(1){}", "--timeout", "300ms", "--json")
	if r.code != clicore.ExitStopped || errCode(t, r) != "TIMEOUT" {
		t.Errorf("timeout: %+v", r)
	}
}

// code.run and code.types are described by `unravis schema`, and every option of the command is a field of the schema.
func TestCodeSchemasListedAndMatchTheFlags(t *testing.T) {
	r := run(t, nil, "schema", "-o", "json")
	var names []string
	for _, o := range decode(t, r.out)["data"].(map[string]any)["operations"].([]any) {
		names = append(names, o.(map[string]any)["operation"].(string))
	}
	for _, want := range []string{"code.run", "code.types"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("%s is not listed in %v", want, names)
		}
	}
	d := decode(t, run(t, nil, "schema", "code.run", "-o", "json").out)["data"].(map[string]any)
	props := d["input"].(map[string]any)["properties"].(map[string]any)
	for field, flag := range map[string]string{"code": "--code", "caseId": "--case", "timeoutMs": "--timeout-ms", "reason": "--reason", "allowIdentifiers": "--allow-identifiers"} {
		if props[field] == nil {
			t.Errorf("the schema lacks %s (%s)", field, flag)
		}
	}
	if len(props) != 5 {
		t.Errorf("the schema has fields no option sets: %v", props)
	}
	if req, _ := d["input"].(map[string]any)["required"].([]any); len(req) != 1 || req[0] != "code" {
		t.Errorf("required: %v", req)
	}
	if run(t, nil, "code", "run", "--help").code != 0 {
		t.Error("help")
	}
}
