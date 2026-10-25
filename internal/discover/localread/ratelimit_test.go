package localread

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTokenBucketPacesToRate(t *testing.T) {
	clk := newFakeClock()
	tb := NewTokenBucket(DefaultRate, DefaultBuffer, clk)
	start := clk.Now()
	// 64 MiB in 1 MiB reads: 1 MiB comes from the full bucket, the other 63 MiB
	// at 32 MiB/s is 1.96875 s.
	for i := 0; i < 64; i++ {
		if err := tb.Wait(context.Background(), 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	got := clk.Now().Sub(start)
	want := time.Duration(63.0 / 32.0 * float64(time.Second))
	if d := got - want; d < -time.Millisecond || d > time.Millisecond {
		t.Fatalf("paced %v, want %v", got, want)
	}
}

func TestTokenBucketBurstThenWait(t *testing.T) {
	clk := newFakeClock()
	tb := NewTokenBucket(1000, 500, clk)
	tb.Wait(context.Background(), 500)
	if clk.slept != 0 {
		t.Fatalf("slept %v inside the burst", clk.slept)
	}
	tb.Wait(context.Background(), 500)
	if clk.slept != 500*time.Millisecond {
		t.Fatalf("slept %v, want 500ms", clk.slept)
	}
}

func TestTokenBucketRefillsWhileIdleButNotPastBurst(t *testing.T) {
	clk := newFakeClock()
	tb := NewTokenBucket(1000, 500, clk)
	tb.Wait(context.Background(), 500)
	clk.Sleep(context.Background(), time.Hour) // idle: bucket refills to the burst only
	clk.slept = 0
	tb.Wait(context.Background(), 500)
	if clk.slept != 0 {
		t.Fatalf("slept %v after idle", clk.slept)
	}
	tb.Wait(context.Background(), 500)
	if clk.slept != 500*time.Millisecond {
		t.Fatalf("idle time must not bank more than the burst: slept %v", clk.slept)
	}
}

func TestTokenBucketRequestLargerThanBurst(t *testing.T) {
	clk := newFakeClock()
	tb := NewTokenBucket(1000, 100, clk)
	if err := tb.Wait(context.Background(), 1100); err != nil {
		t.Fatal(err)
	}
	if clk.slept != time.Second {
		t.Fatalf("slept %v, want 1s", clk.slept)
	}
}

func TestTokenBucketCancel(t *testing.T) {
	clk := newFakeClock()
	tb := NewTokenBucket(1000, 100, clk)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tb.Wait(ctx, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestTokenBucketConcurrentWaitersShareTheRate(t *testing.T) {
	// Real clock: 4 goroutines drawing 6000 bytes in total from a 20000 B/s
	// bucket with a 2000 byte burst must take about (6000-2000)/20000 = 0.2 s.
	tb := NewTokenBucket(20000, 2000, nil)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				tb.Wait(context.Background(), 100)
			}
		}()
	}
	wg.Wait()
	if d := time.Since(start); d < 150*time.Millisecond || d > 2*time.Second {
		t.Fatalf("took %v, want about 200ms", d)
	}
}

func TestThrottledReaderCapsEachRead(t *testing.T) {
	clk := newFakeClock()
	tb := NewTokenBucket(100, 100, clk)
	src := strings.NewReader(strings.Repeat("x", 1000))
	r := &throttled{ctx: context.Background(), r: src, lim: tb, max: 100}
	start := clk.Now()
	n, err := io.Copy(io.Discard, r)
	if err != nil || n != 1000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := clk.Now().Sub(start); got != 9*time.Second {
		t.Fatalf("1000 bytes at 100 B/s with a 100 byte burst took %v, want 9s", got)
	}
}

func TestDefaultLimiterIsShared(t *testing.T) {
	if DefaultLimiter() != DefaultLimiter() {
		t.Fatal("default limiter must be one process-wide bucket")
	}
	if DefaultRate != 32<<20 || DefaultBuffer != 1<<20 {
		t.Fatal("politeness defaults changed from the spec")
	}
}

func TestEnterBackgroundIsReentrantSafe(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		leave := EnterBackground()
		leave()
		leave2 := EnterBackground()
		leave2()
	}()
	<-done
}
