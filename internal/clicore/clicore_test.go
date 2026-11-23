package clicore

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExitMapComplete(t *testing.T) {
	seen := map[Code]bool{}
	for _, c := range Codes() {
		if seen[c] {
			t.Errorf("%s is listed twice", c)
		}
		seen[c] = true
		if _, ok := codeExit[c]; !ok {
			t.Errorf("%s has no exit code", c)
		}
		if n := ExitCode(c); n < 1 || n > 7 || n == 0 {
			t.Errorf("%s: exit %d is outside 1 to 7", c, n)
		}
	}
	for c := range codeExit {
		if !seen[c] {
			t.Errorf("%s has an exit code but is not in Codes()", c)
		}
	}
	// The table of the spec, spot checked by name.
	for c, want := range map[Code]int{CodeInternal: 1, CodeInvalidArgument: 2, CodeLegacyOption: 2, CodeAppNotRunning: 3, CodeOwnerBusy: 3,
		CodeCaseLocked: 4, CodePermissionDenied: 4, CodeResultExpired: 5, CodeVersionIncompatible: 5, CodeCanceled: 6, CodeTimeout: 6, CodeIncomplete: 7} {
		if got := ExitCode(c); got != want {
			t.Errorf("%s: exit %d, want %d", c, got, want)
		}
	}
	if ExitCode("NO_SUCH_CODE") != ExitInternal || ExitCode("") != ExitInternal {
		t.Error("an unknown code must not exit 0")
	}
}

func TestRetryableOnlyBeforeWorkStarted(t *testing.T) {
	yes := []Code{CodeAppNotRunning, CodeOwnerBusy, CodeUpdateInProgress, CodeBudgetExceeded, CodeUpstreamUnavailable}
	isYes := map[Code]bool{}
	for _, c := range yes {
		isYes[c] = true
		if !Retryable(c, false) {
			t.Errorf("%s should be retryable when no work started", c)
		}
		if Retryable(c, true) {
			t.Errorf("%s must not be retryable after work started (a repeat would run the query again)", c)
		}
	}
	for _, c := range Codes() {
		if !isYes[c] && (Retryable(c, false) || Retryable(c, true)) {
			t.Errorf("%s must never be retryable", c)
		}
	}
	// Defaults of the two constructors: an owner that died mid-call is not retryable; a refusal is.
	if !NewError(CodeOwnerBusy, "m", "").Retryable || !NewError(CodeBudgetExceeded, "m", "").Retryable {
		t.Error("a refusal before work is retryable")
	}
	if NewErrorAfterWork(CodeUpstreamUnavailable, "m", "").Retryable || NewErrorAfterWork(CodeOwnerBusy, "m", "").Retryable {
		t.Error("an error after work started is never retryable")
	}
}

// TestRetryableNeverTrustsTheOwner: a wrong "retryable":true from the owner does not reach the output.
func TestRetryableNeverTrustsTheOwner(t *testing.T) {
	e := &Error{Code: CodeResultExpired, Message: "m", Retryable: true}
	b, err := MarshalError("audit.groups", "req_0123456789abcdef", e)
	if err != nil || !strings.Contains(string(b), `"retryable":false`) {
		t.Errorf("%s %v", b, err)
	}
	if !e.Retryable {
		t.Error("the caller's value must not be changed")
	}
}

func TestExitMapGolden(t *testing.T) {
	golden := map[Code]int{
		CodeInternal:        1,
		CodeInvalidArgument: 2, CodeUnknownOperation: 2, CodeUnsupportedFormat: 2, CodeRequestTooLarge: 2, CodeLegacyOption: 2, CodeLiveCaseRefused: 2,
		CodeAppNotRunning: 3, CodeEndpointUnavailable: 3, CodeEndpointUntrusted: 3, CodeStaleLocator: 3, CodeOwnerBusy: 3, CodeUpdateInProgress: 3,
		CodeUpstreamUnavailable: 3, CodeBudgetExceeded: 3,
		CodePermissionDenied: 4, CodeApprovalRequired: 4, CodeCaseLocked: 4, CodeElevationRefused: 4,
		CodeResultExpired: 5, CodeNotFound: 5, CodeResultTooLarge: 5, CodeVersionIncompatible: 5,
		CodeCanceled: 6, CodeTimeout: 6,
		CodeIncomplete: 7,
	}
	if len(golden) != len(Codes()) {
		t.Errorf("%d codes in the golden table, %d in Codes()", len(golden), len(Codes()))
	}
	for c, want := range golden {
		if got := ExitCode(c); got != want {
			t.Errorf("%s: exit %d, want %d", c, got, want)
		}
	}
}

