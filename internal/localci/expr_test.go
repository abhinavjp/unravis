package localci

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testCtx() *ExprContext {
	return &ExprContext{Vars: map[string]any{
		"github": map[string]any{
			"event_name": "push",
			"ref":        "refs/heads/master",
			"sha":        "abc123",
			"event":      map[string]any{"before": "000"},
		},
		"inputs": map[string]any{
			"flag":     true,
			"flagstr":  "true",
			"fuzztime": "",
			"n":        3,
			"empty":    "",
		},
		"steps": map[string]any{
			"signgate": map[string]any{"outputs": map[string]any{"enabled": "true"}},
		},
		"vars":   map[string]any{"X": "xval"},
		"matrix": map[string]any{"shard": 2.0, "list": []any{"a", "B", 3.0}},
	}}
}

func TestEvalExpr(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		// literals
		{"'abc'", "abc"},
		{"'it''s'", "it's"},
		{"''''", "'"},
		{"42", 42.0},
		{"-1.5", -1.5},
		{"1e2", 100.0},
		{"0x10", 16.0},
		{"true", true},
		{"false", false},
		{"null", nil},
		// property access
		{"github.ref", "refs/heads/master"},
		{"GITHUB.REF", "refs/heads/master"},
		{"github['event_name']", "push"},
		{"github.event.before", "000"},
		{"github.nope", nil},
		{"github.nope.deeper.still", nil},
		{"steps.nothere.x", nil},
		{"github.ref.sub", nil},
		{"matrix.list[1]", "B"},
		{"matrix.list[9]", nil},
		{"matrix.list[-1]", nil},
		{"vars.x", "xval"},
		{"steps.signgate.outputs.enabled", "true"},
		{"inputs.n", 3.0},
		// not
		{"!true", false},
		{"!''", true},
		{"!'x'", false},
		{"!null", true},
		{"!0", true},
		{"!!1", true},
		{"!github.nope", true},
		// equality & coercion
		{"1 == 1", true},
		{"1 != 1", false},
		{"'a' == 'A'", true},
		{"'a' != 'A'", false},
		{"'a' == 'b'", false},
		{"null == null", true},
		{"null == ''", true},
		{"null == 0", true},
		{"null == false", true},
		{"'' == 0", true},
		{"'' == false", true},
		{"'1' == 1", true},
		{"'abc' == 0", false},
		{"'abc' == 'abc'", true},
		{"true == 1", true},
		{"false == 0", true},
		{"true == 'true'", false},
		{"'true' == true", false},
		{"inputs.flag == true", true},
		{"inputs.flagstr == true", false},
		{"inputs.flagstr == 'true'", true},
		{"inputs.flagstr != true", true},
		{"inputs.missing == true", false},
		{"inputs.missing != true", true},
		{"matrix.list == matrix.list", false},
		{"matrix.list == 0", false},
		{"' 5 ' == 5", true},
		{"'0x10' == 16", true},
		{"'nan' == 'nan'", true},
		{"'inf' == 1", false},
		// relational
		{"1 < 2", true},
		{"2 < 1", false},
		{"2 <= 2", true},
		{"3 > 2", true},
		{"2 >= 3", false},
		{"'a' < 'B'", true},
		{"'abc' < 1", false},
		{"'abc' >= 1", false},
		{"null <= 0", true},
		{"'2' > 1", true},
		{"matrix.shard >= 2", true},
		// && / || return operands
		{"'a' || 'b'", "a"},
		{"'' || 'b'", "b"},
		{"null || 0 || 'z'", "z"},
		{"'a' && 'b'", "b"},
		{"'' && 'b'", ""},
		{"0 && 'b'", 0.0},
		{"null && 'b'", nil},
		{"inputs.fuzztime || '2m'", "2m"},
		{"inputs.nope || '2m'", "2m"},
		{"steps.signgate.outputs.enabled == 'true' && vars.X || ''", "xval"},
		{"steps.signgate.outputs.enabled == 'false' && vars.X || ''", ""},
		{"true || steps.nothing.at.all", true},
		{"false && steps.nothing.at.all", false},
		{"(github.event_name == 'push' && github.ref == 'refs/heads/master') && github.sha || github.ref", "abc123"},
		{"(github.event_name == 'pull_request' && github.ref == 'refs/heads/master') && github.sha || github.ref", "refs/heads/master"},
		// precedence
		{"!false == true", true},
		{"!true == false", true},
		{"true || false && false", true},
		{"(true || false) && false", false},
		{"1 == 1 && 2 == 2", true},
		{"1 < 2 == true", true},
		{"false == false == true", true},
		// functions
		{"success()", true},
		{"SUCCESS()", true},
		{"failure()", false},
		{"cancelled()", false},
		{"always()", true},
		{"contains('Hello World', 'WORLD')", true},
		{"contains('Hello', 'x')", false},
		{"contains(matrix.list, 'b')", true},
		{"contains(matrix.list, 3)", true},
		{"contains(matrix.list, 'zz')", false},
		{"startsWith('Hello', 'he')", true},
		{"startsWith('Hello', 'lo')", false},
		{"endsWith('Hello', 'LO')", true},
		{"endsWith('Hello', 'he')", false},
		{"format('{0}-{1} {{x}} {0}', 'a', 2)", "a-2 {x} a"},
		{"format('plain')", "plain"},
		{"join(matrix.list, '/')", "a/B/3"},
		{"join(matrix.list)", "a,B,3"},
		{"join('solo', '/')", "solo"},
		{"fromJSON('{\"a\":[1,2,{\"b\":\"c\"}]}').a[2].b", "c"},
		{"fromJSON('5') == 5", true},
		{"fromJSON('true')", true},
		{"toJSON('x')", "\"x\""},
		{"toJSON(null)", "null"},
		{"toJSON(matrix.list)", "[\n  \"a\",\n  \"B\",\n  3\n]"},
		{"ToJson(1)", "1"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got, err := EvalExpr(tc.src, testCtx())
			if err != nil {
				t.Fatalf("EvalExpr(%q): %v", tc.src, err)
			}
			if !sameValue(got, tc.want) {
				t.Fatalf("EvalExpr(%q) = %#v, want %#v", tc.src, got, tc.want)
			}
			if err := CheckExpr(tc.src); err != nil {
				t.Fatalf("CheckExpr(%q): %v", tc.src, err)
			}
		})
	}
}

