package cmdtree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/version"
)

type result struct {
	code     int
	out, err string
}

// run runs the command line with no environment and a fixed executable path.
func run(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, IO{
		In: strings.NewReader(""), Out: &out, Err: &errb,
		Getenv: func(n string) string { return env[n] },
		Exe:    func() (string, error) { return "/opt/unravis/bin/unravis.exe", nil },
	})
	return result{code, out.String(), errb.String()}
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	if strings.Count(s, "\n") != 1 || !strings.HasSuffix(s, "\n") {
		t.Fatalf("not exactly one line: %q", s)
	}
	for _, c := range []byte(s) {
		if c >= 0x80 {
			t.Fatalf("not 7-bit: %q", s)
		}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %v: %q", err, s)
	}
	return m
}

func errCode(t *testing.T, r result) string {
	t.Helper()
	if r.out != "" {
		t.Errorf("an error left text on stdout: %q", r.out)
	}
	m := decode(t, r.err)
	e, _ := m["error"].(map[string]any)
	if e == nil {
		t.Fatalf("no error object: %q", r.err)
	}
	return e["code"].(string)
}

func TestVersion(t *testing.T) {
	r := run(t, nil, "version", "-o", "json")
	if r.code != 0 || r.err != "" {
		t.Fatalf("%+v", r)
	}
	m := decode(t, r.out)
	d := m["data"].(map[string]any)
	if m["operation"] != "version" || m["schemaVersion"] != "1" || d["role"] != "cli" || d["cliSchema"] != "1" || d["exe"] != "/opt/unravis/bin/unravis.exe" || d["app"] != version.Short() {
		t.Errorf("%v", m)
	}
	if p := d["proto"].(map[string]any); p["major"] != float64(1) || p["minor"] != float64(0) {
		t.Errorf("proto %v", p)
	}
	tb := run(t, nil, "version")
	if tb.code != 0 || !strings.Contains(tb.out, "FIELD") || !strings.Contains(tb.out, version.Short()) {
		t.Errorf("table: %+v", tb)
	}
	// the legacy flag prints the same text as the app program
	lv := run(t, nil, "--version")
	if lv.code != 0 || lv.out != version.String()+"\n" || lv.err != "" {
		t.Errorf("--version: %+v", lv)
	}
	if env := run(t, map[string]string{"UNRAVIS_OUTPUT": "json"}, "version"); decode(t, env.out)["operation"] != "version" {
		t.Errorf("UNRAVIS_OUTPUT: %q", env.out)
	}
}

func TestUsageErrorsAreContractErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"nosuch", "--json"}, "UNKNOWN_OPERATION"},
		{[]string{"version", "--bogus", "-o", "json"}, "INVALID_ARGUMENT"},
		{[]string{"version", "extra", "--json"}, "INVALID_ARGUMENT"},
		{[]string{"version", "--timeout", "abc-secret", "--json"}, "INVALID_ARGUMENT"},
		{[]string{"version", "--timeout", "2h", "--json"}, "INVALID_ARGUMENT"},
		{[]string{"version", "--timeout", "0s", "--json"}, "INVALID_ARGUMENT"},
		{[]string{"version", "-o", "yaml", "--json"}, "UNSUPPORTED_FORMAT"},
		{[]string{"version", "-o", "jsonl"}, "UNSUPPORTED_FORMAT"},
		{[]string{"--mcp", "--json"}, "LEGACY_OPTION"},
		{[]string{"-mcp", "--json"}, "LEGACY_OPTION"},
		{[]string{"--pipe-server=true", "--json"}, "LEGACY_OPTION"},
		{[]string{"version", "--overwrite", "--json"}, "INVALID_ARGUMENT"},
	} {
		r := run(t, nil, tc.args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d (%q %q)", tc.args, r.code, r.out, r.err)
			continue
		}
		if got := errCode(t, r); got != tc.code {
			t.Errorf("%v: code %s, want %s", tc.args, got, tc.code)
		}
		if strings.Contains(r.err, "abc-secret") {
			t.Errorf("%v: the value was echoed: %s", tc.args, r.err)
		}
	}
	// a bad variable is refused, but a flag beats it
	if r := run(t, map[string]string{"UNRAVIS_OUTPUT": "xml"}, "version"); r.code != 2 {
		t.Errorf("bad variable: %+v", r)
	}
	if r := run(t, map[string]string{"UNRAVIS_OUTPUT": "xml"}, "version", "-o", "json"); r.code != 0 {
		t.Errorf("flag beats variable: %+v", r)
	}
	// table mode usage errors: three plain lines on stderr, nothing on stdout
	tb := run(t, nil, "nosuch")
	if tb.code != 2 || tb.out != "" || !strings.HasPrefix(tb.err, "unravis: ") || !strings.Contains(tb.err, "Code: UNKNOWN_OPERATION") {
		t.Errorf("table error: %+v", tb)
	}
}

