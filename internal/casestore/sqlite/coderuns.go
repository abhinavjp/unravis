package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

var _ casestore.CodeRunStore = (*Case)(nil)

// The code-mode audit record lives in the encrypted case database (spec 04
// section 3.6, BL-369). The tables are created on the first BeginRun with IF NOT
// EXISTS, so a case made before this feature opens as before, an older build
// opens a case that has them and ignores them (it knows no table of this name),
// and the schema version does not change.
//
// The triggers are the first guard, against a mistake or a careless UPDATE: the
// chain and the call rows can only be appended to, a run row can only change
// while it is "running", and a call can only be added to a running run. They are
// not the security: whoever holds the case key can drop a trigger. The hash chain
// (casestore.VerifyCodeChain) is what notices an edit made after that.
const codeSQL = `
CREATE TABLE IF NOT EXISTS code_runs(
  run_id TEXT PRIMARY KEY,
  created_at_utc INTEGER NOT NULL,
  ended_at_utc INTEGER NOT NULL DEFAULT 0,
  surface TEXT NOT NULL,
  caller_id TEXT NOT NULL DEFAULT '',
  caller_declared INTEGER NOT NULL DEFAULT 0,
  allow_identifiers INTEGER NOT NULL DEFAULT 0,
  keep_results INTEGER NOT NULL DEFAULT 0,
  sandbox_mode TEXT NOT NULL DEFAULT '',
  runtime TEXT NOT NULL DEFAULT '',
  worker_sha256 TEXT NOT NULL DEFAULT '',
  tool_schema_hash TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  source_ts TEXT NOT NULL,
  stripped_js_hash TEXT NOT NULL DEFAULT '',
  seed TEXT NOT NULL DEFAULT '',
  clock_ms INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL CHECK (status IN ('running','ok','error','timeout','limit_exceeded','cancelled','crashed')),
  error_code TEXT NOT NULL DEFAULT '',
  error_text TEXT NOT NULL DEFAULT '',
  result_hash TEXT NOT NULL DEFAULT '',
  result_bytes INTEGER NOT NULL DEFAULT 0,
  result_preview TEXT NOT NULL DEFAULT '',
  console_logs TEXT NOT NULL DEFAULT '',
  call_count INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  script_ms INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS code_run_calls(
  run_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  done_seq INTEGER NOT NULL,
  tool_name TEXT NOT NULL,
  args_json TEXT NOT NULL DEFAULT '',
  args_hash TEXT NOT NULL,
  args_state TEXT NOT NULL DEFAULT 'kept',
  result_hash TEXT NOT NULL,
  result_bytes INTEGER NOT NULL DEFAULT 0,
  result_blob BLOB,
  blob_state TEXT NOT NULL CHECK (blob_state IN ('kept','off','oversize','withheld')),
  status TEXT NOT NULL CHECK (status IN ('ok','error','denied')),
  duration_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(run_id, seq),
  UNIQUE(run_id, done_seq)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS code_chain(
  n INTEGER PRIMARY KEY,
  run_id TEXT NOT NULL,
  phase TEXT NOT NULL CHECK (phase IN ('begin','call','end')),
  seq INTEGER NOT NULL DEFAULT 0,
  record_json TEXT NOT NULL,
  prev_hash TEXT NOT NULL,
  record_hash TEXT NOT NULL UNIQUE,
  UNIQUE(run_id, phase, seq)
);
` + codeTriggersSQL

