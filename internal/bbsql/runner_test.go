package bbsql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runner_test.go pins the Tick contract (BBX-5-10): one tick at a time, one
// connection re-proven at every connect, allowlist-only sends, fixed budgets,
// fixed output shapes, and retention that cannot silently fill the drive.

// fakeSampler is a Sampler over an allowlisted statement. It never builds SQL:
// the runner resolves ID() through Statement.
type fakeSampler struct {
	id       string
	kind     string
	interval time.Duration
	decode   func([]Row) (any, bool, error)
}

func (s *fakeSampler) ID() string              { return s.id }
func (s *fakeSampler) Kind() string            { return s.kind }
func (s *fakeSampler) Interval() time.Duration { return s.interval }
func (s *fakeSampler) Decode(rows []Row) (any, bool, error) {
	if s.decode != nil {
		return s.decode(rows)
	}
	return []Waits{{WaitType: "XET_LRU_BATCH_PAGE", DeltaMs: 1, DeltaTasks: 2}}, false, nil
}

// fakeGatedSampler adds the optional Gate seam. It is a distinct type so a
// plain fakeSampler is never accidentally treated as gated.
type fakeGatedSampler struct {
	fakeSampler
	gateID   string
	gateRows []Row
	closed   bool
}

func (s *fakeGatedSampler) GateID() string { return s.gateID }

func (s *fakeGatedSampler) GateOpen(rows []Row) bool {
	s.gateRows = rows
	return !s.closed
}

func fakeWaitRows(n int) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, Row{"wait_type": fmt.Sprintf("XET_LRU_BATCH_PAGE_%d", i),
			"wait_time_ms": int64(i), "waiting_tasks_count": int64(i)})
	}
	return rows
}

// recQuery is the send seam: it records every text in order, replies from a
// table, and can inject an error or a re-entrant Tick.
type recQuery struct {
	mu       sync.Mutex
	seen     []string
	replies  map[string][]Row
	errFor   func(text string) error
	onQuery  func(text string)
	tickHook func()
}

func (q *recQuery) Query(ctx context.Context, text string) ([]Row, error) {
	q.mu.Lock()
	q.seen = append(q.seen, text)
	q.mu.Unlock()
	if q.onQuery != nil {
		q.onQuery(text)
	}
	// The seam honours its deadline the way the driver does, so a test that
	// overruns the tick budget sees the same fixed timeout error as production.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.errFor != nil {
		if err := q.errFor(text); err != nil {
			return nil, err
		}
	}
	if rows, ok := q.replies[text]; ok {
		return rows, nil
	}
	return nil, nil
}

func (q *recQuery) texts() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.seen...)
}

func (q *recQuery) count(want string) int {
	n := 0
	for _, text := range q.texts() {
		if text == want {
			n++
		}
	}
	return n
}

func (q *recQuery) countID(id string) int {
	text, ok := Statement(id)
	if !ok {
		panic("test uses a non-allowlisted id " + id)
	}
	return q.count(text)
}

// ceilingOK replies to the two control statements the way a compliant login
// does: no privileged role flag set, no permission outside the ceiling.
func ceilingOK(q *recQuery) {
	row := Row{}
	for _, name := range ceilingRoleNames {
		row[name] = int64(0)
	}
	q.replies[Statements["bb:ceiling_role"]] = []Row{row}
	q.replies[Statements["bb:ceiling_perms"]] = nil
}

type runnerOpts struct {
	samplers []Sampler
	now      time.Time
	closeFn  func() error
}

func newFakeRunner(t *testing.T, q *recQuery, opts runnerOpts) *Runner {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sql")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if q != nil {
		ceilingOK(q)
	}
	r := NewRunner(dir, ConnConfig{})
	r.samplers = opts.samplers
	r.Now = func() time.Time { return opts.now }
	closer := opts.closeFn
	if closer == nil {
		closer = func() error { return nil }
	}
	r.Connect = func(ConnConfig) (Queryer, func() error, error) { return q, closer, nil }
	return r
}

