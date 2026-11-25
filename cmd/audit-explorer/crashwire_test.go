package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name) //nolint:gosec // test reads a source file of this package
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// BL-315: main starts the local crash capture. Without this call an unhandled panic leaves nothing but the console text (and a windowless
// build has no console), and no other test notices.
func TestMainStartsTheCrashCapture(t *testing.T) {
	src := readSource(t, "main.go")
	const start, call = "crash := startCrashCapture(getenv)", "defer crash.CloseUnlessPanic()"
	for _, want := range []string{start, call} {
		if n := strings.Count(src, want); n != 1 {
			t.Fatalf("want exactly one %q in main.go, found %d", want, n)
		}
	}
	// A plain deferred Close would release the capture before the runtime prints a panic on this goroutine, and the dump would be lost.
	if strings.Contains(src, "crash.Close()") {
		t.Error("main.go must close the capture with CloseUnlessPanic, not Close")
	}
	at := strings.Index(src, call)
	// It runs for every real run, so before the first mode that can serve or attach (--mcp) and before the data is opened.
	for _, later := range []string{"return runMCP(", ":= wireData(", "enterPipeSingleton("} {
		if i := strings.Index(src, later); i < 0 || i < at {
			t.Errorf("startCrashCapture must come before %q in run()", later)
		}
	}
	wire := readSource(t, "crashwire.go")
	for _, want := range []string{"crashlog.Start(", "appdirs.CrashDir(", "casestore.SecureDir", "casestore.SecureFile"} {
		if !strings.Contains(wire, want) {
			t.Errorf("crashwire.go does not use %s", want)
		}
	}
}

func TestStartCrashCaptureMakesTheFolderAndCleansUpAfterItself(t *testing.T) {
	cfg := t.TempDir()
	c := startCrashCapture(envOf(map[string]string{"AUDIT_CONFIG_DIR": cfg}))
	if c == nil || c.Path() == "" {
		t.Fatal("no capture started")
	}
	dir := filepath.Join(cfg, "crashes")
	if got := entriesOf(t, dir); len(got) != 1 {
		t.Fatalf("want this run's pending file, got %v", got)
	}
	c.Close()
	if got := entriesOf(t, dir); len(got) != 0 {
		t.Fatalf("a clean run left %v", got)
	}
}

func TestStartCrashCaptureIsOffWithoutAFolderButDoesNotFail(t *testing.T) {
	if c := startCrashCapture(envOf(map[string]string{})); c != nil {
		t.Fatalf("got %v", c)
	}
}