const codeTriggersSQL = `
CREATE TRIGGER IF NOT EXISTS code_chain_no_update BEFORE UPDATE ON code_chain
BEGIN SELECT RAISE(ABORT, 'code_chain is append-only'); END;
CREATE TRIGGER IF NOT EXISTS code_chain_no_delete BEFORE DELETE ON code_chain
BEGIN SELECT RAISE(ABORT, 'code_chain is append-only'); END;
CREATE TRIGGER IF NOT EXISTS code_chain_exists BEFORE INSERT ON code_chain
WHEN EXISTS (SELECT 1 FROM code_chain WHERE n = NEW.n OR record_hash = NEW.record_hash OR (run_id = NEW.run_id AND phase = NEW.phase AND seq = NEW.seq))
BEGIN SELECT RAISE(ABORT, 'code_chain rows are never replaced'); END;
CREATE TRIGGER IF NOT EXISTS code_chain_link BEFORE INSERT ON code_chain
WHEN NEW.n <> COALESCE((SELECT MAX(n) FROM code_chain), 0) + 1
  OR NEW.prev_hash <> COALESCE((SELECT record_hash FROM code_chain ORDER BY n DESC LIMIT 1), '` + casestore.ChainZeroHash + `')
BEGIN SELECT RAISE(ABORT, 'code_chain must continue the chain'); END;
CREATE TRIGGER IF NOT EXISTS code_runs_no_delete BEFORE DELETE ON code_runs
BEGIN SELECT RAISE(ABORT, 'code_runs is append-only'); END;
CREATE TRIGGER IF NOT EXISTS code_runs_exists BEFORE INSERT ON code_runs
WHEN EXISTS (SELECT 1 FROM code_runs WHERE run_id = NEW.run_id)
BEGIN SELECT RAISE(ABORT, 'code_runs rows are never replaced'); END;
CREATE TRIGGER IF NOT EXISTS code_runs_sealed BEFORE UPDATE ON code_runs
WHEN OLD.status <> 'running' OR NEW.rowid <> OLD.rowid OR NEW.run_id <> OLD.run_id OR NEW.source_ts <> OLD.source_ts OR NEW.created_at_utc <> OLD.created_at_utc
  OR NEW.surface <> OLD.surface OR NEW.caller_id <> OLD.caller_id OR NEW.allow_identifiers <> OLD.allow_identifiers
  OR NEW.keep_results <> OLD.keep_results OR NEW.reason <> OLD.reason OR NEW.seed <> OLD.seed OR NEW.clock_ms <> OLD.clock_ms
BEGIN SELECT RAISE(ABORT, 'a sealed code run cannot change'); END;
CREATE TRIGGER IF NOT EXISTS code_calls_no_update BEFORE UPDATE ON code_run_calls
BEGIN SELECT RAISE(ABORT, 'code_run_calls is append-only'); END;
CREATE TRIGGER IF NOT EXISTS code_calls_no_delete BEFORE DELETE ON code_run_calls
BEGIN SELECT RAISE(ABORT, 'code_run_calls is append-only'); END;
CREATE TRIGGER IF NOT EXISTS code_calls_exists BEFORE INSERT ON code_run_calls
WHEN EXISTS (SELECT 1 FROM code_run_calls WHERE run_id = NEW.run_id AND (seq = NEW.seq OR done_seq = NEW.done_seq))
BEGIN SELECT RAISE(ABORT, 'code_run_calls rows are never replaced'); END;
CREATE TRIGGER IF NOT EXISTS code_calls_running BEFORE INSERT ON code_run_calls
WHEN COALESCE((SELECT status FROM code_runs WHERE run_id = NEW.run_id), '') <> 'running'
BEGIN SELECT RAISE(ABORT, 'calls can only be added to a running run'); END;
`

// The "exists" triggers close INSERT OR REPLACE (and an upsert): SQLite does not
// fire a delete trigger for the row a REPLACE removes (unless recursive_triggers
// is on), but it does fire BEFORE INSERT triggers first, and these refuse a key
// that is already there. UPDATE OR REPLACE is the other way in: an update that
// gives a row the rowid of another one deletes that one the same way. The
// code_runs rowid is therefore frozen by code_runs_sealed (the other two tables
// have no rowid that can be set: code_run_calls is WITHOUT ROWID, and the rowid
// of code_chain is its n, which code_chain_no_update refuses to change).
//
// A trigger can also make a write do nothing without an error (RAISE(IGNORE)),
// so every write checks that exactly one row was written (oneRow), and the
// verifier reports any trigger on these tables that the store did not create.

// legacyGuards are earlier texts of a guard whose current text is stronger. A
// case written by a build that had the old text gets the new one at the next
// BeginRun, and only an exact match is replaced: a trigger somebody else
// changed stays as it is, for VerifyCodeChain to report.
var legacyGuards = map[string][]string{
	"code_runs_sealed": {normalizeSQL(`CREATE TRIGGER code_runs_sealed BEFORE UPDATE ON code_runs
WHEN OLD.status <> 'running' OR NEW.run_id <> OLD.run_id OR NEW.source_ts <> OLD.source_ts OR NEW.created_at_utc <> OLD.created_at_utc
  OR NEW.surface <> OLD.surface OR NEW.caller_id <> OLD.caller_id OR NEW.allow_identifiers <> OLD.allow_identifiers
  OR NEW.keep_results <> OLD.keep_results OR NEW.reason <> OLD.reason OR NEW.seed <> OLD.seed OR NEW.clock_ms <> OLD.clock_ms
BEGIN SELECT RAISE(ABORT, 'a sealed code run cannot change'); END`)},
}

// upgradeGuards replaces a guard that still has an earlier text (legacyGuards).
func upgradeGuards(ctx context.Context, tx *sql.Tx) error {
	for _, g := range codeGuards {
		old := legacyGuards[g.name]
		if len(old) == 0 {
			continue
		}
		var have string
		switch err := tx.QueryRowContext(ctx, "SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='trigger' AND name=?", g.name).Scan(&have); {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return err
		}
		have = normalizeSQL(have)
		if have == g.sql || !slices.Contains(old, have) {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+g.name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, strings.Replace(g.sql, "CREATE TRIGGER ", "CREATE TRIGGER IF NOT EXISTS ", 1)); err != nil {
			return err
		}
	}
	return nil
}

// oneRow checks that a write changed exactly one row. A trigger that was planted
// with RAISE(IGNORE) turns a write into a silent no-op; this is how the store
// notices instead of telling its caller that the row is safe.
func oneRow(res sql.Result, what string) error {
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: the write of %s changed %d rows instead of one (a trigger the store did not create swallowed it)", casestore.ErrCodeTampered, what, n)
	}
	return nil
}

