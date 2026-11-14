// conn.go opens the single local SQL connection (C4 timeouts, C5 one
// connection, C12 local-only) and gates every send through the allowlist (C1).
// Integrated auth only: no password is accepted or stored. Errors carry fixed
// codes and sentences, never driver text.
package bbsql

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
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

// ConnConfig: the local target plus the per-query budget (3 s / 5 s) the
// runner passes to Query. The optional trust anchors let the OWNER supply the
// local instance's certificate so encrypt=true can verify it; with no anchor
// the default self-signed cert still fails closed (connect-failed).
type ConnConfig struct {
	Server       string
	QueryTimeout time.Duration
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

// C5: exactly one connection, one idle kept, recycled after one hour.
type poolSettings struct {
	maxOpen, maxIdle int
	maxLifetime      time.Duration
}

var pool = poolSettings{maxOpen: 1, maxIdle: 1, maxLifetime: time.Hour}

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
type loopbackDialer struct{}

func (loopbackDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
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
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// Open checks C12 first, opens one integrated-auth connection and proves it
// with a 5 s ping. Failures are fixed-code errors.
func Open(cfg ConnConfig) (*sql.DB, error) {
	if err := CheckLocalTarget(cfg.Server); err != nil {
		return nil, err
	}
	dsn, err := connDSN(cfg)
	if err != nil {
		return nil, err
	}
	mcfg, err := msdsn.Parse(dsn)
	if err != nil {
		return nil, &Error{CodeConnect, "The local SQL connection settings are invalid."}
	}
	connector := mssql.NewConnectorConfig(mcfg)
	connector.Dialer = loopbackDialer{}
	db := openDB(connector)
	applyPool(db, pool)
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, &Error{CodeConnect, "Could not connect to the local SQL Server."}
	}
	return db, nil
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
		return nil, fixedQueryError(ctx)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, &Error{CodeQuery, "The query result could not be read."}
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
		return nil, fixedQueryError(ctx)
	}
	return out, nil
}

// fixedQueryError maps a driver failure to a fixed code, never driver text.
func fixedQueryError(ctx context.Context) error {
	if ctx.Err() != nil {
		return &Error{CodeTimeout, "The query did not finish within its time limit."}
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
		var e *Error
		if errors.As(err, &e) {
			return nil, e
		}
		return nil, fixedQueryError(qctx)
	}
	// C6 defense in depth: even a Queryer that over-returns is capped at
	// cap+1 here; DBQueryer already stops at the boundary itself.
	if limit := statementCap(text) + 1; len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}