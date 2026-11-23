package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// build compiles the real program once per test run.
func build(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool")
	}
	exe := filepath.Join(t.TempDir(), "unravis")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput() //nolint:gosec // the go tool and a path made by this test
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return exe
}

func runExe(t *testing.T, exe string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(exe, args...) //nolint:gosec // the program this test just built
	cmd.Env = []string{}              // an empty environment: nothing is needed
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String(), code
}

func TestRealProgram(t *testing.T) {
	exe := build(t)
	out, errs, code := runExe(t, exe, "version", "--json")
	if code != 0 || errs != "" || !json.Valid([]byte(out)) || !strings.Contains(out, `"role":"cli"`) {
		t.Errorf("version: %d %q %q", code, out, errs)
	}
	out, errs, code = runExe(t, exe, "audit", "--json")
	if code != 2 || out != "" || !strings.Contains(errs, `"code":"UNKNOWN_OPERATION"`) {
		t.Errorf("unknown command: %d %q %q", code, out, errs)
	}
	_, errs, code = runExe(t, exe, "--mcp")
	if code != 2 || !strings.Contains(errs, "LEGACY_OPTION") {
		t.Errorf("legacy: %d %q", code, errs)
	}
	out, _, code = runExe(t, exe, "--version")
	if code != 0 || !strings.HasPrefix(out, "Unravis ") {
		t.Errorf("--version: %d %q", code, out)
	}
	// the rest of the exit map by a command that fails for a known reason
	_, errs, code = runExe(t, exe, "blackbox", "inspect", "--offline", "--input", filepath.Join(t.TempDir(), "missing"), "--json")
	if code != 5 || !strings.Contains(errs, `"code":"NOT_FOUND"`) {
		t.Errorf("not found: %d %q", code, errs)
	}
}

func TestClosedOutputPipeStopsQuietly(t *testing.T) {
	exe := build(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()                                // nobody reads
	cmd := exec.Command(exe, "schema", "--json") //nolint:gosec // the program this test just built
	cmd.Env = []string{}
	var e bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &e
	runErr := cmd.Run()
	_ = w.Close()
	code := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	if runtime.GOOS != "windows" && code != 6 {
		t.Errorf("exit %d, want 6 (stderr %q)", code, e.String())
	}
	if e.Len() != 0 {
		t.Errorf("text on stderr: %q", e.String())
	}
}