func readSampleLines(t *testing.T, dir string) []SampleDoc {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "bb-sql-*.ndjson"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []SampleDoc
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		for _, raw := range strings.Split(string(data), "\n") {
			raw = strings.TrimSuffix(raw, "\r")
			if raw == "" {
				continue
			}
			var doc SampleDoc
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatalf("bad ndjson line %q: %v", raw, err)
			}
			out = append(out, doc)
		}
	}
	return out
}

func readStatusRaw(t *testing.T, dir string) StatusDoc {
	t.Helper()
	data, err := os.ReadFile(StatusPath(dir))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc StatusDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("status shape is not the four C11 fields: %v", err)
	}
	return doc
}

func sampleCount(t *testing.T, dir, kind string) int {
	t.Helper()
	n := 0
	for _, doc := range readSampleLines(t, dir) {
		if doc.Kind == kind {
			n++
		}
	}
	return n
}

func baseSampler(id, kind string) *fakeSampler {
	return &fakeSampler{id: id, kind: kind, interval: BaseInterval}
}

func TestTickSendsCeilingThenEachDueStatement(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:      fakeWaitRows(3),
		Statements["bb:schedulers"]: {{}},
	}}
	r := newFakeRunner(t, q, runnerOpts{
		now: now,
		samplers: []Sampler{
			baseSampler("bb:schedulers", "schedulers"),
			baseSampler("bb:waits", "waits"),
		},
	})

	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}

	texts := q.texts()
	if len(texts) == 0 {
		t.Fatal("nothing was sent")
	}
	// C8: the ceiling is proven before any sample statement.
	if texts[0] != Statements["bb:ceiling_role"] || texts[1] != Statements["bb:ceiling_perms"] {
		t.Fatalf("first sends are not the ceiling check: %q", texts[:2])
	}
	if q.countID("bb:waits") != 1 || q.countID("bb:schedulers") != 1 {
		t.Fatalf("each due sampler sends once, got %d waits / %d schedulers",
			q.countID("bb:waits"), q.countID("bb:schedulers"))
	}

	lines := readSampleLines(t, r.Dir)
	if len(lines) != 2 {
		t.Fatalf("sample lines = %d, want 2", len(lines))
	}
	for _, doc := range lines {
		if doc.V != 1 {
			t.Errorf("sample version = %d, want 1", doc.V)
		}
		if !doc.T.Equal(now) {
			t.Errorf("sample t = %v, want %v (the tick clock, in UTC)", doc.T, now)
		}
	}
	status := readStatusRaw(t, r.Dir)
	if !status.Enabled || status.State != StateOn || status.Failures != 0 || status.LastOK == nil {
		t.Errorf("status = %+v, want enabled/on/0/lastOK", status)
	}
	if r.Skipped() != 0 || r.Running() {
		t.Errorf("skipped = %d, running = %v after a completed tick", r.Skipped(), r.Running())
	}
}

