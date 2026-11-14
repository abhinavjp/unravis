package bbsql

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"database/sql/driver"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// writeTestCert creates a real self-signed PEM the driver can read at parse
// time (msdsn.Parse loads the anchor file), and returns its absolute path.
func writeTestCert(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDSNTrustAnchor proves the owner-supplied trust anchor reaches the DSN:
// serverCertificate byte-pin OR certificate+hostnameincertificate CA chain,
// encrypt stays true, trustservercertificate is NEVER set, and bad anchors
// (relative paths, credentials, half pairs) fail closed.
func TestDSNTrustAnchor(t *testing.T) {
	certPath := writeTestCert(t)

	t.Run("byte-pin", func(t *testing.T) {
		cfg := ConnConfig{Server: "localhost", ServerCertificate: certPath}
		dsn, err := connDSN(cfg)
		if err != nil {
			t.Fatalf("connDSN: %v", err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if got := q.Get("servercertificate"); got != certPath {
			t.Errorf("servercertificate = %q; want %q", got, certPath)
		}
		if q.Get("encrypt") != "true" {
			t.Errorf("encrypt = %q; want true", q.Get("encrypt"))
		}
		if q.Get("trustservercertificate") != "" || strings.Contains(strings.ToLower(dsn), "trustservercertificate") {
			t.Error("trustservercertificate must never be set")
		}
		if q.Get("certificate") != "" || q.Get("hostnameincertificate") != "" {
			t.Error("byte-pin mode must not carry the CA pair")
		}
		mc, err := msdsn.Parse(dsn)
		if err != nil {
			t.Fatalf("driver cannot parse anchored DSN: %v", err)
		}
		if mc.Encryption != msdsn.EncryptionRequired {
			t.Errorf("encryption = %v; want required", mc.Encryption)
		}
		if mc.TLSConfig == nil || mc.TLSConfig.VerifyPeerCertificate == nil {
			t.Error("byte-pin callback not installed by the driver")
		}
	})

	t.Run("ca-chain", func(t *testing.T) {
		cfg := ConnConfig{Server: `.\SQL2022`, Certificate: certPath, HostNameInCertificate: "sql.local"}
		dsn, err := connDSN(cfg)
		if err != nil {
			t.Fatalf("connDSN: %v", err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if q.Get("certificate") != certPath || q.Get("hostnameincertificate") != "sql.local" {
			t.Errorf("CA pair missing: %v", q)
		}
		if q.Get("encrypt") != "true" {
			t.Errorf("encrypt = %q; want true", q.Get("encrypt"))
		}
		if strings.Contains(strings.ToLower(dsn), "trustservercertificate") {
			t.Error("trustservercertificate must never be set")
		}
		if q.Get("servercertificate") != "" {
			t.Error("CA mode must not carry servercertificate")
		}
		mc, err := msdsn.Parse(dsn)
		if err != nil {
			t.Fatalf("driver cannot parse anchored DSN: %v", err)
		}
		if mc.TLSConfig == nil || mc.TLSConfig.RootCAs == nil {
			t.Error("CA chain roots not installed by the driver")
		}
		if mc.TLSConfig.InsecureSkipVerify {
			t.Error("CA mode must verify the chain (InsecureSkipVerify=false)")
		}
		if mc.TLSConfig.ServerName != "sql.local" {
			t.Errorf("ServerName = %q; want sql.local", mc.TLSConfig.ServerName)
		}
	})

	t.Run("no-anchor-fails-closed", func(t *testing.T) {
		dsn, err := connDSN(ConnConfig{Server: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		for _, k := range []string{"servercertificate", "certificate", "hostnameincertificate", "trustservercertificate"} {
			if q.Get(k) != "" {
				t.Errorf("no-anchor DSN carries %q", k)
			}
		}
		if q.Get("encrypt") != "true" {
			t.Errorf("encrypt = %q; want true", q.Get("encrypt"))
		}
		mc, err := msdsn.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		// Default local self-signed cert: verify on, no roots, no skip —
		// the handshake fails closed until the owner supplies an anchor.
		if mc.TLSConfig == nil || mc.TLSConfig.InsecureSkipVerify || mc.TLSConfig.RootCAs != nil {
			t.Error("no-anchor TLS must verify with no trust anchor")
		}
	})

	t.Run("bad-anchors-refused", func(t *testing.T) {
		bad := []ConnConfig{
			{Server: "localhost", ServerCertificate: "cert.pem"},                        // relative
			{Server: "localhost", ServerCertificate: filepath.Join(t.TempDir(), "c.txt")}, // wrong ext
			{Server: "localhost", ServerCertificate: certPath + ";password=x"},          // credentials
			{Server: "localhost", ServerCertificate: certPath + "user id=sa"},           // credentials
			{Server: "localhost", Certificate: "ca.pem", HostNameInCertificate: "sql.local"}, // relative CA
			{Server: "localhost", Certificate: certPath},                                // half pair
			{Server: "localhost", HostNameInCertificate: "sql.local"},                   // half pair
			{Server: "localhost", Certificate: certPath, HostNameInCertificate: "sa:pw@host"}, // credential host
			{Server: "localhost", Certificate: certPath, HostNameInCertificate: "bad name"},   // not a host
			{Server: "localhost", ServerCertificate: certPath, Certificate: certPath, HostNameInCertificate: "sql.local"}, // mixed
			{Server: "localhost", ServerCertificate: certPath, HostNameInCertificate: "sql.local"}, // mixed
		}
		for i, cfg := range bad {
			if _, err := connDSN(cfg); err == nil {
				t.Errorf("case %d: bad anchor accepted: %+v", i, cfg)
			} else {
				var e *Error
				if !errors.As(err, &e) || e.Code != CodeConnect {
					t.Errorf("case %d: err = %v; want fixed connect code", i, err)
				}
			}
		}
	})
}

// TestLoopbackDialer proves the post-resolution C12 assert in the dial path:
// any non-loopback address (including a hosts-file downgrade of localhost) is
// refused before TLS/auth, loopback dials proceed, and Open installs it.
func TestLoopbackDialer(t *testing.T) {
	oldLookup := lookupIP
	defer func() { lookupIP = oldLookup }()

	d := loopbackDialer{}
	ctx := context.Background()

	// A real loopback listener proves the accept path end to end.
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
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// Non-loopback IPs are refused outright (hosts-file / lpc->TCP downgrade).
	for _, addr := range []string{"10.0.0.5:" + port, "192.168.1.9:" + port, "[::2]:" + port} {
		if _, err := d.DialContext(ctx, "tcp", addr); !errors.Is(err, errNotLoopback) {
			t.Errorf("DialContext(%q) = %v; want errNotLoopback", addr, err)
		}
	}

	// A hostname that resolves to a remote address is refused post-resolution.
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	}
	if _, err := d.DialContext(ctx, "tcp", "localhost:"+port); !errors.Is(err, errNotLoopback) {
		t.Errorf("downgraded localhost = %v; want errNotLoopback", err)
	}

	// Mixed resolution (loopback + remote) is refused too: no partial trust.
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("10.0.0.5")}}, nil
	}
	if _, err := d.DialContext(ctx, "tcp", "localhost:"+port); !errors.Is(err, errNotLoopback) {
		t.Errorf("mixed resolution = %v; want errNotLoopback", err)
	}

	// All-loopback resolution dials the pinned loopback address.
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	conn, err := d.DialContext(ctx, "tcp", "localhost:"+port)
	if err != nil {
		t.Fatalf("loopback dial refused: %v", err)
	}
	conn.Close()

	// Bare loopback IPs (v4 and v6) pass the assert.
	if conn, err := d.DialContext(ctx, "tcp", "127.0.0.1:"+port); err != nil {
		t.Fatalf("127.0.0.1 dial: %v", err)
	} else {
		conn.Close()
	}
	if _, err := d.DialContext(ctx, "tcp", "[::1]:"+port); errors.Is(err, errNotLoopback) {
		t.Error("::1 must pass the loopback assert")
	}

	// Open installs the loopback dialer on the real connector.
	oldOpen := openDB
	var captured driver.Connector
	openDB = func(c driver.Connector) *sql.DB {
		captured = c
		return sql.OpenDB(stubConnector{connects: new(int32)})
	}
	defer func() { openDB = oldOpen }()
	db, err := Open(ConnConfig{Server: "localhost"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()
	mc, ok := captured.(*mssql.Connector)
	if !ok {
		t.Fatalf("Open connector = %T; want *mssql.Connector", captured)
	}
	if _, ok := mc.Dialer.(loopbackDialer); !ok {
		t.Fatalf("Open dialer = %T; want loopbackDialer", mc.Dialer)
	}
}

// --- fake driver for the reader-cap test: more rows than any C6 cap ---

type capDriver struct{}

func (capDriver) Open(string) (driver.Conn, error) { return nil, errors.New("stub") }

type capConnector struct {
	n      int
	reads  *int
	closed *bool
}

func (c capConnector) Connect(context.Context) (driver.Conn, error) {
	return capConn{c.n, c.reads, c.closed}, nil
}
func (c capConnector) Driver() driver.Driver { return capDriver{} }

type capConn struct {
	n      int
	reads  *int
	closed *bool
}

func (c capConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("stub") }
func (c capConn) Close() error                        { return nil }
func (c capConn) Begin() (driver.Tx, error)           { return nil, errors.New("stub") }
func (c capConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &capRows{n: c.n, reads: c.reads, closed: c.closed}, nil
}

type capRows struct {
	n, i   int
	reads  *int
	closed *bool
}

func (r *capRows) Columns() []string { return []string{"current_tasks", "runnable_tasks"} }
func (r *capRows) Close() error      { *r.closed = true; return nil }
func (r *capRows) Next(dest []driver.Value) error {
	if r.i >= r.n {
		return io.EOF
	}
	r.i++
	*r.reads++
	dest[0] = int64(r.i)
	dest[1] = int64(r.i)
	return nil
}

// TestReaderCap proves C6 at the reader boundary: the fake driver offers far
// more rows than the cap, but DBQueryer reads exactly cap+1, closes early, and
// never pulls the rest. MaxRows overrides; the send path caps over-returning
// Queryers too.
func TestReaderCap(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		text string
		cap  int
	}{
		{Statements["bb:schedulers"], CapSchedulers},
		{Statements["bb:requests"], CapRequests},
		{Statements["bb:ag"], CapAG},
	} {
		reads, closed := 0, false
		db := sql.OpenDB(capConnector{n: tc.cap + 100, reads: &reads, closed: &closed})
		rows, err := DBQueryer{DB: db}.Query(ctx, tc.text)
		db.Close()
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(rows) != tc.cap+1 {
			t.Errorf("read %d rows; want cap+1 = %d", len(rows), tc.cap+1)
		}
		if reads != tc.cap+1 {
			t.Errorf("driver Next called %d times; want exactly %d (stop at the boundary)", reads, tc.cap+1)
		}
		if !closed {
			t.Error("result set was not closed early at the cap")
		}
	}

	// MaxRows override is honored.
	reads, closed := 0, false
	db := sql.OpenDB(capConnector{n: 50, reads: &reads, closed: &closed})
	rows, err := DBQueryer{DB: db, MaxRows: 5}.Query(ctx, Statements["bb:schedulers"])
	db.Close()
	if err != nil || len(rows) != 5 || reads != 5 {
		t.Fatalf("MaxRows=5: rows=%d reads=%d err=%v; want 5/5/nil", len(rows), reads, err)
	}

	// The send path caps a Queryer that over-returns (defense in depth).
	over := overQueryer{n: CapSchedulers + 9}
	rows, err = Query(ctx, over, Statements["bb:schedulers"], time.Second)
	if err != nil || len(rows) != CapSchedulers+1 {
		t.Fatalf("send-path cap: rows=%d err=%v; want %d", len(rows), err, CapSchedulers+1)
	}
}

// overQueryer returns more rows than the C6 cap allows.
type overQueryer struct{ n int }

func (o overQueryer) Query(context.Context, string) ([]Row, error) {
	rows := make([]Row, o.n)
	for i := range rows {
		rows[i] = Row{"current_tasks": int64(i), "runnable_tasks": int64(i)}
	}
	return rows, nil
}