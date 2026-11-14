package bbsql

// sqlresidual_test.go closes the wave-7-11 residuals that the landed
// control-plane code only asserted at the DBQueryer boundary:
//
//   - W8-02/W9-02: the REAL Runner, driving the REAL production samplers over a
//     fake database/sql/driver that records physical connection generations,
//     proves BOTH ceiling controls precede the first sampler on EVERY connection
//     id — including a silent pool replacement between batches (session-reset
//     expiry) and a retryable bad connection mid-tick.
//   - W8-01: OpenContext bounds the WHOLE connect lifecycle by the caller's
//     context; a driver Connect that ignores its context (stuck prelogin/TLS)
//     still returns within budget and the orphaned connection is closed.
//   - W8-06: the OpenContext startup transcript contains ONLY the approved,
//     tagged ceiling controls — no driver Ping `select 1;`, no untagged SQL.
//
// The fake driver is injected through the connectInner seam (conn.go), so these
// tests exercise the full OpenContext -> ceilingConnector -> guardedConn path,
// not a stub that bypasses admission. No live SQL, no network, no credentials.
//
// Generation churn: conn.go's watchBudget conservatively retires a physical
// connection when the per-query context's deferred cancel races a finished
// statement (documented there as "only retires the connection early ... the next
// use re-proves the ceiling"). database/sql therefore may mint a fresh,
// freshly-admitted generation for almost every statement. The assertions below
// are written against the CONTRACT (every generation proves the ceiling before
// any sampler), never against a fixed generation count or index, so they hold
// regardless of how aggressively the pool recycles.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// --- fake generation driver -------------------------------------------------

// residConnector mints a new numbered physical connection per Connect and can
// model the two ways database/sql replaces a generation behind the runner's
// back: a failed session reset on idle reuse (expireOnReset) and a retryable
// bad connection on a chosen statement (badStmt, fired once). connectBlock makes
// Connect ignore its context, like a stuck prelogin/TLS handshake.
type residConnector struct {
	mu            sync.Mutex
	gens          []*residConn
	expireOnReset bool
	connectBlock  chan struct{}
	badStmt       string // first send of this statement returns ErrBadConn once
	badFired      bool
}

func (c *residConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.connectBlock != nil {
		<-c.connectBlock // deliberately context-ignorant, like the real driver
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	g := &residConn{
		id:       len(c.gens) + 1,
		parent:   c,
		closeCh:  make(chan struct{}),
		resetBad: c.expireOnReset,
	}
	c.gens = append(c.gens, g)
	return g, nil
}

func (c *residConnector) Driver() driver.Driver { return residDriver{} }

func (c *residConnector) generations() []*residConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*residConn(nil), c.gens...)
}

// takeBad reports whether this send is the one-shot retryable bad connection.
func (c *residConnector) takeBad(q string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.badStmt != "" && !c.badFired && q == c.badStmt {
		c.badFired = true
		return true
	}
	return false
}

type residDriver struct{}

func (residDriver) Open(string) (driver.Conn, error) { return nil, errors.New("stub") }

type residConn struct {
	id       int
	parent   *residConnector
	mu       sync.Mutex
	sent     []string
	closed   bool
	closeCh  chan struct{}
	resetBad bool // ResetSession returns ErrBadConn (silent pool replacement)
}

func (c *residConn) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func (c *residConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *residConn) Prepare(q string) (driver.Stmt, error) { return residStmt{c: c, q: q}, nil }
func (c *residConn) PrepareContext(_ context.Context, q string) (driver.Stmt, error) {
	return residStmt{c: c, q: q}, nil
}
func (c *residConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.closeCh)
	}
	return nil
}
func (c *residConn) Begin() (driver.Tx, error) { return nil, errors.New("stub") }
func (c *residConn) IsValid() bool             { return !c.isClosed() }

// ResetSession models a transport that died while idle: database/sql discards
// the connection and Connects a new generation (which must re-prove the ceiling).
func (c *residConn) ResetSession(context.Context) error {
	if c.resetBad {
		return driver.ErrBadConn
	}
	return nil
}

type residStmt struct {
	c *residConn
	q string
}

func (s residStmt) Close() error  { return nil }
func (s residStmt) NumInput() int { return 0 }
func (s residStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("stub")
}
func (s residStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("stub")
}

