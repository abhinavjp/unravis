package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/applog"
)

// fakeReopen is a reopener with fakes and a controllable clock.
type fakeReopen struct {
	mu       sync.Mutex
	clock    time.Time
	codes    []string // codes newCode hands out, in order
	nextCode int
	codeErr  error
	rearmOK  bool
	openErr  error

	rearmCodes []string
	rearmUntil []time.Time
	opened     []string
	warns      chan [2]string
	r          *reopener
}

func newFakeReopen() *fakeReopen {
	f := &fakeReopen{
		clock:   time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		rearmOK: true,
		warns:   make(chan [2]string, 8),
	}
	f.r = &reopener{
		base: "http://127.0.0.1:5555",
		rearm: func(c string, u time.Time) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.rearmCodes = append(f.rearmCodes, c)
			f.rearmUntil = append(f.rearmUntil, u)
			return f.rearmOK
		},
		newCode: func() (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.codeErr != nil {
				return "", f.codeErr
			}
			c := "SECRETCODE-0000000000000000"
			if f.nextCode < len(f.codes) {
				c = f.codes[f.nextCode]
			}
			f.nextCode++
			return c, nil
		},
		open: func(u string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.opened = append(f.opened, u)
			return f.openErr
		},
		warn: func(title, text string) { f.warns <- [2]string{title, text} },
		now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.clock
		},
		link: func(c string) string { return "http://127.0.0.1:5555/launch?c=" + c },
	}
	return f
}

func (f *fakeReopen) advance(d time.Duration) {
	f.mu.Lock()
	f.clock = f.clock.Add(d)
	f.mu.Unlock()
}

func (f *fakeReopen) waitWarn(t *testing.T) [2]string {
	t.Helper()
	select {
	case w := <-f.warns:
		return w
	case <-time.After(10 * time.Second):
		t.Fatal("no warning shown")
		return [2]string{}
	}
}

func (f *fakeReopen) noWarn(t *testing.T) {
	t.Helper()
	select {
	case w := <-f.warns:
		t.Fatalf("unexpected warning %q", w)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReopenRearmsAndOpensTheBrowser(t *testing.T) {
	f := newFakeReopen()
	start := f.clock
	f.r.Reopen()
	if len(f.rearmCodes) != 1 || f.rearmCodes[0] != "SECRETCODE-0000000000000000" {
		t.Fatalf("rearm codes = %v", f.rearmCodes)
	}
	if want := start.Add(2 * time.Minute); !f.rearmUntil[0].Equal(want) {
		t.Fatalf("until = %v, want %v", f.rearmUntil[0], want)
	}
	if len(f.opened) != 1 || f.opened[0] != "http://127.0.0.1:5555/launch?c="+f.rearmCodes[0] {
		t.Fatalf("opened = %v", f.opened)
	}
	f.noWarn(t)
}

func TestReopenIsThrottled(t *testing.T) {
	f := newFakeReopen()
	f.r.Reopen()
	f.advance(time.Second)
	f.r.Reopen()
	if len(f.opened) != 1 {
		t.Fatalf("opened %d times inside the gap, want 1", len(f.opened))
	}
	f.advance(reopenMinGap)
	f.r.Reopen()
	if len(f.opened) != 2 {
		t.Fatalf("opened %d times after the gap, want 2", len(f.opened))
	}
}

func TestReopenWarnsWithoutTheLinkWhenTheBrowserFails(t *testing.T) {
	f := newFakeReopen()
	f.openErr = errors.New("no browser")
	f.r.Reopen()
	w := f.waitWarn(t)
	if !strings.Contains(w[1], f.r.base) || !strings.Contains(w[1], "Open Unravis") {
		t.Fatalf("warning lacks the address or the tray hint: %q", w[1])
	}
	for _, bad := range []string{f.rearmCodes[0], "/launch"} {
		if strings.Contains(w[0], bad) || strings.Contains(w[1], bad) {
			t.Fatalf("warning contains %q", bad)
		}
	}
}

func TestReopenDoesNotBlockOnAModalWarning(t *testing.T) {
	f := newFakeReopen()
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	f.r.warn = func(string, string) {
		started <- struct{}{}
		<-release
	}
	f.openErr = errors.New("no browser")
	done := make(chan struct{})
	go func() { f.r.Reopen(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Reopen blocked on the modal warning")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("warning not started")
	}
	f.advance(reopenMinGap)
	f.r.Reopen()
	if len(f.rearmCodes) != 2 {
		t.Fatalf("rearm calls = %d, want 2", len(f.rearmCodes))
	}
	close(release)
}

// A request that failed must not start the gap: the warning says "try again", and the person's next click has to be tried.
func TestReopenFailedAttemptsDoNotStartTheGap(t *testing.T) {
	f := newFakeReopen()
	f.rearmOK = false
	f.r.Reopen()
	f.waitWarn(t)
	f.mu.Lock()
	f.rearmOK = true
	f.mu.Unlock()
	f.r.Reopen() // no time has passed
	if len(f.rearmCodes) != 2 || len(f.opened) != 1 {
		t.Fatalf("after a failed rearm, an immediate retry: rearm calls %d, opens %d, want 2 and 1", len(f.rearmCodes), len(f.opened))
	}

	g := newFakeReopen()
	g.codeErr = errors.New("rand failed")
	g.r.Reopen()
	g.waitWarn(t)
	g.mu.Lock()
	g.codeErr = nil
	g.mu.Unlock()
	g.r.Reopen()
	if len(g.opened) != 1 {
		t.Fatalf("after a failed code, an immediate retry opened %d times, want 1", len(g.opened))
	}

	h := newFakeReopen()
	h.openErr = errors.New("no browser")
	h.r.Reopen()
	h.waitWarn(t)
	h.mu.Lock()
	h.openErr = nil
	h.mu.Unlock()
	h.r.Reopen()
	if len(h.opened) != 2 {
		t.Fatalf("after a failed open, an immediate retry tried %d times, want 2", len(h.opened))
	}
	h.r.Reopen() // this one worked, so the gap now applies
	if len(h.opened) != 2 {
		t.Fatalf("a request right after a success opened again: %d", len(h.opened))
	}
}

// Two requests at the same moment open the browser once.
func TestReopenDropsARequestWhileAnotherIsBeingServed(t *testing.T) {
	f := newFakeReopen()
	inRearm := make(chan struct{})
	release := make(chan struct{})
	f.r.rearm = func(string, time.Time) bool {
		close(inRearm)
		<-release
		return true
	}
	done := make(chan struct{})
	go func() { f.r.Reopen(); close(done) }()
	<-inRearm
	f.r.Reopen() // dropped: the first is still in progress
	close(release)
	<-done
	if len(f.opened) != 1 {
		t.Fatalf("opened %d times, want 1", len(f.opened))
	}
}

func TestReopenShowsOneWarningAtATime(t *testing.T) {
	f := newFakeReopen()
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	f.r.warn = func(string, string) {
		mu.Lock()
		calls++
		mu.Unlock()
		started <- struct{}{}
		<-release
	}
	f.openErr = errors.New("no browser")
	f.r.Reopen()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first warning not started")
	}
	f.advance(reopenMinGap)
	f.r.Reopen()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("warn called %d times while the first box was open, want 1", n)
	}
	close(release)
}

func TestReopenNeverLogsTheCode(t *testing.T) {
	f := newFakeReopen()
	f.openErr = errors.New("no browser")
	var buf bytes.Buffer
	oldOut, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(oldOut); log.SetFlags(oldFlags) })
	f.r.Reopen()
	f.waitWarn(t)
	if s := buf.String(); strings.Contains(s, f.rearmCodes[0]) || strings.Contains(s, "/launch?c=") {
		t.Fatalf("log holds the code or link: %q", s)
	}
}

