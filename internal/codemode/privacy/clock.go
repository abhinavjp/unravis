package privacy

import (
	"errors"
	"sync"
	"time"
)

// Quantum is the step of the script clock (spec D12): Date.now() and performance.now() move in steps of 10 ms, so a script cannot
// time a host call or a memory access finely.
const Quantum = 10 * time.Millisecond

// maxSafeMs is the largest integer a JavaScript number holds exactly (2^53 - 1). A base time beyond it is refused.
const maxSafeMs = 1<<53 - 1

// ClockMode says how the clock moves.
type ClockMode string

const (
	// ClockLive follows the host clock from the recorded base, in steps of Quantum.
	ClockLive ClockMode = "live"
	// ClockFrozen never moves: Date.now() is the base and performance.now() is 0 (replay).
	ClockFrozen ClockMode = "frozen"
)

var errBadBase = errors.New("privacy: clock base out of range")
var errBadMode = errors.New("privacy: unknown clock mode")

// ScriptClock is the time a script sees. The caller gives the base (the run start, stored in code_runs.clock_ms, or the stored value
// on replay) and a function for the host time, so tests inject their own and never wait. Safe for concurrent use. Time never goes
// backwards, even when the host clock does.
type ScriptClock struct {
	mode   ClockMode
	baseMs int64 // the base, already floored to Quantum
	start  time.Time
	now    func() time.Time
	mu     sync.Mutex
	last   time.Duration // the last quantized elapsed time given out
}

// NewScriptClock makes the clock. baseMs is milliseconds since the Unix epoch and is floored to a multiple of 10 (BaseMs returns
// the floored value; store that one). now may be nil (time.Now). In frozen mode now is not read.
func NewScriptClock(mode ClockMode, baseMs int64, now func() time.Time) (*ScriptClock, error) {
	if mode != ClockLive && mode != ClockFrozen {
		return nil, errBadMode
	}
	if baseMs < 0 || baseMs > maxSafeMs-int64(24*time.Hour/time.Millisecond) {
		return nil, errBadBase
	}
	if now == nil {
		now = time.Now
	}
	c := &ScriptClock{mode: mode, baseMs: baseMs - baseMs%int64(Quantum/time.Millisecond), now: now}
	if mode == ClockLive {
		c.start = now()
	}
	return c, nil
}

// BaseMs is the base time of the run, a multiple of 10 ms.
func (c *ScriptClock) BaseMs() int64 { return c.baseMs }

// Mode is the clock mode.
func (c *ScriptClock) Mode() ClockMode { return c.mode }

// elapsed is the time since the start, floored to Quantum and never smaller than the last answer.
func (c *ScriptClock) elapsed() time.Duration {
	if c.mode == ClockFrozen {
		return 0
	}
	d := c.now().Sub(c.start)
	if d < 0 {
		d = 0
	}
	d -= d % Quantum
	c.mu.Lock()
	defer c.mu.Unlock()
	if d < c.last {
		d = c.last
	}
	c.last = d
	return d
}

// DateNowMs is what Date.now() returns: the base plus the elapsed time in whole 10 ms steps.
func (c *ScriptClock) DateNowMs() int64 {
	return c.baseMs + int64(c.elapsed()/time.Millisecond)
}

// PerfNowMs is what performance.now() returns: milliseconds since the start of the run, in whole 10 ms steps.
func (c *ScriptClock) PerfNowMs() float64 {
	return float64(c.elapsed() / time.Millisecond)
}

// ManualSource is a host clock that moves only when told. It is for tests and for a replay driver; Now is the function to pass to
// NewScriptClock.
type ManualSource struct {
	mu sync.Mutex
	t  time.Time
}

// NewManualSource starts at t.
func NewManualSource(t time.Time) *ManualSource { return &ManualSource{t: t} }

// Now returns the current manual time.
func (m *ManualSource) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

// Advance moves the time by d (a negative d moves it back, which ScriptClock must survive).
func (m *ManualSource) Advance(d time.Duration) {
	m.mu.Lock()
	m.t = m.t.Add(d)
	m.mu.Unlock()
}