// TestTickNeverQueuesOrOverlaps pins C4: a tick that starts while one is
// running is skipped and counted, and adds no sends of its own.
func TestTickNeverQueuesOrOverlaps(t *testing.T) {
	now := time.Date(2026, 3, 4, 6, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	var innerErr error
	reentered := false
	q.onQuery = func(text string) {
		if text != Statements["bb:waits"] || reentered {
			return
		}
		reentered = true
		innerErr = r.Tick(now) // the timer fires while this tick is mid-flight
	}

	if err := r.Tick(now); err != nil {
		t.Fatalf("outer tick: %v", err)
	}
	if innerErr != nil {
		t.Fatalf("skipped tick returned %v, want nil", innerErr)
	}
	if !reentered {
		t.Fatal("the tick never re-entered, so overlap was not exercised")
	}
	if r.Skipped() != 1 {
		t.Errorf("Skipped() = %d, want 1", r.Skipped())
	}
	if got := q.countID("bb:waits"); got != 1 {
		t.Errorf("bb:waits sent %d times, want 1 (the skipped tick must send nothing)", got)
	}
	if got := sampleCount(t, r.Dir, "waits"); got != 1 {
		t.Errorf("waits samples = %d, want 1 (the skipped tick writes nothing)", got)
	}
}

// TestAutoOffSurvivesRestartWithoutQuerying: the persisted auto-off is
// authoritative, so a tick writes status and touches neither SQL nor samples.
func TestTickAutoOffWritesStatusOnly(t *testing.T) {
	now := time.Date(2026, 3, 4, 7, 0, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), "sql")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := Save(filepath.Join(dir, "state.json"), State{Interval: MaxInterval, Failures: 10, AutoOff: true, State: StateAutoOff}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	sent := 0
	q := &recQuery{replies: map[string][]Row{}}
	q.onQuery = func(string) { sent++ }
	r := NewRunner(dir, ConnConfig{})
	r.samplers = []Sampler{baseSampler("bb:waits", "waits")}
	r.Now = func() time.Time { return now }
	r.Connect = func(ConnConfig) (Queryer, func() error, error) { return q, func() error { return nil }, nil }

	if err := r.Tick(now); err != nil {
		t.Fatalf("tick while auto-off: %v", err)
	}
	if sent != 0 {
		t.Errorf("%d statements sent while auto-off, want 0", sent)
	}
	if len(readSampleLines(t, dir)) != 0 {
		t.Error("an auto-off tick wrote samples")
	}
	status := readStatusRaw(t, dir)
	if status.Enabled || status.State != StateAutoOff {
		t.Errorf("status = %+v, want disabled/auto-off", status)
	}
}

// TestCeilingRunsAtEveryConnect: a failure drops the connection, so the next
// tick must re-prove rights before sampling again (C8).
func TestCeilingRunsAtEveryConnect(t *testing.T) {
	now := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	closes := 0
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
		closeFn: func() error {
			closes++
			return nil
		},
	})
	q.errFor = func(text string) error {
		if text == Statements["bb:waits"] {
			return &Error{CodeNetwork, "The local SQL Server could not be reached."}
		}
		return nil
	}

	if err := r.Tick(now); err != nil {
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeNetwork {
			t.Fatalf("tick error = %v, want code %q", err, CodeNetwork)
		}
	}
	if closes != 1 {
		t.Errorf("closer ran %d times after a failure, want 1 (one connection, dropped on failure)", closes)
	}
	if got := q.countID("bb:ceiling_role"); got != 1 {
		t.Errorf("ceiling_role sent %d times on the failing tick, want 1", got)
	}

	// Heal: the second tick reconnects, so the ceiling runs again.
	q.errFor = nil
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("recovered tick: %v", err)
	}
	if got := q.countID("bb:ceiling_role"); got != 2 {
		t.Errorf("ceiling_role sent %d times across two connects, want 2", got)
	}
	if r.CurrentState().AutoOff {
		t.Error("a single failure latched auto-off, want back-off only")
	}
}

// TestPermissionFailureIsDeniedAndOwnerOnly: permission, login and ceiling
// failures go straight to auto-off, which no later tick can clear.
func TestPermissionFailureIsDeniedAndOwnerOnly(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	for _, code := range []string{CodePermission, CodeLoginFailed, CodeTooManyRights} {
		q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
		q.errFor = func(text string) error {
			if text == Statements["bb:ceiling_role"] {
				return classified(code)
			}
			return nil
		}
		r := newFakeRunner(t, q, runnerOpts{
			now:      now,
			samplers: []Sampler{baseSampler("bb:waits", "waits")},
		})
		err := r.Tick(now)
		var e *Error
		if !errors.As(err, &e) || e.Code != code {
			t.Fatalf("%s: tick error = %v, want the fixed code", code, err)
		}
		if e.Message != classified(code).Message {
			t.Errorf("%s: message = %q, want the fixed sentence %q", code, e.Message, classified(code).Message)
		}
		st := r.CurrentState()
		if !st.AutoOff || st.State != StateDenied {
			t.Errorf("%s: state = %+v, want auto-off/denied", code, st)
		}
		status := readStatusRaw(t, r.Dir)
		if status.Enabled || status.State != StateDenied {
			t.Errorf("%s: status = %+v, want disabled/denied", code, status)
		}
		// Later ticks must not try again: only OwnerEnable re-arms the service.
		before := len(q.texts())
		if err := r.Tick(now.Add(time.Minute)); err != nil {
			t.Fatalf("%s: tick after denial: %v", code, err)
		}
		if got := len(q.texts()); got != before {
			t.Errorf("%s: sent %d more statements after denial, want 0", code, got-before)
		}
	}
}