func (s residStmt) QueryContext(ctx context.Context, _ []driver.NamedValue) (driver.Rows, error) {
	role, perms := Statements["bb:ceiling_role"], Statements["bb:ceiling_perms"]
	s.c.mu.Lock()
	s.c.sent = append(s.c.sent, s.q)
	s.c.mu.Unlock()
	if s.c.parent.takeBad(s.q) {
		// A retryable broken connection: database/sql discards this generation
		// and retries the SAME statement through Connect (a new generation).
		return nil, driver.ErrBadConn
	}
	switch s.q {
	case role:
		cols := append([]string(nil), ceilingRoleNames...)
		vals := make([]driver.Value, len(cols))
		for i := range cols {
			vals[i] = int64(0)
		}
		return &residRows{cols: cols, rows: [][]driver.Value{vals}}, nil
	case perms:
		return &residRows{cols: []string{"permission_name"}}, nil
	case Statements["bb:hadr"]:
		return &residRows{cols: []string{"hadr"}, rows: [][]driver.Value{{int64(1)}}}, nil
	default:
		// An empty sampler reply: every production sampler reports it missing
		// (nil payload, no error), so the tick still records the send order.
		return &residRows{cols: []string{}}, nil
	}
}

type residRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *residRows) Columns() []string { return r.cols }
func (r *residRows) Close() error      { return nil }
func (r *residRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// setResidInner injects the fake driver through the OpenContext seam.
func setResidInner(t *testing.T, gc *residConnector) {
	t.Helper()
	old := connectInner
	connectInner = func(mssql.Dialer) driver.Connector { return gc }
	t.Cleanup(func() { connectInner = old })
}

// newResidRunner wires the REAL production samplers (fresh instances) to a
// ceiling-admitted *sql.DB over the fake driver.
func newResidRunner(t *testing.T, db *sql.DB) *Runner {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sql")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r := NewRunner(dir, ConnConfig{Server: "localhost"})
	r.samplers = freshProductionSamplerList()
	r.lastRun = make(map[string]time.Time)
	r.Connect = func(ConnConfig) (Queryer, func() error, error) {
		return DBQueryer{DB: db}, func() error { return nil }, nil
	}
	return r
}

// assertResidCeilingFirst proves the connection-generation contract on one
// physical connection: admission sent BOTH ceiling controls first, and every
// text on the wire is allowlisted (no driver Ping, no untagged SQL). Because the
// controls are always the first two sends, no sampler can precede them.
func assertResidCeilingFirst(t *testing.T, g *residConn) {
	t.Helper()
	texts := g.texts()
	role, perms := Statements["bb:ceiling_role"], Statements["bb:ceiling_perms"]
	if len(texts) < 2 {
		t.Fatalf("gen %d sent %d texts, want at least the two ceiling controls: %v", g.id, len(texts), texts)
	}
	if texts[0] != role || texts[1] != perms {
		t.Fatalf("gen %d: admission did not send both ceiling controls first: %v", g.id, texts[:2])
	}
	for _, tx := range texts {
		if !IsAllowed(tx) {
			t.Fatalf("gen %d sent non-allowlisted text (driver Ping?): %q", g.id, tx)
		}
	}
}

func residRanSampler(g *residConn) bool {
	role, perms := Statements["bb:ceiling_role"], Statements["bb:ceiling_perms"]
	for _, tx := range g.texts() {
		if tx != role && tx != perms {
			return true
		}
	}
	return false
}

// residCountSends counts how many times a statement was sent across all
// generations (a retryable bad connection sends it once on the retired
// generation and again on the admitted replacement).
func residCountSends(gens []*residConn, text string) int {
	n := 0
	for _, g := range gens {
		for _, tx := range g.texts() {
			if tx == text {
				n++
			}
		}
	}
	return n
}

// TestRunnerSilentExpiryEveryGenerationProvesCeiling: a session that dies while
// idle (the one-hour expiry / transport-reset path) makes database/sql replace
// the physical connection BETWEEN batches. Every replacement generation must
// re-prove the ceiling before any sampler runs on it (W8-02/W9-02).
func TestRunnerSilentExpiryEveryGenerationProvesCeiling(t *testing.T) {
	gc := &residConnector{expireOnReset: true}
	setResidInner(t, gc)
	db, err := OpenContext(context.Background(), ConnConfig{Server: "localhost"})
	if err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := newResidRunner(t, db)

	now := time.Date(2026, 4, 5, 6, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		now = now.Add(BaseInterval)
	}
	gens := gc.generations()
	if len(gens) < 2 {
		t.Fatalf("generations = %d, want a silent pool replacement across ticks", len(gens))
	}
	sampled := 0
	for _, g := range gens {
		assertResidCeilingFirst(t, g)
		if residRanSampler(g) {
			sampled++
		}
	}
	if sampled == 0 {
		t.Fatal("no generation ran a sampler; the proof is vacuous")
	}
}

// TestRunnerMidTickBadConnEveryGenerationProvesCeiling: a retryable bad
// connection returned MID-tick (on bb:requests, after ag/filestats already ran)
// makes database/sql discard the generation and retry the same statement on a
// new one. The replacement must re-prove the ceiling before the retried sampler
// runs, and no generation may run a sampler on an unproved session (W8-02/W9-02).
func TestRunnerMidTickBadConnEveryGenerationProvesCeiling(t *testing.T) {
	gc := &residConnector{badStmt: Statements["bb:requests"]}
	setResidInner(t, gc)
	db, err := OpenContext(context.Background(), ConnConfig{Server: "localhost"})
	if err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := newResidRunner(t, db)

	now := time.Date(2026, 4, 5, 7, 0, 0, 0, time.UTC)
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick 1 (the bad conn must be retried, not fail the tick): %v", err)
	}
	gens := gc.generations()
	if len(gens) < 2 {
		t.Fatalf("generations = %d, want a mid-tick replacement after the retryable bad conn", len(gens))
	}
	// The bad statement was sent on the retired generation AND retried on a
	// freshly admitted one: the retry crossed a generation boundary.
	if n := residCountSends(gens, Statements["bb:requests"]); n < 2 {
		t.Fatalf("bb:requests sent %d times, want >= 2 (the failed attempt plus the retry)", n)
	}
	sampled := 0
	for _, g := range gens {
		assertResidCeilingFirst(t, g)
		if residRanSampler(g) {
			sampled++
		}
	}
	if sampled == 0 {
		t.Fatal("no generation ran a sampler; the retry did not succeed on an admitted session")
	}
	// A second tick reuses the admitted pool; the contract still holds.
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	for _, g := range gc.generations() {
		assertResidCeilingFirst(t, g)
	}
}