func TestHelpIsText(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {}, {"blackbox", "inspect", "--help"}, {"version", "--help"}} {
		r := run(t, nil, args...)
		if r.code != 0 || r.err != "" || !strings.Contains(r.out, "unravis") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestSchemaListsAndDescribesEveryOperation(t *testing.T) {
	r := run(t, nil, "schema", "-o", "json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	ops := decode(t, r.out)["data"].(map[string]any)["operations"].([]any)
	var names []string
	for _, o := range ops {
		names = append(names, o.(map[string]any)["operation"].(string))
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("not sorted: %v", names)
	}
	want := map[string]bool{"version": true, "schema": true, "blackbox.inspect": true, "sources.list": true}
	for _, r := range clicap.Rows() {
		if strings.HasPrefix(r.Op, "audit.") {
			want[r.Op] = true
		}
	}
	for _, n := range names {
		delete(want, n)
		d := run(t, nil, "schema", n, "-o", "json")
		if d.code != 0 {
			t.Errorf("%s: %+v", n, d)
			continue
		}
		data := decode(t, d.out)["data"].(map[string]any)
		if data["operation"] != n || data["input"] == nil || data["errors"] == nil {
			t.Errorf("%s: %v", n, data)
		}
	}
	for n := range want {
		t.Errorf("operation %s cannot be described", n)
	}
	if u := run(t, nil, "schema", "no.such", "--json"); u.code != 2 || errCode(t, u) != "UNKNOWN_OPERATION" {
		t.Errorf("unknown: %+v", u)
	}
	if u := run(t, nil, "schema", `../x`, "--json"); u.code != 2 {
		t.Errorf("path: %+v", u)
	}
	if tb := run(t, nil, "schema", "audit.compare"); tb.code != 0 || tb.err != "" || !strings.Contains(tb.out, `"operation": "audit.compare"`) || !strings.Contains(tb.out, `"input"`) {
		t.Errorf("table schema: %+v", tb)
	}
}

func TestEveryMCPToolHasARow(t *testing.T) {
	rows := map[string]string{}
	for _, r := range clicap.Rows() {
		if r.Tool != "" {
			rows[r.Tool] = r.Op
		}
	}
	cat := mcp.Catalog()
	if len(cat) != 10 || len(rows) != 10 {
		t.Fatalf("%d tools in the catalog, %d rows with a tool", len(cat), len(rows))
	}
	for _, tool := range cat {
		name, _ := tool["name"].(string)
		if _, ok := rows[name]; !ok {
			t.Errorf("tool %s has no matrix row", name)
		}
		delete(rows, name)
	}
	for tool := range rows {
		t.Errorf("row for tool %s that the server does not advertise", tool)
	}
	// the copy is a copy
	cat[0]["name"] = "changed"
	if mcp.Catalog()[0]["name"] == "changed" {
		t.Error("Catalog returned shared data")
	}
}

func TestOutputFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "v.json")
	r := run(t, nil, "version", "-o", "json", "--output-file", p)
	if r.code != 0 || r.out != "" || r.err != "" {
		t.Fatalf("%+v", r)
	}
	b, err := os.ReadFile(p) //nolint:gosec // a temp file of this test
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:3]) == "\xef\xbb\xbf" {
		t.Error("a byte order mark")
	}
	decode(t, string(b))
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v (%v)", fi, err)
		}
	}
	// the file exists now: refused, and unchanged
	r = run(t, nil, "version", "-o", "json", "--output-file", p)
	if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("existing: %+v", r)
	}
	if r = run(t, nil, "version", "-o", "json", "--output-file", p, "--overwrite"); r.code != 0 {
		t.Errorf("overwrite: %+v", r)
	}
	// table mode has no file form; a folder and a link are refused
	if r = run(t, nil, "version", "--output-file", filepath.Join(dir, "t.txt")); r.code != 2 {
		t.Errorf("table: %+v", r)
	}
	if r = run(t, nil, "version", "--json", "--output-file", dir, "--overwrite"); r.code != 2 {
		t.Errorf("folder: %+v", r)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(p, link); err == nil {
		if r = run(t, nil, "version", "--json", "--output-file", link, "--overwrite"); r.code != 2 {
			t.Errorf("link: %+v", r)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".unravis-out-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// record writes a real recording with the recorder's own store.
func record(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	now := t0
	s, err := blackbox.OpenStore(dir, blackbox.StoreOptions{Name: "box", Version: "test", Host: "HOST1", IntervalSeconds: 20, RunID: "r1", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	at := t0.Add(59*time.Minute + 30*time.Second)
	for i := 0; i < 6; i++ {
		now = at
		cpu := float64(i)
		if err := s.Append(blackbox.Sample{Run: "r1", Seq: uint64(i + 1), T: at, CPU: &cpu}, ""); err != nil {
			t.Fatal(err)
		}
		at = at.Add(20 * time.Second)
	}
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBlackboxInspect(t *testing.T) {
	dir := record(t)
	r := run(t, nil, "blackbox", "inspect", "--offline", "--input", dir, "--samples", "2", "-o", "json")
	if r.code != 0 || r.err != "" {
		t.Fatalf("%+v", r)
	}
	m := decode(t, r.out)
	d := m["data"].(map[string]any)
	if m["operation"] != "blackbox.inspect" || d["kind"] != "folder" || d["input"] != dir {
		t.Errorf("%v", m)
	}
	if s := d["samples"].(map[string]any); s["count"] != float64(6) || len(s["first"].([]any)) != 2 {
		t.Errorf("samples %v", s)
	}
	if mf := d["manifest"].(map[string]any); mf["host"] != "HOST1" || mf["state"] != "stopped" || mf["files"] != float64(2) {
		t.Errorf("manifest %v", mf)
	}
	// a window: only the second hour
	w := run(t, nil, "blackbox", "inspect", "--offline", "--input", dir, "--from", "2026-10-27T11:00:00Z", "--json")
	if w.code != 0 || decode(t, w.out)["data"].(map[string]any)["samples"].(map[string]any)["count"] != float64(4) {
		t.Errorf("window: %+v", w)
	}
	if w := run(t, nil, "blackbox", "inspect", "--offline", "--input", dir, "--to", "2026-10-27T11:00:00Z", "--json"); w.code != 0 || decode(t, w.out)["data"].(map[string]any)["samples"].(map[string]any)["count"] != float64(2) {
		t.Errorf("window to: %+v", w)
	}
	tb := run(t, nil, "blackbox", "inspect", "--offline", "--input", dir, "--samples", "1")
	if tb.code != 0 || !strings.Contains(tb.out, "samples read") || !strings.Contains(tb.out, "CPU%") || !strings.Contains(tb.out, "HOST1") {
		t.Errorf("table: %+v", tb)
	}
	// usage and refusals
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"blackbox", "inspect", "--input", dir}, "INVALID_ARGUMENT"},
		{[]string{"blackbox", "inspect", "--offline"}, "INVALID_ARGUMENT"},
		{[]string{"blackbox", "inspect", "--offline", "--input", dir, "--from", "yesterday"}, "INVALID_ARGUMENT"},
		{[]string{"blackbox", "inspect", "--offline", "--input", dir, "--from", "2026-10-27T12:00:00Z", "--to", "2026-10-27T11:00:00Z"}, "INVALID_ARGUMENT"},
		{[]string{"blackbox", "inspect", "--offline", "--input", dir, "--max-bytes", "0"}, "INVALID_ARGUMENT"},
		{[]string{"blackbox", "inspect", "--offline", "--input", dir, "--max-bytes", "10"}, "REQUEST_TOO_LARGE"},
		{[]string{"blackbox", "inspect", "--offline", "--input", dir, "--samples", "5000"}, "INVALID_ARGUMENT"},
		{[]string{"blackbox", "inspect", "--offline", "--input", filepath.Join(dir, "nope")}, "NOT_FOUND"},
	} {
		a := append(tc.args, "--json")
		r := run(t, nil, a...)
		want := 2
		if tc.code == "NOT_FOUND" {
			want = 5
		}
		if r.code != want {
			t.Errorf("%v: exit %d, want %d", tc.args, r.code, want)
			continue
		}
		if got := errCode(t, r); got != tc.code {
			t.Errorf("%v: %s, want %s", tc.args, got, tc.code)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "case.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = run(t, nil, "blackbox", "inspect", "--offline", "--input", dir, "--json")
	if r.code != 2 || errCode(t, r) != "LIVE_CASE_REFUSED" {
		t.Errorf("case data: %+v", r)
	}
}

func TestCompletionIsStatic(t *testing.T) {
	for _, sh := range shells {
		r := run(t, nil, "completion", sh)
		if r.code != 0 || len(r.out) < 100 || r.err != "" {
			t.Errorf("%s: exit %d, %d bytes (%q)", sh, r.code, len(r.out), r.err)
		}
	}
	// no command or flag asks for a candidate list from a function (D24)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.ValidArgsFunction != nil {
			t.Errorf("%s has a ValidArgsFunction", c.CommandPath())
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if fn, ok := c.GetFlagCompletionFunc(f.Name); ok && fn != nil {
				t.Errorf("%s --%s has a completion function", c.CommandPath(), f.Name)
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk((&state{}).root())
	// what the shell asks for is only fixed names; no file name appears
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker-file-name"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(old) })
	_ = os.Chdir(dir)
	r := run(t, nil, "__complete", "blackbox", "")
	if !strings.Contains(r.out, "inspect") || strings.Contains(r.out+r.err, "marker-file-name") {
		t.Errorf("completion of a group: %q", r.out)
	}
	// a wrong shell, no shell, JSON mode and a file are errors (exit 2), never a script with exit 0
	for _, args := range [][]string{{"completion", "nosuch", "--json"}, {"completion", "--json"}, {"completion", "bash", "--json"}, {"completion", "bash", "-o", "json", "--output-file", filepath.Join(dir, "c.json")}} {
		r := run(t, nil, args...)
		if r.code != 2 || r.out != "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := run(t, nil, "completion", "nosuch"); r.code != 2 || r.out != "" {
		t.Errorf("table, wrong shell: %+v", r)
	}
}

func TestGroupsAndHelpNeverSucceedOnATypo(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"help", "nosuch", "--json"}, "UNKNOWN_OPERATION"},
		{[]string{"blackbox", "nosuch", "--json"}, "UNKNOWN_OPERATION"},
		{[]string{"blackbox", "--json"}, "INVALID_ARGUMENT"},
		{[]string{"--json"}, "INVALID_ARGUMENT"},
		{[]string{"help", "version", "--json"}, "INVALID_ARGUMENT"},
	} {
		r := run(t, nil, tc.args...)
		if r.code != 2 || errCode(t, r) != tc.code {
			t.Errorf("%v: %+v", tc.args, r)
		}
	}
	if r := run(t, nil, "help", "nosuch"); r.code != 2 || r.out != "" {
		t.Errorf("table: %+v", r)
	}
	if r := run(t, nil, "help", "blackbox"); r.code != 0 || !strings.Contains(r.out, "inspect") {
		t.Errorf("help of a group: %+v", r)
	}
}

// A flag that takes another flag as its value changes what the person meant: it is refused, and no file named like a flag appears.
func TestModeMustMatchWhatWasParsed(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(old) })
	_ = os.Chdir(dir)
	for _, args := range [][]string{
		{"schema", "--output-file", "--json"},
		{"blackbox", "inspect", "--offline", "--input", "--json"},
		{"version", "--json", "--json=false"},
		{"version", "-o", "table", "--json"},
	} {
		r := run(t, nil, args...)
		if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
		t.Errorf("files were made: %v", left)
	}
	if r := run(t, nil, "version", "--json", "--output-file", "-x"); r.code != 2 {
		t.Errorf("file name that looks like a flag: %+v", r)
	}
}

func TestLegacyOptionsAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"-o", "json", "--mcp"}, {"--json", "--mcp"}, {"version", "--mcp", "-o", "json"}, {"--quit", "--json"}, {"--check-update", "--json"},
		{"-port=1", "--json"}, {"version", "--background", "--json"}, {"--owner=headless", "--json"}, {"app", "stop", "--owner", "headless", "--json"},
	} {
		r := run(t, nil, args...)
		if r.code != 2 || errCode(t, r) != "LEGACY_OPTION" {
			t.Errorf("%v: %+v", args, r)
			continue
		}
		if !strings.Contains(r.err, "/opt/unravis/unravis.exe") {
			t.Errorf("%v: the path of the app program is missing: %s", args, r.err)
		}
	}
	// the app path in the table text is one line plus the code
	if r := run(t, nil, "--mcp"); r.code != 2 || strings.Count(r.err, "\n") != 2 || !strings.Contains(r.err, "/opt/unravis/unravis.exe") {
		t.Errorf("table: %+v", r)
	}
	// --version is answered wherever it stands, but not with a value or a third dash; and nothing after "--" is read
	if r := run(t, nil, "--json", "--version"); r.code != 0 || !strings.Contains(r.out, version.Short()) {
		t.Errorf("--version: %+v", r)
	}
	for _, args := range [][]string{{"---version", "--json"}, {"--version=x", "--json"}, {"version", "--json", "--", "--mcp"}} {
		if r := run(t, nil, args...); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestTimeoutIsApplied(t *testing.T) {
	dir := record(t)
	r := run(t, nil, "blackbox", "inspect", "--offline", "--input", dir, "--timeout", "1ns", "--json")
	if r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("flag: %+v", r)
	}
	r = run(t, map[string]string{"UNRAVIS_TIMEOUT": "1ns"}, "blackbox", "inspect", "--offline", "--input", dir, "--json")
	if r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("variable: %+v", r)
	}
	// the flag beats the variable
	if r = run(t, map[string]string{"UNRAVIS_TIMEOUT": "1ns"}, "blackbox", "inspect", "--offline", "--input", dir, "--timeout", "30s", "--json"); r.code != 0 {
		t.Errorf("flag beats variable: %+v", r)
	}
	if r = run(t, map[string]string{"UNRAVIS_TIMEOUT": "soon"}, "version", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("bad variable: %+v", r)
	}
}