func TestReopenReportsAFailedRearm(t *testing.T) {
	f := newFakeReopen()
	f.rearmOK = false
	f.r.Reopen()
	w := f.waitWarn(t)
	if !strings.Contains(w[1], "still starting") {
		t.Fatalf("warning = %q", w[1])
	}
	if len(f.opened) != 0 {
		t.Fatal("browser opened after a failed rearm")
	}
}

func TestReopenReportsAFailedCodeGeneration(t *testing.T) {
	f := newFakeReopen()
	f.codeErr = errors.New("rand failed")
	f.r.Reopen()
	w := f.waitWarn(t)
	if !strings.Contains(w[1], "Could not prepare a new link") {
		t.Fatalf("warning = %q", w[1])
	}
	if len(f.rearmCodes) != 0 || len(f.opened) != 0 {
		t.Fatal("rearm or open ran without a code")
	}
}

// letterCodes makes n distinct, letters-only 24-character codes. Sanitize's
// long-token guess leaves them alone, so only the registry can hide them.
func letterCodes(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strings.Repeat("q", 20) + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "zz"
	}
	return out
}

func withCodeSource(t *testing.T, codes []string) {
	t.Helper()
	old := launchCodeSource
	t.Cleanup(func() { launchCodeSource = old })
	next := 0
	launchCodeSource = func() (string, error) {
		c := codes[next]
		next++
		return c, nil
	}
}

func TestReopenRegistersTheCodeAsASecret(t *testing.T) {
	f := newFakeReopen()
	withCodeSource(t, letterCodes(1))
	f.r.newCode = mintLaunchCode
	f.r.Reopen()
	if len(f.rearmCodes) != 1 {
		t.Fatal("no rearm")
	}
	code := f.rearmCodes[0]
	if got := applog.Sanitize("x " + code + " y"); strings.Contains(got, code) {
		t.Fatal("the code survived Sanitize")
	}
}

