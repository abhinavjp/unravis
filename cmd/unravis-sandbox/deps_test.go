package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"

	"api-audit-explorer/internal/codemode/wire"
)

// goList runs go list in the module root and returns its output lines.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...) //nolint:gosec // fixed go list arguments built by the test
	cmd.Dir = "../.."
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %v: %v\n%s", args, err, stderr.String())
	}
	return strings.Fields(out.String())
}

// The worker links the wire protocol, the clock and random generator of privacy, wazero and the standard library, and nothing that
// could read a database, a key or a file of the app.
func TestWorkerDepsAllowList(t *testing.T) {
	deps := goList(t, "-deps", "./cmd/unravis-sandbox")
	const mod = "api-audit-explorer"
	allowed := map[string]bool{
		mod + "/cmd/unravis-sandbox":       true,
		mod + "/internal/codemode/wire":    true,
		mod + "/internal/codemode/privacy": true,
		mod + "/internal/secretscrub":      true, // privacy imports it
		mod + "/internal/querytext":        true, // privacy imports it
	}
	for _, d := range deps {
		if strings.HasPrefix(d, mod+"/") || d == mod {
			if !allowed[d] {
				t.Errorf("the worker links %s, which is not on the allow-list", d)
			}
		}
		low := strings.ToLower(d)
		for _, bad := range []string{"sqlite", "mongo", "keeper", "dpapi", "casestore", "/server", "/mcp", "net/http", "database/sql", "os/exec", "plugin"} {
			if strings.Contains(low, bad) {
				t.Errorf("the worker links %s (matches %q)", d, bad)
			}
		}
	}
	for _, want := range []string{mod + "/internal/codemode/wire", "github.com/tetratelabs/wazero"} {
		found := false
		for _, d := range deps {
			if d == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the worker does not link %s: the check looks at the wrong package", want)
		}
	}
	// The worker itself has no os/exec and starts nothing.
	for _, f := range []string{"main.go", "runtime.go", "bridge.go"} {
		b, err := os.ReadFile(f) //nolint:gosec // paths come from go list of this module
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{`"os/exec"`, `"net"`, `"net/http"`, `"syscall"`, `"plugin"`, `"unsafe"`, `"wazero/imports`} {
			if bytes.Contains(b, []byte(bad)) {
				t.Errorf("%s imports %s", f, bad)
			}
		}
	}
}

// Only the worker links wazero: the app and every other command and package stay free of it.
func TestAppDoesNotLinkWazero(t *testing.T) {
	all := goList(t, "./...")
	var others []string
	for _, p := range all {
		if p == "api-audit-explorer/cmd/unravis-sandbox" {
			continue
		}
		others = append(others, p)
	}
	sort.Strings(others)
	// -test adds the test dependencies, so a test helper that pulls wazero in is caught as well.
	deps := goList(t, append([]string{"-deps", "-test"}, others...)...)
	for _, d := range deps {
		if strings.Contains(d, "tetratelabs/wazero") {
			t.Errorf("%s is linked outside cmd/unravis-sandbox", d)
		}
	}
	if len(deps) < 50 {
		t.Fatalf("only %d dependencies listed: the check looks at too little", len(deps))
	}
}

// The wasm file is the pinned one, hello reports its hash, and it asks the host for nothing beyond the allow-listed functions.
func TestWasmPinnedAndImports(t *testing.T) {
	pin, err := os.ReadFile("qjs.wasm.sha256")
	if err != nil {
		t.Fatal(err)
	}
	if got := helloFrame().WasmSHA256; got != strings.TrimSpace(string(pin)) {
		t.Fatalf("qjs.wasm hashes to %s but qjs.wasm.sha256 says %s: rebuild it as tools/codemode/BUILD-WASM.md says and update the pin", got, strings.TrimSpace(string(pin)))
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer func() { _ = rt.Close(ctx) }()
	cm, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, f := range cm.ImportedFunctions() {
		mod, name, _ := f.Import()
		imports = append(imports, mod+"."+name)
	}
	sort.Strings(imports)
	want := []string{
		"env.host_call", "env.host_fatal", "env.host_log", "env.host_perf_now", "env.host_random",
		"wasi_snapshot_preview1.clock_time_get", "wasi_snapshot_preview1.fd_close", "wasi_snapshot_preview1.fd_fdstat_get",
		"wasi_snapshot_preview1.fd_seek", "wasi_snapshot_preview1.fd_write",
	}
	if strings.Join(imports, " ") != strings.Join(want, " ") {
		t.Fatalf("the engine imports\n %v\nwant\n %v", imports, want)
	}
	if len(cm.ImportedMemories()) != 0 {
		t.Fatalf("the engine imports a memory")
	}
	var exports []string
	for name := range cm.ExportedFunctions() {
		exports = append(exports, name)
	}
	sort.Strings(exports)
	wantExports := []string{"_initialize", "qjs_alloc", "qjs_free", "qjs_init", "qjs_pump", "qjs_report", "qjs_ret", "qjs_run"}
	if strings.Join(exports, " ") != strings.Join(wantExports, " ") {
		t.Fatalf("the engine exports\n %v\nwant\n %v", exports, wantExports)
	}
	_ = wire.Version
}
