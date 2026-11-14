package bbsql

// sqlcontrol_conn_test.go is the W8-01/W8-02/W8-06 regression gate at the
// driver boundary: an in-process fake database/sql/driver records physical
// connection generations and every SQL text sent on each, and proves
//   - every generation passes BOTH ceiling controls before any sampler text
//     (including silent pool replacement after expiry/bad-conn),
//   - an over-privileged generation is closed and never samples,
//   - cancellation closes the active physical connection within budget,
//   - the connect watchdog returns within budget even when the driver's
//     Connect ignores its context, and the orphan is closed,
//   - the dialer clamps driver SetDeadline calls to the absolute connect
//     deadline until the connect is released,
//   - the startup transcript contains only allowlisted SQL (no driver Ping).

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// --- fake generation driver -------------------------------------------------

type genConnector struct {
	mu           sync.Mutex
	gens         []*genConn
	roleOverride Row          // when set, replaces the compliant role row
	connectDelay time.Duration // Connect sleeps this long, ignoring ctx
	block        bool          // new connections block on sampler queries
}

func (c *genConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.connectDelay > 0 {
		time.Sleep(c.connectDelay) // deliberately context-ignorant, like the driver
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	g := &genConn{id: len(c.gens) + 1, closeCh: make(chan struct{}), role: c.roleOverride, block: c.block}
	c.gens = append(c.gens, g)
	return g, nil
}

func (c *genConnector) Driver() driver.Driver { return genDriver{} }

func (c *genConnector) generations() []*genConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*genConn(nil), c.gens...)
}

type genDriver struct{}

func (genDriver) Open(string) (driver.Conn, error) { return nil, errors.New("stub") }

type genConn struct {
	id      int
	mu      sync.Mutex
	sent    []string
	closed  bool
	closeCh chan struct{}
	role    Row
	block   bool // sampler queries block until close/ctx, ignoring ctx return
	bad     bool // next query returns driver.ErrBadConn (pool retry trigger)
}

func (c *genConn) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func (c *genConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *genConn) Prepare(q string) (driver.Stmt, error) { return genStmt{c: c, q: q}, nil }
func (c *genConn) PrepareContext(_ context.Context, q string) (driver.Stmt, error) {
	return genStmt{c: c, q: q}, nil
}
func (c *genConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.closeCh)
	}
	return nil
}
func (c *genConn) Begin() (driver.Tx, error) { return nil, errors.New("stub") }
func (c *genConn) IsValid() bool             { return !c.isClosed() }

type genStmt struct {
	c *genConn
	q string
}

func (s genStmt) Close() error  { return nil }
func (s genStmt) NumInput() int { return 0 }
func (s genStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("stub")
}
func (s genStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("stub")
}

