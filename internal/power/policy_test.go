package power

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

type fakeClock struct {
	mu        sync.Mutex
	now       time.Time
	afterCall int
	timers    []fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.afterCall++
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, fakeTimer{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []fakeTimer
	kept := c.timers[:0]
	for _, timer := range c.timers {
		if !timer.at.After(c.now) {
			due = append(due, timer)
		} else {
			kept = append(kept, timer)
		}
	}
	c.timers = kept
	c.mu.Unlock()
	for _, timer := range due {
		timer.ch <- c.now
	}
}

func (c *fakeClock) AfterCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.afterCall
}

type modeLog struct {
	mu    sync.Mutex
	modes []Mode
}

func (l *modeLog) add(mode Mode) {
	l.mu.Lock()
	l.modes = append(l.modes, mode)
	l.mu.Unlock()
}

func (l *modeLog) snapshot() []Mode {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Mode{}, l.modes...)
}

func runPolicy(t *testing.T, p *Policy) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestPolicyThrottlesAfterGrace(t *testing.T) {
	clock := newFakeClock()
	var modes modeLog
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		return nil
	}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace - time.Nanosecond)
	if p.Mode() != Normal {
		t.Fatal("throttled before the grace period")
	}
	clock.Advance(time.Nanosecond)
	eventually(t, func() bool { return p.Mode() == Idle })
	if !reflect.DeepEqual(modes.snapshot(), []Mode{Idle}) {
		t.Fatalf("applied modes = %v", modes.snapshot())
	}
}

func TestTouchClearsIdle(t *testing.T) {
	clock := newFakeClock()
	var modes modeLog
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		return nil
	}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return p.Mode() == Idle })
	p.Touch()
	if p.Mode() != Normal {
		t.Fatal("touch did not clear idle mode")
	}
	if !reflect.DeepEqual(modes.snapshot(), []Mode{Idle, Normal}) {
		t.Fatalf("applied modes = %v", modes.snapshot())
	}
}

func TestInFlightBlocksIdle(t *testing.T) {
	clock := newFakeClock()
	p := New(Config{Now: clock.Now, After: clock.After})
	runPolicy(t, p)
	done := p.Begin()
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return clock.AfterCalls() >= 2 })
	if p.Mode() != Normal {
		t.Fatal("in-flight request was throttled")
	}
	done()
	clock.Advance(DefaultCheck)
	eventually(t, func() bool { return p.Mode() == Idle })
}

func TestInUseBlocksIdle(t *testing.T) {
	clock := newFakeClock()
	var busy atomic.Bool
	busy.Store(true)
	p := New(Config{Now: clock.Now, After: clock.After, InUse: func() bool { return busy.Load() }})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return clock.AfterCalls() >= 2 })
	if p.Mode() != Normal {
		t.Fatal("background work was throttled")
	}
	busy.Store(false)
	clock.Advance(DefaultCheck)
	eventually(t, func() bool { return p.Mode() == Idle })
}

func TestAddedBusyCheckBlocksIdle(t *testing.T) {
	clock := newFakeClock()
	var busy atomic.Bool
	busy.Store(true)
	p := New(Config{Now: clock.Now, After: clock.After})
	p.AddBusy(busy.Load)
	p.AddBusy(nil)
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return clock.AfterCalls() >= 2 })
	if p.Mode() != Normal {
		t.Fatal("registered background work was throttled")
	}
	busy.Store(false)
	clock.Advance(DefaultCheck)
	eventually(t, func() bool { return p.Mode() == Idle })
}

func TestNoTimerWhileIdle(t *testing.T) {
	clock := newFakeClock()
	p := New(Config{Now: clock.Now, After: clock.After})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return p.Mode() == Idle })
	calls := clock.AfterCalls()
	clock.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	if got := clock.AfterCalls(); got != calls {
		t.Fatalf("idle policy armed another timer: %d -> %d", calls, got)
	}
}