// codeGuard is one trigger: its name and the text it must have.
type codeGuard struct{ name, sql string }

// codeGuards are the triggers of codeTriggersSQL, read from it so the two cannot drift.
var codeGuards = parseGuards(codeTriggersSQL)

func parseGuards(sqlText string) []codeGuard {
	var out []codeGuard
	for _, part := range strings.Split(sqlText, "CREATE TRIGGER IF NOT EXISTS ")[1:] {
		name, _, _ := strings.Cut(part, " ")
		out = append(out, codeGuard{name: name, sql: normalizeSQL("CREATE TRIGGER " + part)})
	}
	return out
}

// normalizeSQL makes two spellings of one statement equal: white space folded,
// the closing semicolon and IF NOT EXISTS dropped.
func normalizeSQL(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "IF NOT EXISTS ", "")
	return strings.TrimSuffix(s, ";")
}

// codeHeadKey is the meta row that keeps the head of the chain (as "n:hash")
// beside the chain, written in the transaction of every append.
const codeHeadKey = "code.chain.head"

// codeGateMode says which rule a write follows.
type codeGateMode int

const (
	// gateNew is a run's first write (BeginRun): refused on a closed case and
	// while an import runs, like every other case write.
	gateNew codeGateMode = iota
	// gateInflight is a write for a run that was begun (RecordCall, EndRun). It
	// must not be lost, so it waits for an import to release the folder lock
	// (on Windows the lock gives up after 5 s with sealedfile.ErrUnavailable and
	// the caller retries) and a closed case still takes it. An archive in
	// progress or finished refuses it.
	gateInflight
	// gateSweep is CloseOrphanRuns: allowed on a closed case, but it does not
	// wait for an import; the next open tries again.
	gateSweep
)

// codeGate is tagGate for the audit writes, with the modes above.
func (c *Case) codeGate(ctx context.Context, mode codeGateMode) (*sql.DB, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, nil, err
	}
	if mode != gateInflight && c.importRun.Load() {
		return nil, nil, casestore.ErrImportRunning
	}
	var release func()
	if mode == gateInflight {
		release, err = c.waitFolderLock(ctx)
	} else {
		release, err = sealedfile.Lock(c.dir)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := c.syncManifest(ctx, db); err != nil {
		release()
		return nil, nil, err
	}
	if mode == gateNew {
		if c.isClosed() {
			err = casestore.ErrCaseClosed
		}
	} else {
		err = c.archiveRefusal()
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	return db, release, nil
}

// waitFolderLock takes the folder lock for a write that must not be lost. It
// waits for a long holder (an import) as long as ctx lives and no longer than
// casestore.InflightWait, then returns ErrUnavailable; a cancelled or timed-out
// ctx returns its own error. The lock belongs to the calling goroutine.
func (c *Case) waitFolderLock(ctx context.Context) (func(), error) {
	wctx, cancel := context.WithTimeout(ctx, casestore.InflightWait)
	defer cancel()
	release, err := sealedfile.LockWait(wctx, c.dir)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: the case folder stayed busy for %s", sealedfile.ErrUnavailable, casestore.InflightWait)
	}
	return release, err
}

// beginDurable starts a write transaction on a connection of its own that runs
// with PRAGMA synchronous=FULL, so the commit of an audit write is on disk before
// the broker answers the script (spec 04 3.5 step 6: write the row, then reply).
// The pool's connections run NORMAL, which in WAL mode can lose the last commits
// on power loss. done rolls back whatever is open, puts the connection back to
// NORMAL and returns it; a connection that cannot be reset is thrown away.
func (c *Case) beginDurable(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		_, rerr := conn.ExecContext(context.Background(), "PRAGMA synchronous=NORMAL")
		if rerr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
		return nil, nil, err
	}
	return tx, func() {
		_ = tx.Rollback()
		if _, err := conn.ExecContext(context.Background(), "PRAGMA synchronous=NORMAL"); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}

// unreadable marks a row that does not read back as the schema says (a column
// of the wrong type, say): that is damage, not a driver fault.
func unreadable(err error) error {
	return fmt.Errorf("%w: a stored row cannot be read (%v)", casestore.ErrCodeTampered, err)
}

const (
	runColsPre = `run_id, created_at_utc, COALESCE(ended_at_utc,0), COALESCE(surface,''), COALESCE(caller_id,''), COALESCE(caller_declared,0),
 COALESCE(allow_identifiers,0), COALESCE(keep_results,0), COALESCE(sandbox_mode,''), COALESCE(runtime,''), COALESCE(worker_sha256,''),
 COALESCE(tool_schema_hash,''), COALESCE(reason,''), `
	runColsMid = `, COALESCE(stripped_js_hash,''), COALESCE(seed,''), COALESCE(clock_ms,0),
 COALESCE(status,''), COALESCE(error_code,''), COALESCE(error_text,''), COALESCE(result_hash,''), COALESCE(result_bytes,0),
 COALESCE(result_preview,''), `
	runColsPost = `, COALESCE(call_count,0), COALESCE(duration_ms,0), COALESCE(script_ms,0)`

	// runColsFull reads the source and the logs; runColsList leaves them out.
	runColsFull = runColsPre + `COALESCE(source_ts,'')` + runColsMid + `COALESCE(console_logs,'')` + runColsPost
	runColsList = runColsPre + `''` + runColsMid + `''` + runColsPost
)

