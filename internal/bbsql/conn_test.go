package bbsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/go-mssqldb/msdsn"
)

// TestCheckLocalTarget proves C12: only the local machine (optionally with an
// instance, optionally lpc:) is accepted; everything else is "remote-target".
func TestCheckLocalTarget(t *testing.T) {
	accept := []string{
		".", "(local)", "localhost", "LOCALHOST", "LocalHost", "127.0.0.1", "::1",
		"lpc:", "lpc:.", "lpc:(local)", "lpc:localhost", "LPC:127.0.0.1", "lpc:::1",
		`.\SQL2022`, `(local)\INST`, `localhost\SQLEXPRESS`, `127.0.0.1\inst`, `::1\inst`,
		`lpc:.\SQL2022`, `LPC:LOCALHOST\INST`, "  localhost  ",
	}
	for _, s := range accept {
		if err := CheckLocalTarget(s); err != nil {
			t.Errorf("CheckLocalTarget(%q) = %v; want accept", s, err)
		}
	}
	reject := []string{
		"sqlserver.prod", "10.0.0.5", "host.database.windows.net",
		"", "   ", "127.0.0.2", "::2", "0.0.0.0", "localhost.evil.com",
		"tcp:localhost", "np:.", "admin:.", "lpc:sqlserver.prod",
		"localhost;encrypt=false", "127.0.0.1,1433", "localhost:1433",
		`.\`, `.\a\b`, `.\inst;foo`, `.\inst?encrypt=false`, `.\in'st`, `lpc:\inst`,
		"local host", "[::1]",
	}
	for _, s := range reject {
		err := CheckLocalTarget(s)
		if err == nil {
			t.Errorf("CheckLocalTarget(%q) accepted a non-local target", s)
			continue
		}
		var e *Error
		if !errors.As(err, &e) || e.Code != "remote-target" {
			t.Errorf("CheckLocalTarget(%q) = %v; want fixed code remote-target", s, err)
		}
	}
}

// --- stub driver: counts real connection attempts, never touches a network ---

type stubConnector struct {
	connects *int32
	pingErr  error
}

func (c stubConnector) Connect(context.Context) (driver.Conn, error) {
	atomic.AddInt32(c.connects, 1)
	return stubConn{pingErr: c.pingErr}, nil
}
func (c stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return nil, errors.New("stub") }

type stubConn struct{ pingErr error }

func (stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("stub") }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return nil, errors.New("stub") }
func (c stubConn) Ping(context.Context) error        { return c.pingErr }

// TestOpenOneConnection proves C5 (max open 1, max idle 1, lifetime 1 h) via
// the constructor seam, that Open pings exactly one connection, that a remote
// target is rejected BEFORE any connect, and that ping failures stay fixed-code.
func TestOpenOneConnection(t *testing.T) {
	if pool != (poolSettings{maxOpen: 1, maxIdle: 1, maxLifetime: time.Hour}) {
		t.Fatalf("C5 pool settings = %+v; want {1 1 1h}", pool)
	}
	var connects int32
	var opened bool
	old := openDB
	openDB = func(driver.Connector) *sql.DB {
		opened = true
		return sql.OpenDB(stubConnector{connects: &connects})
	}
	defer func() { openDB = old }()

	db, err := Open(ConnConfig{Server: `.\SQL2022`, QueryTimeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("Open local: %v", err)
	}
	defer db.Close()
	if !opened {
		t.Fatal("Open did not use the constructor seam")
	}
	if n := db.Stats().MaxOpenConnections; n != 1 {
		t.Errorf("MaxOpenConns = %d; want 1", n)
	}
	if n := atomic.LoadInt32(&connects); n != 1 {
		t.Errorf("connections opened = %d; want exactly 1 (the ping)", n)
	}

	// applyPool really applies the limit to a *sql.DB.
	probe := sql.OpenDB(stubConnector{connects: new(int32)})
	defer probe.Close()
	applyPool(probe, pool)
	if n := probe.Stats().MaxOpenConnections; n != 1 {
		t.Errorf("applyPool MaxOpenConns = %d; want 1", n)
	}

	// C12: remote rejected before the connector is ever built.
	opened = false
	_, err = Open(ConnConfig{Server: "sqlserver.prod"})
	var e *Error
	if !errors.As(err, &e) || e.Code != "remote-target" {
		t.Fatalf("Open remote = %v; want remote-target", err)
	}
	if opened || atomic.LoadInt32(&connects) != 1 {
		t.Fatal("a remote target reached the connector")
	}

	// Ping failure: fixed code, never the driver's text.
	openDB = func(driver.Connector) *sql.DB {
		return sql.OpenDB(stubConnector{connects: new(int32), pingErr: errors.New("driver-secret-text")})
	}
	_, err = Open(ConnConfig{Server: "localhost"})
	if !errors.As(err, &e) || e.Code != "connect-failed" {
		t.Fatalf("Open ping failure = %v; want connect-failed", err)
	}
	if strings.Contains(err.Error(), "driver-secret-text") {
		t.Fatal("driver error text leaked")
	}
}

// recQueryer records every text sent to it (the fake Queryer).
type recQueryer struct{ sent []string }

func (r *recQueryer) Query(_ context.Context, text string) ([]Row, error) {
	r.sent = append(r.sent, text)
	return []Row{{"ok": int64(1)}}, nil
}

// TestQueryRefusesNonAllowlisted proves C1: Query never sends text outside the
// allowlist — the fake records nothing — and allowlisted text passes untouched.
func TestQueryRefusesNonAllowlisted(t *testing.T) {
	q := &recQueryer{}
	bad := []string{"", "SELECT 1", "EXEC sp_who", "DROP TABLE x", Statements["bb:waits"] + " ", " " + Statements["bb:waits"]}
	for _, text := range bad {
		rows, err := Query(context.Background(), q, text, 3*time.Second)
		var e *Error
		if rows != nil || !errors.As(err, &e) || e.Code != "not-allowlisted" {
			t.Fatalf("Query(%q) = (%v, %v); want not-allowlisted refusal", text, rows, err)
		}
	}
	if len(q.sent) != 0 {
		t.Fatalf("non-allowlisted text was sent: %v", q.sent)
	}
	text := Statements["bb:schedulers"]
	rows, err := Query(context.Background(), q, text, 3*time.Second)
	if err != nil || len(rows) != 1 {
		t.Fatalf("allowlisted query failed: %v", err)
	}
	if len(q.sent) != 1 || q.sent[0] != text {
		t.Fatalf("sent text altered: %v", q.sent)
	}
	// The real wrapper gates too: a nil DB would panic if text were sent.
	if _, err := (DBQueryer{}).Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("DBQueryer sent non-allowlisted text")
	}
}

