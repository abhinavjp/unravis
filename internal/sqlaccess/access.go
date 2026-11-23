// Package sqlaccess answers one question before anything is set up: can this SQL Server login do what the
// Blackbox needs, and nothing more? It reads and writes nothing on the server. It runs the same fixed read
// statements the Blackbox SQL service runs (internal/bbsql), plus three small fixed statements of its own, and
// says in plain words what passed, what failed and, only when a right is missing, the exact script to run.
//
// Rules kept here:
//   - Every statement is a compile-time constant. No text is built from input; the one name that goes into the
//     printed script (the login) is escaped and is never sent to the server.
//   - Nothing is changed on the server. The app prints a grant script; it never runs one.
//   - Answers carry numbers, fixed words and (for the writable-database warning) at most a few database names
//     the person's own login can already see. Driver text, host names and error text never reach the answer.
package sqlaccess

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/dbhealth"
)

// Querier runs one fixed text and returns its rows.
type Querier interface {
	Query(ctx context.Context, text string) ([]map[string]any, error)
}

// Check states.
const (
	Passed  = "passed"
	Failed  = "failed"
	Warning = "warning"
	Skipped = "skipped"
)

// Check ids, in the order they run.
const (
	CheckConnect   = "connect"
	CheckRights    = "rights"
	CheckRead      = "read"
	CheckWrite     = "write"
	CheckDatabases = "databases"
	CheckGroupsAG  = "ag"
)

// Check is one line of the answer.
type Check struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// Report is the whole answer.
type Report struct {
	Reached bool    `json:"reached"`
	Version string  `json:"version,omitempty"`
	Checks  []Check `json:"checks"`
	// Ready is true when the Blackbox can read everything it needs with this login and the login holds no more
	// than the Blackbox accepts. A warning about write access to databases does not block.
	Ready bool `json:"ready"`
	// Verdict is one of ready, needs-grant, too-many-rights, unreadable.
	Verdict string `json:"verdict"`
	// Script is set only when a right is missing or the login is too strong: the exact text to run.
	Script string `json:"script,omitempty"`
	// WritableDatabases counts databases this login can change data in (database level). Names are the first few.
	WritableDatabases int      `json:"writableDatabases"`
	WritableNames     []string `json:"writableNames,omitempty"`
}

// Verdicts.
const (
	VerdictReady      = "ready"
	VerdictNeedsGrant = "needs-grant"
	VerdictTooMany    = "too-many-rights"
	VerdictUnreadable = "unreadable"
)

const (
	prefix = "SET NOCOUNT ON; SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW; SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED; "

	// who: the login name as the server sees it, the major version and the two server rights the Blackbox reads with.
	stmtWho = prefix + `SELECT CAST(SUSER_SNAME() AS nvarchar(256)) AS login_name,
  CAST(SERVERPROPERTY(N'ProductMajorVersion') AS int) AS major,
  CAST(SERVERPROPERTY(N'ProductVersion') AS nvarchar(64)) AS version,
  CASE WHEN HAS_PERMS_BY_NAME(NULL, NULL, N'VIEW SERVER STATE') = 1 THEN 1
       WHEN ISNULL(HAS_PERMS_BY_NAME(NULL, NULL, N'VIEW SERVER PERFORMANCE STATE'), 0) = 1 THEN 1 ELSE 0 END AS view_state,
  ISNULL(HAS_PERMS_BY_NAME(NULL, NULL, N'VIEW ANY DATABASE'), 0) AS view_any_db /* unravis:access:who */`

	// writable: databases where this login can change data or structure (database-level rights are outside the
	// server-level ceiling). Names are returned only so the person can see which ones; at most 11 rows.
	stmtWritable = prefix + `SELECT TOP 11 name FROM sys.databases
WHERE state = 0 AND database_id <> 2
  AND (HAS_PERMS_BY_NAME(name, N'DATABASE', N'INSERT') = 1 OR HAS_PERMS_BY_NAME(name, N'DATABASE', N'UPDATE') = 1
    OR HAS_PERMS_BY_NAME(name, N'DATABASE', N'DELETE') = 1 OR HAS_PERMS_BY_NAME(name, N'DATABASE', N'ALTER') = 1
    OR HAS_PERMS_BY_NAME(name, N'DATABASE', N'CONTROL') = 1)
ORDER BY name /* unravis:access:writable */`
)

// samplers are the Blackbox read statements run once each. Any permission refusal here means a missing right.
var samplers = []struct{ id, label string }{
	{"bb:requests", "running requests"},
	{"bb:waits", "wait statistics"},
	{"bb:schedulers", "processor schedulers"},
	{"bb:semaphores", "memory grants"},
	{"bb:tempdb", "tempdb use"},
	{"bb:filestats", "file read and write times"},
}

const sampleTimeout = 5 * time.Second

// adapter lets the bbsql helpers (CheckCeiling, Query) run through a Querier.
type adapter struct{ q Querier }