func TestWriteErrorSanitizesCodeAndIDs(t *testing.T) {
	var b bytes.Buffer
	WriteError(&b, ModeTable, "x", "req_0123456789abcdef", &Error{Code: "X\x1b[2J", Message: "m"})
	if strings.ContainsAny(b.String(), "\x1b") || !strings.Contains(b.String(), "Code: INTERNAL") {
		t.Errorf("a code outside the contract: %q", b.String())
	}
	b.Reset()
	if n := WriteError(&b, ModeJSON, "bad op\x1b", "evil\x1b[2J", &Error{Code: "NOPE", Message: "m"}); n != 1 {
		t.Errorf("exit %d", n)
	}
	if !json.Valid(b.Bytes()) || strings.Contains(b.String(), "NOPE") || strings.Contains(b.String(), "evil") || !strings.Contains(b.String(), `"operation":"unknown"`) {
		t.Errorf("json: %q", b.String())
	}
	b.Reset()
	WriteError(&b, ModeTable, "x", "evil\x1b[2J", NewError(CodeInternal, "m", ""))
	if strings.Contains(b.String(), "\x1b") || !regexp.MustCompile(`Request: req_[0-9a-f]{16}\n`).MatchString(b.String()) {
		t.Errorf("table: %q", b.String())
	}
}

func TestMappingsFromOtherAreas(t *testing.T) {
	for _, tc := range []struct {
		code    string
		pending bool
		backend string
		want    Code
	}{
		{"invalid_arguments", false, "", CodeInvalidArgument},
		{"permission_required", true, "", CodeApprovalRequired},
		{"permission_required", false, "", CodePermissionDenied},
		{"result_expired", false, "", CodeResultExpired},
		{"query_budget_exceeded", false, "", CodeBudgetExceeded},
		{"source_unavailable", false, "", CodeUpstreamUnavailable},
		{"cancelled", false, "", CodeCanceled},
		{"deadline_exceeded", false, "", CodeTimeout},
		{"result_too_large", false, "", CodeResultTooLarge},
		{"backend_unavailable", false, "", CodeAppNotRunning},
		{"backend_unavailable", false, "stale", CodeStaleLocator},
		{"backend_unavailable", false, "untrusted", CodeEndpointUntrusted},
		{"internal", false, "", CodeInternal},
		{"something_new", false, "", CodeInternal},
	} {
		if got := FromMCP(tc.code, tc.pending, tc.backend); got != tc.want {
			t.Errorf("%+v: got %s", tc, got)
		}
	}
	for status, want := range map[int]Code{423: CodeCaseLocked, 404: CodeNotFound, 403: CodePermissionDenied, 429: CodeOwnerBusy, 503: CodeEndpointUnavailable, 400: CodeInvalidArgument, 413: CodeRequestTooLarge, 500: CodeInternal, 302: CodeInternal} {
		if got, ok := FromHTTPStatus(status); !ok || got != want {
			t.Errorf("HTTP %d: %s %v", status, got, ok)
		}
	}
	if _, ok := FromHTTPStatus(200); ok {
		t.Error("200 is not an error")
	}
	for exit, want := range map[int]Code{11: CodeElevationRefused, 12: CodeOwnerBusy, 13: CodeUpstreamUnavailable, 1: CodeInternal, 10: CodeInternal} {
		if got := FromSidecarExit(exit); got != want {
			t.Errorf("sidecar exit %d: %s", exit, got)
		}
	}
}

func TestAsciiJSON(t *testing.T) {
	v := map[string]any{"s": "caf\u00e9 \u2028 \U0001F600 <a&b> \x7f \"q\" \\", "bad": "x\xffy", "n": 1}
	b, err := ASCIIJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range b {
		if c >= 0x80 {
			t.Fatalf("a byte above 0x7f in %q", b)
		}
	}
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.Count(b, []byte("\n")) != 1 {
		t.Fatalf("must be one line with one newline: %q", b)
	}
	if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		t.Fatal("a byte order mark")
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("not valid JSON: %v: %q", err, b)
	}
	if back["s"] != "caf\u00e9 \u2028 \U0001F600 <a&b> \x7f \"q\" \\" {
		t.Errorf("did not survive the round trip: %q", back["s"])
	}
	if back["bad"] != "x\uFFFDy" {
		t.Errorf("invalid UTF-8 should become U+FFFD: %q", back["bad"])
	}
	if !strings.Contains(string(b), `\ud83d\ude00`) || !strings.Contains(string(b), `<a&b>`) {
		t.Errorf("surrogate pair or HTML characters: %q", b)
	}
}