type rowScanner interface{ Scan(dest ...any) error }

// scanRun scans the columns of runColsFull or runColsList; withSize says that
// the query added the byte length of the source as a last column.
func scanRun(s rowScanner, withSize bool) (casestore.CodeRun, error) {
	var r casestore.CodeRun
	var declared, allow, keep, calls int64
	var size int
	dest := []any{&r.RunID, &r.CreatedMS, &r.EndedMS, &r.Surface, &r.CallerID, &declared, &allow, &keep, &r.SandboxMode,
		&r.Runtime, &r.WorkerSHA256, &r.ToolSchemaHash, &r.Reason, &r.Source, &r.StrippedJSHash, &r.Seed, &r.ClockMS,
		&r.Status, &r.ErrorCode, &r.ErrorText, &r.ResultHash, &r.ResultBytes, &r.ResultPreview, &r.ConsoleLogs, &calls,
		&r.DurationMS, &r.ScriptMS}
	if withSize {
		dest = append(dest, &size)
	}
	if err := s.Scan(dest...); err != nil {
		return r, err
	}
	for _, flag := range []int64{declared, allow, keep} {
		if flag != 0 && flag != 1 { // a flag is 0 or 1; anything else was written by hand
			return r, fmt.Errorf("flag column holds %d", flag)
		}
	}
	r.CallerDeclared, r.AllowIdentifiers, r.KeepResults = declared != 0, allow != 0, keep != 0
	r.CallCount = int(calls)
	r.SourceBytes = len(r.Source)
	if withSize {
		r.SourceBytes = size
	}
	return r, nil
}

const (
	callColsPre = `run_id, seq, done_seq, COALESCE(tool_name,''), COALESCE(args_json,''), COALESCE(args_hash,''), COALESCE(args_state,''),
 COALESCE(result_hash,''), COALESCE(result_bytes,0), `
	callColsPost = `, COALESCE(blob_state,''), COALESCE(status,''), COALESCE(duration_ms,0)`

	callColsFull = callColsPre + `result_blob` + callColsPost
	callColsList = callColsPre + `NULL` + callColsPost
)

