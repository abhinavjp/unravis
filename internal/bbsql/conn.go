// conn.go opens the single local SQL connection (C4 timeouts, C5 one
// connection, C12 local-only) and gates every send through the allowlist (C1).
// Integrated auth only: no password is accepted or stored. Errors carry fixed
// codes and sentences, never driver text.
//
// W8-01: OpenContext threads the caller's context through the WHOLE connect
// lifecycle (certificate parse, dial, prelogin/TLS, auth) and the query path;
// the dialer clamps every driver SetDeadline to the absolute connect deadline
// so per-I/O timeouts cannot extend it, and a cancelled query closes the
// physical connection so the driver's cancel-drain cannot outlive the budget.
// W8-02: every physical connection is admitted through CheckCeiling on that
// exact connection BEFORE database/sql can use it (ceilingConnector), so pool
// expiry and ErrBadConn retries can never run a sampler on an unproved
// generation. W8-06: there is no driver Ping — the driver's own `select 1;`
// never reaches the wire; the first statements on a new connection are the
// approved, tagged ceiling controls.
package bbsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// Fixed error codes; messages are fixed sentences, never driver text.
const (
	CodeRemoteTarget = "remote-target"
	CodeConnect      = "connect-failed"
	CodeNotAllowed   = "not-allowlisted"
	CodeTimeout      = "query-timeout"
	CodeQuery        = "query-failed"
)

const (
	appName        = "Unravis Blackbox SQL"
	connectTimeout = 5 * time.Second // C4 connect budget
)

// Error is a fixed-code error; driver messages (host/login/DSN parts) are
// never wrapped or surfaced.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

var errNotAllowed = &Error{CodeNotAllowed, "The statement is not on the allowlist."}

// splitTarget trims the optional lpc: prefix and splits host\instance. ok is
// false when an instance part is present but empty or carries unsafe chars.
func splitTarget(server string) (host, instance string, lpc, ok bool) {
	s := strings.TrimSpace(server)
	if len(s) >= 4 && strings.EqualFold(s[:4], "lpc:") {
		s, lpc = s[4:], true
	}
	host = s
	if i := strings.IndexByte(s, '\\'); i >= 0 {
		host, instance = s[:i], s[i+1:]
		if instance == "" || strings.ContainsAny(instance, "/\\;=@'\":,?#%& \t\r\n") {
			return "", "", lpc, false
		}
	}
	return host, instance, lpc, true
}

// CheckLocalTarget enforces C12: only the local machine, optionally with an
// instance, is valid; anything else fails "remote-target" before connecting.
func CheckLocalTarget(server string) error {
	host, instance, lpc, ok := splitTarget(server)
	if !ok {
		return &Error{CodeRemoteTarget, "Only the local SQL Server can be monitored."}
	}
	if host == "" && instance == "" && lpc {
		return nil // bare "lpc:" is the local default instance
	}
	switch strings.ToLower(host) {
	case ".", "(local)", "localhost", "127.0.0.1", "::1":
		return nil
	}
	return &Error{CodeRemoteTarget, "Only the local SQL Server can be monitored."}
}

// ConnConfig: the local target plus the owner's trust anchors, which let
// encrypt=true verify the local instance's certificate; with no anchor the
// default self-signed cert still fails closed (connect-failed). The per-query
// C4 budgets (3 s / 5 s) are NOT configurable: the runner derives them from
// each sampler's cadence, so a config file cannot widen a timeout.
type ConnConfig struct {
	Server string
	// ServerCertificate is the absolute path to the instance certificate PEM.
	// The driver byte-pins it (serverCertificate=<path>): the handshake only
	// succeeds when the server presents exactly this certificate. Mutually
	// exclusive with Certificate.
	ServerCertificate string
	// Certificate is the absolute path to the CA PEM that issued the instance
	// certificate (chain validation). Must be paired with HostNameInCertificate.
	Certificate string
	// HostNameInCertificate is the name the CA-issued certificate must carry
	// (certificate=<CA PEM> + hostnameincertificate=<name>).
	HostNameInCertificate string
}

