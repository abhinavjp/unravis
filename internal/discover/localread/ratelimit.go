package localread

import (
	"context"
	"io"
	"sync"
	"time"
)

// Politeness defaults from the discovery spec (section 2.2): 32 MiB/s with a
// 1 MiB buffer, which is also the burst.
const (
	DefaultRate   = 32 << 20
	DefaultBuffer = 1 << 20
)

// Limiter paces reads. Wait blocks until n bytes may be consumed or ctx ends.
type Limiter interface {
	Wait(ctx context.Context, n int) error
}

// Clock is the time source of a TokenBucket; tests inject a virtual one.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TokenBucket is a byte-rate Limiter. It starts full, refills at rate bytes a
// second up to burst, and lets callers borrow against future tokens so that
// concurrent waiters are served in order.
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	clk    Clock
}

// NewTokenBucket makes a bucket. clk nil means the real clock. Non-positive
// rate or burst fall back to the defaults.
func NewTokenBucket(bytesPerSecond, burst int64, clk Clock) *TokenBucket {
	if bytesPerSecond <= 0 {
		bytesPerSecond = DefaultRate
	}
	if burst <= 0 {
		burst = DefaultBuffer
	}
	if clk == nil {
		clk = realClock{}
	}
	return &TokenBucket{rate: float64(bytesPerSecond), burst: float64(burst), tokens: float64(burst), last: clk.Now(), clk: clk}
}

// Wait implements Limiter. Requests larger than the burst are paced in
// burst-sized pieces.
func (b *TokenBucket) Wait(ctx context.Context, n int) error {
	for n > 0 {
		piece := n
		if float64(piece) > b.burst {
			piece = int(b.burst)
		}
		if err := b.waitOne(ctx, piece); err != nil {
			return err
		}
		n -= piece
	}
	return nil
}

func (b *TokenBucket) waitOne(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	now := b.clk.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	b.tokens -= float64(n)
	var wait time.Duration
	if b.tokens < 0 {
		wait = time.Duration(-b.tokens / b.rate * float64(time.Second))
	}
	b.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	if err := b.clk.Sleep(ctx, wait); err != nil {
		b.mu.Lock()
		b.tokens += float64(n) // give the unused reservation back
		b.mu.Unlock()
		return err
	}
	return nil
}

var (
	defaultOnce    sync.Once
	defaultLimiter *TokenBucket
)

// DefaultLimiter is the process-wide 32 MiB/s bucket shared by every reader,
// so the whole process stays under the budget however many ranges are open.
func DefaultLimiter() Limiter {
	defaultOnce.Do(func() { defaultLimiter = NewTokenBucket(DefaultRate, DefaultBuffer, nil) })
	return defaultLimiter
}

// throttled paces an underlying reader: each Read is capped at the buffer
// size and charged to the limiter before its bytes are handed on.
type throttled struct {
	ctx context.Context
	r   io.Reader
	lim Limiter
	max int
}

func (t *throttled) Read(p []byte) (int, error) {
	if len(p) > t.max {
		p = p[:t.max]
	}
	n, err := t.r.Read(p)
	if n > 0 {
		if werr := t.lim.Wait(t.ctx, n); werr != nil {
			return 0, werr
		}
	}
	return n, err
}
