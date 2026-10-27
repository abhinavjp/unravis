package main

import (
	"bytes"
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