// anchorHostRe is the certificate host-name shape: letters, digits, dots and
// dashes only — no scheme, port, path or credential characters can ride along.
var anchorHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// validAnchorPath reports whether p is an owner-supplied certificate path:
// absolute, a PEM/DER file the driver can read, and carrying no credentials or
// DSN metacharacters.
func validAnchorPath(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".pem", ".der":
	default:
		return false
	}
	if strings.ContainsAny(p, ";=@'\"\t\r\n") {
		return false
	}
	lower := strings.ToLower(p)
	for _, tok := range []string{"password", "user id", "pwd"} {
		if strings.Contains(lower, tok) {
			return false
		}
	}
	return true
}

// validateAnchors enforces the trust-anchor shape: absolute credential-free
// paths, the CA pair set together, and the two anchor modes never mixed (the
// driver rejects the mix too).
func validateAnchors(cfg ConnConfig) error {
	bad := &Error{CodeConnect, "The local SQL connection settings are invalid."}
	if cfg.ServerCertificate != "" && cfg.Certificate != "" {
		return bad
	}
	if cfg.ServerCertificate != "" {
		if !validAnchorPath(cfg.ServerCertificate) || cfg.HostNameInCertificate != "" {
			return bad
		}
		return nil
	}
	if cfg.Certificate == "" && cfg.HostNameInCertificate == "" {
		return nil // no anchor: encrypt=true still fails closed on self-signed
	}
	if cfg.Certificate == "" || cfg.HostNameInCertificate == "" {
		return bad // the CA pair must be set together
	}
	if !validAnchorPath(cfg.Certificate) || !anchorHostRe.MatchString(cfg.HostNameInCertificate) {
		return bad
	}
	return nil
}

