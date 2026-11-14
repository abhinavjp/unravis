// runner.go is the SQL service's own tick loop (BBX-5-10): one connection, one
// tick at a time, fixed budgets and fixed output shapes. It never runs in the
// host recorder process, never queues a tick, and never clears auto-off. The
// samplers (BBX-5-11..14) register themselves; the runner only schedules them,
// sends their allowlisted statement, decodes and appends.
package bbsql

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fixed budgets (C4): tickBudget caps a whole tick, the item limits are 3 s for
// 15 s samples and 5 s for 60 s samples.
const (
	tickBudget = 5 * time.Second
	quickLimit = 3 * time.Second
	longLimit  = 5 * time.Second
)

// hourLayout names the hourly sample file. The file name is the clock that
// rotate reads, so it is written and parsed from one constant.
const hourLayout = "20060102-15"

// defaultKeepHours bounds the SQL folder the way keepDays bounds the host
// folder. The SQL service writes its own files and host housekeeping never
// looks inside \sql, so without this the folder grows until it fills the disk.
const defaultKeepHours = 7 * 24

// errDiskLow is the fixed code for "the drive is under the owner's floor, so
// this tick wrote nothing". It is deliberately not a SQL failure: State tracks
// instance problems, not free space.
var errDiskLow = &Error{CodeDiskLow, "The drive holding the recordings folder is below its free-space floor."}

// Sampler is one allowlisted SQL sample. Samplers register from their own
// package init, so the runner never needs to know which samples exist.
type Sampler interface {
	ID() string
	Kind() string
	Interval() time.Duration
	Decode(rows []Row) (any, bool, error) // payload, truncated, fixed error
}

// Gate is an optional Sampler interface for a sample of a feature this instance
// may not have. The runner sends the gate statement BEFORE the sampler's own
// statement; when GateOpen reports false the sampler is skipped for this tick
// (nothing is written, its due time still advances) so its DMV is never
// queried. GateOpen must fail closed.
type Gate interface {
	GateID() string
	GateOpen(rows []Row) bool
}

// registryMu guards the registry: init order is not defined, and tests register
// samplers while other code reads the list.
var (
	registryMu sync.Mutex
	registry   []Sampler
)