func TestWriteErrorKinds(t *testing.T) {
	if err := writeErr(nil); err != nil {
		t.Error(err)
	}
	if err := writeErr(&os.PathError{Op: "write", Path: "x", Err: syscall.EPIPE}); !errors.Is(err, errOutputClosed) {
		t.Errorf("a closed pipe: %v", err)
	}
	var ce *clicore.Error
	if err := writeErr(errors.New("no space left")); !errors.As(err, &ce) || ce.Code != clicore.CodeInternal {
		t.Errorf("a full disk: %v", err)
	}
}

func TestNilArgsDoNotReadTheProcessArguments(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), nil, IO{In: strings.NewReader(""), Out: &out, Err: &errb, Getenv: func(string) string { return "" }})
	if code != 0 || !strings.Contains(out.String(), "unravis") {
		t.Errorf("%d %q %q", code, out.String(), errb.String())
	}
}

func TestUnknownOptionNamesAreNotEchoedWhenOdd(t *testing.T) {
	r := run(t, nil, "version", "--ghp_secretTOKEN123456789012345678901234567890", "--json")
	if r.code != 2 || strings.Contains(r.err, "secret") || strings.Contains(r.err, "TOKEN") {
		t.Errorf("%+v", r)
	}
	if r := run(t, nil, "version", "--bogus", "--json"); !strings.Contains(r.err, "--bogus") {
		t.Errorf("a plain name is repeated: %+v", r)
	}
}