func TestApplyFailureWaitsForTouch(t *testing.T) {
	clock := newFakeClock()
	var modes modeLog
	var idleCalls atomic.Int32
	failIdle := true
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		if mode == Idle && failIdle {
			failIdle = false
			return errors.New("unsupported")
		}
		return nil
	}, OnIdle: []func(){func() { idleCalls.Add(1) }}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return clock.AfterCalls() >= 2 })
	if p.Mode() != Normal {
		t.Fatal("failed throttle changed mode")
	}
	if got := idleCalls.Load(); got != 0 {
		t.Fatalf("failed throttle ran %d idle hooks", got)
	}
	clock.Advance(time.Hour)
	if p.Mode() != Normal {
		t.Fatal("failed throttle retried without new activity")
	}
	attempts := len(modes.snapshot())
	p.Touch()
	clock.Advance(DefaultGrace)
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return len(modes.snapshot()) > attempts })
	eventually(t, func() bool { return p.Mode() == Idle })
	if got := idleCalls.Load(); got != 1 {
		t.Fatalf("idle hooks = %d, want 1", got)
	}
}

func TestTouchRacingIdleApplyClearsIt(t *testing.T) {
	clock := newFakeClock()
	started := make(chan struct{})
	release := make(chan struct{})
	var modes modeLog
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		if mode == Idle {
			close(started)
			<-release
		}
		return nil
	}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	<-started
	touched := make(chan struct{})
	go func() {
		p.Touch()
		close(touched)
	}()
	close(release)
	<-touched
	if p.Mode() != Normal {
		t.Fatal("request arrived during throttle but idle mode remained")
	}
	if !reflect.DeepEqual(modes.snapshot(), []Mode{Idle, Normal}) {
		t.Fatalf("applied modes = %v", modes.snapshot())
	}
}

func TestBeginRacingIdleApplyClearsIt(t *testing.T) {
	clock := newFakeClock()
	started := make(chan struct{})
	release := make(chan struct{})
	var modes modeLog
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		if mode == Idle {
			close(started)
			<-release
		}
		return nil
	}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	<-started
	done := p.Begin()
	defer done()
	close(release)
	eventually(t, func() bool { return p.Mode() == Normal })
	if !reflect.DeepEqual(modes.snapshot(), []Mode{Idle, Normal}) {
		t.Fatalf("applied modes = %v", modes.snapshot())
	}
}

// A failed idle apply whose rollback also fails may leave the process partly throttled: the policy must not call that Normal, and the
// next request must retry the restore.
func TestFailedIdleApplyWithFailedRollbackIsRetriedOnTouch(t *testing.T) {
	clock := newFakeClock()
	var modes modeLog
	var mu sync.Mutex
	failNormal := true
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case mode == Idle:
			return errors.New("memory priority failed")
		case failNormal:
			failNormal = false
			return errors.New("rollback failed")
		}
		return nil
	}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return len(modes.snapshot()) >= 2 })
	if got := modes.snapshot(); !reflect.DeepEqual(got[:2], []Mode{Idle, Normal}) {
		t.Fatalf("a failed idle apply was not followed by a restore: %v", got)
	}
	eventually(t, func() bool { return p.Mode() == Idle })
	p.Touch()
	if p.Mode() != Normal {
		t.Fatal("the next request did not retry the failed restore")
	}
	if got := modes.snapshot(); got[len(got)-1] != Normal || len(got) != 3 {
		t.Fatalf("modes = %v, want Idle, Normal (failed), Normal", got)
	}
}

// When the restore after a failed idle apply works, the mode is Normal and nothing is retried until a request arrives.
func TestFailedIdleApplyIsRestoredAtOnce(t *testing.T) {
	clock := newFakeClock()
	var modes modeLog
	p := New(Config{Now: clock.Now, After: clock.After, Apply: func(mode Mode) error {
		modes.add(mode)
		if mode == Idle {
			return errors.New("unsupported")
		}
		return nil
	}})
	runPolicy(t, p)
	eventually(t, func() bool { return clock.AfterCalls() > 0 })
	clock.Advance(DefaultGrace)
	eventually(t, func() bool { return len(modes.snapshot()) >= 2 })
	if got := modes.snapshot(); !reflect.DeepEqual(got[:2], []Mode{Idle, Normal}) || p.Mode() != Normal {
		t.Fatalf("modes = %v, mode %v", got, p.Mode())
	}
}