// TestOpenContextCancellableConnect: the driver's Connect can ignore its
// context (prelogin/TLS). OpenContext must still return within the caller's
// budget with a fixed code, and the orphaned connection must be closed when it
// finally answers, not leaked (W8-01).
func TestOpenContextCancellableConnect(t *testing.T) {
	block := make(chan struct{})
	gc := &residConnector{connectBlock: block}
	setResidInner(t, gc)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := OpenContext(ctx, ConnConfig{Server: "localhost"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a stalled connect was admitted")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeConnectTimeout {
		t.Fatalf("err = %v, want the fixed connect-timeout code", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("connect returned after %v; the caller budget was not a hard bound", elapsed)
	}
	// Release the orphaned attempt: it must be closed, not left behind.
	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		gens := gc.generations()
		if len(gens) >= 1 && gens[0].isClosed() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the orphaned physical connection was never closed")
}

// TestOpenContextStartupTranscriptOnlyApprovedSQL: through the full OpenContext
// path (no stub bypassing admission), the startup transcript on the first
// physical connection is EXACTLY the two approved, tagged ceiling controls.
// There is no driver Ping `select 1;` and no untagged SQL (W8-06).
func TestOpenContextStartupTranscriptOnlyApprovedSQL(t *testing.T) {
	gc := &residConnector{}
	setResidInner(t, gc)
	db, err := OpenContext(context.Background(), ConnConfig{Server: "localhost"})
	if err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	defer db.Close()

	gens := gc.generations()
	if len(gens) != 1 {
		t.Fatalf("generations = %d, want exactly one at startup", len(gens))
	}
	texts := gens[0].texts()
	role, perms := Statements["bb:ceiling_role"], Statements["bb:ceiling_perms"]
	if len(texts) != 2 || texts[0] != role || texts[1] != perms {
		t.Fatalf("startup transcript = %v, want only the two approved ceiling controls (no driver Ping)", texts)
	}
	for _, tx := range texts {
		if !IsAllowed(tx) {
			t.Fatalf("non-allowlisted startup SQL: %q", tx)
		}
	}
}
