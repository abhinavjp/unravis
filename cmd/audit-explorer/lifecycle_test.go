package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/tray"
)

// hangGuard only stops a broken test from hanging; no test asserts a duration.
const hangGuard = 10 * time.Second

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(hangGuard):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestWaitForReturnsTrueWhenClosedAndFalseAfterTheGuard(t *testing.T) {
	closed := make(chan struct{})
	close(closed)
	if !waitFor(closed, hangGuard) {
		t.Fatal("waitFor on a closed channel = false")
	}
	if waitFor(make(chan struct{}), 50*time.Millisecond) {
		t.Fatal("waitFor on an open channel = true")
	}
}

func TestStartTrayClosesDoneWhenRunReturns(t *testing.T) {
	release := make(chan struct{})
	run := func(ctx context.Context, o tray.Options) error {
		<-release
		return nil
	}
	done := startTray(context.Background(), run, tray.Options{}, func(string, ...any) { t.Error("unexpected log") })
	select {
	case <-done:
		t.Fatal("done closed before run returned")
	default:
	}
	close(release)
	waitClosed(t, done, "done")
}

func TestStartTrayLogsAnErrorOnceAndStillCloses(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, format)
		if len(args) != 1 {
			t.Errorf("logf args = %d, want 1", len(args))
		}
	}
	run := func(ctx context.Context, o tray.Options) error { return errors.New("no shell") }
	done := startTray(context.Background(), run, tray.Options{}, logf)
	waitClosed(t, done, "done")
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || lines[0] != "Tray icon unavailable: %v" {
		t.Fatalf("logged %q, want the one unavailable line", lines)
	}
}

func TestStopTrayWaitsForTheIconToBeRemoved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var events []string
	run := func(ctx context.Context, o tray.Options) error {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		events = append(events, "remove")
		mu.Unlock()
		return nil
	}
	done := startTray(ctx, run, tray.Options{}, func(string, ...any) {})
	var ending atomic.Bool
	stopTray(cancel, &ending, done, hangGuard)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0] != "remove" {
		t.Fatalf("events after stopTray = %v, want [remove]", events)
	}
}

func TestStopTraySkipsTheWaitDuringEndSession(t *testing.T) {
	var cancelled atomic.Bool
	var ending atomic.Bool
	ending.Store(true)
	never := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		// A guard far longer than the test would take if the wait were not skipped.
		stopTray(func() { cancelled.Store(true) }, &ending, never, hangGuard)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("stopTray waited for the tray during an end-session")
	}
	if !cancelled.Load() {
		t.Fatal("stopTray did not cancel")
	}
}

func TestEndSessionHandlerCancelsThenWaitsForRunToFinish(t *testing.T) {
	var cancelled atomic.Bool
	var ending atomic.Bool
	done := make(chan struct{})
	started := make(chan struct{})
	returned := make(chan struct{})
	h := endSessionHandler(func() { cancelled.Store(true); close(started) }, &ending, done, hangGuard)
	go func() {
		h()
		close(returned)
	}()
	waitClosed(t, started, "cancel")
	select {
	case <-returned:
		t.Fatal("handler returned before run finished")
	case <-time.After(50 * time.Millisecond):
	}
	if !ending.Load() {
		t.Fatal("handler did not mark the session as ending")
	}
	if !cancelled.Load() {
		t.Fatal("cancel not called before the wait")
	}
	close(done)
	waitClosed(t, returned, "handler")
}

func TestEndSessionHandlerStopsWaitingAtTheGuard(t *testing.T) {
	var ending atomic.Bool
	returned := make(chan struct{})
	h := endSessionHandler(func() {}, &ending, make(chan struct{}), 50*time.Millisecond)
	go func() {
		h()
		close(returned)
	}()
	waitClosed(t, returned, "handler")
}

func mainGoText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func TestMainClosesRunDoneAfterRun(t *testing.T) {
	text := mainGoText(t)
	start := strings.Index(text, "func main() {")
	if start < 0 {
		t.Fatal("func main() not found")
	}
	body := text[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	r, c, f := strings.Index(body, "run("), strings.Index(body, "close(runDone)"), strings.Index(body, "reportFatal(")
	if r < 0 || c < 0 || f < 0 || !(r < c && c < f) {
		t.Fatalf("in main: run( at %d, close(runDone) at %d, reportFatal( at %d; want that order", r, c, f)
	}
}

func TestMainStartsTheTrayOnlyWhenInteractive(t *testing.T) {
	text := mainGoText(t)
	i := strings.Index(text, "startTray(")
	if i < 0 {
		t.Fatal("main.go does not call startTray")
	}
	if strings.Count(text, "startTray(") != 1 {
		t.Fatal("startTray is called more than once in main.go")
	}
	// startTray lives in startInteractive, which run calls once, only inside its `if interactive` block.
	if fn := strings.LastIndex(text[:i], "\nfunc "); fn < 0 || !strings.HasPrefix(text[fn:], "\nfunc startInteractive(") {
		t.Fatal("startTray is not inside startInteractive")
	}
	if strings.Count(text, "defer startInteractive(appCtx") != 1 {
		t.Fatal("run must call startInteractive exactly once")
	}
	call := strings.Index(text, "defer startInteractive(appCtx")
	open := strings.LastIndex(text[:call], "\n\tif interactive {")
	if open < 0 {
		t.Fatal("startInteractive is not inside an `if interactive` block")
	}
	// The block ends at the first line that is just a tab and a closing brace.
	if end := strings.Index(text[open+1:], "\n\t}\n"); end < 0 || open+1+end < call {
		t.Fatal("startInteractive is after the end of the `if interactive` block")
	}
}
