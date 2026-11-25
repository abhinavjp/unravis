// Package mcpaudit is the small SQLite file of the MCP area (spec 03 D10): mcp.db in %LOCALAPPDATA%\Unravis\mcp. BL-346 puts the grants
// in it (profiles, meta). The call ledger (BL-351) and the evidence aliases (BL-349) add their tables later, with CREATE TABLE IF NOT
// EXISTS, so a file written by this version opens in the next one.
//
// The file holds no audit data, no raw arguments and no credential: a profile row keeps only a keyed digest of the host's secret and of
// its claim nonce. The key of those digests (serverKey) lives in the meta table sealed for the current user (DPAPI on Windows), so a
// copy of mcp.db on another account or PC can neither be read for secrets (there are none) nor be used to forge a digest.
package mcpaudit

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

// FileName is the database file inside the MCP folder.
const FileName = "mcp.db"

const (
	metaServerKey = "serverKey"
	metaRevFloor  = "revFloor" // the highest revision any removed profile had: a new profile starts above it (S3)
	keyLen        = 32
	schemaVersion = 5 // 2: profiles.contested; 3: audit_events; 4: aliases; 5: profiles.code
)

// ErrNewerVersion means the file was written by a later version of Unravis (a downgrade). It is not set aside: the grants in it are
// valid for the newer version, and the person decides.
var ErrNewerVersion = errors.New("mcpaudit: the store was written by a newer version")

// ErrBadFolder means the folder is not an absolute local path.
var ErrBadFolder = errors.New("mcpaudit: the folder must be an absolute local path")

// ErrNoProtector means the platform cannot seal the server key (not Windows, or a nil Protector was passed): the store does not
// open, because an unsealed key in the file would make the digests forgeable by anyone who can read it.
var ErrNoProtector = errors.New("mcpaudit: sealing is not available, the grant store stays closed")

// DB is the open grant store. It is safe for concurrent use.
type DB struct {
	sql    *sql.DB
	kmu    sync.RWMutex // guards key against Close wiping it while MAC runs
	closed bool         // Close wiped the key: MAC answers random bytes from then on
	key    []byte
	amu    sync.Mutex // one writer of the aliases table at a time
}

// Open opens (and creates) dir/mcp.db. dir must be an absolute local path (no relative path, no UNC share). The folder gets the
// current-user-only ACL of casestore.SecureDir before anything is written (on Windows an inheritable DACL, so mcp.db, -wal and -shm
// created inside are covered; mode 0700 elsewhere), and the three files are restricted too. The caller passes the protector of the
// current user (sealedfile.CurrentUser()); sealedfile.LocalMachine() would let every account on the PC unseal the server key. If the file is
// unreadable (corrupt, or the server key cannot be unsealed because the file came from another account), it is renamed to
// mcp.db.bad-<unix> and an empty store is made: every host must be approved again, which fails closed (spec 03 section 3.7). The
// new name is returned as recovered, empty when nothing was set aside.
func Open(ctx context.Context, dir string, prot sealedfile.Protector, now func() time.Time) (db *DB, recovered string, err error) {
	if prot == nil {
		return nil, "", ErrNoProtector
	}
	if now == nil {
		now = time.Now
	}
	if !filepath.IsAbs(dir) || strings.HasPrefix(dir, `\\`) || strings.HasPrefix(dir, "//") {
		return nil, "", ErrBadFolder
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", errors.New("mcpaudit: cannot create the folder")
	}
	if err := casestore.SecureDir(dir); err != nil {
		return nil, "", errors.New("mcpaudit: restricting the folder failed")
	}
	path := filepath.Join(dir, FileName)
	db, err = openFile(ctx, path, prot)
	if err == nil {
		return db, "", nil
	}
	if errors.Is(err, ErrNewerVersion) {
		return nil, "", err
	}
	if _, serr := os.Stat(path); serr != nil {
		return nil, "", err // nothing to set aside: a real failure (folder, disk)
	}
	bad := fmt.Sprintf("%s.bad-%d", path, now().Unix())
	if rerr := os.Rename(path, bad); rerr != nil {
		return nil, "", errors.New("mcpaudit: the store is unreadable and could not be set aside")
	}
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Rename(path+ext, bad+ext)
	}
	db, err = openFile(ctx, path, prot)
	if err != nil {
		return nil, "", err
	}
	return db, bad, nil
}

