package power

import (
	"context"
	"sync"
	"time"
)

const (
	DefaultGrace = 30 * time.Second
	DefaultCheck = 10 * time.Second
)

type Mode uint8

const (
	Normal Mode = iota
	Idle
)

type Config struct {
	Grace  time.Duration
	Check  time.Duration
	Now    func() time.Time
	After  func(time.Duration) <-chan time.Time
	InUse  func() bool
	Apply  func(Mode) error
	OnIdle []func()
}

type Policy struct {
	mu              sync.Mutex
	mode            Mode
	lastTouch       time.Time
	activity        uint64
	inflight        int
	retryAfterTouch bool
	applyMu         sync.Mutex
	busyMu          sync.RWMutex
	busy            []func() bool
	grace           time.Duration
	check           time.Duration
	now             func() time.Time
	after           func(time.Duration) <-chan time.Time
	inUse           func() bool
	apply           func(Mode) error
	onIdle          []func()
	wake            chan struct{}
}

func New(c Config) *Policy {
	if c.Grace <= 0 {
		c.Grace = DefaultGrace
	}
	if c.Check <= 0 {
		c.Check = DefaultCheck
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.After == nil {
		c.After = time.After
	}
	if c.InUse == nil {
		c.InUse = func() bool { return false }
	}
	if c.Apply == nil {
		c.Apply = func(Mode) error { return nil }
	}
	return &Policy{
		lastTouch: c.Now(),
		grace:     c.Grace,
		check:     c.Check,
		now:       c.Now,
		after:     c.After,
		inUse:     c.InUse,
		apply:     c.Apply,
		onIdle:    append([]func(){}, c.OnIdle...),
		wake:      make(chan struct{}, 1),
	}
}

func (p *Policy) Mode() Mode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

func (p *Policy) Touch() {
	p.mu.Lock()
	p.lastTouch = p.now()
	p.activity++
	p.retryAfterTouch = false
	idle := p.mode == Idle
	p.mu.Unlock()
	if idle {
		p.applyNormal()
	}
	p.signal()
}

func (p *Policy) Begin() func() {
	p.mu.Lock()
	p.activity++
	p.inflight++
	p.mu.Unlock()
	p.signal()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.inflight--
			p.mu.Unlock()
			p.signal()
		})
	}
}

// AddBusy registers background work that must keep the process at normal power.
func (p *Policy) AddBusy(f func() bool) {
	if f == nil {
		return
	}
	p.busyMu.Lock()
	p.busy = append(p.busy, f)
	p.busyMu.Unlock()
	p.signal()
}

func (p *Policy) isBusy() bool {
	if p.inUse() {
		return true
	}
	p.busyMu.RLock()
	checks := append([]func() bool(nil), p.busy...)
	p.busyMu.RUnlock()
	for _, check := range checks {
		if check() {
			return true
		}
	}
	return false
}

func (p *Policy) Run(ctx context.Context) {
	for {
		if p.Mode() == Idle {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			}
			p.mu.Lock()
			inflight := p.inflight
			p.mu.Unlock()
			if inflight > 0 || p.isBusy() {
				p.applyNormal()
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
			continue
		case <-p.after(p.check):
			if !p.tryIdle() {
				continue
			}
			for _, fn := range p.onIdle {
				fn()
			}
		}
	}
}

func (p *Policy) tryIdle() bool {
	p.applyMu.Lock()
	defer p.applyMu.Unlock()
	now := p.now()
	p.mu.Lock()
	eligible := p.mode == Normal && !p.retryAfterTouch && p.inflight == 0 && now.Sub(p.lastTouch) >= p.grace
	p.mu.Unlock()
	if !eligible || p.isBusy() {
		return false
	}
	now = p.now()
	p.mu.Lock()
	eligible = p.mode == Normal && !p.retryAfterTouch && p.inflight == 0 && now.Sub(p.lastTouch) >= p.grace
	if !eligible {
		p.mu.Unlock()
		return false
	}
	p.mode = Idle
	activity := p.activity
	p.mu.Unlock()
	if err := p.apply(Idle); err != nil {
		// A failed idle apply may have changed part of the state. Ask for normal once more; if that fails too, the process may be
		// partly throttled, so keep the mode Idle and the next Touch (or the wake in Run) retries the restore.
		rollbackErr := p.apply(Normal)
		p.mu.Lock()
		if rollbackErr != nil {
			p.mode = Idle
		} else {
			p.mode = Normal
			p.retryAfterTouch = true
		}
		p.mu.Unlock()
		return false
	}
	p.mu.Lock()
	stale := p.activity != activity || p.inflight > 0
	p.mu.Unlock()
	if stale || p.isBusy() {
		if err := p.apply(Normal); err == nil {
			p.mu.Lock()
			p.mode = Normal
			p.mu.Unlock()
		}
		return false
	}
	return true
}

func (p *Policy) applyNormal() {
	p.applyMu.Lock()
	defer p.applyMu.Unlock()
	p.mu.Lock()
	idle := p.mode == Idle
	p.mu.Unlock()
	if !idle {
		return
	}
	if err := p.apply(Normal); err == nil {
		p.mu.Lock()
		p.mode = Normal
		p.mu.Unlock()
	}
}

func (p *Policy) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