// TestDriverTextNeverReachesDiskOrLog (C10): a raw driver error naming a
// database, an instance and a login must only ever surface as its fixed code,
// in the return value, the log and the files on disk. The send seam converts
// query errors itself, so this goes in through connect, where the driver's own
// text is what the runner receives.
func TestDriverTextNeverReachesDiskOrLog(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	raw := errors.New(`Cannot open database "ProdFin" requested by the login. ` +
		`Login failed for user 'CORP\svc-blackbox'.`)
	var logs []string
	r := newFakeRunner(t, nil, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	r.Connect = func(ConnConfig) (Queryer, func() error, error) { return nil, nil, raw }
	r.Log = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeLoginFailed {
		t.Fatalf("tick error = %v, want code %q", err, CodeLoginFailed)
	}
	if len(logs) == 0 {
		t.Error("a failed tick logged nothing")
	}

	for _, secret := range []string{"svc-blackbox", "ProdFin", "CORP", "Cannot open database"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("returned error leaks %q: %v", secret, err)
		}
		for _, line := range logs {
			if strings.Contains(line, secret) {
				t.Errorf("log leaks %q: %q", secret, line)
			}
			if !strings.Contains(line, CodeLoginFailed) {
				t.Errorf("log line %q does not name the fixed code", line)
			}
		}
		matches, globErr := filepath.Glob(filepath.Join(r.Dir, "*"))
		if globErr != nil {
			t.Fatalf("glob: %v", globErr)
		}
		for _, p := range matches {
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				t.Fatalf("read %s: %v", p, readErr)
			}
			if strings.Contains(string(data), secret) {
				t.Errorf("%s leaks %q on disk", filepath.Base(p), secret)
			}
		}
	}
	if !r.CurrentState().AutoOff {
		t.Error("a login failure did not latch auto-off, so it would retry forever")
	}
}

// TestSamplerOutsideAllowlistFailsClosed: a sampler cannot make the runner send
// anything that is not allowlisted.
func TestSamplerOutsideAllowlistFailsClosed(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:does_not_exist", "ghost")},
	})
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeNotAllowed {
		t.Fatalf("tick error = %v, want code %q", err, CodeNotAllowed)
	}
	if len(q.texts()) != 2 {
		t.Errorf("statements sent = %d, want only the 2 ceiling checks", len(q.texts()))
	}
	if len(readSampleLines(t, r.Dir)) != 0 {
		t.Error("a non-allowlisted sampler wrote a sample")
	}
}

// TestGateClosesTheSampler: with the feature absent the gate statement is sent
// and the sampler's own DMV query never is, and nothing is written. The closed
// gate still advances the due time, so it is re-checked on the sampler's own
// cadence instead of every tick.
func TestGateClosesTheSampler(t *testing.T) {
	now := time.Date(2026, 3, 4, 11, 0, 0, 0, time.UTC)
	gated := &fakeGatedSampler{
		fakeSampler: fakeSampler{id: "bb:ag", kind: "ag", interval: DemotedInterval},
		gateID:      "bb:hadr",
		closed:      true,
	}
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:hadr"]: {{"hadr": int64(0)}},
		Statements["bb:ag"]:   {{"database_id": int64(7), "synchronization_state": int64(1)}},
	}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{gated}})

	if err := r.Tick(now); err != nil {
		t.Fatalf("tick with a closed gate: %v", err)
	}
	if q.countID("bb:hadr") != 1 {
		t.Errorf("gate statement sent %d times, want 1", q.countID("bb:hadr"))
	}
	if got := q.countID("bb:ag"); got != 0 {
		t.Errorf("bb:ag sent %d times behind a closed gate, want 0", got)
	}
	if sampleCount(t, r.Dir, "ag") != 0 {
		t.Error("a closed gate still wrote an ag sample")
	}
	if gated.gateRows == nil {
		t.Fatal("GateOpen never saw the gate rows")
	}

	// Closed gate advances lastRun: the next tick must not re-send the gate.
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if got := q.countID("bb:hadr"); got != 1 {
		t.Errorf("gate sent %d times on the tick right after a closed gate, want 1 (due time must advance)", got)
	}

	// Open gate: the sampler's statement goes and a line lands.
	gated.closed = false
	q.replies[Statements["bb:hadr"]] = []Row{{"hadr": int64(1)}}
	if err := r.Tick(now.Add(DemotedInterval)); err != nil {
		t.Fatalf("tick with the gate open: %v", err)
	}
	if q.countID("bb:hadr") != 2 {
		t.Errorf("gate sent %d times on its own cadence, want 2", q.countID("bb:hadr"))
	}
	if q.countID("bb:ag") != 1 {
		t.Errorf("bb:ag sent %d times with the gate open, want 1", q.countID("bb:ag"))
	}
	if sampleCount(t, r.Dir, "ag") != 1 {
		t.Errorf("ag samples = %d, want 1", sampleCount(t, r.Dir, "ag"))
	}
}

