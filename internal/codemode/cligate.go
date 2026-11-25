package codemode

import (
	"context"
	"sync"
	"time"
)

// The command-line window (BL-371, owner ruling 2026-11-25): "unravis code run" is off unless the app has opened a window for it.
// Code mode alone does not let a program on the PC run scripts through the owner pipe; the person at the app has to say "allow the
// command line for N minutes". The window lives in memory only: it is closed when the app starts, when it ends, and when the person
// closes it. It is never written to code-mode.json, so no file edit and no restart of a script can turn it on.
//
// The gate is the broker's Authorize hook (Config.Authorize), so it runs on every path to Run, after the on/off switch and before any
// identifier, case or record work: nothing is recorded for a refused run. Only the surface "cli" is held by it. The app's own page
// ("ui") and the MCP entrance have their own checks and are not affected.

// MaxCLIWindow is the longest one opening may last. A longer wish is refused, not cut, so the person always sees what they got.
const MaxCLIWindow = 60 * time.Minute

// CLIGate holds the window. The zero value is not usable; use NewCLIGate. Safe for concurrent use.
type CLIGate struct {
	mu    sync.Mutex
	until time.Time
	now   func() time.Time
}

// NewCLIGate returns a closed gate. now is the clock (time.Now when nil).
func NewCLIGate(now func() time.Time) *CLIGate {
	if now == nil {
		now = time.Now
	}
	return &CLIGate{now: now}
}

// Open lets the command line run for d, counted from now. A d of zero or less closes the window; a d over MaxCLIWindow is clamped to it.
// It returns the moment the window ends (the zero time when closed). Opening again replaces the end, it does not add to it.
func (g *CLIGate) Open(d time.Duration) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d <= 0 {
		g.until = time.Time{}
		return time.Time{}
	}
	if d > MaxCLIWindow {
		d = MaxCLIWindow
	}
	g.until = g.now().Add(d)
	return g.until
}

// Close ends the window now.
func (g *CLIGate) Close() { g.Open(0) }

// OpenUntil reports when the window ends, or false when it is closed or has run out.
func (g *CLIGate) OpenUntil() (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.until.IsZero() || !g.now().Before(g.until) {
		return time.Time{}, false
	}
	return g.until, true
}

// Authorize is Config.Authorize: a command-line run is refused with cli_closed unless the window is open. A nil gate is closed.
func (g *CLIGate) Authorize(_ context.Context, req RunRequest) error {
	if req.Surface != "cli" {
		return nil
	}
	if g != nil {
		if _, open := g.OpenUntil(); open {
			return nil
		}
	}
	return refuse(CodeCLIClosed, plain[CodeCLIClosed])
}