// connDSN builds the driver URL: integrated auth (no user/password), encrypt
// on, fixed app name, 5 s connect/dial timeouts. The lpc: hint is dropped: the
// shared-memory dialer is Windows-only and cannot be imported here, so the
// same local host is reached over TCP. An owner-supplied trust anchor is added
// as serverCertificate (byte-pin) or certificate+hostnameincertificate (CA
// chain); trustservercertificate is NEVER set. Test seam.
func connDSN(cfg ConnConfig) (string, error) {
	if err := CheckLocalTarget(cfg.Server); err != nil {
		return "", err
	}
	if err := validateAnchors(cfg); err != nil {
		return "", err
	}
	host, instance, _, _ := splitTarget(cfg.Server)
	if host == "" {
		host = "."
	}
	u := url.URL{Scheme: "sqlserver", Host: host}
	if instance != "" {
		u.Path = "/" + instance
	}
	q := url.Values{}
	q.Set("app name", appName)
	q.Set("encrypt", "true")
	q.Set("connection timeout", "5")
	q.Set("dial timeout", "5")
	if cfg.ServerCertificate != "" {
		q.Set("servercertificate", cfg.ServerCertificate)
	} else if cfg.Certificate != "" {
		q.Set("certificate", cfg.Certificate)
		q.Set("hostnameincertificate", cfg.HostNameInCertificate)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ConnMaxLifetime is the C5 connection lifetime. It is exported because the
// runner has to know when database/sql is allowed to have re-created the
// physical connection on its own: a silent recycle is a new connect, and the
// rights ceiling must be re-proven at a new connect.
const ConnMaxLifetime = time.Hour

// C5: exactly one connection, one idle kept, recycled after one hour.
type poolSettings struct {
	maxOpen, maxIdle int
	maxLifetime      time.Duration
}

var pool = poolSettings{maxOpen: 1, maxIdle: 1, maxLifetime: ConnMaxLifetime}

func applyPool(db *sql.DB, p poolSettings) {
	db.SetMaxOpenConns(p.maxOpen)
	db.SetMaxIdleConns(p.maxIdle)
	db.SetConnMaxLifetime(p.maxLifetime)
}

// openDB is the constructor seam tests use to observe Open's *sql.DB.
var openDB = sql.OpenDB

// lookupIP is the resolver seam for the loopback dial assert (hosts-file
// aware, like the driver's own pre-dial resolution).
var lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// errNotLoopback refuses any dial whose resolved address is not loopback. It
// closes the hosts-file / lpc-to-TCP downgrade gap: CheckLocalTarget gates the
// NAME, this gates the ADDRESS actually dialed after resolution.
var errNotLoopback = errors.New("bbsql: refusing a non-loopback SQL address")

// loopbackDialer is the post-resolution C12 assert in the dial path. The
// driver resolves the host and hands the address here; every resolved IP must
// be loopback or the dial is refused before any TLS handshake or auth. The
// dial is pinned to the resolved loopback IP (no second resolution).
//
// W8-01: when deadline is set, the dialed socket's deadlines are CLAMPED to
// that absolute instant until released is set (the connect finished). The
// driver resets the socket deadline to now+ConnTimeout on every I/O
// (go-mssqldb/net.go); without the clamp a peer supplying partial prelogin or
// TLS bytes could stretch the 5 s connect budget without bound. After the
// connect is released, query I/O uses the driver's normal per-query deadlines.
type loopbackDialer struct {
	deadline time.Time    // absolute connect deadline; zero means no clamp
	released *atomic.Bool // set once the connect lifecycle finished
}

func (d loopbackDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := lookupIP(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errNotLoopback
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return nil, errNotLoopback
		}
	}
	var dl net.Dialer
	c, err := dl.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	if err != nil {
		return nil, err
	}
	if !d.deadline.IsZero() && d.released != nil {
		return clampedConn{Conn: c, dialer: d}, nil
	}
	return c, nil
}

// clampedConn refuses any deadline later than the absolute connect deadline
// while the connect is still in flight. The driver's per-I/O SetDeadline calls
// go through here, so prelogin, the TLS handshake and auth all share ONE
// absolute budget that cannot be extended by partial progress.
type clampedConn struct {
	net.Conn
	dialer loopbackDialer
}

func (c clampedConn) clamp(t time.Time) time.Time {
	if !c.dialer.released.Load() && t.After(c.dialer.deadline) {
		return c.dialer.deadline
	}
	return t
}

func (c clampedConn) SetDeadline(t time.Time) error {
	return c.Conn.SetDeadline(c.clamp(t))
}

func (c clampedConn) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(c.clamp(t))
}

func (c clampedConn) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(c.clamp(t))
}

// ceilingConnector admits EVERY physical connection through the C8 ceiling on
// that exact connection before database/sql may use it (W8-02/W9-02). A pool
// expiry, a bad-connection retry or a reconnect therefore can never run a
// sampler on a generation that has not passed: database/sql calls Connect for
// each new physical connection, and a failed admission closes it and surfaces
// the fixed ceiling error. The connect itself is bounded by the caller's
// context (W8-01): a watchdog returns within budget even though the driver's
// prelogin/TLS handshake ignores contexts, and the clamped dialer guarantees
// the orphaned attempt dies at the absolute deadline and is closed.
type ceilingConnector struct {
	cfg msdsn.Config
	// inner builds the driver connector with the per-connect dialer. Test seam;
	// nil means the real go-mssqldb connector.
	inner func(d mssql.Dialer) driver.Connector
}

func (c ceilingConnector) Driver() driver.Driver { return &mssql.Driver{} }

func (c ceilingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(connectTimeout)
	}
	d := loopbackDialer{deadline: deadline, released: &atomic.Bool{}}
	inner := c.inner
	if inner == nil {
		inner = func(dl mssql.Dialer) driver.Connector {
			mc := mssql.NewConnectorConfig(c.cfg)
			mc.Dialer = dl
			return mc
		}
	}
	conn, err := connectGuarded(ctx, inner(d))
	d.released.Store(true) // the connect lifecycle is over; query I/O is unclamped
	if err != nil {
		return nil, err
	}
	// C8 admission on THIS physical connection, before it is exposed. The
	// guarded wrapper closes the connection if the budget expires mid-proof,
	// so the driver's cancel-drain cannot stretch the connect either.
	g := guardedConn{Conn: conn}
	if err := CheckCeiling(ctx, pinnedQueryer{conn: g}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return g, nil
}