// TestUnreadableGateStaysClosed: a gate that cannot be read must not become a
// stream of sample failures, and must not query the feature either.
func TestUnreadableGateStaysClosed(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	gated := &fakeGatedSampler{
		fakeSampler: fakeSampler{id: "bb:ag", kind: "ag", interval: DemotedInterval},
		gateID:      "bb:hadr",
	}
	q := &recQuery{replies: map[string][]Row{}}
	q.errFor = func(text string) error {
		if text == Statements["bb:hadr"] {
			return &Error{CodeTimeout, "the gate ran long"}
		}
		return nil
	}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{gated}})
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := q.countID("bb:ag"); got != 0 {
		t.Errorf("bb:ag sent %d times when its gate failed, want 0", got)
	}
	if sampleCount(t, r.Dir, "ag") != 0 {
		t.Error("a failed gate still wrote an ag sample")
	}
	if r.CurrentState().Failures != 0 {
		t.Errorf("failures = %d, want 0: an unreadable gate is not an instance failure", r.CurrentState().Failures)
	}
}

// TestGateRightsErrorStillFailsTheTick: a gate that is refused is a C8/C10
// problem, not a missing feature, so it must reach fail().
func TestGateRightsErrorStillFailsTheTick(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 30, 0, 0, time.UTC)
	gated := &fakeGatedSampler{
		fakeSampler: fakeSampler{id: "bb:ag", kind: "ag", interval: DemotedInterval},
		gateID:      "bb:hadr",
	}
	q := &recQuery{replies: map[string][]Row{}}
	q.errFor = func(text string) error {
		if text == Statements["bb:hadr"] {
			return &Error{CodePermission, "refused"}
		}
		return nil
	}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{gated}})
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodePermission {
		t.Fatalf("tick error = %v, want code %q", err, CodePermission)
	}
	if !r.CurrentState().AutoOff {
		t.Error("a refused gate did not latch auto-off")
	}
}

// TestDueSchedulingKeepsCadences: 15 s items every tick, 60 s items every
// fourth tick, with one shared clock.
func TestDueSchedulingKeepsCadences(t *testing.T) {
	now := time.Date(2026, 3, 4, 13, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:     fakeWaitRows(1),
		Statements["bb:filestats"]: {{}},
	}}
	r := newFakeRunner(t, q, runnerOpts{
		now: now,
		samplers: []Sampler{
			baseSampler("bb:waits", "waits"),
			&fakeSampler{id: "bb:filestats", kind: "filestats", interval: DemotedInterval},
		},
	})

	const ticks = 8
	for i := 0; i < ticks; i++ {
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		now = now.Add(BaseInterval)
	}
	if got := q.countID("bb:waits"); got != ticks {
		t.Errorf("bb:waits sent %d times over %d ticks, want %d", got, ticks, ticks)
	}
	if got := q.countID("bb:filestats"); got != ticks/4 {
		t.Errorf("bb:filestats sent %d times over %d ticks, want %d", got, ticks, ticks/4)
	}
	if got := q.countID("bb:ceiling_role"); got != 1 {
		t.Errorf("ceiling ran %d times over %d ticks on one connection, want 1", got, ticks)
	}
}

