```go
package bbsql

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const tickBudget = 5 * time.Second

var (
	registryMu sync.Mutex
	registry   []Sampler
)

// Sampler is one allowlisted SQL sample registered from a packet init (C1).
// Decode may keep cumulative state and emit deltas in Go (C13).
type Sampler interface {
	ID() string
	Kind() string
	Interval() time.Duration
	Decode(rows []Row) (any, bool, error)
}

// RegisterSampler adds s to the global registry. Nil samplers, samplers with a
// non-allowlisted cadence, and samplers whose Kind is already registered are
// ignored so startup remains deterministic.
func RegisterSampler(s Sampler) {
	if s == nil {
		return
	}
	iv := s.Interval()
	if iv != BaseInterval && iv != DemotedInterval {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, r := range registry {
		if r.Kind() == s.Kind() {
			return
		}
	}
	registry = append(registry, s)
}

// Samplers returns a snapshot of registered samplers sorted by Kind so tick
// order is stable across runs.
func Samplers() []Sampler {
	registryMu.Lock()
	out := make([]Sampler, len(registry))
	copy(out, registry)
	registryMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

// ConnectFunc opens the single SQL connection and returns the send seam and its
// closer (C4/C13). Nil means defaultConnect.
type ConnectFunc func(ConnConfig) (Queryer, func() error, error)

func defaultConnect(cfg ConnConfig) (Queryer, func() error, error) {
	db, err := Open(cfg)
	if err != nil {
		return nil, nil, err
	}
	return DBQueryer{DB: db, MaxRows: 0}, db.Close, nil
}

// SampleDoc is one persisted NDJSON line containing only fixed, numeric or
// enumerated sample data (C10/C13).
type SampleDoc struct {
	V         int       `json:"v"`
	T         time.Time `json:"t"`
	Kind      string    `json:"kind"`
	Truncated bool      `json:"truncated,omitempty"`
	Rows      any       `json:"rows"`
}

// StatusDoc is the host-facing C11 status shape; its tags and values are strict.
type StatusDoc struct {
	Enabled  bool       `json:"enabled"`
	State    string     `json:"state"`
	LastOK   *time.Time `json:"lastOK,omitempty"`
	Failures int        `json:"failures"`
}

// Runner owns the single-connection, single-tick SQL sampling loop for the SQL
// service (C4/C7).
type Runner struct {
	Dir     string
	Conn    ConnConfig
	Connect ConnectFunc
	Now     func() time.Time
	Log     func(format string, args ...any)

	state     State
	samplers  []Sampler
	lastRun   map[string]time.Time
	lastOK    time.Time
	running   bool
	skipped   int
	q         Queryer
	closeConn func() error
	connected bool
}

// NewRunner loads persisted back-off state from dir and fails closed on a
// corrupt state file (C7). It never clears auto-off.
func NewRunner(dir string, conn ConnConfig) *Runner {
	st, _ := Load(filepath.Join(dir, "state.json"))
	return &Runner{
		Dir:      dir,
		Conn:     conn,
		state:    st,
		samplers: Samplers(),
		lastRun:  make(map[string]time.Time),
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

func (r *Runner) connectFn() ConnectFunc {
	if r.Connect != nil {
		return r.Connect
	}
	return defaultConnect
}

func (r *Runner) dropConn() {
	if r.closeConn != nil {
		_ = r.closeConn()
	}
	r.closeConn = nil
	r.q = nil
	r.connected = false
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

// StatusPath returns the C11 status file path inside the SQL data folder.
func StatusPath(dir string) string { return filepath.Join(dir, "status.json") }

// SamplePath returns the hourly NDJSON sample path for t in UTC (C13).
func SamplePath(dir string, t time.Time) string {
	return filepath.Join(dir, "bb-sql-"+t.UTC().Format("20060102-15")+".ndjson")
}

// Tick runs one sampling tick. It is never re-entrant and never queued (C4).
func (r *Runner) Tick(now time.Time) error {
	if r.running {
		r.skipped++
		return nil
	}
	r.running = true
	defer func() { r.running = false }()

	if r.state.AutoOff {
		return r.writeStatus()
	}

	ctx, cancel := context.WithDeadline(context.Background(), now.Add(tickBudget))
	defer cancel()

	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return r.fail(err, ctx, false, false)
	}

	if !r.connected {
		q, closeFn, err := r.connectFn()(r.Conn)
		if err != nil {
			e := Classify(err)
			perm := e.Code == CodePermission || e.Code == CodeLoginFailed
			r.state.OnFailure(perm, false)
			r.dropConn()
			_ = r.saveAndStatus()
			r.logf("tick: %s", e.Code)
			return e
		}
		r.q = q
		r.closeConn = closeFn
		r.connected = true
		if err := CheckCeiling(r.q); err != nil {
			e := Classify(err)
			perm := e.Code == CodePermission || e.Code == CodeLoginFailed || e.Code == CodeTooManyRights
			ceil := e.Code == CodeTooManyRights
			r.state.OnFailure(perm, ceil)
			r.dropConn()
			_ = r.saveAndStatus()
			r.logf("tick: %s", e.Code)
			return e
		}
	}

	for _, s := range r.samplers {
		eff := max(r.state.Interval, s.Interval())
		if lr, ok := r.lastRun[s.Kind()]; ok && now.Sub(lr) < eff {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		ilim := itemLimit(s.Interval())
		timeout := ilim
		if dl, ok := ctx.Deadline(); ok {
			rem := time.Until(dl)
			if rem <= 0 {
				break
			}
			timeout = min(ilim, rem)
		}

		start := time.Now()
		text, ok := Statement(s.ID())
		if !ok {
			e := &Error{Code: CodeFailed, Message: "statement not allowlisted"}
			return r.fail(e, ctx, false, false)
		}
		rows, err := Query(ctx, r.q, text, timeout)
		if err != nil {
			return r.fail(err, ctx, false, false)
		}
		payload, truncated, err := s.Decode(rows)
		if err != nil {
			return r.fail(err, ctx, false, false)
		}
		used := time.Since(start)
		doc := SampleDoc{V: 1, T: now.UTC(), Kind: s.Kind(), Truncated: truncated, Rows: payload}
		if err := appendSample(SamplePath(r.Dir, now), doc); err != nil {
			return r.fail(err, ctx, false, false)
		}
		r.state.OnSuccess()
		r.state.OnSlow(used, ilim)
		r.lastRun[s.Kind()] = now
		r.lastOK = now
	}

	return r.saveAndStatus()
}

func itemLimit(iv time.Duration) time.Duration {
	if iv <= 15*time.Second {
		return 3 * time.Second
	}
	return 5 * time.Second
}

func (r *Runner) fail(err error, _ context.Context, _, _ bool) error {
	e := Classify(err)
	perm := e.Code == CodePermission || e.Code == CodeLoginFailed || e.Code == CodeTooManyRights
	ceil := e.Code == CodeTooManyRights
	r.state.OnFailure(perm, ceil)
	r.dropConn()
	_ = r.saveAndStatus()
	r.logf("tick: %s", e.Code)
	return e
}

func (r *Runner) saveAndStatus() error {
	if err := Save(statePath(r.Dir), r.state); err != nil {
		return err
	}
	return r.writeStatus()
}

func (r *Runner) writeStatus() error {
	var last *time.Time
	if !r.lastOK.IsZero() {
		t := r.lastOK.UTC()
		last = &t
	}
	doc := StatusDoc{
		Enabled:  !r.state.AutoOff,
		State:    r.state.State,
		LastOK:   last,
		Failures: r.state.Failures,
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.Dir, "status-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, StatusPath(r.Dir)); err != nil {
		return err
	}
	ok = true
	return nil
}

func appendSample(path string, doc SampleDoc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// Run drives ticks on a timer re-armed to the current back-off interval (C7)
// until ctx is done.
func (r *Runner) Run(ctx context.Context) error {
	for {
		interval := r.state.Interval
		if interval <= 0 {
			interval = BaseInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
		if err := r.Tick(r.now()); err != nil {
			if e, ok := err.(*Error); ok {
				r.logf("tick: %s", e.Code)
			} else {
				r.logf("tick: %s", CodeFailed)
			}
		}
	}
}

// Skipped reports ticks skipped because a previous tick was still running (C4).
func (r *Runner) Skipped() int { return r.skipped }

// Running reports whether a tick is currently executing.
func (r *Runner) Running() bool { return r.running }

// CurrentState returns a copy of the runner's back-off state for tests/status.
func (r *Runner) CurrentState() State { return r.state }

var _ sql.DB // ensure database/sql import retained only if needed; not required
```

- Assumed `context.WithDeadline(context.Background(), now.Add(tickBudget))` satisfies the contract's "whole-tick budget" wording (uses the tick `now` as the anchor).
- Enforced registry rules required by peer tests/shared delta: mutex-protected, snapshot copy sorted by Kind, duplicate Kind no-op, and ignore intervals not in `{BaseInterval, DemotedInterval}`.
- `NewRunner` joins `state.json` directly under `dir` (never `StatePath(dir)`); `Run` uses a `time.Timer` re-armed per tick; auto-off short-circuits before connect and is never cleared.
- `Tick` failure path drops the connection (so next tick re-checks ceiling), classifies to fixed codes, logs only `*Error.Code`, and persists state/status atomically with 0700 dirs/0600 files.
- Item timeout is 3s for `Interval() <= 15s` else 5s, capped by remaining tick budget; `OnSlow(used, itemLimit)` records against the item limit. Verify `RegisterSampler` interval filter matches all intended production samplers (only 15s/60s cadences allowed).
