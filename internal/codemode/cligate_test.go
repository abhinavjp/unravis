package codemode

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type gateClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *gateClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *gateClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newGate() (*CLIGate, *gateClock) {
	c := &gateClock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	return NewCLIGate(c.now), c
}

func cliRefused(err error) bool {
	var ref *Refusal
	return errors.As(err, &ref) && ref.Code == CodeCLIClosed && ref.Message != ""
}

func TestCLIGateIsClosedUntilOpened(t *testing.T) {
	g, _ := newGate()
	if _, open := g.OpenUntil(); open {
		t.Fatal("a new gate is open")
	}
	if err := g.Authorize(context.Background(), RunRequest{Surface: "cli"}); !cliRefused(err) {
		t.Fatalf("closed gate, cli run: %v", err)
	}
}

func TestCLIGateOpenExpireCloseAndReplace(t *testing.T) {
	g, clk := newGate()
	cli := RunRequest{Surface: "cli"}
	end := g.Open(10 * time.Minute)
	if !end.Equal(clk.now().Add(10 * time.Minute)) {
		t.Fatalf("end = %v", end)
	}
	if err := g.Authorize(context.Background(), cli); err != nil {
		t.Fatalf("open gate: %v", err)
	}
	clk.add(9*time.Minute + 59*time.Second)
	if err := g.Authorize(context.Background(), cli); err != nil {
		t.Fatalf("a second before the end: %v", err)
	}
	clk.add(time.Second) // exactly at the end: closed
	if err := g.Authorize(context.Background(), cli); !cliRefused(err) {
		t.Fatalf("at the end: %v", err)
	}
	if _, open := g.OpenUntil(); open {
		t.Fatal("OpenUntil says open after the end")
	}
	// Opening again replaces the end, it does not add to it.
	g.Open(5 * time.Minute)
	clk.add(2 * time.Minute)
	end = g.Open(5 * time.Minute)
	if !end.Equal(clk.now().Add(5 * time.Minute)) {
		t.Fatalf("a second opening added time: %v", end)
	}
	// Close and a zero or negative length shut it at once.
	g.Close()
	if err := g.Authorize(context.Background(), cli); !cliRefused(err) {
		t.Fatalf("after Close: %v", err)
	}
	g.Open(time.Minute)
	if !g.Open(0).IsZero() {
		t.Fatal("Open(0) returned an end")
	}
	g.Open(time.Minute)
	g.Open(-time.Minute)
	if err := g.Authorize(context.Background(), cli); !cliRefused(err) {
		t.Fatalf("after a negative length: %v", err)
	}
}

func TestCLIGateClampsToTheLongestWindow(t *testing.T) {
	g, clk := newGate()
	end := g.Open(48 * time.Hour)
	if want := clk.now().Add(MaxCLIWindow); !end.Equal(want) {
		t.Fatalf("end = %v, want %v", end, want)
	}
	clk.add(MaxCLIWindow)
	if _, open := g.OpenUntil(); open {
		t.Fatal("still open after the longest window")
	}
}

// Only the command line is held. The app's own page and the MCP entrance have their own checks, and a closed gate must not change them.
func TestCLIGateLeavesOtherSurfacesAlone(t *testing.T) {
	g, _ := newGate()
	for _, s := range []string{"ui", "mcp", ""} {
		if err := g.Authorize(context.Background(), RunRequest{Surface: s}); err != nil {
			t.Errorf("surface %q on a closed gate: %v", s, err)
		}
	}
	// Look-alikes of "cli" are not "cli": the hook compares the exact name, and the entrance sets it, not the caller.
	for _, s := range []string{"CLI", "cli ", "cli2"} {
		if err := g.Authorize(context.Background(), RunRequest{Surface: s}); err != nil {
			t.Errorf("surface %q: %v", s, err)
		}
	}
}

func TestNilCLIGateIsClosed(t *testing.T) {
	var g *CLIGate
	if err := g.Authorize(context.Background(), RunRequest{Surface: "cli"}); !cliRefused(err) {
		t.Fatalf("nil gate: %v", err)
	}
}

func TestCLIGateIsSafeForConcurrentUse(t *testing.T) {
	g, clk := newGate()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				switch (i + j) % 5 {
				case 4:
					g.OpenUnlimited()
				case 0:
					g.Open(time.Minute)
				case 1:
					g.Close()
				case 2:
					_ = g.Authorize(context.Background(), RunRequest{Surface: "cli"})
				default:
					clk.add(time.Second)
					g.OpenUntil()
				}
			}
		}(i)
	}
	wg.Wait()
}