// TestOneSuccessPerTick: OnSuccess halves the interval, so calling it once per
// tick (not once per sampler) is what keeps a back-off step per tick.
func TestOneSuccessPerTick(t *testing.T) {
	now := time.Date(2026, 3, 4, 14, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:      fakeWaitRows(1),
		Statements["bb:schedulers"]: {{}},
		Statements["bb:tempdb"]:     {{}},
	}}
	r := newFakeRunner(t, q, runnerOpts{
		now: now,
		samplers: []Sampler{
			baseSampler("bb:waits", "waits"),
			baseSampler("bb:schedulers", "schedulers"),
			baseSampler("bb:tempdb", "tempdb"),
		},
	})
	r.state = State{Interval: 4 * BaseInterval, Failures: 2, State: StateBackingOff}

	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := r.CurrentState().Interval; got != 2*BaseInterval {
		t.Errorf("interval after one successful tick = %v, want %v (halve once, not three times)",
			got, 2*BaseInterval)
	}
	if got := r.CurrentState().Failures; got != 0 {
		t.Errorf("failures after success = %d, want 0", got)
	}
}

// TestFreeSpaceFloorStopsTheTick: below the owner's floor the tick writes
// nothing and reports the fixed disk code, without counting an instance
// failure (State tracks the instance, not the drive).
func TestFreeSpaceFloorStopsTheTick(t *testing.T) {
	now := time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	r.MinFreePercent = 10
	r.FreePercent = func(string) (float64, error) { return 4.2, nil }

	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeDiskLow {
		t.Fatalf("tick error = %v, want code %q", err, CodeDiskLow)
	}
	if len(q.texts()) != 0 {
		t.Errorf("statements sent = %d, want 0 (the floor is checked before connecting)", len(q.texts()))
	}
	if len(readSampleLines(t, r.Dir)) != 0 {
		t.Error("the tick wrote samples below the free-space floor")
	}
	if r.CurrentState().Failures != 0 {
		t.Errorf("failures = %d, want 0 (free space is not an instance failure)", r.CurrentState().Failures)
	}

	// Back above the floor: sampling resumes on its own.
	r.FreePercent = func(string) (float64, error) { return 40, nil }
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick after the drive recovered: %v", err)
	}
	if sampleCount(t, r.Dir, "waits") != 1 {
		t.Error("sampling did not resume above the floor")
	}
}