func TestEnvelopes(t *testing.T) {
	b, err := MarshalSuccess("audit.compare", "req_0123456789abcdef", map[string]any{"resultId": "x", "nextCursor": nil}, Meta{Coverage: "complete", Identifiers: "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schemaVersion":"1","operation":"audit.compare","requestId":"req_0123456789abcdef","data":{"nextCursor":null,"resultId":"x"},"meta":{"coverage":"complete","identifiers":"hidden","truncated":null,"warnings":[]}}` + "\n"
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	b, err = MarshalError("audit.groups", "req_0123456789abcdef", NewError(CodeResultExpired, "The comparison result has expired.", "Run audit compare again."))
	if err != nil {
		t.Fatal(err)
	}
	want = `{"schemaVersion":"1","operation":"audit.groups","requestId":"req_0123456789abcdef","error":{"code":"RESULT_EXPIRED","message":"The comparison result has expired.","retryable":false,"action":"Run audit compare again."}}` + "\n"
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	if _, err := MarshalError("x", "r", nil); err == nil {
		t.Error("no error given, no output")
	}
}

func TestRequestID(t *testing.T) {
	re := regexp.MustCompile(`^req_[0-9a-f]{16}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewRequestID()
		if !re.MatchString(id) || seen[id] {
			t.Fatalf("bad or repeated id %q", id)
		}
		seen[id] = true
	}
}

func TestWriteErrorStreamsAndExit(t *testing.T) {
	e := NewError(CodeAppNotRunning, "Unravis is not running.", "Start it, or run 'unravis app start --headless'.")
	var js, tb bytes.Buffer
	if n := WriteError(&js, ModeJSON, "app.status", "req_0123456789abcdef", e); n != 3 {
		t.Errorf("exit %d", n)
	}
	if !json.Valid(js.Bytes()) || !strings.Contains(js.String(), `"retryable":true`) || strings.Count(js.String(), "\n") != 1 {
		t.Errorf("json mode: %q", js.String())
	}
	if n := WriteError(&tb, ModeTable, "app.status", "req_0123456789abcdef", e); n != 3 {
		t.Errorf("exit %d", n)
	}
	if tb.String() != "unravis: Unravis is not running.\nAction: Start it, or run 'unravis app start --headless'.\nCode: APP_NOT_RUNNING\n" {
		t.Errorf("table mode: %q", tb.String())
	}
	// an internal error carries the correlation id in table mode
	tb.Reset()
	WriteError(&tb, ModeTable, "x", "req_0123456789abcdef", NewError(CodeInternal, "Something broke.", ""))
	if !strings.Contains(tb.String(), "Request: req_0123456789abcdef") {
		t.Errorf("internal error without the request id: %q", tb.String())
	}
	// escape sequences in a message do not reach the terminal
	tb.Reset()
	WriteError(&tb, ModeTable, "x", "r", NewError(CodeNotFound, "gone \x1b[31mred\x07 \r overwrite", ""))
	if strings.ContainsAny(tb.String(), "\x1b\x07\r") {
		t.Errorf("control characters in table mode: %q", tb.String())
	}
	// a nil error is internal, never exit 0
	if n := WriteError(&tb, ModeJSON, "x", "r", nil); n != 1 {
		t.Errorf("nil error: exit %d", n)
	}
}