func (a adapter) Query(ctx context.Context, text string) ([]bbsql.Row, error) {
	rows, err := a.q.Query(ctx, text)
	if err != nil {
		return nil, err
	}
	out := make([]bbsql.Row, len(rows))
	for i, r := range rows {
		out[i] = bbsql.Row(r)
	}
	return out, nil
}

// Run runs the access check on an open connection. connectErr is the result of opening it: when set, nothing else runs.
func Run(ctx context.Context, q Querier, connectErr error) Report {
	rep := Report{Checks: []Check{}, Verdict: VerdictUnreadable}
	if connectErr != nil {
		rep.Checks = append(rep.Checks, Check{CheckConnect, Failed, "Reach the server and sign in", connectWords(connectErr)})
		return rep
	}
	rep.Reached = true
	login, major, viewState, viewAnyDB := "", 0, false, false
	rows, err := q.Query(ctx, stmtWho)
	if err != nil || len(rows) != 1 {
		rep.Checks = append(rep.Checks, Check{CheckConnect, Failed, "Reach the server and sign in", "Connected, but the login could not read its own details. Nothing else was tried."})
		return rep
	}
	login = text(rows[0]["login_name"])
	major = int(num(rows[0]["major"]))
	rep.Version = text(rows[0]["version"])
	viewState = num(rows[0]["view_state"]) == 1
	viewAnyDB = num(rows[0]["view_any_db"]) == 1
	rep.Checks = append(rep.Checks, Check{CheckConnect, Passed, "Reach the server and sign in", "Connected" + versionWords(rep.Version) + "."})

	// Rights ceiling: the same proof the Blackbox runs at every connect.
	tooMany := false
	rightsUnread := false
	switch cerr := bbsql.CheckCeiling(ctx, adapter{q}); {
	case cerr == nil:
		rep.Checks = append(rep.Checks, Check{CheckRights, Passed, "Holds no more rights than needed", "Only connect and view server state. The Blackbox accepts this login."})
	case errors.Is(cerr, bbsql.ErrTooManyRights):
		tooMany = true
		rep.Checks = append(rep.Checks, Check{CheckRights, Failed, "Holds no more rights than needed", "This login has more rights than the Blackbox needs, so the Blackbox would refuse it. Use a login that can only read server state."})
	default:
		rightsUnread = true
		rep.Checks = append(rep.Checks, Check{CheckRights, Failed, "Holds no more rights than needed", "The rights of this login could not be read, so the Blackbox would not use it."})
	}

	// Read access: run each fixed read statement once.
	var denied, other []string
	for _, s := range samplers {
		text, ok := bbsql.Statement(s.id)
		if !ok {
			other = append(other, s.label)
			continue
		}
		_, qerr := bbsql.Query(ctx, adapter{q}, text, sampleTimeout)
		if qerr == nil {
			continue
		}
		if bbsql.Classify(qerr).Code == bbsql.CodePermission {
			denied = append(denied, s.label)
		} else {
			other = append(other, s.label)
		}
	}
	missingState := !viewState
	switch {
	case len(denied) == 0 && len(other) == 0 && viewState:
		rep.Checks = append(rep.Checks, Check{CheckRead, Passed, "Can read the activity views", "Running requests, waits, schedulers, memory grants, tempdb and file times were all readable."})
	case missingState:
		d := "The login is missing the right to view server state."
		if len(denied) > 0 {
			d = "Refused: " + strings.Join(denied, ", ") + ". The login is missing the right to view server state."
		}
		rep.Checks = append(rep.Checks, Check{CheckRead, Failed, "Can read the activity views", d})
	case len(denied) > 0:
		// The login holds the right but a statement was still refused: a DENY or another restriction. A grant would not help, so no script.
		rep.Checks = append(rep.Checks, Check{CheckRead, Failed, "Can read the activity views", "Refused: " + strings.Join(denied, ", ") + ". The login holds the right to view server state, but something on the server still blocks these. Ask the server's admin."})
	default:
		rep.Checks = append(rep.Checks, Check{CheckRead, Failed, "Can read the activity views", "These could not be read: " + strings.Join(other, ", ") + "."})
	}
	missingAnyDB := !viewAnyDB

	// Write access (database level): a warning, never a block. The Blackbox only sends read statements.
	if wrows, werr := q.Query(ctx, stmtWritable); werr != nil {
		rep.Checks = append(rep.Checks, Check{CheckWrite, Skipped, "Cannot change data", "Not checked: the databases could not be listed."})
	} else {
		for i, r := range wrows {
			if i < 10 {
				rep.WritableNames = append(rep.WritableNames, text(r["name"]))
			}
		}
		rep.WritableDatabases = len(wrows)
		if len(wrows) == 0 {
			rep.Checks = append(rep.Checks, Check{CheckWrite, Passed, "Cannot change data", "This login cannot insert, update, delete or alter in any database."})
		} else {
			more := ""
			if len(wrows) > 10 {
				more = " or more"
				rep.WritableDatabases = 11
			}
			rep.Checks = append(rep.Checks, Check{CheckWrite, Warning, "Cannot change data",
				fmt.Sprintf("This login can change data in %d%s database(s). The Blackbox sends only read statements, but a login that can only read is safer.", rep.WritableDatabases, more)})
		}
	}

	// Verdict and script.
	switch {
	case tooMany:
		rep.Verdict = VerdictTooMany
		rep.Script = ReadOnlyLoginScript(major)
	case rightsUnread:
		rep.Verdict = VerdictUnreadable
	case missingState || missingAnyDB:
		rep.Verdict = VerdictNeedsGrant
		rep.Script = GrantScript(login, major, missingState, missingAnyDB)
	case len(denied) > 0 || len(other) > 0:
		rep.Verdict = VerdictUnreadable
	default:
		rep.Verdict = VerdictReady
		rep.Ready = true
	}
	if missingAnyDB && !missingState && !tooMany && !rightsUnread {
		rep.Checks = append(rep.Checks, Check{CheckDatabases, Failed, "Can see every database", "The login cannot see all databases, so file times would be left out for some."})
	}
	return rep
}