func TestReopenManyTimesKeepsTheSecretListBounded(t *testing.T) {
	f := newFakeReopen()
	withCodeSource(t, letterCodes(30))
	f.r.newCode = mintLaunchCode
	for i := 0; i < 30; i++ {
		f.advance(reopenMinGap)
		f.r.Reopen()
	}
	if len(f.rearmCodes) != 30 {
		t.Fatalf("rearm calls = %d, want 30", len(f.rearmCodes))
	}
	for i, c := range f.rearmCodes {
		hidden := !strings.Contains(applog.Sanitize(c), c)
		if want := i >= 22; hidden != want {
			t.Fatalf("code %d hidden = %v, want %v", i, hidden, want)
		}
	}
}

func TestSystem32FileUsesTheWindowsFolder(t *testing.T) {
	t.Setenv("SystemRoot", `C:\X`)
	if got, want := system32File("rundll32.exe"), filepath.Join(`C:\X`, "System32", "rundll32.exe"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	t.Setenv("SystemRoot", "")
	if got, want := system32File("rundll32.exe"), filepath.Join(`C:\Windows`, "System32", "rundll32.exe"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	src := readMainGo(t)
	if strings.Contains(src, `exec.Command("rundll32.exe"`) {
		t.Fatal("main.go starts rundll32.exe without a full path")
	}
}

func readMainGo(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMainUsesReoNotAShadowedType(t *testing.T) {
	src := readMainGo(t)
	if !strings.Contains(src, "reo.Reopen") {
		t.Fatal("main.go does not use reo.Reopen")
	}
	if strings.Contains(src, "reopener.Reopen") {
		t.Fatal("main.go uses reopener.Reopen")
	}
}

func TestQuitSignalCancelsTheAppContext(t *testing.T) {
	src := readMainGo(t)
	i := strings.Index(src, "instance.Listen(")
	if i < 0 {
		t.Fatal("main.go has no instance.Listen call")
	}
	call := src[i : i+strings.Index(src[i:], "\n")]
	if !strings.Contains(call, "cancel)") {
		t.Fatalf("Listen call does not receive cancel: %s", call)
	}
	if !strings.Contains(src, "signal.NotifyContext(appCtx") {
		t.Fatal("the signal context is not a child of appCtx")
	}
	if !strings.Contains(src, "shutdownOnSignal(") {
		t.Fatal("shutdownOnSignal is no longer called")
	}
	if strings.Contains(src, "newLaunchCode(") && strings.Count(src, "newLaunchCode(") > 1 {
		t.Fatal("main.go calls newLaunchCode directly")
	}
}

func TestReopenToLandsOnAScreen(t *testing.T) {
	f := newFakeReopen()
	f.r.ReopenTo("whatsnew")
	if len(f.opened) != 1 || f.opened[0] != "http://127.0.0.1:5555/launch?c="+f.rearmCodes[0]+"&to=whatsnew" {
		t.Fatalf("opened = %v", f.opened)
	}
	f.noWarn(t)
}

func TestReopenToEscapesTheScreenName(t *testing.T) {
	f := newFakeReopen()
	f.r.ReopenTo("a&b=c")
	if len(f.opened) != 1 || !strings.HasSuffix(f.opened[0], "&to=a%26b%3Dc") {
		t.Fatalf("opened = %v", f.opened)
	}
}

func TestReopenToDifferentScreenIsNotThrottled(t *testing.T) {
	f := newFakeReopen()
	f.r.Reopen()
	f.advance(time.Second)
	f.r.ReopenTo("updates") // a notice click right after another open still works
	f.r.ReopenTo("updates") // the same request again is dropped
	if len(f.opened) != 2 {
		t.Fatalf("opened %d times, want 2: %v", len(f.opened), f.opened)
	}
}

// A panic inside the work (the callers recover it and the process lives on) must not leave the limiter busy: the next request has to be
// tried, and the panicked attempt must not start the gap.
func TestReopenPanicDoesNotLeaveTheLimiterBusy(t *testing.T) {
	f := newFakeReopen()
	f.r.rearm = func(string, time.Time) bool { panic("rearm blew up") }
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected the panic to reach the caller")
			}
		}()
		f.r.Reopen()
	}()

	f.r.rearm = func(c string, u time.Time) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.rearmCodes = append(f.rearmCodes, c)
		return true
	}
	f.r.Reopen() // no time has passed: only a stuck busy flag or a started gap would drop this
	if len(f.rearmCodes) != 1 || len(f.opened) != 1 {
		t.Fatalf("after a panicked attempt, an immediate retry: rearm calls %d, opens %d, want 1 and 1", len(f.rearmCodes), len(f.opened))
	}
	f.r.Reopen() // that one worked, so the gap now applies
	if len(f.opened) != 1 {
		t.Fatalf("a request right after a success opened again: %d", len(f.opened))
	}
}