// connectGuarded runs the driver's Connect under a hard watchdog: the caller
// returns within the context budget even when the driver is stuck in a
// context-less prelogin read or TLS handshake. An orphaned attempt is closed
// when it finally answers (its socket is already clamped to the deadline, so
// it cannot linger past it by more than one I/O step).
func connectGuarded(ctx context.Context, inner driver.Connector) (driver.Conn, error) {
	type result struct {
		conn driver.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := inner.Connect(ctx)
		ch <- result{conn, err}
	}()
	if ctx.Done() == nil {
		r := <-ch
		return r.conn, r.err
	}
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-ctx.Done():
		go func() {
			r := <-ch
			if r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// guardedConn forwards the driver connection but watches every statement's
// context: when the budget expires it CLOSES the physical connection, which
// aborts the driver's attention/cancel-drain (go-mssqldb drains with fresh
// background timeouts otherwise) and invalidates the session. database/sql
// discards the closed connection (IsValid) and the next use goes through
// ceilingConnector.Connect again — a new generation, a new proof. It does NOT
// forward driver.Pinger: a Ping would send the driver's untagged `select 1;`
// outside the allowlist (W8-06).
type guardedConn struct{ driver.Conn }

func (g guardedConn) Prepare(query string) (driver.Stmt, error) {
	s, err := g.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return guardedStmt{Stmt: s, conn: g.Conn}, nil
}

func (g guardedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var s driver.Stmt
	var err error
	if pc, ok := g.Conn.(driver.ConnPrepareContext); ok {
		s, err = pc.PrepareContext(ctx, query)
	} else {
		s, err = g.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return guardedStmt{Stmt: s, conn: g.Conn}, nil
}

func (g guardedConn) ResetSession(ctx context.Context) error {
	if sr, ok := g.Conn.(driver.SessionResetter); ok {
		return sr.ResetSession(ctx)
	}
	return nil
}

func (g guardedConn) IsValid() bool {
	if v, ok := g.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// guardedStmt closes the physical connection when the statement's context is
// cancelled, so cancellation is a hard return bound, not a cooperative one.
type guardedStmt struct {
	driver.Stmt
	conn driver.Conn
}

func (s guardedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, &Error{CodeQuery, "The query could not be run."}
	}
	stop := watchBudget(ctx, s.conn)
	rows, err := qc.QueryContext(ctx, args)
	stop()
	if ctx.Err() != nil {
		// Deterministic invalidation within budget: a cancelled query never
		// leaves a reusable physical connection behind (W8-01/W8-02).
		_ = s.conn.Close()
	}
	return rows, err
}

func (s guardedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, &Error{CodeQuery, "The query could not be run."}
	}
	stop := watchBudget(ctx, s.conn)
	res, err := ec.ExecContext(ctx, args)
	stop()
	if ctx.Err() != nil {
		_ = s.conn.Close()
	}
	return res, err
}

// watchBudget closes conn when ctx is done. The returned stop ends the watch;
// a close that races a finished statement only retires the connection early,
// which is conservative (the next use re-proves the ceiling). Even when stop
// wins the select, a cancelled context still invalidates the connection, so
// cancellation is a hard guarantee, not a race (W8-01).
func watchBudget(ctx context.Context, conn driver.Conn) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
			if ctx.Err() != nil {
				_ = conn.Close()
			}
		}
	}()
	return func() { close(done) }
}

// pinnedQueryer sends the ceiling controls on ONE physical driver connection,
// so the proof and every later sample refer to the same established session
// (W8-02). go-mssqldb's Prepare is a thin wrapper: a parameterless statement
// is sent as the exact SQLBatch text, so nothing outside the allowlist reaches
// the wire.
type pinnedQueryer struct{ conn driver.Conn }