func sameValue(a, b any) bool {
	if af, ok := a.(float64); ok {
		bf, ok := b.(float64)
		return ok && (af == bf || (math.IsNaN(af) && math.IsNaN(bf)))
	}
	return a == b
}

func TestEvalExprNilContext(t *testing.T) {
	v, err := EvalExpr("github.ref || 'dflt'", nil)
	if err != nil || v != "dflt" {
		t.Fatalf("got %#v, %v", v, err)
	}
}

func TestExprErrors(t *testing.T) {
	cases := []struct {
		src     string
		wantMsg string
		off     int
	}{
		{"'abc", "unterminated string", 0},
		{"1 == 'abc", "unterminated string", 5},
		{"foo(1)", "unsupported function foo", 0},
		{"1 == bar('x')", "unsupported function bar", 5},
		{"toUpper('x')", "unsupported function toUpper", 0},
		{"github.*", "unsupported", 7},
		{"github.*.x", "unsupported", 7},
		{"github[*]", "unsupported", 7},
		{"", "empty expression", 0},
		{"   ", "empty expression", 0},
		{"1 +", "unexpected character", 2},
		{"(1 == 1", "expected ')'", 7},
		{"1 == 1)", "unexpected \")\"", 6},
		{"github.", "expected property name", 7},
		{"github[0", "expected ']'", 8},
		{"== 1", "unexpected \"==\"", 0},
		{"1 &&", "unexpected end", 4},
		{"a b", "unknown context", 0},
		{"True", "unknown context", 0},
		{"success(1)", "wrong number of arguments", 0},
		{"contains('a')", "wrong number of arguments", 0},
		{"hashFiles()", "wrong number of arguments", 0},
		{"1 2", "unexpected", 2},
		{"contains('a' 'b')", "expected ',' or ')'", 13},
		{"1 = 1", "unexpected character", 2},
		{"github.ref @ 1", "unexpected character", 11},
		{"1.2.3", "invalid number", 0},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			err := CheckExpr(tc.src)
			if err == nil {
				t.Fatalf("CheckExpr(%q) succeeded, want error", tc.src)
			}
			var ee *ExprError
			if !errors.As(err, &ee) {
				t.Fatalf("error %T is not *ExprError", err)
			}
			if !strings.Contains(ee.Msg, tc.wantMsg) {
				t.Errorf("msg = %q, want contains %q", ee.Msg, tc.wantMsg)
			}
			if ee.Offset != tc.off {
				t.Errorf("offset = %d, want %d (%v)", ee.Offset, tc.off, err)
			}
			if ee.Src != tc.src {
				t.Errorf("Src = %q", ee.Src)
			}
			if !strings.Contains(err.Error(), "offset") {
				t.Errorf("Error() lacks offset: %v", err)
			}
			if _, err := EvalExpr(tc.src, testCtx()); err == nil {
				t.Errorf("EvalExpr(%q) succeeded, want error", tc.src)
			}
		})
	}
}