func TestPeekOutputMode(t *testing.T) {
	env := func(v string) func(string) string {
		return func(n string) string {
			if n == "UNRAVIS_OUTPUT" {
				return v
			}
			return ""
		}
	}
	for _, tc := range []struct {
		args []string
		env  string
		want Mode
	}{
		{nil, "", ModeTable},
		{[]string{"audit", "nosuch", "--json"}, "", ModeJSON},
		{[]string{"-o", "json"}, "", ModeJSON},
		{[]string{"-ojsonl"}, "", ModeJSONL},
		{[]string{"-o=json"}, "", ModeJSON},
		{[]string{"--output", "jsonl"}, "", ModeJSONL},
		{[]string{"--output=json"}, "", ModeJSON},
		{[]string{"--json", "-o", "table"}, "", ModeTable}, // the last one wins
		{[]string{"-o", "table", "--json"}, "", ModeJSON},
		{[]string{"-qo", "json"}, "", ModeJSON},
		{[]string{"-qojson"}, "", ModeJSON},
		{[]string{"-qo=jsonl"}, "", ModeJSONL},
		{[]string{"-q"}, "json", ModeJSON},
		{[]string{"--json=true"}, "", ModeJSON},
		{[]string{"--json=false"}, "", ModeTable},
		{[]string{"--json", "-o", "yaml"}, "", ModeTable}, // a later invalid value wins, as a table
		{[]string{"-o", "yaml"}, "json", ModeTable},       // a flag beats the variable, even when its value is wrong
		{[]string{"-o", "yaml"}, "", ModeTable},           // not a mode: the command reports UNSUPPORTED_FORMAT
		{[]string{"-o", "yaml", "--json"}, "", ModeJSON},
		{[]string{"--", "--json"}, "", ModeTable},               // after "--" nothing is read
		{[]string{"version"}, "json", ModeJSON},                 // the variable, when no flag names a mode
		{[]string{"version", "-o", "table"}, "json", ModeTable}, // a flag beats the variable
		{[]string{"version"}, "bogus", ModeTable},
		{[]string{"-o"}, "", ModeTable},
		{[]string{"--output-file", "x.json"}, "", ModeTable}, // another flag that starts with --output
	} {
		if got := PeekOutputMode(tc.args, env(tc.env)); got != tc.want {
			t.Errorf("%v env=%q: got %s want %s", tc.args, tc.env, got, tc.want)
		}
	}
}

func TestOnlyAllowlistedEnvRead(t *testing.T) {
	if got := AllowedEnv(); len(got) != 4 {
		t.Fatalf("allowed: %v", got)
	}
	t.Setenv("PATH_SECRET_TEST", "x")
	t.Setenv("UNRAVIS_OUTPUT", "json")
	if Getenv("PATH_SECRET_TEST") != "" || Getenv("UNRAVIS_OUTPUT") != "json" {
		t.Error("Getenv must read the allowed names only")
	}
	// No code of this package reads the environment by any other way: scan the syntax, not the text.
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	reads := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); p == "syscall" || p == "golang.org/x/sys/windows" || p == "golang.org/x/sys/unix" {
				t.Errorf("%s imports %s, which can read the environment", f, p)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Getenv", "LookupEnv", "Environ", "ExpandEnv", "Expand", "Clearenv", "Setenv", "Unsetenv":
				reads++
				if pk, ok := sel.X.(*ast.Ident); !ok || pk.Name != "os" || f != "peek.go" || sel.Sel.Name != "Getenv" {
					t.Errorf("%s: %s reads or changes the environment", fset.Position(sel.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
	if reads != 1 {
		t.Errorf("%d environment accesses, want exactly the one in Getenv", reads)
	}
}

func TestTable(t *testing.T) {
	var b bytes.Buffer
	err := WriteTable(&b, []string{"ID", "NAME", "N"}, [][]string{{"a", "caf\u00e9", "1"}, {"longer-id", "x\x1b[2Jy", ""}, {"short"}, {"p", "q", "r", "dropped"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "ID         NAME    N\n" +
		"a          caf\u00e9    1\n" +
		"longer-id  x [2Jy\n" +
		"short\n" +
		"p          q       r\n"
	if b.String() != want {
		t.Errorf("got\n%q\nwant\n%q", b.String(), want)
	}
	if !utf8.ValidString(b.String()) {
		t.Error("invalid UTF-8")
	}
	b.Reset()
	_ = WriteTable(&b, []string{"A"}, nil)
	if b.String() != "A\n" {
		t.Errorf("empty table: %q", b.String())
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"plain":              "plain",
		"a\x1b]0;title\x07b": "a ]0;title b",
		"line1\nline2\r\nx":  "line1 line2  x",
		"name\u202egpj.exe":  "name gpj.exe", // right-to-left override
		"zero\u200bwidth\u2028sep\ufeffbom\u00adsoft": "zero width sep bom soft",
		"tag\U000E0041hidden":                         "tag hidden",
		"bad\xffbyte":                                 "bad\uFFFDbyte",
		"tab\there":                                   "tab here",
		"\u0085next":                                  " next", // C1 control
	} {
		if got := Printable(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