// slowQueryer blocks until its context is cancelled, like a stuck query.
type slowQueryer struct{ cancelled bool }

func (s *slowQueryer) Query(ctx context.Context, _ string) ([]Row, error) {
	<-ctx.Done()
	s.cancelled = ctx.Err() == context.DeadlineExceeded
	return nil, ctx.Err()
}

// TestQueryTimeout proves C4: the per-query deadline cancels a slow query and
// the failure is the fixed timeout code; a fast query returns its rows.
func TestQueryTimeout(t *testing.T) {
	slow := &slowQueryer{}
	start := time.Now()
	_, err := Query(context.Background(), slow, Statements["bb:requests"], 50*time.Millisecond)
	var e *Error
	if !errors.As(err, &e) || e.Code != "query-timeout" {
		t.Fatalf("slow query = %v; want query-timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the timeout was not enforced")
	}
	if !slow.cancelled {
		t.Fatal("the fake query was not cancelled by a context deadline")
	}
	rows, err := Query(context.Background(), &recQueryer{}, Statements["bb:waits"], 3*time.Second)
	if err != nil || len(rows) != 1 {
		t.Fatalf("fast query within timeout failed: %v", err)
	}
}

// TestAppNameAndEncrypt proves the built DSN carries the C5 app name, encrypt
// on, the C4 5 s timeouts, and NO credentials, and that the driver parses it.
func TestAppNameAndEncrypt(t *testing.T) {
	for _, server := range []string{".", "(local)", "localhost", "127.0.0.1", "::1", `.\SQL2022`, "lpc:."} {
		dsn, err := connDSN(server)
		if err != nil {
			t.Fatalf("connDSN(%q): %v", server, err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("DSN for %q does not parse: %v", server, err)
		}
		q := u.Query()
		if got := q.Get("app name"); got != "Unravis Blackbox SQL" {
			t.Errorf("%q: app name = %q", server, got)
		}
		if got := q.Get("encrypt"); got != "true" {
			t.Errorf("%q: encrypt = %q; want true", server, got)
		}
		if q.Get("connection timeout") != "5" || q.Get("dial timeout") != "5" {
			t.Errorf("%q: connect/dial timeouts not 5 s: %v", server, q)
		}
		if u.User != nil {
			t.Errorf("%q: DSN carries credentials", server)
		}
		for k := range q {
			if strings.Contains(k, "password") || k == "user id" {
				t.Errorf("%q: DSN carries a login parameter %q", server, k)
			}
		}
		cfg, err := msdsn.Parse(dsn)
		if err != nil {
			t.Fatalf("driver cannot parse DSN for %q: %v", server, err)
		}
		if cfg.Encryption != msdsn.EncryptionRequired {
			t.Errorf("%q: driver encryption = %v; want required", server, cfg.Encryption)
		}
		if cfg.AppName != "Unravis Blackbox SQL" || cfg.User != "" || cfg.Password != "" {
			t.Errorf("%q: driver config app/user/password wrong", server)
		}
		if cfg.DialTimeout != 5*time.Second || cfg.ConnTimeout != 5*time.Second {
			t.Errorf("%q: driver timeouts = %v/%v; want 5s/5s", server, cfg.DialTimeout, cfg.ConnTimeout)
		}
	}
	dsn, err := connDSN(`localhost\SQLEXPRESS`)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Path != "/SQLEXPRESS" {
		t.Fatalf("instance not in DSN path: %q (%v)", dsn, err)
	}
	if _, err := connDSN("sqlserver.prod"); err == nil {
		t.Fatal("connDSN built a DSN for a remote target")
	}
}