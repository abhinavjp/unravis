// Package leakcheck is the goroutine-leak test helper (BL-424): run a start/stop
// cycle several times and assert the goroutine count returns to where it began.
//
// It uses runtime.NumGoroutine with a bounded retry, because a goroutine that is
// on its way out (a closed server's connection handler, a timer callback) needs a
// moment to finish. When the toolchain has the "goroutineleak" profile (Go's
// goroutineleakprofile experiment) the profile must also be empty at the end; the
// profile is looked up by name, so a toolchain without it just skips that check.
package leakcheck

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"
)

// Options tunes a check. The zero value is right for most cycles.
type Options struct {
	// Slack is how many goroutines above the baseline are accepted after a cycle
	// (default 2: a lazily started runtime or net/http helper).
	Slack int
	// Wait is how long to wait for the count to settle after a cycle (default 3 s).
	Wait time.Duration
	// IdleMax, when above zero, is the most goroutines the process may have after
	// the last cycle in total (BL-424: "idle goroutines stay under about 30").
	IdleMax int
}

func (o Options) slack() int {
	if o.Slack <= 0 {
		return 2
	}
	return o.Slack
}

func (o Options) wait() time.Duration {
	if o.Wait <= 0 {
		return 3 * time.Second
	}
	return o.Wait
}

// Count settles the runtime (a GC, so finalizers and closed timers run) and
// returns the goroutine count.
func Count() int {
	runtime.GC()
	return runtime.NumGoroutine()
}

// Settle waits until the goroutine count is at most limit, for up to wait. It
// returns the last count seen and whether it got there.
func Settle(limit int, wait time.Duration) (int, bool) {
	deadline := time.Now().Add(wait)
	for {
		n := Count()
		if n <= limit {
			return n, true
		}
		if !time.Now().Before(deadline) {
			return n, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Cycles runs cycle n times and fails t when the goroutine count after any cycle
// stays above the count before the first one (plus Options.Slack). The first
// cycle runs once before the baseline is taken (warm-up), so goroutines the
// runtime and standard library start once per process (the HTTP client's
// transport, a signal watcher) are not counted as leaks. It returns the baseline.
func Cycles(t testing.TB, n int, o Options, cycle func(i int)) int {
	t.Helper()
	if n < 1 {
		t.Fatal("leakcheck: n must be at least 1")
	}
	cycle(-1)                         // warm-up
	time.Sleep(50 * time.Millisecond) // let the warm-up's goroutines finish before the baseline
	base := Count()
	for i := 0; i < n; i++ {
		cycle(i)
		if got, ok := Settle(base+o.slack(), o.wait()); !ok {
			t.Fatalf("leakcheck: after cycle %d of %d there are %d goroutines, baseline %d (slack %d)\n%s",
				i+1, n, got, base, o.slack(), Dump())
		}
	}
	if o.IdleMax > 0 {
		if got := Count(); got > o.IdleMax {
			t.Fatalf("leakcheck: %d goroutines after %d cycles, limit %d\n%s", got, n, o.IdleMax, Dump())
		}
	}
	if p := pprof.Lookup("goroutineleak"); p != nil && p.Count() > 0 {
		var b bytes.Buffer
		_ = p.WriteTo(&b, 1)
		t.Fatalf("leakcheck: goroutineleak profile is not empty:\n%s", b.String())
	}
	return base
}

// Dump returns every goroutine stack, grouped by identical stack, for a failure
// message.
func Dump() string {
	var b bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&b, 1); err != nil {
		return fmt.Sprintf("(no goroutine dump: %v)", err)
	}
	return b.String()
}
