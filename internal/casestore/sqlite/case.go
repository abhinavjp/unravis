package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	sqlite3 "github.com/ncruces/go-sqlite3"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

const (
	// maxEvents is the most one Events call returns (plan section 1.5).
	maxEvents = 50000
	// maxFilter bounds each Kinds, Sources and Hosts list.
	maxFilter = 200

	maxAckText   = 4096
	maxLabelRune = 128
	maxNameBytes = 255
)

var importerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// caseState is what every handle of one case shares: the database pool, keys,
// manifest and the import lock. A running import holds one pooled connection
// inside its transaction; readers use the others and, in WAL mode, see only
// what was committed before they started.
type caseState struct {
	s    *Store
	id   casestore.CaseID
	dir  string
	refs int // open handles; guarded by mu

	importMu sync.Mutex

	mu     sync.Mutex // guards the fields below
	db     *sql.DB
	keys   casestore.Keys
	man    manifest
	erased bool // Erase destroyed the case: handles answer ErrNotFound, not ErrHandleClosed
}

// Case is one handle from Store.Open. Handles of the same case share a
// caseState; closing one handle leaves the others working.
type Case struct {
	*caseState
	closed atomic.Bool
}

var (
	_ casestore.Case        = (*Case)(nil)
	_ casestore.Quarantiner = (*Case)(nil)
)

func (c *Case) handle() (*sql.DB, error) {
	if c.closed.Load() {
		return nil, ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		if c.erased {
			return nil, casestore.ErrNotFound
		}
		return nil, ErrClosed
	}
	return c.db, nil
}

// manifestBytes sums the bytes of the imports a manifest lists: what the case
// holds, as the plan's per-case cap counts it (not the size of the database).
func manifestBytes(m manifest) int64 {
	var n int64
	for _, im := range m.Imports {
		n += im.Bytes
	}
	return n
}

// Info reports the case summary. Bytes is the total size of the imported files
// (quarantined, duplicate and failed files do not count).
func (c *Case) Info() casestore.CaseInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return casestore.CaseInfo{
		ID: c.id, Title: c.man.Title, Created: c.man.Created, Closed: c.man.Closed,
		Bytes: manifestBytes(c.man), Imports: len(c.man.Imports),
	}
}

// Close releases this handle; closing twice is harmless. The database closes
// when the last handle of the case is closed.
func (c *Case) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.mu.Lock()
	if c.db == nil {
		c.mu.Unlock()
		return nil
	}
	c.refs--
	last := c.refs <= 0
	c.mu.Unlock()
	if !last {
		return nil
	}
	if c.s.open[c.id] == c.caseState {
		delete(c.s.open, c.id)
	}
	return c.shutdown()
}

// shutdown closes the database and zeroes the keys. Erase calls it directly.
func (c *caseState) shutdown() error {
	c.importMu.Lock() // let a running import finish first
	defer c.importMu.Unlock()
	c.mu.Lock()
	db := c.db
	c.db = nil
	c.mu.Unlock()
	if db == nil {
		return nil
	}
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err := db.Close()
	c.mu.Lock()
	c.keys.Zero()
	c.mu.Unlock()
	return err
}

