package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"api-audit-explorer/internal/instance"
)

// fakeQuitEnv replaces the message box and console probe; nothing real opens.
func fakeQuitEnv(t *testing.T, unusable bool) *[]string {
	t.Helper()
	oldShow, oldUnusable := showMessage, stderrUnusable
	var boxes []string
	showMessage = func(title, text string) { boxes = append(boxes, text) }
	stderrUnusable = func() bool { return unusable }
	t.Cleanup(func() { showMessage, stderrUnusable = oldShow, oldUnusable })
	return &boxes
}

func fakeSignal(err error, got *[]instance.Kind) func(instance.Kind) error {
	return func(k instance.Kind) error {
		if got != nil {
			*got = append(*got, k)
		}
		return err
	}
}

func TestRunQuitSuccessPrintsNothingAndShowsNoBox(t *testing.T) {
	for _, unusable := range []bool{true, false} {
		boxes := fakeQuitEnv(t, unusable)
		var w bytes.Buffer
		var sent []instance.Kind
		if code := runQuit(&w, fakeSignal(nil, &sent)); code != 0 {
			t.Fatalf("unusable=%v: code %d", unusable, code)
		}
		if w.Len() != 0 || len(*boxes) != 0 {
			t.Fatalf("unusable=%v: output %q boxes %v", unusable, w.String(), *boxes)
		}
		if len(sent) != 1 || sent[0] != instance.Quit {
			t.Fatalf("sent %v, want exactly one Quit", sent)
		}
	}
}

func TestRunQuitWhenNothingRunsExitsCleanly(t *testing.T) {
	fakeQuitEnv(t, false)
	var w bytes.Buffer
	if code := runQuit(&w, fakeSignal(instance.ErrNoInstance, nil)); code != 0 {
		t.Fatalf("code %d", code)
	}
	if got := strings.TrimSpace(w.String()); got != "Unravis is not running." {
		t.Fatalf("text %q", got)
	}
}

func TestRunQuitReportsAnotherAccountOrAdministrator(t *testing.T) {
	fakeQuitEnv(t, false)
	var w bytes.Buffer
	if code := runQuit(&w, fakeSignal(instance.ErrAccessDenied, nil)); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(w.String(), "another Windows account or as administrator") {
		t.Fatalf("text %q", w.String())
	}
}

func TestRunQuitPropagatesOtherErrors(t *testing.T) {
	fakeQuitEnv(t, false)
	var w bytes.Buffer
	if code := runQuit(&w, fakeSignal(errors.New("boom happened"), nil)); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(w.String(), "Unravis could not be asked to quit: boom happened") {
		t.Fatalf("text %q", w.String())
	}
}

func TestRunQuitShowsABoxOnlyWhenThereIsNoConsole(t *testing.T) {
	boxes := fakeQuitEnv(t, true)
	var w bytes.Buffer
	runQuit(&w, fakeSignal(instance.ErrNoInstance, nil))
	if w.Len() != 0 || len(*boxes) != 1 || (*boxes)[0] != "Unravis is not running." {
		t.Fatalf("no console: writer %q boxes %v", w.String(), *boxes)
	}

	boxes = fakeQuitEnv(t, false)
	w.Reset()
	runQuit(&w, fakeSignal(instance.ErrNoInstance, nil))
	if len(*boxes) != 0 || !strings.Contains(w.String(), "Unravis is not running.") {
		t.Fatalf("console: writer %q boxes %v", w.String(), *boxes)
	}
}

func TestParseFlagsQuitStandsAlone(t *testing.T) {
	old := hostOS
	hostOS = "windows"
	t.Cleanup(func() { hostOS = old })
	for _, args := range [][]string{
		{"--quit", "--demo"},
		{"--quit", "--port", "1"},
		{"--quit", "--background"},
		{"--quit", "--no-browser"},
	} {
		if _, err := parseFlags(args); err == nil || !strings.Contains(err.Error(), "--quit cannot be combined with other options") {
			t.Errorf("%v: err %v, want rejected", args, err)
		}
	}
	f, err := parseFlags([]string{"--quit"})
	if err != nil || !f.quit {
		t.Fatalf("--quit alone: %+v %v", f, err)
	}
	f, err = parseFlags([]string{"--quit", "--version"})
	if err != nil || !f.version {
		t.Fatalf("--quit --version: %+v %v", f, err)
	}
}

func TestParseFlagsBackgroundRules(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "windows"
	for _, args := range [][]string{
		{"--background", "--no-browser"},
		{"--background", "--mcp"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("%v accepted, want rejected", args)
		}
	}
	f, err := parseFlags([]string{"--background"})
	if err != nil || !f.background {
		t.Fatalf("windows: %+v %v", f, err)
	}
	hostOS = "linux"
	if _, err := parseFlags([]string{"--background"}); err == nil || !strings.Contains(err.Error(), "only available on Windows") {
		t.Fatalf("linux: err %v, want rejected", err)
	}
}

func TestMainRunsQuitBeforeServerSetup(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "func run(")
	if start < 0 {
		t.Fatal("run not found")
	}
	body := src[start:]
	q, d := strings.Index(body, "runQuit("), strings.Index(body, "demoEnv(")
	if q < 0 || d < 0 || q > d {
		t.Fatalf("runQuit at %d, demoEnv at %d inside run; want runQuit first", q, d)
	}
}
