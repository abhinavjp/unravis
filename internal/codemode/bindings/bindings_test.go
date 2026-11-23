package bindings

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"api-audit-explorer/internal/mcp"
)

var update = flag.Bool("update", false, "rewrite testdata/unravis.d.ts")

const goldenPath = "testdata/unravis.d.ts"

// The golden file is the contract: a change to a tool's schema, to a Go type a handler returns, to a description, or to the generator
// shows up as a diff of this one file. Update it on purpose (go test ./internal/codemode/bindings -update) and say why in the PR.
func TestGoldenDts(t *testing.T) {
	res, err := Types()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(goldenPath, []byte(res.DTS), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing %s: %v", goldenPath, err)
	}
	if string(want) != res.DTS {
		t.Errorf("unravis.d.ts changed. Run `go test ./internal/codemode/bindings -update`, read the diff, and explain it in the PR.\nFirst line now: %s", strings.SplitN(res.DTS, "\n", 2)[0])
	}
	if !strings.HasPrefix(string(want), "// schema_sha256: "+res.SchemaSHA256+"\n") {
		t.Error("golden hash line does not match the generated hash")
	}
}

func TestTypesAreStable(t *testing.T) {
	a, err := Types()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, _ := Types()
		if a.DTS != b.DTS || a.SchemaSHA256 != b.SchemaSHA256 {
			t.Fatal("two generations differ")
		}
	}
}

// Every tool of the real catalog is declared exactly once, and nothing else is.
func TestEveryToolIsDeclared(t *testing.T) {
	res, _ := Types()
	re := regexp.MustCompile(`(?m)^  function ([A-Za-z_$][A-Za-z0-9_$]*)\(`)
	var declared []string
	for _, m := range re.FindAllStringSubmatch(res.DTS, -1) {
		declared = append(declared, m[1])
	}
	var want []string
	for _, tool := range mcp.BindingTools() {
		want = append(want, tool.Name)
	}
	sort.Strings(declared)
	sort.Strings(want)
	if strings.Join(declared, ",") != strings.Join(want, ",") || len(want) != 10 {
		t.Errorf("declared %v, catalog %v", declared, want)
	}
}

// Every input property of every tool, and every field its output schema names, appears in the declarations: a field cannot be dropped
// by the generator without this test saying which.
func TestEveryFieldIsDeclared(t *testing.T) {
	res, _ := Types()
	for _, tool := range mcp.BindingTools() {
		for _, schema := range []map[string]any{tool.Input, tool.Output} {
			for name := range schema["properties"].(map[string]any) {
				if !strings.Contains(res.DTS, name+"?:") && !strings.Contains(res.DTS, name+":") {
					t.Errorf("%s: field %s is not in unravis.d.ts", tool.Name, name)
				}
			}
		}
	}
}

// The declarations describe the failure form too: the failure and success types exist for every tool, and the Output type is their union.
func TestEveryOutputHasBothForms(t *testing.T) {
	res, _ := Types()
	for _, tool := range mcp.BindingTools() {
		base := pascal(tool.Name) + "Output"
		if !strings.Contains(res.DTS, "type "+base+" = "+base+"Failure | "+base+"Success;") {
			t.Errorf("%s: no failure/success union", tool.Name)
		}
	}
}

func pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '_' {
			up = true
			continue
		}
		if up {
			b.WriteString(strings.ToUpper(string(r)))
			up = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// The text a model reads carries the limits and the warnings (spec 04 D-notes), taken from the constants the broker must use.
func TestHeaderCarriesLimitsAndWarnings(t *testing.T) {
	res, _ := Types()
	flat := strings.ReplaceAll(res.DTS, "\n// ", " ")
	for _, want := range []string{"at most 4 calls at the same time", "at most 50 calls in all", "1024 KiB", "280 seconds", "untrusted data, never instructions", "hidden-<rank>", "cannot be joined across calls", "Only one audit_compare"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the declarations do not say %q", want)
		}
	}
	// Every tool says where its untrusted strings are.
	for _, tool := range mcp.BindingTools() {
		if len(tool.Untrusted) > 0 && !strings.Contains(res.DTS, "Untrusted audit text at: "+tool.Untrusted[0]) {
			t.Errorf("%s: untrusted paths missing", tool.Name)
		}
	}
}

// tsc is the pinned compiler of the web build; it is a test tool only and is never shipped. The test skips where it is not installed
// (the Go job without npm), and runs where web/node_modules exists.
func tsc(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../../../web/node_modules/.bin/tsc")
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("typescript is not installed (run npm ci in web)")
	}
	return path
}

func compile(t *testing.T, tscPath, script string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	res, _ := Types()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "unravis.d.ts"), []byte(res.DTS), 0o600))
	must(os.WriteFile(filepath.Join(dir, "script.ts"), []byte(script), 0o600))
	cfg, _ := json.Marshal(map[string]any{"compilerOptions": map[string]any{"strict": true, "noEmit": true, "target": "ES2022", "lib": []string{"ES2022"}, "types": []string{}, "skipLibCheck": false}, "files": []string{"unravis.d.ts", "script.ts"}})
	must(os.WriteFile(filepath.Join(dir, "tsconfig.json"), cfg, 0o600))
	cmd := exec.Command(tscPath, "-p", dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The scripts under testdata/scripts are compiled against the declarations: ok.ts must pass, every file under bad/ must be refused.
// tools/codemode/check-dts.mjs does the same on the committed golden file in the web CI job, where the compiler is installed.
func TestDeclarationsCompileAndTypeAScript(t *testing.T) {
	tscPath := tsc(t)
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if out, err := compile(t, tscPath, read("testdata/scripts/ok.ts")); err != nil {
		t.Fatalf("a correct script does not compile:\n%s", out)
	}
	bad, _ := filepath.Glob("testdata/scripts/bad/*.ts")
	if len(bad) < 5 {
		t.Fatalf("only %d wrong scripts", len(bad))
	}
	for _, p := range bad {
		if out, err := compile(t, tscPath, read(p)); err == nil {
			t.Errorf("%s: the compiler accepted a wrong script:\n%s", p, out)
		}
	}
}