// scanCall scans the columns of callColsFull or callColsList; withSize says that
// the query added the stored length of the result copy as a last column.
func scanCall(s rowScanner, withSize bool) (casestore.CodeCall, error) {
	var c casestore.CodeCall
	var size int
	dest := []any{&c.RunID, &c.Seq, &c.DoneSeq, &c.Tool, &c.ArgsJSON, &c.ArgsHash, &c.ArgsState, &c.ResultHash, &c.ResultBytes,
		&c.Blob, &c.BlobState, &c.Status, &c.DurationMS}
	if withSize {
		dest = append(dest, &size)
	}
	if err := s.Scan(dest...); err != nil {
		return c, err
	}
	c.BlobBytes = len(c.Blob)
	if withSize {
		c.BlobBytes = size
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Writes.

type txQuerier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func chainTail(ctx context.Context, q txQuerier) (int64, string, error) {
	var n int64
	var h string
	err := q.QueryRowContext(ctx, "SELECT n, record_hash FROM code_chain ORDER BY n DESC LIMIT 1").Scan(&n, &h)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, casestore.ChainZeroHash, nil
	}
	return n, h, err
}

// appendChain adds one chain row and moves the head kept beside the chain.
// Each write must change exactly one row (oneRow).
func appendChain(ctx context.Context, q txQuerier, row casestore.ChainRow) error {
	res, err := q.ExecContext(ctx, "INSERT INTO code_chain(n, run_id, phase, seq, record_json, prev_hash, record_hash) VALUES(?,?,?,?,?,?,?)",
		row.N, row.RunID, row.Phase, row.Seq, row.RecordJSON, row.PrevHash, row.RecordHash)
	if err != nil {
		return err
	}
	if err := oneRow(res, "a chain record"); err != nil {
		return err
	}
	res, err = q.ExecContext(ctx, "INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v",
		codeHeadKey, strconv.FormatInt(row.N, 10)+":"+row.RecordHash)
	if err != nil {
		return err
	}
	return oneRow(res, "the chain head")
}

// codeHashKey is the meta row of the case secret that keys the hash of withheld values.
const codeHashKey = "code.hash_key"

// hashKey reads the case secret, making it first if the case has none.
func hashKey(ctx context.Context, q txQuerier) ([]byte, error) {
	for range 2 {
		var v string
		switch err := q.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = ?", codeHashKey).Scan(&v); {
		case err == nil:
			key, derr := hex.DecodeString(v)
			if derr != nil || len(key) != casestore.HashKeyBytes {
				return nil, fmt.Errorf("%w: the case secret for withheld hashes is damaged", casestore.ErrCodeTampered)
			}
			return key, nil
		case !errors.Is(err, sql.ErrNoRows):
			return nil, err
		}
		fresh, err := casestore.NewHashKey()
		if err != nil {
			return nil, err
		}
		if _, err := q.ExecContext(ctx, "INSERT OR IGNORE INTO meta(k, v) VALUES(?, ?)", codeHashKey, hex.EncodeToString(fresh)); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: the case secret for withheld hashes cannot be stored", casestore.ErrCodeTampered)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (c *Case) BeginRun(ctx context.Context, in casestore.RunStart) (casestore.CodeRun, error) {
	run, err := casestore.NewCodeRun(in, c.s.now())
	if err != nil {
		return casestore.CodeRun{}, err
	}
	db, release, err := c.codeGate(ctx, gateNew)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	defer release()
	tx, done, err := c.beginDurable(ctx, db)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	defer done()
	if _, err := tx.ExecContext(ctx, codeSQL); err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	if err := upgradeGuards(ctx, tx); err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	// A marker for a later build that has to change the tables (CREATE TABLE IF
	// NOT EXISTS can never add a column).
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO meta(k, v) VALUES('code.schema', '1')"); err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	var have int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM code_runs").Scan(&have); err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	if have >= casestore.MaxCodeRuns {
		return casestore.CodeRun{}, casestore.ErrCapExceeded
	}
	var one int
	switch err := tx.QueryRowContext(ctx, "SELECT 1 FROM code_runs WHERE run_id = ?", run.RunID).Scan(&one); {
	case err == nil:
		return casestore.CodeRun{}, casestore.ErrRunExists
	case !errors.Is(err, sql.ErrNoRows):
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	n, prev, err := chainTail(ctx, tx)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	row, err := casestore.BeginChainRow(run, n+1, prev)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	if _, err := hashKey(ctx, tx); err != nil { // the case secret exists from the first run on
		return casestore.CodeRun{}, c.mapDBErrIfDB(err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO code_runs(run_id, created_at_utc, surface, caller_id, caller_declared, allow_identifiers,
		keep_results, tool_schema_hash, reason, source_ts, seed, clock_ms, status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'running')`,
		run.RunID, run.CreatedMS, run.Surface, run.CallerID, b2i(run.CallerDeclared), b2i(run.AllowIdentifiers), b2i(run.KeepResults),
		run.ToolSchemaHash, run.Reason, run.Source, run.Seed, run.ClockMS)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	if err := oneRow(res, "a run"); err != nil {
		return casestore.CodeRun{}, err
	}
	if err := appendChain(ctx, tx, row); err != nil {
		return casestore.CodeRun{}, c.mapDBErrIfDB(err)
	}
	if err := tx.Commit(); err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	return run, nil
}

func (c *Case) RecordCall(ctx context.Context, runID string, rec casestore.CallRecord) error {
	if !casestore.ValidRunID(runID) {
		return casestore.ErrRunNotFound
	}
	db, release, err := c.codeGate(ctx, gateInflight)
	if err != nil {
		return err
	}
	defer release()
	tx, done, err := c.beginDurable(ctx, db)
	if err != nil {
		return c.mapDBErr(err)
	}
	defer done()
	run, err := c.loadRun(ctx, tx, runID, runColsList)
	if err != nil {
		return err
	}
	if run.Status != casestore.RunRunning {
		return casestore.ErrRunFinished
	}
	var count int
	var used int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(length(result_blob)),0) FROM code_run_calls WHERE run_id = ?", runID).Scan(&count, &used); err != nil {
		return c.mapDBErr(err)
	}
	if count >= casestore.MaxCallsPerRun {
		return casestore.ErrCapExceeded
	}
	key, err := hashKey(ctx, tx)
	if err != nil {
		return c.mapDBErrIfDB(err)
	}
	call, err := casestore.NewCodeCall(run, rec, used, key)
	if err != nil {
		return err
	}
	var one int
	switch err := tx.QueryRowContext(ctx, "SELECT 1 FROM code_run_calls WHERE run_id = ? AND (seq = ? OR done_seq = ?)", runID, call.Seq, call.DoneSeq).Scan(&one); {
	case err == nil:
		return casestore.ErrCallExists
	case !errors.Is(err, sql.ErrNoRows):
		return c.mapDBErr(err)
	}
	var blob any
	if call.BlobState == casestore.BlobKept {
		blob = call.Blob
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO code_run_calls(run_id, seq, done_seq, tool_name, args_json, args_hash, args_state,
		result_hash, result_bytes, result_blob, blob_state, status, duration_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		call.RunID, call.Seq, call.DoneSeq, call.Tool, call.ArgsJSON, call.ArgsHash, call.ArgsState,
		call.ResultHash, call.ResultBytes, blob, call.BlobState, call.Status, call.DurationMS)
	if err != nil {
		return c.mapDBErr(err)
	}
	if err := oneRow(res, "a call"); err != nil {
		return err
	}
	n, prev, err := chainTail(ctx, tx)
	if err != nil {
		return c.mapDBErr(err)
	}
	row, err := casestore.CallChainRow(call, n+1, prev)
	if err != nil {
		return err
	}
	if err := appendChain(ctx, tx, row); err != nil {
		return c.mapDBErrIfDB(err)
	}
	if err := tx.Commit(); err != nil {
		return c.mapDBErr(err)
	}
	return nil
}

func (c *Case) loadRun(ctx context.Context, q txQuerier, runID, cols string) (casestore.CodeRun, error) {
	run, err := scanRun(q.QueryRowContext(ctx, "SELECT "+cols+" FROM code_runs WHERE run_id = ?", runID), false)
	switch {
	case errors.Is(err, sql.ErrNoRows), noTable(err):
		return casestore.CodeRun{}, casestore.ErrRunNotFound
	case err != nil:
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	return run, nil
}

func (c *Case) loadCalls(ctx context.Context, q txQuerier, runID string, withBlob bool) ([]casestore.CodeCall, error) {
	cols := callColsList
	if withBlob {
		cols = callColsFull
	}
	rows, err := q.QueryContext(ctx, "SELECT "+cols+" FROM code_run_calls WHERE run_id = ? ORDER BY seq", runID)
	if err != nil {
		if noTable(err) {
			return nil, nil
		}
		return nil, c.mapDBErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []casestore.CodeCall
	for rows.Next() {
		call, err := scanCall(rows, false)
		if err != nil {
			return nil, unreadable(err)
		}
		out = append(out, call)
	}
	return out, c.mapDBErr(rows.Err())
}

// sealRun ends one running run inside tx: it fills the outcome, updates the row
// and appends the end record.
func (c *Case) sealRun(ctx context.Context, tx *sql.Tx, run casestore.CodeRun, end casestore.RunEnd) (casestore.CodeRun, error) {
	calls, err := c.loadCalls(ctx, tx, run.RunID, false)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	key, err := hashKey(ctx, tx)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErrIfDB(err)
	}
	fin, err := casestore.FinishCodeRun(run, calls, end, c.s.now(), key)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	n, prev, err := chainTail(ctx, tx)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	row, err := casestore.EndChainRow(fin, calls, n+1, prev)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE code_runs SET ended_at_utc=?, sandbox_mode=?, runtime=?, worker_sha256=?, stripped_js_hash=?,
		status=?, error_code=?, error_text=?, result_hash=?, result_bytes=?, result_preview=?, console_logs=?, call_count=?, duration_ms=?, script_ms=?
		WHERE run_id=? AND status='running'`,
		fin.EndedMS, fin.SandboxMode, fin.Runtime, fin.WorkerSHA256, fin.StrippedJSHash, fin.Status, fin.ErrorCode, fin.ErrorText,
		fin.ResultHash, fin.ResultBytes, fin.ResultPreview, fin.ConsoleLogs, fin.CallCount, fin.DurationMS, fin.ScriptMS, fin.RunID)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		// Nothing changed: either the run was sealed meanwhile, or a trigger the
		// store did not create swallowed the update (RAISE(IGNORE)) and the row is
		// still running, which is tampering, not a finished run.
		var status string
		if err := tx.QueryRowContext(ctx, "SELECT status FROM code_runs WHERE run_id = ?", run.RunID).Scan(&status); err == nil && status == casestore.RunRunning {
			return casestore.CodeRun{}, fmt.Errorf("%w: the write that seals a run changed no row (a trigger the store did not create swallowed it)", casestore.ErrCodeTampered)
		}
		return casestore.CodeRun{}, casestore.ErrRunFinished
	}
	if err := appendChain(ctx, tx, row); err != nil {
		return casestore.CodeRun{}, c.mapDBErrIfDB(err)
	}
	return fin, nil
}

func (c *Case) EndRun(ctx context.Context, runID string, end casestore.RunEnd) (casestore.CodeRun, error) {
	if !casestore.ValidRunID(runID) {
		return casestore.CodeRun{}, casestore.ErrRunNotFound
	}
	db, release, err := c.codeGate(ctx, gateInflight)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	defer release()
	tx, done, err := c.beginDurable(ctx, db)
	if err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	defer done()
	run, err := c.loadRun(ctx, tx, runID, runColsFull)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	if run.Status != casestore.RunRunning {
		return casestore.CodeRun{}, casestore.ErrRunFinished
	}
	fin, err := c.sealRun(ctx, tx, run, end)
	if err != nil {
		return casestore.CodeRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return casestore.CodeRun{}, c.mapDBErr(err)
	}
	return fin, nil
}

func (c *Case) CloseOrphanRuns(ctx context.Context) (int, error) {
	db, release, err := c.codeGate(ctx, gateSweep)
	if err != nil {
		return 0, err
	}
	defer release()
	tx, done, err := c.beginDurable(ctx, db)
	if err != nil {
		return 0, c.mapDBErr(err)
	}
	defer done()
	// Only runs older than the longest a run can last: a younger one may be live in another window.
	cutoff := c.s.now().Add(-casestore.OrphanGrace).UnixMilli()
	rows, err := tx.QueryContext(ctx, "SELECT "+runColsFull+" FROM code_runs WHERE status = 'running' AND created_at_utc <= ? ORDER BY rowid", cutoff)
	if err != nil {
		if noTable(err) {
			return 0, nil
		}
		return 0, c.mapDBErr(err)
	}
	var orphans []casestore.CodeRun
	for rows.Next() {
		r, err := scanRun(rows, false)
		if err != nil {
			_ = rows.Close()
			return 0, unreadable(err)
		}
		orphans = append(orphans, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, c.mapDBErr(err)
	}
	_ = rows.Close()
	for _, r := range orphans {
		if _, err := c.sealRun(ctx, tx, r, casestore.CrashedEnd()); err != nil {
			return 0, err
		}
	}
	if len(orphans) == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, c.mapDBErr(err)
	}
	return len(orphans), nil
}

// ---------------------------------------------------------------------------
// Reads.

func (c *Case) ListRuns(ctx context.Context, q casestore.RunQuery) ([]casestore.CodeRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > casestore.MaxRunsPage {
		limit = casestore.MaxRunsPage
	}
	offset := max(q.Offset, 0)
	rows, err := db.QueryContext(ctx, "SELECT "+runColsList+", length(CAST(source_ts AS BLOB)) FROM code_runs ORDER BY rowid DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		if noTable(err) {
			return nil, nil
		}
		return nil, c.mapDBErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []casestore.CodeRun
	for rows.Next() {
		r, err := scanRun(rows, true)
		if err != nil {
			return nil, unreadable(err)
		}
		out = append(out, r)
	}
	return out, c.mapDBErr(rows.Err())
}

func (c *Case) GetRun(ctx context.Context, runID string) (casestore.CodeRun, error) {
	if err := ctx.Err(); err != nil {
		return casestore.CodeRun{}, err
	}
	db, err := c.handle()
	if err != nil {
		return casestore.CodeRun{}, err
	}
	return c.loadRun(ctx, db, runID, runColsFull)
}

func (c *Case) RunCalls(ctx context.Context, runID string) ([]casestore.CodeCall, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	if _, err := c.loadRun(ctx, db, runID, runColsList); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT "+callColsList+", COALESCE(length(result_blob),0) FROM code_run_calls WHERE run_id = ? ORDER BY seq", runID)
	if err != nil {
		return nil, c.mapDBErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []casestore.CodeCall
	for rows.Next() {
		call, err := scanCall(rows, true)
		if err != nil {
			return nil, unreadable(err)
		}
		out = append(out, call)
	}
	return out, c.mapDBErr(rows.Err())
}

func (c *Case) CallResult(ctx context.Context, runID string, seq int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	call, err := scanCall(db.QueryRowContext(ctx, "SELECT "+callColsFull+" FROM code_run_calls WHERE run_id = ? AND seq = ?", runID, seq), false)
	switch {
	case errors.Is(err, sql.ErrNoRows), noTable(err):
		return nil, casestore.ErrRunNotFound
	case err != nil:
		return nil, c.mapDBErr(err)
	}
	return casestore.ResultFromCall(call)
}

func (c *Case) ChainRows(ctx context.Context, afterN int64, limit int) ([]casestore.ChainRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > casestore.MaxChainRowsPage {
		limit = casestore.MaxChainRowsPage
	}
	var out []casestore.ChainRow
	err = chainEach(ctx, db, "WHERE n > ? ORDER BY n LIMIT ?", []any{afterN, limit}, func(r casestore.ChainRow) error {
		out = append(out, r)
		return nil
	})
	if noTable(err) {
		return nil, nil
	}
	return out, c.mapDBErrIfDB(err)
}

// mapDBErrIfDB maps a database error and leaves other errors (a callback's, a
// context's, the tamper marker) as they are.
func (c *Case) mapDBErrIfDB(err error) error {
	if err == nil || errors.Is(err, casestore.ErrCodeTampered) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var se *sqlite3.Error
	if errors.As(err, &se) {
		return c.mapDBErr(err)
	}
	return err
}

func chainEach(ctx context.Context, q txQuerier, tail string, args []any, fn func(casestore.ChainRow) error) error {
	rows, err := q.QueryContext(ctx, "SELECT n, COALESCE(run_id,''), COALESCE(phase,''), COALESCE(seq,0), COALESCE(record_json,''), COALESCE(prev_hash,''), COALESCE(record_hash,'') FROM code_chain "+tail, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r casestore.ChainRow
		if err := rows.Scan(&r.N, &r.RunID, &r.Phase, &r.Seq, &r.RecordJSON, &r.PrevHash, &r.RecordHash); err != nil {
			return unreadable(err)
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (c *Case) CodeRunStats(ctx context.Context) (casestore.CodeStats, error) {
	if err := ctx.Err(); err != nil {
		return casestore.CodeStats{}, err
	}
	db, err := c.handle()
	if err != nil {
		return casestore.CodeStats{}, err
	}
	var s casestore.CodeStats
	err = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status='running'),0),
		COALESCE(SUM(length(CAST(source_ts AS BLOB)) + length(CAST(console_logs AS BLOB))),0) FROM code_runs`).Scan(&s.Runs, &s.OpenRuns, &s.TextBytes)
	if noTable(err) {
		return casestore.CodeStats{}, nil
	}
	if err != nil {
		return s, c.mapDBErr(err)
	}
	err = db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(length(result_blob)),0) FROM code_run_calls").Scan(&s.Calls, &s.BlobBytes)
	return s, c.mapDBErr(err)
}

// ---------------------------------------------------------------------------
// Verification.

func (c *Case) VerifyCodeChain(ctx context.Context, anchor *casestore.ChainAnchor) (casestore.ChainReport, error) {
	if err := ctx.Err(); err != nil {
		return casestore.ChainReport{}, err
	}
	db, err := c.handle()
	if err != nil {
		return casestore.ChainReport{}, err
	}
	// One read transaction, so a write that lands during the check cannot
	// make a sound record look torn.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return casestore.ChainReport{}, c.mapDBErr(err)
	}
	defer func() { _ = tx.Rollback() }()
	rep, err := casestore.VerifyCodeChain(ctx, codeSource{c: c, q: tx}, anchor)
	return rep, c.mapDBErrIfDB(err)
}

// codeSource reads the tables raw for VerifyCodeChain.
type codeSource struct {
	c *Case
	q txQuerier
}

var _ casestore.CodeChainSource = codeSource{}

func (s codeSource) EachChainRow(ctx context.Context, fn func(casestore.ChainRow) error) error {
	err := chainEach(ctx, s.q, "ORDER BY n", nil, fn)
	if noTable(err) {
		return nil
	}
	return err
}

func (s codeSource) EachRunID(ctx context.Context, fn func(string) error) error {
	rows, err := s.q.QueryContext(ctx, "SELECT run_id FROM code_runs")
	if err != nil {
		if noTable(err) {
			return nil
		}
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return unreadable(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

func (s codeSource) LoadRun(ctx context.Context, id string) (casestore.CodeRun, bool, error) {
	r, err := scanRun(s.q.QueryRowContext(ctx, "SELECT "+runColsFull+" FROM code_runs WHERE run_id = ?", id), false)
	switch {
	case errors.Is(err, sql.ErrNoRows), noTable(err):
		return casestore.CodeRun{}, false, nil
	case err != nil:
		return r, false, unreadable(err)
	}
	return r, true, nil
}

func (s codeSource) LoadCalls(ctx context.Context, id string) ([]casestore.CodeCall, error) {
	return s.c.loadCalls(ctx, s.q, id, true)
}

func (s codeSource) CountCalls(ctx context.Context) (int64, error) {
	var n int64
	err := s.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM code_run_calls").Scan(&n)
	if noTable(err) {
		return 0, nil
	}
	return n, err
}

func (s codeSource) StoredHead(ctx context.Context) (int64, string, bool, error) {
	var v string
	err := s.q.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = ?", codeHeadKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if ns, hash, ok := strings.Cut(v, ":"); ok {
		if n, perr := strconv.ParseInt(ns, 10, 64); perr == nil {
			return n, hash, true, nil
		}
	}
	return -1, v, true, nil // unreadable head: it will not match the chain
}

func (s codeSource) MissingGuards(ctx context.Context) ([]string, error) {
	var tables int
	if err := s.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('code_chain','code_runs','code_run_calls')").Scan(&tables); err != nil {
		return nil, err
	}
	if tables == 0 {
		// Nothing was ever written, or everything was dropped: the markers tell which.
		var marked int
		if err := s.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM meta WHERE k IN ('code.schema', ?, ?)", codeHeadKey, codeHashKey).Scan(&marked); err != nil {
			return nil, err
		}
		if marked > 0 {
			return []string{"the code tables are gone, but the case still marks them as created"}, nil
		}
		return nil, nil
	}
	var bad []string
	if tables != 3 {
		bad = append(bad, "one or more of the code tables is missing")
	}
	// Every trigger on a code table or on meta, with the name it has.
	rows, err := s.q.QueryContext(ctx, `SELECT name, tbl_name, COALESCE(sql,'') FROM sqlite_master WHERE type = 'trigger'
		AND tbl_name IN ('code_runs','code_run_calls','code_chain','meta')`)
	if err != nil {
		return nil, err
	}
	have := map[string]string{}
	var names []string
	for rows.Next() {
		var name, tbl, text string
		if err := rows.Scan(&name, &tbl, &text); err != nil {
			_ = rows.Close()
			return nil, unreadable(err)
		}
		have[name] = normalizeSQL(text)
		names = append(names, name+"\x00"+tbl)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, g := range codeGuards {
		known[g.name] = true
		if have[g.name] != g.sql { // missing, or a trigger of that name that does something else
			bad = append(bad, g.name)
		}
	}
	slices.Sort(names)
	for _, nt := range names {
		name, tbl, _ := strings.Cut(nt, "\x00")
		if !known[name] { // a trigger the store never made (RAISE(IGNORE) would switch the audit off)
			bad = append(bad, "unexpected trigger "+name+" on "+tbl)
		}
	}
	return bad, nil
}