func TestEvalRuntimeErrors(t *testing.T) {
	for _, src := range []string{
		"fromJSON('{nope')",
		"format('{1}', 'a')",
		"format('{', 'a')",
		"format('}', 'a')",
		"format('{x}', 'a')",
		"hashFiles('[')",
	} {
		if _, err := EvalExpr(src, testCtx()); err == nil {
			t.Errorf("EvalExpr(%q) succeeded, want error", src)
		} else if !errors.As(err, new(*ExprError)) {
			t.Errorf("EvalExpr(%q) error %T not *ExprError", src, err)
		}
	}
}

func TestStatusFunctions(t *testing.T) {
	for _, tc := range []struct {
		status    string
		success   bool
		failure   bool
		cancelled bool
	}{
		{"", true, false, false},
		{"success", true, false, false},
		{"failure", false, true, false},
		{"cancelled", false, false, true},
	} {
		c := &ExprContext{JobStatus: tc.status}
		for fn, want := range map[string]bool{"success()": tc.success, "failure()": tc.failure, "cancelled()": tc.cancelled, "always()": true} {
			got, err := EvalExpr(fn, c)
			if err != nil || got != want {
				t.Errorf("status %q %s = %v, %v; want %v", tc.status, fn, got, err, want)
			}
		}
	}
}

func TestEvalCond(t *testing.T) {
	cases := []struct {
		cond   string
		status string
		want   bool
	}{
		{"", "", true},
		{"", "success", true},
		{"", "failure", false},
		{"   ", "cancelled", false},
		// implicit success()
		{"true", "", true},
		{"true", "success", true},
		{"true", "failure", false},
		{"true", "cancelled", false},
		{"${{ true }}", "failure", false},
		{"github.event_name == 'push'", "", true},
		{"github.event_name == 'push'", "failure", false},
		{"github.event_name == 'pull_request'", "", false},
		{"${{ github.event_name == 'push' }}", "", true},
		{"  ${{ !cancelled() }}  ", "failure", true},
		{"${{ !cancelled() }}", "cancelled", false},
		{"${{ !cancelled() }}", "", true},
		// explicit status functions disable the implicit one
		{"always()", "failure", true},
		{"always()", "cancelled", true},
		{"${{ always() }}", "success", true},
		{"failure()", "success", false},
		{"failure()", "failure", true},
		{"${{ failure() }}", "failure", true},
		{"cancelled()", "cancelled", true},
		{"cancelled()", "success", false},
		{"success()", "failure", false},
		{"${{ success() && github.ref == 'refs/heads/master' }}", "", true},
		{"${{ success() && github.ref == 'refs/heads/develop' }}", "", false},
		{"${{ always() && (failure() || steps.compare.outcome == 'failure') }}", "failure", true},
		{"${{ always() && (failure() || steps.compare.outcome == 'failure') }}", "success", false},
		{"ALWAYS()", "failure", true},
		{"${{ !inputs.seeded }}", "", true},
		{"inputs.flag == true", "", true},
		{"inputs.flagstr == true", "", false},
		{"${{ always() && hashFiles('nothing-here') != '' }}", "", false},
	}
	for _, tc := range cases {
		c := testCtx()
		c.JobStatus = tc.status
		c.Workdir = t.TempDir()
		got, err := EvalCond(tc.cond, c)
		if err != nil {
			t.Errorf("EvalCond(%q,%q): %v", tc.cond, tc.status, err)
			continue
		}
		if got != tc.want {
			t.Errorf("EvalCond(%q, status %q) = %v, want %v", tc.cond, tc.status, got, tc.want)
		}
	}
	for _, bad := range []string{"${{ nope() }}", "'x", "${{ 1 == }}"} {
		if _, err := EvalCond(bad, testCtx()); err == nil {
			t.Errorf("EvalCond(%q) succeeded, want error", bad)
		}
	}
}