// "No time limit": a second open state of the same gate. It allows the command line with no end, has no end time to report, and is
// replaced or closed by every other call. A new gate is closed, which is what makes it need a fresh confirmation after an app start.
func TestCLIGateUnlimitedAllowsTheCommandLineWithNoEnd(t *testing.T) {
	g, clk := newGate()
	cli := RunRequest{Surface: "cli"}
	if g.Unlimited() || g.Allows() {
		t.Fatal("a new gate is open")
	}
	g.OpenUnlimited()
	if !g.Unlimited() || !g.Allows() {
		t.Fatal("unlimited did not open the gate")
	}
	if _, open := g.OpenUntil(); open {
		t.Fatal("unlimited reports an end time")
	}
	for _, d := range []time.Duration{time.Minute, 61 * time.Minute, 24 * time.Hour, 30 * 24 * time.Hour} {
		clk.add(d)
		if err := g.Authorize(context.Background(), cli); err != nil {
			t.Fatalf("unlimited after %v: %v", d, err)
		}
	}
	// Only the command line is judged, as for a timed window.
	if err := g.Authorize(context.Background(), RunRequest{Surface: "ui"}); err != nil {
		t.Fatalf("ui: %v", err)
	}
}

// A restart is a new gate: nothing carries the state over, so the person has to say it again.
func TestCLIGateUnlimitedDoesNotSurviveANewGate(t *testing.T) {
	g, _ := newGate()
	g.OpenUnlimited()
	g2, _ := newGate()
	if g2.Unlimited() || g2.Allows() {
		t.Fatal("a new gate starts open")
	}
	if err := g2.Authorize(context.Background(), RunRequest{Surface: "cli"}); !cliRefused(err) {
		t.Fatalf("new gate, cli run: %v", err)
	}
}

func TestCLIGateAnyOtherCallEndsUnlimited(t *testing.T) {
	cli := RunRequest{Surface: "cli"}
	// Close.
	g, _ := newGate()
	g.OpenUnlimited()
	g.Close()
	if g.Unlimited() || g.Allows() || !cliRefused(g.Authorize(context.Background(), cli)) {
		t.Fatal("close left it open")
	}
	// A timed window replaces it, and then ends on time.
	g, clk := newGate()
	g.OpenUnlimited()
	end := g.Open(5 * time.Minute)
	if g.Unlimited() {
		t.Fatal("a timed window left unlimited on")
	}
	if got, open := g.OpenUntil(); !open || !got.Equal(end) {
		t.Fatalf("timed window: %v %v", got, open)
	}
	clk.add(5 * time.Minute)
	if !cliRefused(g.Authorize(context.Background(), cli)) {
		t.Fatal("the timed window that replaced unlimited did not end")
	}
	// A zero window closes it.
	g, _ = newGate()
	g.OpenUnlimited()
	g.Open(0)
	if g.Unlimited() || g.Allows() {
		t.Fatal("a zero window left it open")
	}
	// Unlimited replaces a timed window and clears its end.
	g, _ = newGate()
	g.Open(5 * time.Minute)
	g.OpenUnlimited()
	if _, open := g.OpenUntil(); open || !g.Unlimited() {
		t.Fatal("unlimited kept the timed end")
	}
}

func TestNilCLIGateDoesNotAllow(t *testing.T) {
	var g *CLIGate
	if g.Allows() {
		t.Fatal("nil gate allows")
	}
}

func TestCLIGateCloseIfOpenReportsWhatItClosed(t *testing.T) {
	var nilGate *CLIGate
	if nilGate.CloseIfOpen() {
		t.Fatal("a nil gate was open")
	}
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	g := NewCLIGate(func() time.Time { return now })
	if g.CloseIfOpen() {
		t.Fatal("a new gate was open")
	}
	g.Open(10 * time.Minute)
	if !g.CloseIfOpen() || g.Allows() || g.CloseIfOpen() {
		t.Fatal("a timed window: first close reports open, second does not")
	}
	g.OpenUnlimited()
	if !g.CloseIfOpen() || g.Allows() || g.Unlimited() {
		t.Fatal("no time limit was not closed")
	}
	g.Open(time.Minute)
	now = now.Add(2 * time.Minute)
	if g.CloseIfOpen() {
		t.Fatal("a window that had run out was reported as open")
	}
}
