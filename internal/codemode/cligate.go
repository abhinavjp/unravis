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
// "No time limit" (owner ruling 2026-11-26, prototype v2) is a second open state of the same gate, not a long window: it has no end, and
// it is held in memory only in exactly the same way. A new gate is closed, so the person must confirm it again after every app start;
// the settings route closes it when code mode is switched off or a timed window is set. It never shows as an end time (OpenUntil stays
// closed while it is on), and nothing about it is written to code-mode.json.
//
// The gate is the broker's Authorize hook (Config.Authorize), so it runs on every path to Run, after the on/off switch and before any
// identifier, case or record work: nothing is recorded for a refused run. Only the surface "cli" is held by it. The app's own page
// ("ui") and the MCP entrance have their own checks and are not affected.

// MaxCLIWindow is the longest one opening may last. A longer wish is refused, not cut, so the person always sees what they got.
const MaxCLIWindow = 60 * time.Minute

// CLIGate holds the window. The zero value is not usable; use NewCLIGate. Safe for concurrent use.
type CLIGate struct {
	mu        sync.Mutex
	until     time.Time
	unlimited bool // the open-ended state: in memory only, never set by anything but OpenUnlimited
	now       func() time.Time
}

// NewCLIGate returns a closed gate. now is the clock (time.Now when nil).
func NewCLIGate(now func() time.Time) *CLIGate {
	if now == nil {
		now = time.Now
	}
	return &CLIGate{now: now}
}

// Open lets the command line run for d, counted from now. A d of zero or less closes the window; a d over MaxCLIWindow is clamped to it.
// It returns the moment the window ends (the zero time when closed). Opening again replaces the end, it does not add to it. Whatever d is,
// the no-time-limit state ends: a timed window replaces it, and a close closes everything.
func (g *CLIGate) Open(d time.Duration) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.unlimited = false
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

// OpenUnlimited lets the command line run with no end until it is closed, code mode goes off, or the app stops. It replaces a timed
// window (the end is cleared: there is no end time to report). Nothing here reaches the disk.
func (g *CLIGate) OpenUnlimited() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until = time.Time{}
	g.unlimited = true
}

// Unlimited reports whether the no-time-limit state is on.
func (g *CLIGate) Unlimited() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.unlimited
}

// Close ends the window, and the no-time-limit state, now.
func (g *CLIGate) Close() { g.Open(0) }

// OpenUntil reports when the timed window ends, or false when it is closed or has run out. It is also false while the no-time-limit
// state is on (that state has no end): ask Unlimited, or Allows, for "can the command line run now".
func (g *CLIGate) OpenUntil() (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.until.IsZero() || !g.now().Before(g.until) {
		return time.Time{}, false
	}
	return g.until, true
}

// Allows reports whether a command-line run may start now: the no-time-limit state is on, or a timed window has not ended. A nil gate
// allows nothing.
func (g *CLIGate) Allows() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.unlimited {
		return true
	}
	return !g.until.IsZero() && g.now().Before(g.until)
}

// Authorize is Config.Authorize: a command-line run is refused with cli_closed unless the window is open (timed, or no time limit).
// A nil gate is closed.
func (g *CLIGate) Authorize(_ context.Context, req RunRequest) error {
	if req.Surface != "cli" {
		return nil
	}
	if g.Allows() {
		return nil
	}
	return refuse(CodeCLIClosed, plain[CodeCLIClosed])
}