// Pseudonymize is HMAC-SHA256 under the case pseudonym key, shown as
// "<kind>-<8 hex>". The key lives in case.key and dies with it.
//
// An empty value maps to "" (there is nothing to hide). After Close, Erase or
// Sweep the keys are gone and the method fails closed, as F0's fake does: it
// returns "", never a value derived from a zeroed key and never the input. (The
// casestore.Case signature has no error to return.)
func (c *Case) Pseudonymize(kind, value string) string {
	if value == "" || c.closed.Load() {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil || len(c.keys.Pseudo) != 32 || allZero(c.keys.Pseudo) {
		return ""
	}
	m := hmac.New(sha256.New, c.keys.Pseudo)
	m.Write([]byte(kind + "\x00" + value))
	return kind + "-" + hex.EncodeToString(m.Sum(nil))[:8]
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// verify is the open-time check: quick_check, schema version, and the manifest
// against the database. A database that is missing something the manifest
// records is damaged. A database that is ahead of the manifest (a crash between
// commit and mirror) is accepted and the manifest is rebuilt from it.
func (c *Case) verify(ctx context.Context) error {
	rows, err := c.db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return mapOpenErr(err)
	}
	ok := false
	for i := 0; rows.Next(); i++ {
		var line string
		if err := rows.Scan(&line); err != nil || line != "ok" || i > 0 {
			_ = rows.Close()
			return ErrDamaged
		}
		ok = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || !ok {
		return ErrDamaged
	}
	// The database must belong to this folder's case. The key blob and manifest
	// were already matched in readCaseFiles; this catches a database swapped in
	// under a key that was copied along with it.
	var dbCase string
	if err := c.db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k='case_id'").Scan(&dbCase); err != nil || dbCase != string(c.id) {
		return ErrDamaged
	}
	var version string
	if err := c.db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k='schema_version'").Scan(&version); err != nil {
		return ErrDamaged
	}
	v, err := strconv.Atoi(version)
	if err != nil {
		return ErrDamaged
	}
	if v > schemaVersion {
		return ErrNewerSchema
	}
	if v < 1 {
		return ErrDamaged
	}
	dbImports, err := c.dbManifestImports(ctx)
	if err != nil {
		return ErrDamaged
	}
	for i, want := range c.man.Imports {
		if i >= len(dbImports) || !sameImport(dbImports[i], want) {
			return ErrDamaged
		}
	}
	if len(dbImports) > len(c.man.Imports) {
		c.man.Imports = dbImports
		c.rebuildManifest()
	}
	return nil
}

// rebuildManifest writes c.man under the folder lock, taking the on-disk
// manifest's Closed stamp so a CloseCase in another process is not undone. It is
// best effort; the next open or import retries.
func (c *Case) rebuildManifest() {
	release, err := sealedfile.Lock(c.dir)
	if err != nil {
		return
	}
	defer release()
	if disk, err := readManifest(c.dir, c.s.opt.Protector); err == nil && disk.CaseID == string(c.id) {
		c.man.Closed = disk.Closed
	}
	_ = writeManifest(c.dir, c.s.opt.Protector, c.man)
}

// dbManifestImports reads the imports table with per-import event counts, in the
// manifest's shape.
func (c *Case) dbManifestImports(ctx context.Context) ([]manifestImport, error) {
	counts := map[int64]int64{}
	crows, err := c.db.QueryContext(ctx, "SELECT import_id, COUNT(*) FROM events GROUP BY import_id")
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var id, n int64
		if err := crows.Scan(&id, &n); err != nil {
			_ = crows.Close()
			return nil, err
		}
		counts[id] = n
	}
	err = crows.Err()
	_ = crows.Close()
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT id, importer, file_name, sha256, bytes, rows, rejected,
		host_label, tz_rule, imported_utc, status, ack_gating, ack_text FROM imports ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []manifestImport{}
	for rows.Next() {
		var m manifestImport
		var ms, gating int64
		var host, tz sql.NullString
		var bytes, nrows, rejected sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Importer, &m.FileName, &m.SHA256, &bytes, &nrows, &rejected,
			&host, &tz, &ms, &m.Status, &gating, &m.AckText); err != nil {
			return nil, err
		}
		m.Bytes, m.Rows, m.Rejected = bytes.Int64, nrows.Int64, rejected.Int64
		m.HostLabel, m.TZRule = host.String, tz.String
		m.Imported = time.UnixMilli(ms).UTC()
		m.AckGating = gating == 1
		m.Events = counts[m.ID]
		out = append(out, m)
	}
	return out, rows.Err()
}

// IntegrityCheck runs the full PRAGMA integrity_check (the maintenance menu
// item). Anything but "ok" is ErrDamaged.
func (c *Case) IntegrityCheck(ctx context.Context) error {
	db, err := c.handle()
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return ErrDamaged
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil || line != "ok" {
			return ErrDamaged
		}
		n++
	}
	if rows.Err() != nil || n != 1 {
		return ErrDamaged
	}
	return nil
}

func capErr(what string) error { return fmt.Errorf("%w: %s", casestore.ErrCapExceeded, what) }

