package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"api-audit-explorer/internal/applog"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no console") }

func TestPrintStartupKeepsTheLaunchLinkOutOfTheLog(t *testing.T) {
	lines := []string{"Unravis listening at http://127.0.0.1:1 (demo=false)", "Open Unravis with this one-time link: http://127.0.0.1:1/launch?c=abc"}
	var logged []string
	var console bytes.Buffer
	printed := printStartup(lines, false, func(s string) { logged = append(logged, s) }, &console)
	for _, l := range logged {
		if strings.Contains(l, "/launch?c=") {
			t.Fatalf("the launch link reached the log: %q", l)
		}
	}
	if len(logged) != 1 || logged[0] != lines[0] {
		t.Fatalf("logged = %q, want only line 0", logged)
	}
	if !strings.Contains(console.String(), "/launch?c=") {
		t.Fatalf("console did not receive the link: %q", console.String())
	}
	if !strings.HasSuffix(console.String(), "\n") {
		t.Fatalf("console line has no trailing newline: %q", console.String())
	}
	if !printed {
		t.Fatal("printed = false, want true")
	}
}

func TestPrintStartupLogsBothLinesWhenTheBrowserOpened(t *testing.T) {
	lines := []string{"line zero", "Unravis was opened in your browser."}
	var logged []string
	var console bytes.Buffer
	printed := printStartup(lines, true, func(s string) { logged = append(logged, s) }, &console)
	if len(logged) != 2 || logged[0] != lines[0] || logged[1] != lines[1] {
		t.Fatalf("logged = %q, want both lines", logged)
	}
	if console.Len() != 0 {
		t.Fatalf("console received %q, want nothing", console.String())
	}
	if printed {
		t.Fatal("printed = true, want false when no link was printed")
	}
}

func TestPrintStartupSurvivesAFailingConsole(t *testing.T) {
	lines := []string{"line zero", "Open Unravis with this one-time link: http://127.0.0.1:1/launch?c=abc"}
	var logged []string
	printed := printStartup(lines, false, func(s string) { logged = append(logged, s) }, failingWriter{})
	if len(logged) != 1 || logged[0] != lines[0] {
		t.Fatalf("logged = %q, want line 0 only", logged)
	}
	if printed {
		t.Fatal("printed = true with a failing console")
	}
}

func TestMintLaunchCodeRegistersTheSecret(t *testing.T) {
	code, err := mintLaunchCode()
	if err != nil {
		t.Fatal(err)
	}
	if got := applog.Sanitize("x " + code + " y"); strings.Contains(got, code) {
		t.Fatal("the launch code survived Sanitize")
	}
}

func TestMintLaunchCodeKeepsOnlyTheLastEight(t *testing.T) {
	// Letters-only codes: Sanitize's long-token guess leaves them alone, so a
	// code stays hidden only while the registry remembers it.
	old := launchCodeSource
	t.Cleanup(func() { launchCodeSource = old })
	next := 0
	launchCodeSource = func() (string, error) {
		next++
		return strings.Repeat("abcdefghijkl"[next-1:next], 32), nil
	}
	var codes []string
	for i := 0; i < 12; i++ {
		code, err := mintLaunchCode()
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, code)
	}
	for i, code := range codes {
		hidden := !strings.Contains(applog.Sanitize("x "+code+" y"), code)
		if i < 4 && hidden {
			t.Errorf("code %d is still hidden, want forgotten", i)
		}
		if i >= 4 && !hidden {
			t.Errorf("code %d is not hidden, want hidden", i)
		}
	}
}

func TestMainCallsTheConsoleShimFirst(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(text, "func main() {")
	if start < 0 {
		t.Fatal("func main() not found")
	}
	body := text[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	shim := strings.Index(body, "attachParentConsole()")
	runCall := strings.Index(body, "run(")
	assign := strings.Index(body, "stdout = os.Stdout")
	if shim < 0 || runCall < 0 || assign < 0 {
		t.Fatalf("func main() lacks the shim, the stdout assignment or run( (%d, %d, %d)", shim, assign, runCall)
	}
	if shim > runCall {
		t.Error("attachParentConsole() must come before run(")
	}
	if assign < shim || assign > runCall {
		t.Error("stdout = os.Stdout must follow attachParentConsole() and come before run(")
	}
}