func TestInterpolate(t *testing.T) {
	c := testCtx()
	cases := []struct{ in, want string }{
		{"no exprs", "no exprs"},
		{"", ""},
		{"${{ github.ref }}", "refs/heads/master"},
		{"ci-${{ github.event_name }}-${{ github.sha }}!", "ci-push-abc123!"},
		{"${{github.event_name}}${{github.sha}}", "pushabc123"},
		{"a ${{ true }} b ${{ false }} c", "a true b false c"},
		{"n=${{ 3 }} f=${{ 1.5 }} big=${{ 1e21 }} neg=${{ -0 }}", "n=3 f=1.5 big=1000000000000000000000 neg=0"},
		{"[${{ null }}][${{ github.nope }}]", "[][]"},
		{"${{ inputs.fuzztime || '2m' }}", "2m"},
		{"${{ matrix.list }}", `["a","B",3]`},
		{"${{ github.event }}", `{"before":"000"}`},
		{"x ${{ '}}' }} y", "x }} y"},
		{"${{ 'it''s' }}", "it's"},
		{"cost $5 and ${ not } ${{ 1 }}", "cost $5 and ${ not } 1"},
		{"line1\n${{ github.sha }}\nline3", "line1\nabc123\nline3"},
	}
	for _, tc := range cases {
		got, err := Interpolate(tc.in, c)
		if err != nil {
			t.Errorf("Interpolate(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Interpolate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"${{ unclosed", "a ${{ foo() }} b", "${{ 'x }}", "${{ }}"} {
		if _, err := Interpolate(bad, c); err == nil {
			t.Errorf("Interpolate(%q) succeeded, want error", bad)
		}
	}
}

func TestExprsIn(t *testing.T) {
	got := ExprsIn("a ${{ x.y }} b ${{github.ref == '}}'}} c ${{ unclosed")
	want := []string{"x.y", "github.ref == '}}'"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(ExprsIn("nothing")) != 0 {
		t.Error("expected none")
	}
}

func TestHashFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.sum", "sum-content")
	write("a/one.go", "one")
	write("a/b/two.go", "two")
	write("a/b/c/three.go", "three")
	write("a/b/c/note.txt", "note")
	write("top.go", "top")

	c := &ExprContext{Workdir: dir}
	hash := func(expr string) string {
		t.Helper()
		v, err := EvalExpr(expr, c)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: got %T", expr, v)
		}
		return s
	}
	hexRe := regexp.MustCompile(`^[0-9a-f]{64}$`)

	single := hash("hashFiles('go.sum')")
	if !hexRe.MatchString(single) {
		t.Fatalf("not a sha256 hex: %q", single)
	}
	if hash("hashFiles('go.sum')") != single {
		t.Error("not deterministic")
	}
	if hash("hashFiles('./go.sum')") != single {
		t.Error("./ prefix should not matter")
	}
	if got := hash("hashFiles('missing.txt')"); got != "" {
		t.Errorf("no match = %q, want empty", got)
	}
	if got := hash("hashFiles('*.nothing')"); got != "" {
		t.Errorf("no glob match = %q, want empty", got)
	}
	if got := hash("hashFiles('a')"); got != "" {
		t.Errorf("directory should not match, got %q", got)
	}
	if got := hash("hashFiles('*.go')"); got == "" || got != hash("hashFiles('top.go')") {
		t.Errorf("*.go should match only top.go")
	}
	all := hash("hashFiles('**/*.go')")
	if all == "" {
		t.Fatal("**/*.go matched nothing")
	}
	if all == hash("hashFiles('a/*.go')") {
		t.Error("** must reach deeper files than *")
	}
	// ** matches zero dirs too: top.go, a/one.go, a/b/two.go, a/b/c/three.go
	explicit := hash("hashFiles('top.go', 'a/one.go', 'a/b/two.go', 'a/b/c/three.go')")
	if all != explicit {
		t.Error("**/*.go should equal the explicit sorted list")
	}
	// order of patterns does not matter: files are sorted
	if hash("hashFiles('a/b/two.go', 'top.go', 'a/b/c/three.go', 'a/one.go')") != explicit {
		t.Error("pattern order must not matter")
	}
	if hash("hashFiles('a/**/three.go')") != hash("hashFiles('a/b/c/three.go')") {
		t.Error("a/**/three.go")
	}
	if hash("hashFiles('a/**')") == "" {
		t.Error("a/** should match files below a")
	}
	// content change changes the hash
	before := hash("hashFiles('**/*.go')")
	write("a/b/two.go", "TWO")
	if hash("hashFiles('**/*.go')") == before {
		t.Error("hash must change with content")
	}
	// exclusion
	if hash("hashFiles('**/*.go', '!a/**')") != hash("hashFiles('top.go')") {
		t.Error("negated pattern should exclude")
	}
	// Used in a condition, like the workflows do.
	ok, err := EvalCond("${{ always() && hashFiles('go.sum') != '' }}", c)
	if err != nil || !ok {
		t.Errorf("cond = %v, %v", ok, err)
	}
	ok, err = EvalCond("${{ always() && hashFiles('go-test.json') != '' }}", c)
	if err != nil || ok {
		t.Errorf("cond = %v, %v", ok, err)
	}
	// Interpolated key.
	k, err := Interpolate("go-${{ runner.os }}-${{ hashFiles('go.sum') }}-", &ExprContext{Workdir: dir, Vars: map[string]any{"runner": map[string]any{"os": "Linux"}}})
	if err != nil || k != "go-Linux-"+single+"-" {
		t.Errorf("key = %q, %v", k, err)
	}
	// Empty Workdir resolves against the current directory (package dir).
	v, err := EvalExpr("hashFiles('expr.go')", &ExprContext{})
	if err != nil || v == "" {
		t.Errorf("cwd hashFiles = %v, %v", v, err)
	}
}

var (
	exprRe = regexp.MustCompile(`(?s)\$\{\{(.*?)\}\}`)
	ifRe   = regexp.MustCompile(`(?m)^\s*(?:-\s+)?if:\s*(.+?)\s*$`)
)

func workflowFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflow files found under ../../.github/workflows")
	}
	return files
}

func TestWorkflowExpressions(t *testing.T) {
	total := 0
	for _, f := range workflowFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		matches := exprRe.FindAllStringSubmatch(text, -1)
		if got := ExprsIn(text); len(got) != len(matches) {
			t.Errorf("%s: ExprsIn found %d expressions, regexp found %d", f, len(got), len(matches))
		}
		for _, m := range matches {
			total++
			src := strings.TrimSpace(m[1])
			if err := CheckExpr(src); err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
		}
	}
	if total < 50 {
		t.Errorf("only %d expressions found in workflows; walk looks broken", total)
	}
	t.Logf("checked %d expressions", total)
}

func TestWorkflowConditions(t *testing.T) {
	total := 0
	for _, f := range workflowFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ifRe.FindAllStringSubmatch(string(data), -1) {
			total++
			cond := m[1]
			if err := CheckExpr(condSource(cond)); err != nil {
				t.Errorf("%s: if: %s: %v", filepath.Base(f), cond, err)
				continue
			}
			// Evaluates against an empty context for every job status.
			for _, st := range []string{"success", "failure", "cancelled"} {
				if _, err := EvalCond(cond, &ExprContext{JobStatus: st, Workdir: t.TempDir()}); err != nil {
					t.Errorf("%s: if: %s (%s): %v", filepath.Base(f), cond, st, err)
				}
			}
		}
	}
	if total < 50 {
		t.Errorf("only %d if: lines found; walk looks broken", total)
	}
}

func TestWorkflowInterpolation(t *testing.T) {
	// Every ${{ }} of every workflow must evaluate against an empty context.
	for _, f := range workflowFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Interpolate(string(data), &ExprContext{Workdir: t.TempDir()}); err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
}