// TestRotationBoundsTheFolder: old hours are swept, the current hour and any
// name that is not an hour are left alone.
func TestRotationBoundsTheFolder(t *testing.T) {
	now := time.Date(2026, 3, 4, 16, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	r.KeepHours = 2

	write := func(name string) string {
		p := filepath.Join(r.Dir, name)
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return name
	}
	old := write("bb-sql-" + now.Add(-3*time.Hour).UTC().Format(hourLayout) + ".ndjson")
	fresh := write("bb-sql-" + now.Add(-time.Hour).UTC().Format(hourLayout) + ".ndjson")
	bogus := write("bb-sql-20060102-99.ndjson")
	foreign := write("notes.txt")

	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, name := range []string{fresh, bogus, foreign} {
		if _, err := os.Stat(filepath.Join(r.Dir, name)); err != nil {
			t.Errorf("%s was removed, want kept (%v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.Dir, old)); !os.IsNotExist(err) {
		t.Errorf("%s survived rotation, want deleted (older than %d hours)", old, r.KeepHours)
	}
	if _, err := os.Stat(SamplePath(r.Dir, now)); err != nil {
		t.Errorf("this hour's file is missing: %v", err)
	}

	// Rotation is one directory sweep per hour, not one per tick.
	r.rotated = ""
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if r.rotated != now.Add(BaseInterval).UTC().Format(hourLayout) {
		t.Errorf("rotated = %q, want the current hour", r.rotated)
	}
}

// TestValidHour: the file name is the clock rotation reads, so an unparsable or
// impossible hour must never be deleted.
func TestValidHour(t *testing.T) {
	for _, tc := range []struct {
		hour string
		want bool
	}{
		{"20260304-05", true},
		{"20260304-23", true},
		{"20260304-99", false},
		{"20260304-5", false},
		{"20260304505", false},
		{"20261304-05", false},
		{"", false},
	} {
		if got := validHour(tc.hour); got != tc.want {
			t.Errorf("validHour(%q) = %v, want %v", tc.hour, got, tc.want)
		}
	}
}

// TestRemainingClampsItemLimitsToTheTickBudget (C4): an item never gets more
// than the tick has left, and a spent budget stops the loop.
func TestRemainingClampsItemLimitsToTheTickBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := remaining(ctx, quickLimit); got > quickLimit || got > 100*time.Millisecond {
		t.Errorf("remaining = %v, want <= the 100ms the tick has left", got)
	}
	if got := remaining(context.Background(), quickLimit); got != quickLimit {
		t.Errorf("remaining without a deadline = %v, want the full %v", got, quickLimit)
	}

	expired, cancelExpired := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelExpired()
	time.Sleep(20 * time.Millisecond)
	if got := remaining(expired, longLimit); got > 0 {
		t.Errorf("remaining on a spent budget = %v, want <= 0", got)
	}

	if got := itemLimit(BaseInterval); got != quickLimit {
		t.Errorf("itemLimit(15s) = %v, want %v", got, quickLimit)
	}
	if got := itemLimit(DemotedInterval); got != longLimit {
		t.Errorf("itemLimit(60s) = %v, want %v", got, longLimit)
	}
}

// TestTickBudgetStopsTheLoopWithASpentDeadline: when the tick budget is gone
// the remaining samplers wait for the next tick instead of overrunning it.
func TestTickBudgetStopsTheLoopWithASpentDeadline(t *testing.T) {
	now := time.Date(2026, 3, 4, 17, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:      fakeWaitRows(1),
		Statements["bb:schedulers"]: {{}},
	}}
	r := newFakeRunner(t, q, runnerOpts{
		now: now,
		samplers: []Sampler{
			baseSampler("bb:waits", "waits"),
			baseSampler("bb:schedulers", "schedulers"),
		},
	})
	// Burn the whole 5 s tick budget inside the first item.
	q.onQuery = func(text string) {
		if text == Statements["bb:waits"] {
			time.Sleep(tickBudget + 50*time.Millisecond)
		}
	}
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("tick error = %v, want the fixed timeout error", err)
	}
	if e.Code != CodeTimeout {
		t.Errorf("tick error code = %q, want %q (the query budget the OS enforces)", e.Code, CodeTimeout)
	}
	if got := q.countID("bb:schedulers"); got != 0 {
		t.Errorf("bb:schedulers sent %d times after the budget was spent, want 0", got)
	}
	if r.CurrentState().Failures != 1 {
		t.Errorf("failures = %d, want 1", r.CurrentState().Failures)
	}
}

// TestRunStopsWithTheContextAndClosesTheConnection: leaving the service must
// release the one connection it holds, so the next start can open it again.
func TestRunStopsWithTheContextAndClosesTheConnection(t *testing.T) {
	now := time.Date(2026, 3, 4, 18, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	closed := 0
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
		closeFn: func() error {
			closed++
			return nil
		},
	})
	// Establish the connection first: Run's exit path is what releases it.
	if err := r.Tick(now); err != nil {
		t.Fatalf("setup tick: %v", err)
	}
	if closed != 0 {
		t.Fatalf("the closer ran %d times during a successful tick, want 0", closed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on context cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored the cancelled context")
	}
	if got := len(q.texts()); got != 3 {
		t.Errorf("statements sent = %d, want the 3 from the setup tick only", got)
	}
	if closed != 1 {
		t.Errorf("connection closed %d times on exit, want 1 (one connection, always released)", closed)
	}
	if r.Running() {
		t.Error("Run left the runner marked running")
	}
}

