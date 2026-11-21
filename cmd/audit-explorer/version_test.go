package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"api-audit-explorer/internal/version"
)

func withVersion(t *testing.T, v, commit, date string) {
	t.Helper()
	oldV, oldC, oldD := version.Version, version.Commit, version.Date
	version.Version, version.Commit, version.Date = v, commit, date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldV, oldC, oldD })
}

func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	old := stdout
	stdout = &out
	t.Cleanup(func() { stdout = old })
	return &out
}

func TestVersionFlagPrintsAndExitsBeforeAnythingElse(t *testing.T) {
	withVersion(t, "1.2.3", "0123456789abcdef", "2026-10-27")
	out := captureStdout(t)
	// getenv panics if run() touches the environment: no token, no session file, no cases.
	getenv := func(name string) string { t.Fatalf("--version read the environment (%s)", name); return "" }
	if err := run([]string{"--version"}, getenv); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Unravis v1.2.3 (commit 0123456, built 2026-10-27)\n"; got != want {
		t.Fatalf("printed %q, want %q", got, want)
	}
}

func TestVersionFlagWinsOverOtherFlags(t *testing.T) {
	withVersion(t, "dev", "", "")
	out := captureStdout(t)
	for _, args := range [][]string{{"--version", "--demo"}, {"--demo", "--version"}, {"--version", "--mcp", "--demo"}, {"--version", "--port", "99999"}} {
		out.Reset()
		if err := run(args, func(string) string { return "" }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.TrimSpace(out.String()) != "Unravis dev" {
			t.Fatalf("%v printed %q", args, out.String())
		}
	}
}

func TestStartupLineNamesTheVersionAndKeepsTheListeningAtMarker(t *testing.T) {
	withVersion(t, "2.0.1", "", "")
	lines := startupLines("http://127.0.0.1:8123", "code", launchCodeLifeManual, true, false)
	if lines[0] != "Unravis v2.0.1 listening at http://127.0.0.1:8123 (demo=true)" {
		t.Fatalf("first line %q", lines[0])
	}
	withVersion(t, "dev", "", "")
	if got := startupLines("http://127.0.0.1:1", "c", launchCodeLifeManual, false, true)[0]; got != "Unravis dev listening at http://127.0.0.1:1 (demo=false)" {
		t.Fatalf("first line %q", got)
	}
}

// TestVersionFromBuildInfoWithStrip builds with the release flags (BL-421) and checks that -s -w does not
// remove the -X version, so --version still works in a stripped exe.
func TestVersionFromBuildInfoWithStrip(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program; skipped under -short")
	}
	exe := filepath.Join(t.TempDir(), "unravis-strip")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	ld := "-s -w -buildid= -X api-audit-explorer/internal/version.Version=9.8.7 -X api-audit-explorer/internal/version.Commit=0123456789abcdef -X api-audit-explorer/internal/version.Date=2026-11-21"
	build := exec.Command("go", "build", "-trimpath", "-ldflags", ld, "-o", exe, ".") //nolint:gosec // test builds and runs its own throw-away binary
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(exe, "--version").CombinedOutput() //nolint:gosec // runs the binary just built above
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	if want := "Unravis v9.8.7 (commit 0123456, built 2026-11-21)"; strings.TrimSpace(string(out)) != want {
		t.Fatalf("printed %q, want %q", out, want)
	}
}