// cleanText drops control characters and cuts to max runes.
func cleanText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if n++; n > max {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// cleanName keeps the display base name only (section 4 rule 7).
func cleanName(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = cleanText(s, 1<<16)
	for len(s) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	if s == "" {
		return "import"
	}
	return s
}

func cleanMeta(m casestore.ImportMeta) (casestore.ImportMeta, error) {
	if !m.AckGating || strings.TrimSpace(m.AckText) == "" {
		return m, casestore.ErrAckRequired
	}
	// An empty importer is allowed: the events then carry their own Source and
	// each is checked against it (F0 contract).
	if m.Importer != "" && !importerName.MatchString(m.Importer) {
		return m, fmt.Errorf("%w: importer name", ErrInvalid)
	}
	if len(m.AckText) > maxAckText {
		return m, fmt.Errorf("%w: acknowledgement text too long", ErrInvalid)
	}
	m.FileName = cleanName(m.FileName)
	m.HostLabel = cleanText(m.HostLabel, maxLabelRune)
	m.TZRule = cleanText(m.TZRule, maxLabelRune)
	return m, nil
}

// capReader counts bytes and fails once more than max have been read.
type capReader struct {
	r      io.Reader
	max, n int64
	err    error
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		c.err = capErr("file size")
		return n, c.err
	}
	return n, err
}

// importedBytes is the total size of the files imported so far.
func (c *Case) importedBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return manifestBytes(c.man)
}

// precheck refuses an import before any byte is read: free-disk floor, the size
// of this case's database, all cases' size, and, when the body reports its
// size, the file cap, the imported-bytes cap and the expected database growth.
func (c *Case) precheck(body io.Reader) error {
	o := c.s.opt
	if o.MinFreeBytes > 0 {
		if free, err := freeBytes(c.s.root); err == nil && free < uint64(o.MinFreeBytes) {
			return capErr("free disk space")
		}
	}
	used := dirBytes(c.dir)
	if used >= o.MaxDBBytes {
		return capErr("case database size")
	}
	if sz, ok := body.(interface{ Size() int64 }); ok {
		n := sz.Size()
		if n > o.MaxFileBytes {
			return capErr("file size")
		}
		if c.importedBytes()+n > o.MaxCaseBytes {
			return capErr("case size")
		}
		if used+n*expansionRatio > o.MaxDBBytes {
			return capErr("case database size")
		}
	}
	if c.s.totalBytes() >= o.MaxTotalBytes {
		return capErr("all cases size")
	}
	return nil
}