func TestOnlyAllowlistedEnvRead(t *testing.T) {
	fset := token.NewFileSet()
	for _, dir := range []string{".", "../../../cmd/unravis"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "Getenv", "LookupEnv", "Environ", "ExpandEnv", "Expand", "Setenv", "Unsetenv", "Clearenv":
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" {
							t.Errorf("%s: os.%s", fset.Position(sel.Pos()), sel.Sel.Name)
						}
					}
				}
				return true
			})
		}
	}
}

func TestErrorsNeverLeakTheRequestedPath(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "very-secret-folder-name")
	r := run(t, nil, "blackbox", "inspect", "--offline", "--input", secret, "--json")
	if strings.Contains(r.err, "very-secret-folder-name") {
		t.Errorf("the path is in the error: %s", r.err)
	}
	for _, args := range [][]string{
		{"version", "--json", "--output-file", filepath.Join(filepath.Dir(secret), "very-secret-folder-name", "x.json")},
		{"version", "--output-file", secret + "-t"},
	} {
		if r := run(t, nil, args...); strings.Contains(r.err, "very-secret-folder-name") {
			t.Errorf("%v: the path is in the error: %s", args, r.err)
		}
	}
	d := record(t)
	_ = os.WriteFile(filepath.Join(d, "case.db"), []byte("x"), 0o600)
	if r := run(t, nil, "blackbox", "inspect", "--offline", "--input", d, "--json"); strings.Contains(r.err, d) || strings.Contains(r.err, "case.db") {
		t.Errorf("the case file or folder is in the error: %s", r.err)
	}
}
