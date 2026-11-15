package main

// sqlrunner.go is the wiring for the separate SQL service process (BBX-5-10).
//
// This code path runs ONLY inside the UnravisBlackboxSql service, as
// NT SERVICE\UnravisBlackboxSql. It writes only to o.Dir (the DACL-locked \sql
// folder), never touches the host recorder's output, opens no connection of
// its own (the runner owns the single connection), serves no HTTP, and reads
// no credentials: the SQL text lives inside internal/bbsql (C1) and nothing
// here passes SQL, parameters or error text onward (C10).
//
// Two things cannot be proven without a real server, so the owner must check
// them on a live instance before this is believed:
//   1. that the SQL service samples a local instance end to end (connect, C8
//      ceiling check, allowlisted query, decode, NDJSON append);
//   2. that host sampling stays unaffected while SQL is slow or backed off.
//      Process separation makes it true by construction here, but the
//      measurable effect needs a loaded server.

import (
	"context"
	"os"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
)

// sqlLocalServer is the only SQL target this build can reach. C12 keeps the
// Blackbox to the machine it runs on, and the default instance is the one
// agreed for this release; a named instance needs a probe.server field, which
// config.go deliberately still refuses (owner decision, live check).
const sqlLocalServer = "(local)"

// sqlBudgetShare is the SQL folder's part of the owner's single maxGB budget.
// It is deliberately the SAME constant the host recorder uses to reserve the
// remaining share (blackbox.SQLBudgetShare), so the two writers cannot drift
// apart: host keeps max - max/share, SQL keeps max/share, and together they stay
// within one ceiling (W10-02). The folder lives under the recorder's folder but
// host housekeeping never looks inside it, so the service must bound it itself.
//
// Measured worst case with every sampler at its row cap AND maximum numeric
// width is ~319 MB a day (internal/bbsql/budget_test.go prints the figure), so
// the default 2 GB budget gives the SQL folder ~537 MB - under two days - and a
// full 168-hour window at that rate would need ~2.2 GB. The byte cap therefore
// bites before the age limit on a busy instance, oldest whole hours going first.
//
// OWNER DECISION OPEN: C13's "<2 KB per sample" is NOT met at the C6 row caps
// (a realistic filestats line is ~4.4 KB, an at-cap max-width line ~53 KB). The
// bound was not relaxed to make a test pass; it needs an explicit owner ruling.
const sqlBudgetShare = blackbox.SQLBudgetShare

// runSQLServiceLoop is the real deps.sqlRun: it runs the bbsql runner in this
// process until ctx is done. The budgets (C4), the back-off and auto-off (C7)
// and the status file shape (C11) all live inside bbsql.Runner; this function
// only points it at the locked folder, the owner's trust anchors and the
// owner's disk bounds (C13).
func runSQLServiceLoop(ctx context.Context, o sqlRunOptions) error {
	// install-sql sets the folder's ACL; this process only creates it - and
	// then proves the ACL before writing anything into it.
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return err
	}
	log := o.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	if err := bbsql.CheckFolderAccess(o.Dir); err != nil {
		// The samples are wait types, counts and database ids, but ids of real
		// databases in a real shop are business data: a folder the operator
		// repointed without re-running install-sql can inherit rights that let
		// ordinary users read it. The check also proves this process IS the
		// UnravisBlackboxSql service identity (W10-11): a console or helper
		// process is refused. Refusing to start is the only honest answer, and
		// the fixed code is what reaches the event log - never a path.
		log("sql: " + bbsql.CodeFolder)
		return err
	}
	r := bbsql.NewRunner(o.Dir, sqlConnConfig(o.Config.Probe))
	r.MinFreePercent, r.KeepHours, r.MaxBytes = sqlDiskBounds(o.Config)
	r.FreePercent = sqlFreePercent
	r.Log = log
	return r.Run(ctx)
}

// sqlDiskBounds resolves the C13 bounds of the SQL sample folder from the
// owner's housekeeping limits: the same free-space floor as the recorder, the
// same age window in hours, and sqlBudgetShare of the byte budget. Config
// validation already refuses zero and out-of-range values here, so there is no
// "housekeeping off" state to defend against.
func sqlDiskBounds(c blackbox.Config) (minFreePercent int, keepHours int, maxBytes int64) {
	return c.MinFreePercent, c.KeepDays * 24, c.MaxBytes() / sqlBudgetShare
}

// sqlFreePercent reads the free space of the volume holding the sample folder.
// bbsql treats an error here as "no room" and writes nothing, so a volume this
// build cannot read (non-Windows) fails closed rather than unbounded.
func sqlFreePercent(path string) (float64, error) {
	info, err := blackbox.OSDisk{}.Free(path)
	if err != nil {
		return 0, err
	}
	return info.FreePercent(), nil
}

// sqlConnConfig maps the probe block onto the connection settings. Empty
// anchors mean no anchor, and bbsql.Open then fails closed rather than
// accepting an unverifiable certificate. The C4 per-query budgets are not
// configurable: the runner derives them from each sampler's cadence.
func sqlConnConfig(p blackbox.Probe) bbsql.ConnConfig {
	return bbsql.ConnConfig{
		Server:                sqlLocalServer,
		ServerCertificate:     p.ServerCertificate,
		Certificate:           p.Certificate,
		HostNameInCertificate: p.HostNameInCertificate,
	}
}