const insertEvent = `INSERT INTO events(evidence_id, import_id, source, kind, host, ts_ms, te_ms, duration_ms, value,
	unit, severity, line, tz_rule, attrs_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// Import runs run over body inside one transaction. Nothing is committed unless
// the file is accepted whole: a limit, a quarantine, a duplicate hash, a cap or
// any error rolls everything back.
//
// A quarantined file returns both a populated result (Status "quarantined") and
// an error satisfying errors.Is(err, importer.ErrQuarantine); the refusal is
// recorded in the quarantine table. A duplicate returns the earlier record with
// Status "duplicate" and casestore.ErrDuplicate.
func (c *Case) Import(ctx context.Context, meta casestore.ImportMeta, body io.Reader, run casestore.RunFunc) (casestore.ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return casestore.ImportResult{}, err
	}
	meta, err := cleanMeta(meta)
	if err != nil {
		return casestore.ImportResult{}, err
	}
	if body == nil || run == nil {
		return casestore.ImportResult{}, fmt.Errorf("%w: missing body or importer", ErrInvalid)
	}
	c.importMu.Lock()
	defer c.importMu.Unlock()
	db, err := c.handle()
	if err != nil {
		return casestore.ImportResult{}, err
	}
	release, err := sealedfile.Lock(c.dir)
	if err != nil {
		return casestore.ImportResult{}, fmt.Errorf("case is busy: %w", err)
	}
	defer release()
	if err := c.syncManifest(ctx, db); err != nil {
		return casestore.ImportResult{}, err
	}
	if err := c.precheck(body); err != nil {
		return casestore.ImportResult{}, err
	}

	hash := sha256.New()
	cr := &capReader{r: body, max: c.s.opt.MaxFileBytes}
	src := io.TeeReader(cr, hash)

	conn, err := db.Conn(ctx)
	if err != nil {
		return casestore.ImportResult{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return casestore.ImportResult{}, err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "PRAGMA synchronous=NORMAL") }()
	// On every exit, a failed or quarantined import included, give the WAL space
	// back so spilled frames never count against the case size cap.
	defer func() { _, _ = conn.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)") }()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return casestore.ImportResult{}, err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()

	started := c.s.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO imports(importer, file_name, sha256, bytes, rows, rejected, host_label,
		tz_rule, imported_utc, status, ack_gating, ack_text) VALUES(?, ?, 'pending', 0, 0, 0, ?, ?, ?, 'ok', 1, ?)`,
		meta.Importer, meta.FileName, meta.HostLabel, meta.TZRule, started.UnixMilli(), meta.AckText)
	if err != nil {
		return casestore.ImportResult{}, c.mapDBErr(err)
	}
	importID, err := res.LastInsertId()
	if err != nil {
		return casestore.ImportResult{}, err
	}
	stmt, err := tx.PrepareContext(ctx, insertEvent)
	if err != nil {
		return casestore.ImportResult{}, c.mapDBErr(err)
	}
	defer stmt.Close()

	caseID := string(c.id)
	var emitted int64
	// emitErr is sticky: the first refusal fails the import, and every later emit
	// repeats it, whether or not run passes the error on.
	var emitErr error
	emit := func(e evidence.Event) error {
		if emitErr != nil {
			return emitErr
		}
		if emitted >= c.s.opt.MaxFileRows {
			emitErr = capErr("file rows")
			return emitErr
		}
		who := meta.Importer
		if who == "" {
			who = e.Source
		}
		allowed := meta.AllowedAttrs
		if allowed == nil {
			// No allowlist supplied: only the generic bounds apply.
			allowed = make(map[string]bool, len(e.Attrs))
			for k := range e.Attrs {
				allowed[k] = true
			}
		}
		if err := e.ValidateFor(caseID, who, allowed); err != nil {
			emitErr = err
			return err
		}
		args, err := eventArgs(importID, e)
		if err != nil {
			emitErr = err
			return err
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			if errors.Is(err, sqlite3.CONSTRAINT_PRIMARYKEY) || errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
				emitErr = casestore.ErrDuplicateEvent
			} else {
				emitErr = c.mapDBErr(err)
			}
			return emitErr
		}
		emitted++
		return nil
	}
	rep, runErr := run(src, emit)
	if emitErr != nil {
		runErr = emitErr
	}
	if cr.err != nil {
		runErr = cr.err
	}
	if runErr == nil || errors.Is(runErr, importer.ErrQuarantine) || errors.Is(runErr, casestore.ErrDuplicateEvent) {
		// The hash must cover the whole file even if the importer stopped early.
		_, drainErr := io.Copy(io.Discard, src)
		if cr.err != nil {
			runErr = cr.err
		} else if drainErr != nil {
			runErr = drainErr // the body failed: no hash, so no import and no record
		}
	}
	if runErr == nil {
		if err := ctx.Err(); err != nil {
			runErr = err
		}
	}
	if runErr == nil && c.importedBytes()+cr.n > c.s.opt.MaxCaseBytes {
		runErr = capErr("case size")
	}
	if runErr != nil {
		_ = tx.Rollback()
		done = true
		switch {
		case errors.Is(runErr, importer.ErrQuarantine) && cr.err == nil:
			return c.quarantine(ctx, conn, meta, runErr, rep, cr, hash)
		case errors.Is(runErr, casestore.ErrDuplicateEvent) && cr.err == nil:
			// Re-importing the same file repeats its event IDs. Report that as a
			// duplicate file, not as a duplicate event, when the bytes are known.
			if res, err := c.duplicate(ctx, conn, hex.EncodeToString(hash.Sum(nil))); res.Record.ID != 0 {
				return res, err
			}
		}
		return casestore.ImportResult{}, c.mapDBErr(runErr)
	}
	if rep.Rows > c.s.opt.MaxFileRows {
		return casestore.ImportResult{}, capErr("file rows")
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	qualityJSON, err := json.Marshal(rep.Quality)
	if err != nil {
		return casestore.ImportResult{}, err
	}
	gaps := rep.KnownGaps
	if gaps == nil {
		gaps = []string{}
	}
	gapsJSON, err := json.Marshal(gaps)
	if err != nil {
		return casestore.ImportResult{}, err
	}
	meta.TZRule = cleanText(meta.DerivedTZRule(rep), maxLabelRune)
	if _, err := tx.ExecContext(ctx, `UPDATE imports SET sha256=?, bytes=?, rows=?, rejected=?, quality_json=?, gaps_json=?, tz_rule=?
		WHERE id=?`, sum, cr.n, rep.Rows, rep.Rejected, string(qualityJSON), string(gapsJSON), meta.TZRule, importID); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
			_ = tx.Rollback()
			done = true
			return c.duplicate(ctx, conn, sum)
		}
		return casestore.ImportResult{}, c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, indexSQL); err != nil {
		return casestore.ImportResult{}, c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		done = true
		return casestore.ImportResult{}, c.mapDBErr(err)
	}
	done = true

	rec := casestore.ImportRecord{
		ID: importID, Importer: meta.Importer, FileName: meta.FileName, SHA256: sum,
		Bytes: cr.n, Rows: rep.Rows, Rejected: rep.Rejected, HostLabel: meta.HostLabel, TZRule: meta.TZRule, AckText: meta.AckText,
		Quality: rep.Quality, KnownGaps: rep.KnownGaps, Imported: time.UnixMilli(started.UnixMilli()).UTC(), Status: "ok",
	}
	_, _ = conn.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	c.mirror(manifestImport{
		ID: importID, Importer: meta.Importer, FileName: meta.FileName, SHA256: sum, Bytes: cr.n, Rows: rep.Rows,
		Rejected: rep.Rejected, Events: emitted, HostLabel: meta.HostLabel, TZRule: meta.TZRule,
		Imported: rec.Imported, Status: "ok", AckGating: true, AckText: meta.AckText,
	})
	c.s.logf("case %s import %s file=%q sha256=%s rows=%d rejected=%d events=%d", c.id, meta.Importer, meta.FileName, sum, rep.Rows, rep.Rejected, emitted)
	return casestore.ImportResult{Record: rec, Report: rep}, nil
}