// RegisterSampler adds s to the registry. A nil sampler, one whose interval is
// neither cadence, or one whose Kind is already registered is ignored, so a
// double registration can never produce two sample streams for one kind.
func RegisterSampler(s Sampler) {
	if s == nil {
		return
	}
	if iv := s.Interval(); iv != BaseInterval && iv != DemotedInterval {
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

// Samplers returns a snapshot sorted by Kind, so tick order is the same in every
// run and the lines of one hour are comparable with the next hour.
func Samplers() []Sampler {
	registryMu.Lock()
	out := make([]Sampler, len(registry))
	copy(out, registry)
	registryMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

// ConnectFunc opens the single connection and hands back the send seam with its
// closer. Tests inject a fake; nil means defaultConnect.
type ConnectFunc func(ConnConfig) (Queryer, func() error, error)

func defaultConnect(cfg ConnConfig) (Queryer, func() error, error) {
	db, err := Open(cfg)
	if err != nil {
		return nil, nil, err
	}
	return DBQueryer{DB: db}, db.Close, nil
}

// SampleDoc is one NDJSON line. Only numbers, enums and validated wait types
// reach it: Rows is whatever a Sampler decoded from a fixed struct (C9).
type SampleDoc struct {
	V         int       `json:"v"`
	T         time.Time `json:"t"`
	Kind      string    `json:"kind"`
	Truncated bool      `json:"truncated,omitempty"`
	Rows      any       `json:"rows"`
}

// StatusDoc is the only SQL thing the host publishes (C11): four fixed fields,
// no instance name, version or error text. The host reads it strictly, so an
// extra key would make it report "off".
type StatusDoc struct {
	Enabled  bool       `json:"enabled"`
	State    string     `json:"state"`
	LastOK   *time.Time `json:"lastOK,omitempty"`
	Failures int        `json:"failures"`
}

// Runner owns one tick loop against one local instance. Everything in it is
// single-goroutine: Tick being non-re-entrant is the control, not a limitation.
type Runner struct {
	// Dir is the SQL data folder (the locked \sql subfolder). state.json,
	// status.json and bb-sql-<hour>.ndjson live directly in it.
	Dir     string
	Conn    ConnConfig
	Connect ConnectFunc
	Now     func() time.Time
	Log     func(format string, args ...any)

	// KeepHours bounds the sample files; 0 means defaultKeepHours.
	KeepHours int
	// MinFreePercent is the owner's free-space floor for the drive holding Dir.
	// FreePercent reads it. With no reader installed the floor cannot be checked
	// at all, so the caller must install one: the SQL service is its own process,
	// and the host recorder's housekeeping never looks inside \sql.
	MinFreePercent int
	FreePercent    func(path string) (float64, error)
	// MaxBytes is the C13 byte bound of the whole sample folder. Age retention
	// alone does not bound it: the measured worst case, every sampler at its
	// row cap on its own cadence, is ~200 MB a day (budget_test.go prints the
	// figure). 0 means no byte bound (tests); the service always sets one from
	// the owner's config.
	MaxBytes int64

	state     State
	samplers  []Sampler
	lastRun   map[string]time.Time
	lastOK    time.Time
	running   bool
	skipped   int
	q         Queryer
	closeConn func() error
	connected bool
	// connectedAt is when the live connection was opened and its ceiling
	// proved. It drives connExpired.
	connectedAt time.Time
	rotated     string // hour last swept, so rotation costs one readdir per hour
}

// NewRunner loads the persisted back-off state. A corrupt or unreadable state
// file leaves the service auto-off (Load fails closed) and nothing here clears
// it: only OwnerEnable, driven by the owner, does.
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

// keepHours resolves the retention default: a zero value must never mean
// "keep forever".
func (r *Runner) keepHours() int {
	if r.KeepHours > 0 {
		return r.KeepHours
	}
	return defaultKeepHours
}

// dropConn closes the one connection so the next tick reconnects and runs the
// rights ceiling again: C8 checks at every connect, not once per service start.
func (r *Runner) dropConn() {
	if r.closeConn != nil {
		_ = r.closeConn()
	}
	r.closeConn = nil
	r.q = nil
	r.connected = false
	r.connectedAt = time.Time{}
}

// StatusPath is the C11 status file inside the SQL data folder.
func StatusPath(dir string) string { return filepath.Join(dir, "status.json") }

// SamplePath is the hourly NDJSON file for t.
func SamplePath(dir string, t time.Time) string {
	return filepath.Join(dir, "bb-sql-"+t.UTC().Format(hourLayout)+".ndjson")
}

// Tick runs one sampling pass. A tick still running when the next is due is
// skipped and counted: ticks never queue and never overlap (C4).
func (r *Runner) Tick(now time.Time) error {
	if r.running {
		// C4: ticks never queue. Run() drives ticks in one goroutine, so this
		// only fires for an external driver - but it is the control that keeps
		// a hung tick from being followed by a pile of new ones, so it is
		// counted and logged rather than silently absorbed. The counter cannot
		// reach /status (C11 fixes those four fields), so the log is the only
		// surface it has.
		r.skipped++
		r.logf("tick: skipped, previous tick still running (%d so far)", r.skipped)
		return nil
	}
	r.running = true
	defer func() { r.running = false }()

	if r.state.AutoOff {
		return r.writeStatus()
	}

	// The budget runs on the real clock, not the test clock: the deadline that
	// has to stop a hung query is one the OS honours.
	ctx, cancel := context.WithTimeout(context.Background(), tickBudget)
	defer cancel()

	if err := r.checkDisk(now); err != nil {
		r.logf("tick: %s", CodeDiskLow)
		return err
	}
	if !r.connected || r.connExpired(now) {
		if err := r.reconnect(ctx); err != nil {
			return err
		}
	}

	// One success per tick, not per sampler: OnSuccess halves the interval, so
	// calling it for every sampler would undo the back-off inside one tick.
	hadSuccess := false
	for _, s := range r.samplers {
		if !r.due(s, now) {
			continue
		}
		limit := itemLimit(s.Interval())
		timeout := remaining(ctx, limit)
		if timeout <= 0 {
			break // the tick budget is spent; the rest waits for the next tick
		}
		if g, ok := s.(Gate); ok {
			open, err := r.gateIsOpen(ctx, g, timeout)
			if err != nil {
				return r.fail(err)
			}
			if !open {
				r.lastRun[s.Kind()] = now
				continue
			}
		}
		text, ok := Statement(s.ID())
		if !ok {
			return r.fail(errNotAllowed)
		}
		start := time.Now()
		rows, err := Query(ctx, r.q, text, timeout)
		if err != nil {
			return r.fail(err)
		}
		used := time.Since(start)
		payload, truncated, err := s.Decode(rows)
		if err != nil {
			return r.fail(err)
		}
		if err := appendSample(SamplePath(r.Dir, now), SampleDoc{
			V: 1, T: now.UTC(), Kind: s.Kind(), Truncated: truncated, Rows: payload,
		}); err != nil {
			return r.fail(err)
		}
		r.state.OnSlow(used, limit)
		r.lastRun[s.Kind()] = now
		r.lastOK = now
		hadSuccess = true
	}
	if hadSuccess {
		r.state.OnSuccess()
	}
	return r.saveAndStatus()
}

// due reports whether a sampler's own interval has elapsed under the current
// back-off interval. A never-run sampler is always due.
func (r *Runner) due(s Sampler, now time.Time) bool {
	lr, ok := r.lastRun[s.Kind()]
	return !ok || now.Sub(lr) >= max(r.state.Interval, s.Interval())
}

// connExpired reports whether the live connection has outlived C5's lifetime.
// database/sql retires the physical connection silently at that point (and
// after any transport error), so "the ceiling is checked at every connect" is
// only true if the runner notices the same moment: a login given sysadmin an
// hour later must not keep sampling on an unproven connection.
func (r *Runner) connExpired(now time.Time) bool {
	return r.connected && !r.connectedAt.IsZero() && now.Sub(r.connectedAt) >= ConnMaxLifetime
}

// reconnect opens the connection and runs the C8 ceiling check before anything
// else is sent. Rights can change while the service runs, so both happen at
// every connect and no sample is taken on an unproven connection. An existing
// connection is closed first: the service holds exactly one (C5).
func (r *Runner) reconnect(ctx context.Context) error {
	if r.connected {
		r.dropConn()
	}
	q, closeFn, err := r.connectFn()(r.Conn)
	if err != nil {
		return r.fail(err)
	}
	r.q, r.closeConn, r.connected = q, closeFn, true
	r.connectedAt = r.now()
	if err := CheckCeiling(ctx, r.q); err != nil {
		return r.fail(err)
	}
	return nil
}

// gateIsOpen sends a gate statement and asks the sampler whether its feature
// exists here. A gate that cannot be read stays closed, so an absent feature is
// never queried instead of turning into a stream of failures. Rights errors are
// different: they are real C8/C10 failures and still fail the tick.
func (r *Runner) gateIsOpen(ctx context.Context, g Gate, timeout time.Duration) (bool, error) {
	text, ok := Statement(g.GateID())
	if !ok {
		return false, errNotAllowed
	}
	rows, err := Query(ctx, r.q, text, timeout)
	if err != nil {
		e := Classify(err)
		switch e.Code {
		case CodePermission, CodeLoginFailed, CodeTooManyRights:
			return false, e
		}
		r.logf("gate %s: %s", g.GateID(), e.Code)
		return false, nil
	}
	return g.GateOpen(rows), nil
}

// fail records one instance failure, drops the connection so the next tick
// re-checks the ceiling, persists the state, and returns the fixed code. The
// raw error is classified and then forgotten: it never reaches disk or logs.
func (r *Runner) fail(err error) error {
	e := Classify(err)
	perm := e.Code == CodePermission || e.Code == CodeLoginFailed || e.Code == CodeTooManyRights
	r.state.OnFailure(perm, e.Code == CodeTooManyRights)
	r.dropConn()
	_ = r.saveAndStatus()
	r.logf("tick: %s", e.Code)
	return e
}

func (r *Runner) saveAndStatus() error {
	if err := Save(filepath.Join(r.Dir, "state.json"), r.state); err != nil {
		return err
	}
	return r.writeStatus()
}

// checkDisk enforces the free-space floor and rotates old sample files. Neither
// is a SQL failure, and rotation runs at most once an hour.
func (r *Runner) checkDisk(now time.Time) error {
	if r.FreePercent != nil && r.MinFreePercent > 0 {
		free, err := r.FreePercent(r.Dir)
		// An unreadable volume is treated as "no room": the floor exists to
		// protect the operator's disk, and a reading that cannot be taken
		// cannot prove there is one. Writing anyway would fail open.
		if err != nil || free < float64(r.MinFreePercent) {
			return errDiskLow
		}
	}
	hour := now.UTC().Format(hourLayout)
	if hour == r.rotated {
		return nil
	}
	// The hour is only marked as swept once the sweep succeeded: a failed
	// rotation must be retried on the next tick, or one unreadable entry would
	// leave the folder unbounded for the rest of the hour with no signal. The
	// error handed back is a fixed code, because a raw os error names the
	// folder path (C10).
	if err := r.rotate(now.UTC()); err != nil {
		return errDiskSweep
	}
	r.rotated = hour
	return nil
}

// errDiskSweep is the fixed code for "the sample folder could not be swept",
// which means the retention bound is not being enforced.
var errDiskSweep = &Error{CodeDiskLow, "The SQL sample folder could not be swept, so its retention bound is not enforced."}

// rotate enforces both bounds of the SQL folder: the age window (keepHours) and
// the byte cap (MaxBytes). Sample files are named by the hour they hold, and
// os.ReadDir returns names in sorted order, so the oldest hour is always the
// first entry and the newest is the last. A name that does not parse as an hour
// is left alone rather than guessed at, so an unrelated file can never be
// swept by accident, and the newest hour is never dropped by the byte cap: a
// reader must always find the file the service is writing into.
func (r *Runner) rotate(now time.Time) error {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return err
	}
	cutoff := now.Add(-time.Duration(r.keepHours()) * time.Hour).Format(hourLayout)
	type sampleFile struct {
		name string
		size int64
	}
	var kept []sampleFile
	var total int64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "bb-sql-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		hh := strings.TrimSuffix(strings.TrimPrefix(name, "bb-sql-"), ".ndjson")
		if !validHour(hh) {
			continue
		}
		if hh < cutoff {
			if err := os.Remove(filepath.Join(r.Dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		kept = append(kept, sampleFile{name: name, size: info.Size()})
		total += info.Size()
	}
	// Retention by age does not bound the folder: the measured worst case is
	// ~200 MB a day with every sampler at its row cap, so 168 hours of it is
	// ~1.4 GB. With MaxBytes set, whole hours go from the oldest end until the
	// folder fits. Dropping a whole hour
	// is honest: the hour is simply missing, never zero-filled.
	for r.MaxBytes > 0 && total > r.MaxBytes && len(kept) > 1 {
		if err := os.Remove(filepath.Join(r.Dir, kept[0].name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= kept[0].size
		kept = kept[1:]
	}
	return nil
}

// validHour reports whether s is exactly the file-name hour layout. The length
// gate is part of it: time.Parse accepts a short hour ("20260304-5"), and a
// name that is not exactly one hour must never be deleted.
func validHour(s string) bool {
	if len(s) != len(hourLayout) {
		return false
	}
	for i := range s {
		if i == 8 {
			if s[i] != '-' {
				return false
			}
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := time.Parse(hourLayout, s)
	return err == nil
}

// writeStatus writes the C11 file atomically: a temp file in the same folder,
// then a rename, so the host never reads a half-written status.
func (r *Runner) writeStatus() error {
	var last *time.Time
	if !r.lastOK.IsZero() {
		t := r.lastOK.UTC()
		last = &t
	}
	data, err := json.Marshal(StatusDoc{
		Enabled: !r.state.AutoOff, State: r.state.State, LastOK: last, Failures: r.state.Failures,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.Dir, "status-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, StatusPath(r.Dir)); err != nil {
		return err
	}
	done = true
	return nil
}

// appendSample writes one line. Files are 0600 and the folder's DACL already
// limits readers to SYSTEM, Administrators and the SQL service account.
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
	_, err = f.Write(append(data, '\n'))
	return err
}

// Run drives ticks until ctx is done. The wait is read from the state after
// every tick, so a failure backs off immediately instead of at the next start.
func (r *Runner) Run(ctx context.Context) error {
	defer r.dropConn()
	for {
		timer := time.NewTimer(max(r.state.Interval, BaseInterval))
		select {
		case <-ctx.Done():
			timer.Stop()
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

// Skipped counts ticks not run because one was still running (C4).
func (r *Runner) Skipped() int { return r.skipped }

// Running reports whether a tick is executing right now.
func (r *Runner) Running() bool { return r.running }

// CurrentState copies the back-off state out for tests and diagnostics.
func (r *Runner) CurrentState() State { return r.state }

// remaining clamps an item limit to the time the tick budget has left.
func remaining(ctx context.Context, limit time.Duration) time.Duration {
	dl, ok := ctx.Deadline()
	if !ok {
		return limit
	}
	return min(limit, time.Until(dl))
}

// itemLimit is the C4 per-query budget: 15 s samples get 3 s, 60 s samples 5 s.
func itemLimit(interval time.Duration) time.Duration {
	if interval <= BaseInterval {
		return quickLimit
	}
	return longLimit
}
