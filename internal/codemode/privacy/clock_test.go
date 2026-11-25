package privacy

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

func TestClockQuantizedTo10ms(t *testing.T) {
	src := NewManualSource(t0)
	c, err := NewScriptClock(ClockLive, 1_790_000_000_007, src.Now)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseMs() != 1_790_000_000_000 {
		t.Fatalf("base %d not floored", c.BaseMs())
	}
	steps := []struct {
		adv  time.Duration
		date int64
		perf float64
	}{
		{0, 1_790_000_000_000, 0},
		{9 * time.Millisecond, 1_790_000_000_000, 0},
		{1 * time.Millisecond, 1_790_000_000_010, 10},
		{19 * time.Millisecond, 1_790_000_000_020, 20},
		{time.Second, 1_790_000_001_020, 1020},
	}
	for i, s := range steps {
		src.Advance(s.adv)
		if g := c.DateNowMs(); g != s.date {
			t.Fatalf("step %d: Date.now %d want %d", i, g, s.date)
		}
		if g := c.PerfNowMs(); g != s.perf {
			t.Fatalf("step %d: performance.now %v want %v", i, g, s.perf)
		}
	}
}

func TestClockEveryReadIsMultipleOf10(t *testing.T) {
	src := NewManualSource(t0)
	c, _ := NewScriptClock(ClockLive, 1_790_000_000_000, src.Now)
	for i := 0; i < 5000; i++ {
		src.Advance(time.Duration(i%37) * time.Millisecond)
		if d := c.DateNowMs(); d%10 != 0 {
			t.Fatalf("Date.now %d not a multiple of 10", d)
		}
		if p := c.PerfNowMs(); int64(p)%10 != 0 {
			t.Fatalf("performance.now %v not a multiple of 10", p)
		}
	}
}

func TestClockNeverGoesBack(t *testing.T) {
	src := NewManualSource(t0)
	c, _ := NewScriptClock(ClockLive, 1_790_000_000_000, src.Now)
	src.Advance(500 * time.Millisecond)
	a := c.DateNowMs()
	src.Advance(-300 * time.Millisecond) // host clock stepped back
	if b := c.DateNowMs(); b != a {
		t.Fatalf("went from %d to %d", a, b)
	}
	src.Advance(-time.Hour)
	if b := c.DateNowMs(); b != a {
		t.Fatalf("went from %d to %d", a, b)
	}
	src.Advance(time.Hour + 400*time.Millisecond)
	if b := c.DateNowMs(); b <= a {
		t.Fatalf("did not move forward after the clock came back: %d", b)
	}
}

func TestClockStartBeforeBaseDoesNotUnderflow(t *testing.T) {
	// the first read cannot be below the start even if the source is behind it from the first call
	src := NewManualSource(t0)
	c, _ := NewScriptClock(ClockLive, 10_000, src.Now)
	src.Advance(-time.Second)
	if g := c.DateNowMs(); g != 10_000 {
		t.Fatalf("got %d", g)
	}
}

func TestClockFrozenDoesNotReadHost(t *testing.T) {
	calls := 0
	c, err := NewScriptClock(ClockFrozen, 1_790_000_000_000, func() time.Time { calls++; return t0 })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if c.DateNowMs() != 1_790_000_000_000 || c.PerfNowMs() != 0 {
			t.Fatal("frozen clock moved")
		}
	}
	if calls != 0 {
		t.Fatalf("host clock read %d times in frozen mode", calls)
	}
}

func TestClockReplayMatchesRecording(t *testing.T) {
	// A live run records its base; a replay with the same base frozen gives the first reading again.
	src := NewManualSource(t0)
	live, _ := NewScriptClock(ClockLive, 1_790_000_000_000, src.Now)
	first := live.DateNowMs()
	src.Advance(3 * time.Second)
	rep, _ := NewScriptClock(ClockFrozen, live.BaseMs(), nil)
	if rep.DateNowMs() != first {
		t.Fatalf("replay %d, recording %d", rep.DateNowMs(), first)
	}
}

func TestClockRefusesBadInput(t *testing.T) {
	if _, err := NewScriptClock("sometimes", 1, nil); err == nil {
		t.Fatal("bad mode accepted")
	}
	for _, b := range []int64{-1, maxSafeMs, 1 << 62} {
		if _, err := NewScriptClock(ClockLive, b, nil); err == nil {
			t.Fatalf("base %d accepted", b)
		}
	}
	if _, err := NewScriptClock(ClockLive, 1_790_000_000_000, nil); err != nil { // nil source = host time
		t.Fatal(err)
	}
}