func (s genStmt) QueryContext(ctx context.Context, _ []driver.NamedValue) (driver.Rows, error) {
	s.c.mu.Lock()
	s.c.sent = append(s.c.sent, s.q)
	block := s.c.block
	bad := s.c.bad
	s.c.mu.Unlock()
	if bad {
		// A retryable broken connection: database/sql discards this physical
		// connection and retries through Connect (a new generation).
		return nil, driver.ErrBadConn
	}
	switch s.q {
	case Statements["bb:ceiling_role"]:
		row := s.c.role
		if row == nil {
			row = Row{}
			for _, name := range ceilingRoleNames {
				row[name] = int64(0)
			}
		}
		cols := rowColumns(row)
		vals := make([]driver.Value, len(cols))
		for i, c := range cols {
			vals[i] = row[c]
		}
		return &genRows{cols: cols, rows: [][]driver.Value{vals}}, nil
	case Statements["bb:ceiling_perms"]:
		return &genRows{cols: []string{"permission_name"}}, nil
	default:
		if block {
			// A stuck driver: it returns only when the socket dies (close) or
			// the context ends — the watchdog must provide the former.
			select {
			case <-s.c.closeCh:
				return nil, errors.New("connection closed")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &genRows{cols: []string{"current_tasks", "runnable_tasks"},
			rows: [][]driver.Value{{int64(1), int64(0)}}}, nil
	}
}

func rowColumns(r Row) []string {
	cols := make([]string, 0, len(r))
	for k := range r {
		cols = append(cols, k)
	}
	return cols
}

type genRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *genRows) Columns() []string { return r.cols }
func (r *genRows) Close() error      { return nil }
func (r *genRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// newGenDB wires the ceiling connector over the fake driver, with the same
// pool settings production uses.
func newGenDB(t *testing.T, gc *genConnector) *sql.DB {
	t.Helper()
	cc := ceilingConnector{cfg: msdsn.Config{}, inner: func(d mssql.Dialer) driver.Connector {
		if ld, ok := d.(loopbackDialer); !ok || ld.deadline.IsZero() || ld.released == nil {
			t.Errorf("connect dialer = %T; want a clamped loopbackDialer", d)
		}
		return gc
	}}
	db := sql.OpenDB(cc)
	applyPool(db, pool)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestEveryGenerationProvesCeilingFirst: the first physical connection AND a
// silent pool replacement (expiry/bad-conn) both run the two ceiling controls
// before any sampler text, on the same generation (W8-02/W9-02).
func TestEveryGenerationProvesCeilingFirst(t *testing.T) {
	gc := &genConnector{}
	db := newGenDB(t, gc)
	ctx := context.Background()
	q := DBQueryer{DB: db}

	if _, err := q.Query(ctx, Statements["bb:schedulers"]); err != nil {
		t.Fatalf("first query: %v", err)
	}
	// Make the physical connection broken behind database/sql's back, the way
	// a transport failure does; the retryable ErrBadConn forces the pool to
	// discard it and Connect a new generation (W8-02's silent-replacement path).
	gens := gc.generations()
	if len(gens) != 1 {
		t.Fatalf("generations = %d, want 1", len(gens))
	}
	gens[0].mu.Lock()
	gens[0].bad = true
	gens[0].mu.Unlock()
	if _, err := q.Query(ctx, Statements["bb:schedulers"]); err != nil {
		t.Fatalf("second query: %v", err)
	}
	gens = gc.generations()
	if len(gens) != 2 {
		t.Fatalf("generations = %d, want 2 (the pool must replace the retired conn)", len(gens))
	}
	for _, g := range gens {
		texts := g.texts()
		if len(texts) < 3 {
			t.Fatalf("gen %d sent %d texts, want controls + sampler: %v", g.id, len(texts), texts)
		}
		if texts[0] != Statements["bb:ceiling_role"] || texts[1] != Statements["bb:ceiling_perms"] {
			t.Fatalf("gen %d: first sends are not the ceiling controls: %v", g.id, texts[:2])
		}
		for _, text := range texts {
			if !IsAllowed(text) {
				t.Fatalf("gen %d sent non-allowlisted text (driver Ping?): %q", g.id, text)
			}
		}
	}
}

// TestOverprivilegedGenerationNeverSamples: a generation whose ceiling proof
// refuses is closed, the error keeps the fixed code and C8 origin, and no
// sampler text ever reaches it.
func TestOverprivilegedGenerationNeverSamples(t *testing.T) {
	role := Row{}
	for _, name := range ceilingRoleNames {
		role[name] = int64(0)
	}
	role["sysadmin"] = int64(1)
	gc := &genConnector{roleOverride: role}
	db := newGenDB(t, gc)
	q := DBQueryer{DB: db}
	_, err := q.Query(context.Background(), Statements["bb:schedulers"])
	if err == nil {
		t.Fatal("an over-privileged generation was admitted")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTooManyRights {
		t.Fatalf("err = %v, want too-many-rights", err)
	}
	if !IsCeilingFailure(err) {
		t.Fatalf("the admission error lost its C8 origin: %v", err)
	}
	for _, g := range gc.generations() {
		if !g.isClosed() {
			t.Errorf("gen %d was not closed after a failed admission", g.id)
		}
		for _, text := range g.texts() {
			if text != Statements["bb:ceiling_role"] && text != Statements["bb:ceiling_perms"] {
				t.Errorf("gen %d sent a sampler on an unproved session: %q", g.id, text)
			}
		}
	}
}

// TestUnreadableProofLatchesOrigin: an unreadable proof (NULL flag) at
// admission keeps the generic public code but the C8 origin (W9-03).
func TestUnreadableProofLatchesOrigin(t *testing.T) {
	role := Row{}
	for _, name := range ceilingRoleNames {
		role[name] = int64(0)
	}
	role["perm_empty"] = nil // unreadable
	gc := &genConnector{roleOverride: role}
	db := newGenDB(t, gc)
	_, err := DBQueryer{DB: db}.Query(context.Background(), Statements["bb:schedulers"])
	if err == nil || !IsCeilingFailure(err) {
		t.Fatalf("unreadable proof: %v, want a C8-origin failure", err)
	}
	if Classify(err).Code != CodeFailed {
		t.Fatalf("public code = %q, want the generic failed", Classify(err).Code)
	}
}

// TestCancelClosesPhysicalConnection: a query whose budget expires must close
// the active physical connection (aborting the driver's cancel-drain) and
// return within budget (W8-01). The ceiling controls still run first; only the
// sampler blocks.
func TestCancelClosesPhysicalConnection(t *testing.T) {
	gc := &genConnector{block: true}
	db := newGenDB(t, gc)
	q := DBQueryer{DB: db}
	qctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := q.Query(qctx, Statements["bb:schedulers"])
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("the stuck query returned rows")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancellation took %v; the budget was not a hard bound", elapsed)
	}
	gens := gc.generations()
	if len(gens) != 1 {
		t.Fatalf("generations = %d, want 1", len(gens))
	}
	if !gens[0].isClosed() {
		t.Fatal("cancellation did not close the active physical connection")
	}
	// The next query must go through a NEW generation with a fresh proof.
	gc.mu.Lock()
	gc.block = false
	gc.mu.Unlock()
	if _, err := q.Query(context.Background(), Statements["bb:schedulers"]); err != nil {
		t.Fatalf("post-cancel query: %v", err)
	}
	all := gc.generations()
	if len(all) != 2 {
		t.Fatalf("generations = %d, want a replacement after the cancelled one", len(all))
	}
	texts := all[1].texts()
	if texts[0] != Statements["bb:ceiling_role"] || texts[1] != Statements["bb:ceiling_perms"] {
		t.Fatalf("the replacement generation skipped the ceiling: %v", texts)
	}
}

// TestConnectWatchdogBoundsContextIgnorantConnect: the driver's Connect can
// ignore its context (prelogin/TLS); the watchdog must still return within
// budget and close the orphan when it finally answers (W8-01).
func TestConnectWatchdogBoundsContextIgnorantConnect(t *testing.T) {
	gc := &genConnector{connectDelay: 300 * time.Millisecond}
	cc := ceilingConnector{cfg: msdsn.Config{}, inner: func(mssql.Dialer) driver.Connector { return gc }}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := cc.Connect(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a stalled connect was admitted")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("connect watchdog returned after %v; budget not enforced", elapsed)
	}
	// The orphaned attempt answers later and must be closed, not leaked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		gens := gc.generations()
		if len(gens) == 1 && gens[0].isClosed() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the orphaned physical connection was never closed")
}

// TestClampedDialerDeadlines: while the connect is in flight the driver cannot
// extend the socket deadline past the absolute connect budget; after release,
// normal deadlines work again (W8-01).
func TestClampedDialerDeadlines(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the socket open without answering: a partial-progress peer.
			defer c.Close()
		}
	}()

	released := &atomic.Bool{}
	d := loopbackDialer{deadline: time.Now().Add(150 * time.Millisecond), released: released}
	c, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The driver's per-I/O reset asks for an hour; the clamp must refuse it.
	if err := c.SetDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	start := time.Now()
	_, err = c.Read(buf)
	if err == nil {
		t.Fatal("read succeeded on a silent peer")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the clamped deadline did not bite: read took %v", time.Since(start))
	}
	// Once the connect is released, deadlines are the driver's own again.
	released.Store(true)
	if err := c.SetDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// TestOpenContextNoPingTranscript: through the constructor seam, OpenContext
// acquires the connection without sending ANY SQL of its own — the transcript
// stays empty until the (separately tested) ceiling admission runs.
func TestOpenContextNoPingTranscript(t *testing.T) {
	var prepared []string
	var connects int32
	old := openDB
	openDB = func(driver.Connector) *sql.DB {
		return sql.OpenDB(stubConnector{connects: &connects, prepared: &prepared})
	}
	defer func() { openDB = old }()
	db, err := OpenContext(context.Background(), ConnConfig{Server: "localhost"})
	if err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	defer db.Close()
	if len(prepared) != 0 {
		t.Fatalf("OpenContext sent its own SQL: %v", prepared)
	}
	if atomic.LoadInt32(&connects) != 1 {
		t.Fatalf("connects = %d, want 1", connects)
	}
}