// TestStatePersistsAndAutoOffSurvivesRestart: the runner writes its own state
// next to the samples, and a restart reads the auto-off back.
func TestStatePersistsAndAutoOffSurvivesRestart(t *testing.T) {
	now := time.Date(2026, 3, 4, 19, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	q.errFor = func(text string) error {
		if text == Statements["bb:waits"] {
			return &Error{CodeNetwork, "unreachable"}
		}
		return nil
	}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})

	for i := 0; i < AutoOffFailures; i++ {
		if err := r.Tick(now); err == nil {
			t.Fatalf("tick %d: want the injected failure", i)
		}
		now = now.Add(r.CurrentState().Interval)
	}
	st := r.CurrentState()
	if !st.AutoOff {
		t.Fatalf("state = %+v, want auto-off after %d failures", st, AutoOffFailures)
	}
	if st.State != StateAutoOff {
		t.Errorf("state string = %q, want %q", st.State, StateAutoOff)
	}

	// The persisted file is the same state, and a new Runner on the same folder
	// reads it back: nothing here can clear it.
	reloaded, err := Load(filepath.Join(r.Dir, "state.json"))
	if err != nil {
		t.Fatalf("load persisted state: %v", err)
	}
	if !reloaded.AutoOff || reloaded.Failures != st.Failures {
		t.Errorf("persisted state = %+v, want auto-off with %d failures", reloaded, st.Failures)
	}

	sent := 0
	q2 := &recQuery{replies: map[string][]Row{}}
	ceilingOK(q2)
	q2.onQuery = func(string) { sent++ }
	r2 := NewRunner(r.Dir, ConnConfig{})
	r2.samplers = []Sampler{baseSampler("bb:waits", "waits")}
	r2.Now = func() time.Time { return now }
	r2.Connect = func(ConnConfig) (Queryer, func() error, error) { return q2, func() error { return nil }, nil }
	if err := r2.Tick(now); err != nil {
		t.Fatalf("tick on the restarted service: %v", err)
	}
	if sent != 0 {
		t.Errorf("the restarted service sent %d statements while auto-off, want 0", sent)
	}

	// OwnerEnable is the only door back.
	OwnerEnable(&r2.state)
	if err := Save(filepath.Join(r2.Dir, "state.json"), r2.state); err != nil {
		t.Fatalf("owner enable: %v", err)
	}
	r3 := NewRunner(r2.Dir, ConnConfig{})
	r3.samplers = []Sampler{baseSampler("bb:waits", "waits")}
	r3.Now = func() time.Time { return now }
	r3.Connect = func(ConnConfig) (Queryer, func() error, error) { return q2, func() error { return nil }, nil }
	if err := r3.Tick(now); err != nil {
		t.Fatalf("tick after the owner re-enabled: %v", err)
	}
	if q2.countID("bb:waits") != 1 {
		t.Errorf("bb:waits sent %d times after OwnerEnable, want 1", q2.countID("bb:waits"))
	}
}

// TestDecodeFailureIsAFixedError: a sampler that cannot decode its rows fails
// the tick with a fixed code and writes a partial-free line count of zero.
func TestDecodeFailureIsAFixedError(t *testing.T) {
	now := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now: now,
		samplers: []Sampler{&fakeSampler{
			id: "bb:waits", kind: "waits", interval: BaseInterval,
			decode: func([]Row) (any, bool, error) {
				return nil, false, errors.New("driver said something verbose")
			},
		}},
	})
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeFailed {
		t.Fatalf("tick error = %v, want code %q", err, CodeFailed)
	}
	if strings.Contains(err.Error(), "verbose") {
		t.Errorf("error text leaked: %v", err)
	}
	if sampleCount(t, r.Dir, "waits") != 0 {
		t.Error("an undecodable sample was still written")
	}
	if r.CurrentState().Failures != 1 {
		t.Errorf("failures = %d, want 1", r.CurrentState().Failures)
	}
}

// TestTruncatedFlagReachesTheLine: C6's cap signal must be visible in the NDJSON.
func TestTruncatedFlagReachesTheLine(t *testing.T) {
	now := time.Date(2026, 3, 4, 21, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(CapWaits + 1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now: now,
		samplers: []Sampler{&fakeSampler{
			id: "bb:waits", kind: "waits", interval: BaseInterval,
			decode: func(rows []Row) (any, bool, error) {
				var out []Waits
				truncated, err := decodeRows(rows, &out, CapWaits)
				return out, truncated, err
			},
		}},
	})
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	lines := readSampleLines(t, r.Dir)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if !lines[0].Truncated {
		t.Error("truncated = false for an over-cap reply, want true (C6)")
	}
	raw, err := os.ReadFile(SamplePath(r.Dir, now))
	if err != nil {
		t.Fatalf("read samples: %v", err)
	}
	if strings.Contains(string(raw), `"truncated":false`) {
		t.Error("a false truncated flag was persisted, want the field omitted")
	}
}