func openFile(ctx context.Context, path string, prot sealedfile.Protector) (*DB, error) {
	// Create the file 0600 before SQLite does: the -wal and -shm files it makes take the mode of the main file.
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err == nil { //nolint:gosec // the store's own file in its own folder
		_ = f.Close()
	} else {
		return nil, errors.New("mcpaudit: creating the store failed")
	}
	dsn := "file:" + escapeURIPath(path) + "?mode=rwc"
	sdb, err := driver.Open(dsn, initConn)
	if err != nil {
		return nil, errors.New("mcpaudit: opening the store failed")
	}
	sdb.SetMaxOpenConns(4)
	sdb.SetMaxIdleConns(1)
	sdb.SetConnMaxIdleTime(30 * time.Second)
	d := &DB{sql: sdb}
	if err := d.init(ctx, prot); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, serr := os.Stat(p); serr != nil {
			continue // the -wal and -shm files exist only while the store is open
		}
		if err := casestore.SecureFile(p); err != nil {
			_ = sdb.Close()
			return nil, errors.New("mcpaudit: restricting the store file failed")
		}
	}
	return d, nil
}

func initConn(c *sqlite3.Conn) error {
	if _, err := c.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		return err
	}
	for _, stmt := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA trusted_schema=OFF",
		"PRAGMA secure_delete=ON",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cell_size_check=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL", // a grant change is on disk before it is acknowledged: a revoke must survive a crash
	} {
		if err := c.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func escapeURIPath(p string) string {
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23", " ", "%20")
	return r.Replace(filepath.ToSlash(p))
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v BLOB NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS profiles(
  id TEXT PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 32),
  state TEXT NOT NULL CHECK (state IN ('pending','active','revoked','denied')),
  rev INTEGER NOT NULL,
  host_name TEXT NOT NULL DEFAULT '',
  host_version TEXT NOT NULL DEFAULT '',
  exe TEXT NOT NULL DEFAULT '',
  ppid INTEGER NOT NULL DEFAULT 0,
  tier TEXT NOT NULL DEFAULT '',
  tools TEXT NOT NULL DEFAULT '[]',
  sources TEXT NOT NULL DEFAULT '[]',
  identifiers INTEGER NOT NULL DEFAULT 0,
  identifiers_until INTEGER NOT NULL DEFAULT 0,
  requested_identifiers INTEGER NOT NULL DEFAULT 0,
  idle_ttl_s INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  approved_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  last_call_at INTEGER NOT NULL DEFAULT 0,
  claimed INTEGER NOT NULL DEFAULT 0,
  secret_digest BLOB,
  claim_digest BLOB,
  contested INTEGER NOT NULL DEFAULT 0,
  code INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS audit_events(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  profile_id TEXT NOT NULL,
  profile_rev INTEGER NOT NULL,
  kind TEXT NOT NULL,
  tool TEXT NOT NULL DEFAULT '',
  op_class TEXT NOT NULL DEFAULT '',
  corr_id TEXT NOT NULL DEFAULT '',
  dur_ms INTEGER NOT NULL DEFAULT 0,
  outcome TEXT NOT NULL DEFAULT '',
  code TEXT NOT NULL DEFAULT '',
  bytes INTEGER NOT NULL DEFAULT 0,
  fingerprint BLOB
);
CREATE INDEX IF NOT EXISTS audit_ts ON audit_events(ts);
CREATE INDEX IF NOT EXISTS audit_profile ON audit_events(profile_id, ts);
`

func (d *DB) init(ctx context.Context, prot sealedfile.Protector) error {
	var ver int
	if err := d.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return errors.New("mcpaudit: the store is unreadable")
	}
	if ver > schemaVersion {
		return ErrNewerVersion
	}
	var qc string
	if err := d.sql.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&qc); err != nil || qc != "ok" {
		return errors.New("mcpaudit: the store is damaged")
	}
	if _, err := d.sql.ExecContext(ctx, schemaSQL+aliasSchemaSQL); err != nil {
		return errors.New("mcpaudit: creating the schema failed")
	}
	// Version 1 had no contested column.
	var has int
	if err := d.sql.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('profiles') WHERE name='contested'").Scan(&has); err != nil {
		return errors.New("mcpaudit: the store is unreadable")
	}
	if has == 0 {
		if _, err := d.sql.ExecContext(ctx, "ALTER TABLE profiles ADD COLUMN contested INTEGER NOT NULL DEFAULT 0"); err != nil {
			return errors.New("mcpaudit: upgrading the schema failed")
		}
	}
	// Version 5 added the code column (code mode as the grant's own permission): an older grant has none, which reads as not granted.
	if err := d.sql.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('profiles') WHERE name='code'").Scan(&has); err != nil {
		return errors.New("mcpaudit: the store is unreadable")
	}
	if has == 0 {
		if _, err := d.sql.ExecContext(ctx, "ALTER TABLE profiles ADD COLUMN code INTEGER NOT NULL DEFAULT 0"); err != nil {
			return errors.New("mcpaudit: upgrading the schema failed")
		}
	}
	if _, err := d.sql.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return errors.New("mcpaudit: creating the schema failed")
	}
	// serverKey: read and unseal, or make it.
	var sealed []byte
	err := d.sql.QueryRowContext(ctx, "SELECT v FROM meta WHERE k=?", metaServerKey).Scan(&sealed)
	switch {
	case err == nil:
		key, uerr := prot.Unprotect(sealed)
		if uerr != nil || len(key) != keyLen {
			return errors.New("mcpaudit: the server key cannot be unsealed")
		}
		d.key = key
	case errors.Is(err, sql.ErrNoRows):
		key := make([]byte, keyLen)
		if _, rerr := rand.Read(key); rerr != nil {
			return errors.New("mcpaudit: no random source")
		}
		sealed, perr := prot.Protect(key)
		if perr != nil || len(sealed) == 0 {
			return errors.New("mcpaudit: sealing the server key failed")
		}
		if _, ierr := d.sql.ExecContext(ctx, "INSERT INTO meta(k,v) VALUES(?,?)", metaServerKey, sealed); ierr != nil {
			return errors.New("mcpaudit: storing the server key failed")
		}
		d.key = key
	default:
		return errors.New("mcpaudit: the store is unreadable")
	}
	return nil
}

// Close closes the store and wipes the key held in memory.
func (d *DB) Close() error {
	d.kmu.Lock()
	for i := range d.key {
		d.key[i] = 0
	}
	d.closed = true
	d.kmu.Unlock()
	return d.sql.Close()
}

// MAC returns HMAC-SHA256(serverKey, domain || 0x00 || data). The domain keeps a digest made for one purpose from being replayed as
// another (a claim nonce is never a valid grant secret).
func (d *DB) MAC(domain string, data []byte) []byte {
	d.kmu.RLock()
	defer d.kmu.RUnlock()
	if d.closed {
		// The key is wiped: a digest under the zero key would be one anyone can make. A fresh random answer matches nothing and
		// opens nothing.
		out := make([]byte, sha256.Size)
		_, _ = rand.Read(out)
		return out
	}
	m := hmac.New(sha256.New, d.key)
	m.Write([]byte(domain))
	m.Write([]byte{0})
	m.Write(data)
	return m.Sum(nil)
}

// SetMeta and Meta keep small flags (the global stop). Values are not secrets.
func (d *DB) SetMeta(ctx context.Context, k string, v []byte) error {
	if k == metaServerKey || k == metaRevFloor {
		return errors.New("mcpaudit: reserved key")
	}
	_, err := d.sql.ExecContext(ctx, "INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", k, v)
	return err
}

func (d *DB) Meta(ctx context.Context, k string) ([]byte, bool, error) {
	if k == metaServerKey || k == metaRevFloor {
		return nil, false, errors.New("mcpaudit: reserved key")
	}
	return d.metaRaw(ctx, k)
}

func (d *DB) metaRaw(ctx context.Context, k string) ([]byte, bool, error) {
	var v []byte
	err := d.sql.QueryRowContext(ctx, "SELECT v FROM meta WHERE k=?", k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return v, err == nil, err
}