func (p pinnedQueryer) Query(ctx context.Context, text string) ([]Row, error) {
	if !IsAllowed(text) {
		return nil, errNotAllowed
	}
	var stmt driver.Stmt
	var err error
	if pc, ok := p.conn.(driver.ConnPrepareContext); ok {
		stmt, err = pc.PrepareContext(ctx, text)
	} else {
		stmt, err = p.conn.Prepare(text)
	}
	if err != nil {
		return nil, fixedQueryError(ctx, err)
	}
	defer stmt.Close()
	qc, ok := stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, &Error{CodeQuery, "The query result could not be read."}
	}
	rows, err := qc.QueryContext(ctx, nil)
	if err != nil {
		return nil, fixedQueryError(ctx, err)
	}
	defer rows.Close()
	cols := rows.Columns()
	limit := statementCap(text) + 1
	var out []Row
	dest := make([]driver.Value, len(cols))
	for len(out) < limit {
		if err := rows.Next(dest); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fixedQueryError(ctx, err)
		}
		r := Row{}
		for i, c := range cols {
			v := dest[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			r[c] = v
		}
		out = append(out, r)
	}
	return out, nil
}

// Open checks C12 first and opens one integrated-auth connection under the
// default connect budget. It is the context-free compatibility form; the
// runner's context-aware seam is OpenContext / DefaultConnectContext.
func Open(cfg ConnConfig) (*sql.DB, error) {
	return OpenContext(context.Background(), cfg)
}

// OpenContext opens the single local SQL connection under the caller's
// context, bounded by the C4 5 s connect budget. Certificate loading, dial,
// prelogin/TLS and auth all share that one absolute budget, and the first
// physical connection is admitted through the C8 ceiling before OpenContext
// returns. There is NO driver Ping (W8-06): db.Conn acquires the connection
// without sending any SQL of its own, so the startup transcript contains only
// the approved, tagged ceiling controls. Failures are fixed-code errors; a
// failed ceiling proof keeps its C8 origin (W9-03).
func OpenContext(ctx context.Context, cfg ConnConfig) (*sql.DB, error) {
	if err := CheckLocalTarget(cfg.Server); err != nil {
		return nil, err
	}
	dsn, err := connDSN(cfg)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	// msdsn.Parse reads the owner's trust-anchor files; bound that loading too
	// (a stalled certificate read must not outlive the connect budget). The
	// abandoned parse goroutine finishes on its own; it holds no connection.
	type parsed struct {
		cfg msdsn.Config
		err error
	}
	ch := make(chan parsed, 1)
	go func() {
		mc, perr := msdsn.Parse(dsn)
		ch <- parsed{mc, perr}
	}()
	var mcfg msdsn.Config
	select {
	case p := <-ch:
		if p.err != nil {
			return nil, &Error{CodeConnect, "The local SQL connection settings are invalid."}
		}
		mcfg = p.cfg
	case <-cctx.Done():
		return nil, classified(CodeConnectTimeout)
	}
	db := openDB(ceilingConnector{cfg: mcfg})
	applyPool(db, pool)
	// Acquire the physical connection WITHOUT a Ping: Connect runs the ceiling
	// admission on it, and closing the handle returns it to the idle pool for
	// the runner's first query (C5: one connection).
	conn, err := db.Conn(cctx)
	if err != nil {
		db.Close()
		// C7 needs this distinction: a refused login or a missing grant is
		// terminal for the service (auto-off, owner-only re-enable), while an
		// instance that is simply down or slow must only back off. A failed C8
		// proof keeps its origin so the runner latches at once (W9-03).
		if IsCeilingFailure(err) {
			return nil, err
		}
		if e := Classify(err); e.Code != CodeFailed {
			return nil, e
		}
		return nil, &Error{CodeConnect, "Could not connect to the local SQL Server."}
	}
	_ = conn.Close()
	return db, nil
}

