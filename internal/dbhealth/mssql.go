package dbhealth

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// SQLTarget is one SQL Server login. Auth is "sql" (user and password) or "windows" (the account this program runs as).
type SQLTarget struct {
	Server, Auth, Username, Password, Database string
	Encrypt                                    string // mandatory | optional | strict
	TrustServerCertificate                     bool
}

// ConnectError carries only a fixed, plain sentence. The driver's own message can hold the host, the login and parts of the connection string,
// so it is never passed on.
type ConnectError struct{ Code, Message string }

func (e *ConnectError) Error() string { return e.Message }

const connectTimeoutSec = 8

// ConnectionURL builds the driver URL. A named instance ("host\\inst") goes in the path so the driver asks the browser service for the port;
// "host,1433" and "host:1433" become host:port. Windows sign-in leaves the user out, which makes the driver use the current Windows account.
func ConnectionURL(t SQLTarget) (string, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(t.Server), "tcp:"), "TCP:")
	if host == "" {
		return "", &ConnectError{"invalid", "Enter the server name."}
	}
	instance, port := "", ""
	if i := strings.Index(host, `\`); i >= 0 {
		instance = host[i+1:]
		host = host[:i]
		if j := strings.LastIndexAny(instance, ",:"); j >= 0 {
			instance, port = instance[:j], instance[j+1:]
		}
	} else if i := strings.LastIndexAny(host, ",:"); i >= 0 && !strings.HasSuffix(host, "]") && (host[i] == ',' || strings.Count(host, ":") == 1) {
		host, port = host[:i], host[i+1:]
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || strings.ContainsAny(host, "/;=@'\" \t\r\n") || strings.ContainsAny(instance, "/;=@'\" \t\r\n") {
		return "", &ConnectError{"invalid", "The server name has characters that are not allowed."}
	}
	if port != "" {
		n, err := strconv.Atoi(strings.TrimSpace(port))
		if err != nil || n < 1 || n > 65535 {
			return "", &ConnectError{"invalid", "The port is not a number between 1 and 65535."}
		}
		port = strconv.Itoa(n)
		// A named instance with the default port saved alongside it means "no port was typed": let the instance lookup choose.
		if instance != "" && port == "1433" {
			port = ""
		}
	}
	u := url.URL{Scheme: "sqlserver", Host: host}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	if instance != "" {
		u.Path = "/" + instance
	}
	switch t.Auth {
	case "sql":
		if t.Username == "" {
			return "", &ConnectError{"invalid", "Enter the SQL login name."}
		}
		u.User = url.UserPassword(t.Username, t.Password)
	case "windows":
	default:
		return "", &ConnectError{"invalid", "Pick how to sign in."}
	}
	q := url.Values{}
	if t.Database != "" {
		q.Set("database", t.Database)
	}
	switch t.Encrypt {
	case "optional":
		q.Set("encrypt", "false")
	case "strict":
		q.Set("encrypt", "strict")
	default:
		q.Set("encrypt", "true")
	}
	q.Set("TrustServerCertificate", strconv.FormatBool(t.TrustServerCertificate))
	q.Set("app name", "Unravis health check")
	q.Set("connection timeout", strconv.Itoa(connectTimeoutSec))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OpenSQL opens a connection and proves it with a ping. The caller must Close it.
func OpenSQL(ctx context.Context, t SQLTarget) (*SQLConn, error) {
	dsn, err := ConnectionURL(t)
	if err != nil {
		return nil, err
	}
	cfg, err := msdsn.Parse(dsn)
	if err != nil {
		return nil, &ConnectError{"invalid", "The connection details could not be understood."}
	}
	db := sql.OpenDB(mssql.NewConnectorConfig(cfg))
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(2 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, (connectTimeoutSec+4)*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		db.Close()
		return nil, ClassifyConnect(err)
	}
	return &SQLConn{db: db}, nil
}

// SQLConn is an open, pinged connection that answers the Queryer questions. It holds one connection so a check can never fan out.
type SQLConn struct{ db *sql.DB }

func (c *SQLConn) Close() { c.db.Close() }

// Query runs one fixed text and returns its rows, each value as the driver gives it.
func (c *SQLConn) Query(ctx context.Context, text string) ([]Row, error) {
	rows, err := c.db.QueryContext(ctx, text)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			r[c] = vals[i]
		}
		out = append(out, r)
		if len(out) >= 5000 {
			break
		}
	}
	return out, rows.Err()
}

const qListDatabases = `SELECT name FROM sys.databases WHERE HAS_DBACCESS(name) = 1 AND state = 0 AND database_id > 4 ORDER BY name`
const qVersion = `SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(64)) AS version, CAST(SERVERPROPERTY('Edition') AS nvarchar(128)) AS edition`

// TestResult is what a successful test tells the person.
type TestResult struct {
	Version   string   `json:"version"`
	Edition   string   `json:"edition"`
	Databases []string `json:"databases"`
}

// TestSQL connects, reads the version and lists the databases the login can open (the system databases are left out).
func TestSQL(ctx context.Context, t SQLTarget) (TestResult, error) {
	c, err := OpenSQL(ctx, t)
	if err != nil {
		return TestResult{}, err
	}
	defer c.Close()
	qctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	res := TestResult{Databases: []string{}}
	if rows, err := c.Query(qctx, qVersion); err == nil && len(rows) == 1 {
		res.Version, res.Edition = text(rows[0]["version"]), text(rows[0]["edition"])
	}
	rows, err := c.Query(qctx, qListDatabases)
	if err != nil {
		return res, &ConnectError{"list", "Connected, but this login could not list the databases. Type the database name instead."}
	}
	for _, r := range rows {
		res.Databases = append(res.Databases, text(r["name"]))
	}
	return res, nil
}

// ClassifyConnect turns a driver error into one fixed sentence. It matches on SQL Server error numbers where the driver gives them and on
// stable phrases otherwise; whatever it does not know becomes a generic "could not connect".
func ClassifyConnect(err error) *ConnectError {
	var ce *ConnectError
	if errors.As(err, &ce) {
		return ce
	}
	var se mssql.Error
	if errors.As(err, &se) {
		switch se.Number {
		case 18456, 18452, 18451:
			return &ConnectError{"login_failed", "The server refused this login. Check the user name and password, or the sign-in method."}
		case 4060, 911:
			return &ConnectError{"database", "The login cannot open that database. Check the name and that the login has access."}
		case 18488, 18487:
			return &ConnectError{"login_failed", "The password has expired or must be changed before this login can be used."}
		}
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "login failed"):
		return &ConnectError{"login_failed", "The server refused this login. Check the user name and password, or the sign-in method."}
	case strings.Contains(m, "cannot open database"):
		return &ConnectError{"database", "The login cannot open that database. Check the name and that the login has access."}
	case strings.Contains(m, "certificate") || strings.Contains(m, "x509"):
		return &ConnectError{"certificate", "The server's certificate is not trusted. Tick \"Trust server certificate\" for a server you know, or install its certificate."}
	case strings.Contains(m, "tls") || strings.Contains(m, "encryption") || strings.Contains(m, "handshake"):
		return &ConnectError{"tls", "The secure connection could not be set up. Try changing the Encrypt setting."}
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline"):
		return &ConnectError{"timeout", "The server did not answer in time. Check the name, the port and that the server is running."}
	case strings.Contains(m, "no such host") || strings.Contains(m, "lookup") || strings.Contains(m, "connection refused") || strings.Contains(m, "unreachable") || strings.Contains(m, "dial") || strings.Contains(m, "unable to open tcp"):
		return &ConnectError{"network", "The server could not be reached. Check the name and the port, and that this PC can connect to it."}
	case strings.Contains(m, "sspi") || strings.Contains(m, "integrated") || strings.Contains(m, "kerberos") || strings.Contains(m, "ntlm"):
		return &ConnectError{"windows_auth", "Windows sign-in did not work. Check that this PC and account can reach the server, or use a SQL login."}
	}
	return &ConnectError{"failed", "Could not connect. Check the server name, sign-in details and the Encrypt setting."}
}
