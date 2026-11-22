// Command qsoffprobe answers one question on a PC that has a real SQL Server: do the Query Store views return rows while the state is OFF
// on this build? (BL-016.) It opens the database the way the app does and runs the app's own SQL lane read (internal/dbhealth RunSQLLane),
// then prints the server version, the database, Query Store's desired and actual state, the first and last kept block, the rows read and
// the sentence the app would show.
//
//	qsoffprobe -database <name>
//
// It only reads. It never runs ALTER DATABASE, never switches Query Store on or off and never writes; every text it sends passes a read-only
// guard. The person who runs it prepares an OFF database by hand (docs/pipeline/BL-016-live-harness.md).
//
// It does nothing unless you opt in with UNRAVIS_LIVE_SQL_SERVER (the server, for example localhost\SQLEXPRESS or host,1433), so a build or
// a test run never reaches a server. Other variables: UNRAVIS_LIVE_SQL_USER and UNRAVIS_LIVE_SQL_PASS (SQL login; without a user the
// Windows account running the command signs in), UNRAVIS_LIVE_SQL_TRUST_CERT=1 (accept the server's certificate), UNRAVIS_LIVE_SQL_DB (the
// database when -database is not given). The password is read from the environment only, never from an argument.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"api-audit-explorer/internal/dbhealth"
)

// Environment variable names.
const (
	envServer = "UNRAVIS_LIVE_SQL_SERVER"
	envUser   = "UNRAVIS_LIVE_SQL_USER"
	envPass   = "UNRAVIS_LIVE_SQL_PASS"
	envTrust  = "UNRAVIS_LIVE_SQL_TRUST_CERT"
	envDB     = "UNRAVIS_LIVE_SQL_DB"
)

// errNoOptIn is returned, before any connection is tried, when the opt-in variable is not set.
var errNoOptIn = errors.New("refusing to run: set " + envServer + " to the SQL Server to read (for example localhost\\SQLEXPRESS). This opt-in keeps the probe from ever reaching a server by accident")

// opener opens a connection scoped to the target and hands back a Queryer and its closer.
type opener func(ctx context.Context, t dbhealth.SQLTarget) (dbhealth.Queryer, func(), error)

func openLive(ctx context.Context, t dbhealth.SQLTarget) (dbhealth.Queryer, func(), error) {
	c, err := dbhealth.OpenSQL(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	return c, c.Close, nil
}

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, openLive, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, "qsoffprobe:", err)
		os.Exit(1)
	}
}

// targetFromEnv builds the login from the environment; errNoOptIn when the opt-in variable is empty.
func targetFromEnv(getenv func(string) string, database string) (dbhealth.SQLTarget, error) {
	server := strings.TrimSpace(getenv(envServer))
	if server == "" {
		return dbhealth.SQLTarget{}, errNoOptIn
	}
	t := dbhealth.SQLTarget{Server: server, Auth: "windows", Database: database, Encrypt: "mandatory"}
	if u := getenv(envUser); u != "" {
		t.Auth, t.Username, t.Password = "sql", u, getenv(envPass)
	}
	switch strings.ToLower(strings.TrimSpace(getenv(envTrust))) {
	case "1", "true", "yes":
		t.TrustServerCertificate = true
	}
	return t, nil
}

func run(args []string, getenv func(string) string, out io.Writer, open opener, now func() time.Time) error {
	fs := flag.NewFlagSet("qsoffprobe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	database := fs.String("database", "", "the database to read (or set "+envDB+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The opt-in is checked first, so without it nothing else (not even the database name) matters and nothing is opened.
	if _, err := targetFromEnv(getenv, ""); err != nil {
		return err
	}
	db := strings.TrimSpace(*database)
	if db == "" {
		db = strings.TrimSpace(getenv(envDB))
	}
	if db == "" {
		return errors.New("name the database: -database <name> (or set " + envDB + ")")
	}
	t, _ := targetFromEnv(getenv, db)
	ctx, cancel := context.WithTimeout(context.Background(), dbhealth.LaneRunTimeout)
	defer cancel()
	q, closeConn, err := open(ctx, t)
	if err != nil {
		return fmt.Errorf("could not connect: %w", err)
	}
	defer closeConn()
	_, _ = fmt.Fprint(out, dbhealth.ProbeQueryStoreOff(ctx, q, "qsoffprobe", db, now).Format())
	return nil
}