// mirror appends a committed import to the sealed manifest. A failure is not an
// import failure (the database is the truth): the next import or the next open
// notices the manifest is behind and rebuilds it from the database.
func (c *Case) mirror(m manifestImport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.man.Imports = append(c.man.Imports, m)
	if hook := c.s.opt.afterCommit; hook != nil && hook() != nil {
		return
	}
	_ = writeManifest(c.dir, c.s.opt.Protector, c.man)
}

// syncManifest runs under the folder lock before an import. It takes the
// on-disk manifest as the starting point (another Store on the same folder may
// have written it: another window, or a CloseCase), then rebuilds the import
// list from the database when the two disagree in count.
func (c *Case) syncManifest(ctx context.Context, db *sql.DB) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if disk, err := readManifest(c.dir, c.s.opt.Protector); err == nil && disk.CaseID == string(c.id) {
		c.man = disk
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM imports").Scan(&n); err != nil {
		return err
	}
	if n == len(c.man.Imports) {
		return nil
	}
	imports, err := c.dbManifestImports(ctx)
	if err != nil {
		return err
	}
	c.man.Imports = imports
	return writeManifest(c.dir, c.s.opt.Protector, c.man)
}

func sameImport(a, b manifestImport) bool {
	at, bt := a.Imported, b.Imported
	a.Imported, b.Imported = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

// mapDBErr maps a full database to ErrCapExceeded and leaves other errors alone.
func (c *Case) mapDBErr(err error) error {
	if errors.Is(err, sqlite3.FULL) {
		return capErr("case size")
	}
	return err
}

// duplicate answers an import whose hash is already in the case.
func (c *Case) duplicate(ctx context.Context, db queryer, sum string) (casestore.ImportResult, error) {
	recs, err := c.records(ctx, db, "WHERE sha256=?", sum)
	if err != nil || len(recs) == 0 {
		return casestore.ImportResult{}, casestore.ErrDuplicate
	}
	existing := recs[0]
	rec := existing
	rec.Status = casestore.StatusDuplicate
	return casestore.ImportResult{Record: rec}, &casestore.DuplicateError{Existing: existing}
}

// quarantine records a refused file after the import transaction rolled back.
// The body has been read to the end (so the hash covers all of it) and was
// within the byte cap. No raw bytes are kept, and the file does not count
// against the case.
func (c *Case) quarantine(ctx context.Context, db *sql.Conn, meta casestore.ImportMeta, cause error,
	rep importer.Report, cr *capReader, hash interface{ Sum([]byte) []byte }) (casestore.ImportResult, error) {
	var qe *importer.QuarantineError
	reason, column := importer.Reason(""), ""
	if errors.As(cause, &qe) {
		reason, column = qe.Reason, cleanText(qe.Column, maxLabelRune)
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	now := c.s.now()
	rec := casestore.ImportRecord{
		Importer: meta.Importer, FileName: meta.FileName, SHA256: sum, Bytes: cr.n, Rows: rep.Rows, Rejected: rep.Rejected,
		HostLabel: meta.HostLabel, TZRule: meta.TZRule, Quality: rep.Quality, KnownGaps: rep.KnownGaps,
		Imported: now, Status: casestore.StatusQuarantined, Reason: reason,
	}
	_, err := db.ExecContext(ctx, `INSERT INTO quarantine(importer, file_name, sha256, reason, column_name, rows_seen, at_utc)
		VALUES(?,?,?,?,?,?,?)`, meta.Importer, meta.FileName, sum, string(reason), column, rep.Rows, now.UnixMilli())
	if err != nil {
		c.s.logf("case %s quarantine record not stored file=%q sha256=%s reason=%s", c.id, meta.FileName, sum, reason)
	} else {
		c.s.logf("case %s quarantined %s file=%q sha256=%s reason=%s", c.id, meta.Importer, meta.FileName, sum, reason)
	}
	return casestore.ImportResult{Record: rec, Report: rep}, cause
}

// Quarantines lists the refused files, oldest first (casestore.Quarantiner).
func (c *Case) Quarantines(ctx context.Context) ([]casestore.QuarantineRecord, error) {
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, importer, file_name, sha256, reason, column_name, rows_seen, at_utc
		FROM quarantine ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []casestore.QuarantineRecord{}
	for rows.Next() {
		var q casestore.QuarantineRecord
		var imp, name, sum, reason, col sql.NullString
		var seen, at sql.NullInt64
		if err := rows.Scan(&q.ID, &imp, &name, &sum, &reason, &col, &seen, &at); err != nil {
			return nil, err
		}
		q.Importer, q.FileName, q.SHA256, q.Reason, q.Column = imp.String, name.String, sum.String, importer.Reason(reason.String), col.String
		q.Rows = seen.Int64
		q.At = time.UnixMilli(at.Int64).UTC()
		out = append(out, q)
	}
	return out, rows.Err()
}

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func eventArgs(importID int64, e evidence.Event) ([]any, error) {
	if e.EvidenceID == "" || e.Start.IsZero() {
		return nil, fmt.Errorf("%w: event needs an evidence ID and a start time", ErrInvalid)
	}
	end := e.End
	if end.IsZero() || end.Before(e.Start) {
		end = e.Start
	}
	var attrs any
	if len(e.Attrs) > 0 {
		b, err := json.Marshal(e.Attrs)
		if err != nil {
			return nil, err
		}
		attrs = string(b)
	}
	return []any{e.EvidenceID, importID, e.Source, e.Kind, e.Host, e.Start.UnixMilli(), end.UnixMilli(),
		nullFloat(e.DurationMs), nullFloat(e.Value), e.Unit, e.Severity, e.Line, e.TZRule, attrs}, nil
}

// Imports lists the committed imports in order.
func (c *Case) Imports(ctx context.Context) ([]casestore.ImportRecord, error) {
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	return c.records(ctx, db, "")
}

// queryer is what *sql.DB and *sql.Conn share. Import's helpers run on the
// connection Import holds, so they see its own state, not another pooled one.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (c *Case) records(ctx context.Context, db queryer, where string, args ...any) ([]casestore.ImportRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, importer, file_name, sha256, bytes, rows, rejected, host_label, tz_rule,
		quality_json, gaps_json, imported_utc, status, ack_text FROM imports `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []casestore.ImportRecord{}
	for rows.Next() {
		var r casestore.ImportRecord
		var bytes, nrows, rejected sql.NullInt64
		var host, tz, quality, gaps, ack sql.NullString
		var ms int64
		if err := rows.Scan(&r.ID, &r.Importer, &r.FileName, &r.SHA256, &bytes, &nrows, &rejected, &host, &tz,
			&quality, &gaps, &ms, &r.Status, &ack); err != nil {
			return nil, err
		}
		r.Bytes, r.Rows, r.Rejected = bytes.Int64, nrows.Int64, rejected.Int64
		r.HostLabel, r.TZRule, r.AckText = host.String, tz.String, ack.String
		r.Imported = time.UnixMilli(ms).UTC()
		if quality.Valid && quality.String != "" {
			if err := json.Unmarshal([]byte(quality.String), &r.Quality); err != nil {
				return nil, ErrDamaged
			}
		}
		if gaps.Valid && gaps.String != "" {
			if err := json.Unmarshal([]byte(gaps.String), &r.KnownGaps); err != nil {
				return nil, ErrDamaged
			}
			if len(r.KnownGaps) == 0 {
				r.KnownGaps = nil
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Events returns events whose Start lies in the window (inclusive), filtered,
// ordered by start time then evidence ID. Limit defaults to and never exceeds
// 50 000.
func (c *Case) Events(ctx context.Context, q casestore.EventQuery) ([]evidence.Event, error) {
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	var where []string
	var args []any
	if !q.Window.From.IsZero() {
		where = append(where, "ts_ms >= ?")
		args = append(args, q.Window.From.UnixMilli())
	}
	if !q.Window.To.IsZero() {
		where = append(where, "ts_ms <= ?")
		args = append(args, q.Window.To.UnixMilli())
	}
	for _, f := range []struct {
		col  string
		vals []string
	}{{"kind", q.Kinds}, {"source", q.Sources}, {"host", q.Hosts}} {
		if len(f.vals) == 0 {
			continue
		}
		if len(f.vals) > maxFilter {
			return nil, fmt.Errorf("%w: too many %s filters", ErrInvalid, f.col)
		}
		where = append(where, f.col+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.vals)), ",")+")")
		for _, v := range f.vals {
			args = append(args, v)
		}
	}
	limit := q.Limit
	if limit <= 0 || limit > maxEvents {
		limit = maxEvents
	}
	query := `SELECT evidence_id, source, kind, host, ts_ms, te_ms, duration_ms, value, unit, severity, line, tz_rule,
		attrs_json FROM events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY ts_ms, evidence_id LIMIT ?"
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.Event
	for rows.Next() {
		var e evidence.Event
		var source, kind, host, unit, severity, tz, attrs sql.NullString
		var ts, te int64
		var dur, val sql.NullFloat64
		var line sql.NullInt64
		if err := rows.Scan(&e.EvidenceID, &source, &kind, &host, &ts, &te, &dur, &val, &unit, &severity, &line, &tz, &attrs); err != nil {
			return nil, err
		}
		e.Source, e.Kind, e.Host, e.Unit, e.Severity, e.TZRule = source.String, kind.String, host.String, unit.String, severity.String, tz.String
		e.Start, e.End = time.UnixMilli(ts).UTC(), time.UnixMilli(te).UTC()
		if dur.Valid {
			v := dur.Float64
			e.DurationMs = &v
		}
		if val.Valid {
			v := val.Float64
			e.Value = &v
		}
		e.Line = line.Int64
		if attrs.Valid && attrs.String != "" {
			if err := json.Unmarshal([]byte(attrs.String), &e.Attrs); err != nil {
				return nil, ErrDamaged
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
