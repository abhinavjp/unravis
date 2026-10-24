package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/store"
)

func TestDefaultPort(t *testing.T) {
	// 0 lets the OS choose a free port, so no local process can squat a
	// well-known one; the real port reaches --mcp through the session file.
	if defaultPort != 0 {
		t.Fatalf("defaultPort=%d, want 0", defaultPort)
	}
}

func TestSessionTokenUsesExplicitValueWithoutChangingIt(t *testing.T) {
	want := strings.Repeat("x", 40)
	got, err := sessionToken(func(name string) string {
		if name == "AUDIT_SESSION_TOKEN" {
			return want
		}
		return ""
	})
	if err != nil || got != want {
		t.Fatalf("sessionToken()=%q, %v", got, err)
	}
}

func TestSessionTokenRejectsShortExplicitValue(t *testing.T) {
	if _, err := sessionToken(func(string) string { return strings.Repeat("x", 31) }); err == nil {
		t.Fatal("expected short token to be rejected")
	}
	if _, err := sessionToken(func(string) string { return strings.Repeat("雪", 16) }); err == nil {
		t.Fatal("byte length must not substitute for the required character count")
	}
}

func TestSessionTokenRejectsHeaderControlCharacters(t *testing.T) {
	if _, err := sessionToken(func(string) string { return strings.Repeat("x", 31) + "\n" }); err == nil {
		t.Fatal("expected control character to be rejected")
	}
}

func TestSessionTokenGeneratesUnloggedRandomValue(t *testing.T) {
	got, err := sessionToken(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 32 {
		t.Fatalf("generated token length=%d", len(got))
	}
	other, err := sessionToken(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if got == other {
		t.Fatal("independent generated tokens unexpectedly matched")
	}
}

func TestHTTPEnvelopeIncludesReadAndFiveMinuteOperation(t *testing.T) {
	if httpWriteTimeout < httpReadTimeout+5*time.Minute+10*time.Second {
		t.Fatal("HTTP response deadline clips the operation after a slow request body")
	}
}

func TestMCPAllowIdentifiersRequiresMCP(t *testing.T) {
	if _, err := parseFlags([]string{"--mcp-allow-identifiers"}); err == nil || !strings.Contains(err.Error(), "--mcp") {
		t.Fatalf("--mcp-allow-identifiers without --mcp: %v", err)
	}
	if _, err := parseFlags([]string{"--demo", "--mcp-allow-identifiers"}); err == nil {
		t.Fatal("--mcp-allow-identifiers with --demo accepted")
	}
	f, err := parseFlags([]string{"--mcp", "--mcp-allow-identifiers"})
	if err != nil || !f.mcpAllowIdentifiers {
		t.Fatalf("--mcp --mcp-allow-identifiers: %+v, %v", f, err)
	}
	f, err = parseFlags([]string{"--mcp"})
	if err != nil || f.mcpAllowIdentifiers {
		t.Fatalf("identifiers must default off: %+v, %v", f, err)
	}
}

func TestBroadScanFlagsRejectedWithMCP(t *testing.T) {
	for _, args := range [][]string{
		{"--mcp", "--allow-broad-scans"},
		{"--mcp", "--docs-examined-budget", "5"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if _, err := parseFlags([]string{"--allow-broad-scans", "--docs-examined-budget", "5"}); err != nil {
		t.Fatalf("dashboard flags rejected: %v", err)
	}
}

func TestNegativeDocsExaminedBudgetRejected(t *testing.T) {
	if _, err := parseFlags([]string{"--docs-examined-budget", "-1"}); err == nil {
		t.Fatal("negative budget accepted")
	}
}

func TestDefaultFlagsLeaveBroadScansOff(t *testing.T) {
	f, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.allowBroadScans || f.docsExaminedBudget != 0 || f.mcpAllowIdentifiers {
		t.Fatalf("defaults not off: %+v", f)
	}
	if got := storeOptions(f); len(got) != 2 {
		t.Fatalf("storeOptions returned %d options, want 2 (broad scans, budget)", len(got))
	}
	// New accepts exactly what storeOptions builds.
	_ = store.New(storeOptions(f)...)
}

type fakeShutdown struct {
	path       string
	fileAtStop bool
	budget     time.Duration
	err        error
	closed     bool
}

func (f *fakeShutdown) Shutdown(ctx context.Context) error {
	_, err := os.Stat(f.path)
	f.fileAtStop = err == nil
	if deadline, ok := ctx.Deadline(); ok {
		f.budget = time.Until(deadline)
	}
	return f.err
}

func (f *fakeShutdown) Close() error { f.closed = true; return nil }

// TestSignalRemovesSessionFileBeforeShutdown is the M-A cleanup regression: on
// Ctrl+C, SIGTERM or console close the session file goes first, and draining
// fits inside the time Windows allows after a console window closes.
func TestSignalRemovesSessionFileBeforeShutdown(t *testing.T) {
	token := strings.Repeat("s", 40)
	path := filepath.Join(t.TempDir(), mcp.SessionFileName)
	if err := mcp.WriteSessionFile(path, 41234, token); err != nil {
		t.Fatal(err)
	}
	remove := func() { _ = mcp.RemoveSessionFile(path, token) }
	srv := &fakeShutdown{path: path}
	if err := shutdownOnSignal(remove, srv); err != nil {
		t.Fatal(err)
	}
	if srv.fileAtStop {
		t.Fatal("session file still present when shutdown began")
	}
	if srv.budget <= 0 || srv.budget > 5*time.Second {
		t.Fatalf("shutdown budget %v, want under 5s", srv.budget)
	}
	failing := &fakeShutdown{path: path, err: errors.New("busy")}
	if err := shutdownOnSignal(func() {}, failing); err == nil || !failing.closed {
		t.Fatalf("failed shutdown: err=%v closed=%v", err, failing.closed)
	}
}
