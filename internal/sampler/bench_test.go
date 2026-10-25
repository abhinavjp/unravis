package sampler

import (
	"fmt"
	"testing"
	"time"
)

// busyHost is a host of 300 processes, as the budget in the design names: a
// server with a few server images and many other processes.
func busyHost() *fakeSource {
	return newFake(func(n int) []ProcSample {
		out := make([]ProcSample, 0, 300)
		out = append(out, proc(1200, "w3wp.exe", float64(3*n), 800_000_000), proc(3300, "sqlservr.exe", float64(2*n), 4_000_000_000))
		for i := 0; i < 298; i++ {
			out = append(out, proc(uint32(5000+i), fmt.Sprintf("svc%03d.exe", i), float64(n*(i%7)), uint64(1_000_000*(i+1))))
		}
		return out
	})
}

// BenchmarkTick measures one sampling tick over 300 processes with the fake
// source: choosing the processes, formatting the rows, and keeping the state.
// The design's budget is under 1 percent of one core at a 15 s interval, which
// is 150 ms per tick; the tick itself is a small fraction of that.
func BenchmarkTick(b *testing.B) {
	f := busyHost()
	e := newEngine(f, pseudo, 15, 1<<62, nil)
	if err := e.prime(at(0)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 1; i <= b.N; i++ {
		if err := e.tick(at(15 * i)); err != nil {
			b.Fatal(err)
		}
		if i%20 == 0 { // a batch every five minutes
			e.cut()
		}
	}
}

// TestTickStaysInTheBudget runs the benchmark body a fixed number of times and
// checks the cost of one tick against the design's budget of 1 percent of one
// core at 15 s (150 ms), with wide headroom for a slow machine.
func TestTickStaysInTheBudget(t *testing.T) {
	f := busyHost()
	e := newEngine(f, pseudo, 15, 1<<62, nil)
	if err := e.prime(at(0)); err != nil {
		t.Fatal(err)
	}
	const ticks = 200
	start := time.Now()
	for i := 1; i <= ticks; i++ {
		if err := e.tick(at(15 * i)); err != nil {
			t.Fatal(err)
		}
		if i%20 == 0 {
			e.cut()
		}
	}
	per := time.Since(start) / ticks
	t.Logf("%v per tick over 300 processes", per)
	if per > 15*time.Millisecond {
		t.Errorf("a tick takes %v; the budget is 150 ms and the target a tenth of it", per)
	}
}