// ConnectFuncContext is the context-aware connect seam (W8-01): the runner
// threads its tick/service context through connect so service Stop reaches an
// in-progress connect. DefaultConnectContext is the production implementation;
// tests inject a fake. The runner-side wiring (Runner.Connect / reconnect)
// belongs to the runner owner.
type ConnectFuncContext func(ctx context.Context, cfg ConnConfig) (Queryer, func() error, error)

// DefaultConnectContext opens the ceiling-admitted connection under ctx and
// hands back the send seam with its closer.
func DefaultConnectContext(ctx context.Context, cfg ConnConfig) (Queryer, func() error, error) {
	db, err := OpenContext(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return DBQueryer{DB: db}, db.Close, nil
}

// Row is one result row: column name to driver value ([]byte becomes string).
type Row map[string]any

// Queryer is the send seam: samplers and the runner test against a fake.
type Queryer interface {
	Query(ctx context.Context, text string) ([]Row, error)
}

// DBQueryer is the real Queryer over the single *sql.DB; it re-checks the
// allowlist so no path can send unlisted text. It reads at most the C6 cap+1
// rows for the statement (MaxRows overrides when > 0) and closes the result
// early: the reader stops at the boundary instead of materializing every row.
type DBQueryer struct {
	DB      *sql.DB
	MaxRows int
}

func (d DBQueryer) Query(ctx context.Context, text string) ([]Row, error) {
	if !IsAllowed(text) {
		return nil, errNotAllowed
	}
	limit := d.MaxRows
	if limit <= 0 {
		limit = statementCap(text) + 1 // C6: stop at cap+1, mark truncated later
	}
	rows, err := d.DB.QueryContext(ctx, text)
	if err != nil {
		return nil, fixedQueryError(ctx, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, fixedQueryError(ctx, err)
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, &Error{CodeQuery, "The query result could not be read."}
		}
		r := Row{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			r[c] = vals[i]
		}
		out = append(out, r)
		if len(out) >= limit {
			break // C6 boundary: never read past cap+1
		}
	}
	rows.Close() // early close: the rest of the result set is not fetched
	if err := rows.Err(); err != nil {
		return nil, fixedQueryError(ctx, err)
	}
	return out, nil
}

// fixedQueryError maps a driver failure to a fixed code, never driver text.
// The classification runs HERE, at the seam, because C7 needs the difference
// between "the login lacks a grant" and "the server is down": a permission or
// login failure goes straight to auto-off, while an ordinary query failure
// backs off and retries. Classify returns only fixed sentences, so C10 holds.
// An unclassifiable driver error keeps the generic query code.
func fixedQueryError(ctx context.Context, err error) error {
	// A failed C8 proof keeps its origin and its fixed code/sentence; the
	// runner latches on it (W9-03). Never flatten it into a generic query code.
	if IsCeilingFailure(err) {
		return err
	}
	if ctx.Err() != nil {
		return &Error{CodeTimeout, "The query did not finish within its time limit."}
	}
	if e := Classify(err); e.Code != CodeFailed {
		return e
	}
	return &Error{CodeQuery, "The query could not be run."}
}

// Query is the only send path: refuse non-allowlisted text (C1), apply the
// per-query C4 timeout. The runner owns the whole-tick budget and
// skip-if-running; no goroutines or tickers here.
func Query(ctx context.Context, q Queryer, text string, timeout time.Duration) ([]Row, error) {
	if !IsAllowed(text) {
		return nil, errNotAllowed
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rows, err := q.Query(qctx, text)
	if err != nil {
		// Keep a failed C8 proof's origin intact (W9-03); errors.As below would
		// otherwise unwrap it to the bare fixed error and lose the latch signal.
		if IsCeilingFailure(err) {
			return nil, err
		}
		var e *Error
		if errors.As(err, &e) {
			return nil, e
		}
		return nil, fixedQueryError(qctx, err)
	}
	// C6 defense in depth: even a Queryer that over-returns is capped at
	// cap+1 here; DBQueryer already stops at the boundary itself.
	if limit := statementCap(text) + 1; len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}