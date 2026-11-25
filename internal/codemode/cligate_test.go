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
				switch (i + j) % 4 {
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
