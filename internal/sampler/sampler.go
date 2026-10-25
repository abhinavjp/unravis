// Package sampler is the live CPU and memory sampler. It reads the host and the
// processes at a fixed interval, keeps the rows in a buffer, and hands the
// buffer to an Import callback as a win-live batch (internal/importer/winlive)
// every five minutes and when it stops.
//
// The sampler reads the operating system through a Source, so everything except
// the Windows calls (source_windows.go) is tested with a fake. Sampling runs on
// one goroutine locked to an OS thread at background priority; the callback
// runs on another, so a slow import never delays a sample. The HTTP server
// never waits on either.
package sampler

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// Defaults and limits.
const (
	DefaultInterval    = 15
	DefaultFlushEvery  = 5 * time.Minute
	DefaultMaxDuration = 72 * time.Hour
	DefaultMaxEvents   = 2_000_000

	// maxBatchRows ends a batch early: the importer refuses a very large file.
	maxBatchRows = 100_000
	// maxReadFailures is how many ticks in a row may fail to read before the
	// sampler stops.
	maxReadFailures = 3
	// queueLen is how many batches may wait for the import callback.
	queueLen = 8
)

// StopReason says why the sampler stopped.
type StopReason string

// Stop reasons.
const (
	StopNone        StopReason = ""
	StopUser        StopReason = "stopped"
	StopLimit       StopReason = "limit"        // 72 hours or 2 million events
	StopWriteError  StopReason = "write-error"  // the import callback failed
	StopSourceError StopReason = "source-error" // the host could not be read
)

// Config configures one sampler run.
type Config struct {
	// IntervalSeconds is 5, 15, 30 or 60. Zero means 15.
	IntervalSeconds int
	// Import stores one batch; name is the file name, body the win-live CSV. A
	// failure stops the sampler: nothing is retried.
	Import func(name string, body []byte) error
	// Pseudonymize names the processes that are not server images
	// (casestore Case.Pseudonymize). A process it gives no name for is left out.
	Pseudonymize func(kind, value string) string
	// Source reads the system. Nil means the operating system (OSSource).
	Source Source
	// FlushEvery is how often a batch is written. Zero means five minutes.
	FlushEvery time.Duration
	// MaxDuration and MaxEvents are the cap. Zero means 72 hours and 2 million.
	MaxDuration time.Duration
	MaxEvents   int
	// OnPartial is called once per process per run when the process could not
	// be opened (access denied): only its name and thread count are sampled.
	OnPartial func(label string, pid uint32)
}

// Status is a snapshot of a run.
type Status struct {
	Running bool
	Since   time.Time
	Samples int // ticks written
	Events  int // rows written, which bounds the events they give
	Partial int // processes that could not be opened
	Stop    StopReason
	Err     error // the callback or read error behind a stop
}

// Sampler is one run.
type Sampler struct {
	cfg     Config
	eng     *engine
	since   time.Time
	batches chan batch

	halt     chan struct{} // closed to ask the sampling goroutine to end
	haltOnce sync.Once
	done     chan struct{} // closed when everything has ended
	upDone   chan struct{} // closed when the import goroutine has ended

	// Hooks for tests.
	newTicker func(d time.Duration) (<-chan time.Time, func())
	now       func() time.Time

	mu      sync.Mutex
	samples int
	events  int
	partial int
	reason  StopReason
	err     error
	running bool
}

type batch struct {
	name string
	body []byte
}

// Start validates cfg, reads the system once as the baseline, and starts
// sampling. It returns an error for a bad config, an unsupported system, or a
// system that cannot be read.
func Start(cfg Config) (*Sampler, error) {
	return start(cfg, nil, nil)
}

