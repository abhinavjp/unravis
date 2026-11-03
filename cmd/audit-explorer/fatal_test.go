package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeFatal swaps the three seams and the log output; it restores them on cleanup.
func fakeFatal(t *testing.T, unusable bool) (code *int, title, text *string, logs *bytes.Buffer) {
	t.Helper()
	oldExit, oldShow, oldUnusable := exitProcess, showMessage, stderrUnusable
	oldOut, oldFlags := log.Writer(), log.Flags()
	code, title, text, logs = new(int), new(string), new(string), &bytes.Buffer{}
	*code = -1
	exitProcess = func(c int) { *code = c }
	showMessage = func(ti, tx string) { *title, *text = ti, tx }
	stderrUnusable = func() bool { return unusable }
	log.SetOutput(logs)
	t.Cleanup(func() {
		exitProcess, showMessage, stderrUnusable = oldExit, oldShow, oldUnusable
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	return
}

func TestReportFatalLogsAndExitsWithOne(t *testing.T) {
	code, title, _, logs := fakeFatal(t, false)
	reportFatal(errors.New("port 8080 is busy"))
	if *code != 1 {
		t.Fatalf("exit code = %d, want 1", *code)
	}
	if *title != "" {
		t.Fatalf("message box shown with a usable stderr: %q", *title)
	}
	if !strings.Contains(logs.String(), "port 8080 is busy") {
		t.Fatalf("log = %q, want the error", logs.String())
	}
}

func TestReportFatalShowsAMessageWhenStderrIsUnusable(t *testing.T) {
	code, title, text, _ := fakeFatal(t, true)
	reportFatal(errors.New("port 8080 is busy"))
	if *code != 1 {
		t.Fatalf("exit code = %d, want 1", *code)
	}
	if *title != "Unravis could not start" {
		t.Fatalf("title = %q", *title)
	}
	if !strings.Contains(*text, "port 8080 is busy") {
		t.Fatalf("text = %q, want the error", *text)
	}
}

func TestFatalTextIsShortened(t *testing.T) {
	const footer = "Details are in the Unravis log folder."
	long := strings.Repeat("start failed é ", 400) // 6000 runes, multi-byte, spaced
	got := fatalText(errors.New(long))
	if !strings.HasSuffix(got, footer) {
		t.Fatalf("text does not end with the footer: %q", got[len(got)-60:])
	}
	if !utf8.ValidString(got) {
		t.Fatal("text is not valid UTF-8")
	}
	body := strings.TrimSuffix(got, "\n\n"+footer)
	if n := utf8.RuneCountInString(body); n > 800+len("...") || !strings.HasSuffix(body, "...") {
		t.Fatalf("body = %d runes, want at most 800 plus an ellipsis", n)
	}
	short := fatalText(errors.New("boom"))
	if short != "boom\n\n"+footer {
		t.Fatalf("short text = %q", short)
	}
}

func TestFatalTextHidesRegisteredSecrets(t *testing.T) {
	const secret = "Zq-not-guessable-by-length"
	code, err := mintLaunchCodeFor(t, secret)
	if err != nil {
		t.Fatal(err)
	}
	got := fatalText(errors.New("bad link /launch?code=" + code))
	if strings.Contains(got, code) {
		t.Fatalf("fatal text leaks the code: %q", got)
	}
}

func TestMainUsesReportFatalNotLogFatal(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "log.Fatal") {
		t.Fatal("main.go still calls log.Fatal")
	}
	if !strings.Contains(string(b), "reportFatal(err)") {
		t.Fatal("main.go does not call reportFatal")
	}
}

// mintLaunchCodeFor mints a code through the real registry path with a fixed value.
func mintLaunchCodeFor(t *testing.T, value string) (string, error) {
	t.Helper()
	old := launchCodeSource
	launchCodeSource = func() (string, error) { return value, nil }
	t.Cleanup(func() { launchCodeSource = old })
	return mintLaunchCode()
}
