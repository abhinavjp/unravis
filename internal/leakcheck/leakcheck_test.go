package leakcheck

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeTB records a Fatalf instead of failing the real test. Fatal calls end the
// calling goroutine, as the real ones do, so Cycles runs in its own goroutine.
type fakeTB struct {
	testing.TB
	msg chan string
}

func (f *fakeTB) Helper()                        {}
func (f *fakeTB) Fatal(args ...any)              { f.msg <- "fatal"; runtime.Goexit() }
func (f *fakeTB) Fatalf(format string, a ...any) { f.msg <- format; runtime.Goexit() }

func runCycles(n int, o Options, cycle func(int)) (failed bool) {
	f := &fakeTB{msg: make(chan string, 4)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Cycles(f, n, o, cycle)
	}()
	<-done
	select {
	case <-f.msg:
		return true
	default:
		return false
	}
}

// TestCyclesFailsOnALeakPerCycle is the negative case: a goroutine left behind in
// every cycle must fail, and a clean cycle (even one that starts and joins
// goroutines) must pass.
func TestCyclesFailsOnALeakPerCycle(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	if !runCycles(5, Options{Wait: 200 * time.Millisecond}, func(int) { go func() { <-stop }() }) {
		t.Error("a goroutine leaked per cycle was not caught")
	}
	if runCycles(5, Options{}, func(int) {
		done := make(chan struct{})
		go func() { time.Sleep(5 * time.Millisecond); close(done) }()
		<-done
	}) {
		t.Error("a clean cycle failed")
	}
}

// TestCyclesIdleMax: the total goroutine count after the cycles is bounded too.
func TestCyclesIdleMax(t *testing.T) {
	if !runCycles(1, Options{IdleMax: 1}, func(int) {}) {
		t.Error("IdleMax of 1 passed with the test goroutines running")
	}
	if got := Dump(); !strings.Contains(got, "goroutine") {
		t.Errorf("Dump gave %q", got)
	}
}