func start(cfg Config, newTicker func(time.Duration) (<-chan time.Time, func()), now func() time.Time) (*Sampler, error) {
	if cfg.IntervalSeconds == 0 {
		cfg.IntervalSeconds = DefaultInterval
	}
	switch cfg.IntervalSeconds {
	case 5, 15, 30, 60:
	default:
		return nil, fmt.Errorf("sampler: interval %d s is not one of 5, 15, 30, 60", cfg.IntervalSeconds)
	}
	if cfg.Import == nil {
		return nil, errors.New("sampler: no import callback")
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = DefaultFlushEvery
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = DefaultMaxDuration
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = DefaultMaxEvents
	}
	if cfg.Source == nil {
		src, err := OSSource()
		if err != nil {
			return nil, err
		}
		cfg.Source = src
	}
	if now == nil {
		now = time.Now
	}
	if newTicker == nil {
		newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	s := &Sampler{
		cfg:       cfg,
		eng:       newEngine(cfg.Source, cfg.Pseudonymize, cfg.IntervalSeconds, cfg.MaxEvents, cfg.OnPartial),
		batches:   make(chan batch, queueLen),
		halt:      make(chan struct{}),
		done:      make(chan struct{}),
		upDone:    make(chan struct{}),
		newTicker: newTicker,
		now:       now,
		running:   true,
	}
	s.since = now().UTC()
	if err := s.eng.prime(s.since); err != nil {
		cfg.Source.Close()
		return nil, fmt.Errorf("sampler: reading the system: %w", err)
	}
	go s.upload()
	go s.run()
	return s, nil
}

// Stop asks the sampler to stop, waits for the last batch to be imported, and
// returns. It may be called more than once.
func (s *Sampler) Stop() {
	s.stop(StopUser, nil)
	<-s.done
}

// Done is closed when the sampler has stopped and its last batch is imported.
func (s *Sampler) Done() <-chan struct{} { return s.done }

// Status returns a snapshot.
func (s *Sampler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Running: s.running, Since: s.since, Samples: s.samples, Events: s.events, Partial: s.partial, Stop: s.reason, Err: s.err}
}

// stop records why the run ends (the first reason stays) and wakes the
// sampling goroutine.
func (s *Sampler) stop(r StopReason, err error) {
	s.mu.Lock()
	if s.reason == StopNone {
		s.reason, s.err = r, err
	}
	s.mu.Unlock()
	s.haltOnce.Do(func() { close(s.halt) })
}

// run is the sampling goroutine. It stays on one OS thread at background
// priority for as long as it samples.
func (s *Sampler) run() {
	runtime.LockOSThread()
	endBackground := beginBackground()
	defer func() {
		endBackground()
		runtime.UnlockOSThread()
	}()
	defer close(s.done)
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	defer s.cfg.Source.Close()
	defer func() { // the last batch, then wait for the import goroutine
		s.flush(true)
		close(s.batches)
		<-s.upDone
	}()

	tick, stopTicker := s.newTicker(time.Duration(s.cfg.IntervalSeconds) * time.Second)
	defer stopTicker()
	lastFlush := s.since
	failures := 0
	for {
		select {
		case <-s.halt:
			return
		case now := <-tick:
			select {
			case <-s.halt: // a stop that arrived with the tick wins
				return
			default:
			}
			if err := s.eng.tick(now); err != nil {
				if errors.Is(err, errLimit) {
					s.stop(StopLimit, nil)
					return
				}
				failures++
				if failures >= maxReadFailures {
					s.stop(StopSourceError, err)
					return
				}
				continue
			}
			failures = 0
			s.mu.Lock()
			s.samples, s.events, s.partial = s.eng.samples, s.eng.events, len(s.eng.partial)
			s.mu.Unlock()
			if now.Sub(s.since) >= s.cfg.MaxDuration {
				s.stop(StopLimit, nil)
				return
			}
			if now.Sub(lastFlush) >= s.cfg.FlushEvery || s.eng.batchRows >= maxBatchRows {
				if !s.flush(false) {
					return
				}
				lastFlush = now
			}
		}
	}
}

// flush cuts the batch and queues it for the import goroutine. A full queue
// stops the sampler (the callback is not keeping up); at the end the send waits,
// since nothing more is sampled. It reports whether sampling can go on.
func (s *Sampler) flush(final bool) bool {
	name, body, ok := s.eng.cut()
	if !ok {
		return true
	}
	b := batch{name, body}
	if final {
		s.batches <- b
		return false
	}
	select {
	case s.batches <- b:
		return true
	default:
		s.stop(StopWriteError, errors.New("sampler: imports are not keeping up"))
		return false
	}
}

// upload is the import goroutine. After the first failure it drops what is
// queued and stops the sampler.
func (s *Sampler) upload() {
	defer close(s.upDone)
	failed := false
	for b := range s.batches {
		if failed {
			continue
		}
		if err := s.cfg.Import(b.name, b.body); err != nil {
			failed = true
			s.stop(StopWriteError, err)
		}
	}
}