func versionWords(v string) string {
	if v == "" {
		return ""
	}
	return " (SQL Server " + v + ")"
}

// connectWords maps a connect failure to one fixed sentence. The connect step reports *dbhealth.ConnectError with a fixed code; any other
// error is classified first, so no driver text, host name or login ever reaches the answer.
func connectWords(err error) string {
	switch dbhealth.ClassifyConnect(err).Code {
	case "login_failed":
		return "The server refused this login. Check the name and password."
	case "certificate", "tls":
		return "The secure connection could not be set up. Check the encryption setting and the server certificate."
	case "timeout", "network":
		return "The server could not be reached. Check the name, the port and that the server is running and open to this PC."
	case "database":
		return "The login cannot open the database."
	case "windows_auth":
		return "Windows sign-in did not work. Check that this PC and account can reach the server, or use a SQL login."
	case "invalid":
		return "The server name or login is not valid. Check what was typed."
	}
	switch bbsql.Classify(err).Code {
	case bbsql.CodePermission:
		return "The login is missing a permission it needs to connect."
	}
	return "Could not connect. Check the server name and the login."
}

// GrantScript is the exact text to run in SSMS for the missing rights of login. SQL Server 2022 and newer have the
// narrower VIEW SERVER PERFORMANCE STATE; older versions use VIEW SERVER STATE.
func GrantScript(login string, major int, state, anyDB bool) string {
	var b strings.Builder
	b.WriteString("-- Run in SSMS as an admin. Unravis never runs this for you.\n")
	name := bracket(login)
	if state {
		if major >= 16 {
			b.WriteString("GRANT VIEW SERVER PERFORMANCE STATE TO " + name + ";\n")
		} else {
			b.WriteString("GRANT VIEW SERVER STATE TO " + name + ";\n")
		}
	}
	if anyDB {
		b.WriteString("GRANT VIEW ANY DATABASE TO " + name + ";\n")
	}
	return b.String()
}

// ReadOnlyLoginScript is the text to create a login that holds only what the Blackbox needs. The password is a
// placeholder the person replaces; the app never sees it.
func ReadOnlyLoginScript(major int) string {
	right := "VIEW SERVER STATE"
	if major >= 16 {
		right = "VIEW SERVER PERFORMANCE STATE"
	}
	return "-- Run in SSMS as an admin. Unravis never runs this for you.\n" +
		"-- Replace the password, then use this login in Unravis instead.\n" +
		"CREATE LOGIN [unravis_reader] WITH PASSWORD = N'<choose a long password>', CHECK_POLICY = ON;\n" +
		"GRANT " + right + " TO [unravis_reader];\n" +
		"GRANT VIEW ANY DATABASE TO [unravis_reader];\n"
}

// bracket quotes a login for the printed script: control characters are dropped and ] is doubled. A name that is
// empty after cleaning becomes a clear placeholder instead of a broken script.
func bracket(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if r == ']' {
			b.WriteString("]]")
			continue
		}
		b.WriteRune(r)
	}
	s := b.String()
	if strings.TrimSpace(s) == "" {
		s = "<login name>"
	}
	return "[" + s + "]"
}

func text(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	}
	return ""
}

func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case []byte:
		i, _ := strconv.ParseInt(strings.TrimSpace(string(n)), 10, 64)
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	}
	return 0
}

// dbQuerier lets an open dbhealth connection answer as a Querier.
type dbQuerier struct{ c *dbhealth.SQLConn }

func (d dbQuerier) Query(ctx context.Context, text string) ([]map[string]any, error) {
	rows, err := d.c.Query(ctx, text)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = map[string]any(r)
	}
	return out, nil
}

// CheckTarget opens one connection to the target, runs the access check and closes it. It never returns the driver's
// error: a failed connect becomes the first check of the report.
func CheckTarget(ctx context.Context, t dbhealth.SQLTarget) Report {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	c, err := dbhealth.OpenSQL(ctx, t)
	if err != nil {
		return Run(ctx, nil, err)
	}
	defer c.Close()
	return Run(ctx, dbQuerier{c}, nil)
}

// checkTimeout bounds the whole check: a connect, three control queries, six samplers and one list.
const checkTimeout = 45 * time.Second
