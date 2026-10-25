// Package sqlite is the SQLite case store: one adiantum-encrypted database per
// case, its key sealed with DPAPI in the case folder, and erase by destroying
// that key (storage decision, tier b; importer wave plan section 1.5).
//
// The package makes no network calls and logs names, hashes, counts and reasons
// only, never row contents.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum" // registers the "adiantum" VFS

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

const (
	keyName   = "case.key"
	dbName    = "case.db"
	retention = 30 * 24 * time.Hour

	// Defaults for the size caps (storage decision, "Size caps").
	defaultMaxFileBytes  = 2 << 30
	defaultMaxFileRows   = 10_000_000
	defaultMaxCaseBytes  = 4 << 30
	defaultMaxDBBytes    = 4 << 30
	defaultMaxTotalBytes = 20 << 30
	defaultMinFreeBytes  = 2 << 30

	// expansionRatio is a deliberately high guess at database bytes per source
	// byte, used only for the pre-import database size check when the body
	// reports its size.
	expansionRatio = 2
)

// Options configure a Store. Zero values take the defaults noted per field.
type Options struct {
	// Root is the folder that holds one sub-folder per case. Required. Use
	// DefaultRoot for the per-user location.
	Root string
	// Protector seals case.key and manifest.dat of a case sealed with DPAPI. Default:
	// DPAPI for the current user.
	Protector sealedfile.Protector
	// Protectors resolves the protector for a kind other than DPAPI, today only
	// casestore.KindMasterKey. It is called on every use, so it can answer "locked"
	// (return casestore.ErrLocked or any error) before the master key is unlocked
	// and the unlocked *masterkey.Keyring after. Nil means no master key: a case
	// sealed under it opens as casestore.ErrLocked.
	Protectors casestore.ProtectorLookup
	// NewCaseKind names the kind Create seals a new case with. Nil, or a result
	// other than casestore.KindMasterKey, means DPAPI (the v1 files an older build
	// can read).
	NewCaseKind func() string
	// Now is the clock. Default: time.Now.
	Now func() time.Time
	// MaxFileBytes and MaxFileRows cap one imported file (default 2 GiB and 10M
	// rows). An importer's own, smaller limit applies first.
	MaxFileBytes, MaxFileRows int64
	// MaxCaseBytes caps the imported files of one case, summed (default 4 GiB),
	// as casestore.Caps.MaxCaseBytes defines it. It is checked before an import
	// when the body reports its size, and again once the file has been read.
	MaxCaseBytes int64
	// MaxDBBytes caps one case database file (default 4 GiB, the same as the
	// default MaxCaseBytes). It is enforced inside SQLite with max_page_count and before
	// each import, and bounds the disk one case can take whatever the ratio of
	// database to source bytes.
	MaxDBBytes int64
	// MaxTotalBytes caps all cases together (default 20 GiB).
	MaxTotalBytes int64
	// MinFreeBytes is the free-disk floor checked before an import (default 2
	// GiB). A negative value disables the check.
	MinFreeBytes int64
	// OnErase runs in Erase after the case key and files are gone, and before the
	// folder is removed. The API layer wires ledger evidence stripping ("case
	// erased") and the deletion of that case's exports here. It must be
	// idempotent: it runs on every Erase attempt, and Sweep retries it for any
	// folder whose Erase did not finish. An error is reported by Erase, and the
	// folder stays (holding only a pending-erase marker) until a retry succeeds.
	OnErase func(ctx context.Context, id casestore.CaseID) error
	// Logf receives one line per import and per erase: case ID, file name, hash,
	// counts and reasons only. Default: no logging.
	Logf func(format string, args ...any)

	afterCommit func() error             // test hook, runs between commit and manifest mirror
	rewrapStep  func(stage string) error // test hook, see casestore.RewrapOptions.Step
	duringErase func()                   // test hook, runs in Erase after the handle is released, before any file is touched
}

// DefaultRoot is %LOCALAPPDATA%\APIAuditExplorer\cases, so multi-GiB case files
// never roam with the profile.
func DefaultRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "APIAuditExplorer", "cases"), nil
}

// Store implements casestore.Store on SQLite.
type Store struct {
	opt  Options
	root string

	mu      sync.Mutex // guards open and erasing, and serialises Open with the start of Erase
	open    map[casestore.CaseID]*caseState
	erasing map[casestore.CaseID]bool // IDs with an Erase in flight; Open refuses them
}

var _ casestore.Store = (*Store)(nil)

// New prepares the cases folder and returns a Store. A folder New creates gets a
// protected DACL (current user and SYSTEM only).
func New(opt Options) (*Store, error) {
	if opt.Root == "" {
		return nil, fmt.Errorf("%w: case store root is required", ErrInvalid)
	}
	root, err := filepath.Abs(opt.Root)
	if err != nil {
		return nil, err
	}
	if opt.Protector == nil {
		p, err := casestore.DefaultProtector()
		if err != nil {
			return nil, err
		}
		opt.Protector = p
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.MaxFileBytes <= 0 {
		opt.MaxFileBytes = defaultMaxFileBytes
	}
	if opt.MaxFileRows <= 0 {
		opt.MaxFileRows = defaultMaxFileRows
	}
	if opt.MaxCaseBytes <= 0 {
		opt.MaxCaseBytes = defaultMaxCaseBytes
	}
	if opt.MaxDBBytes <= 0 {
		opt.MaxDBBytes = defaultMaxDBBytes
	}
	if opt.MaxTotalBytes <= 0 {
		opt.MaxTotalBytes = defaultMaxTotalBytes
	}
	if opt.MinFreeBytes == 0 {
		opt.MinFreeBytes = defaultMinFreeBytes
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	// Root and every ancestor up to the volume must be plain folders. This is
	// checked before the ACL is applied, so the ACL never lands through a link.
	if err := casestore.RejectReparse(filepath.VolumeName(root)+string(filepath.Separator), root); err != nil {
		return nil, err
	}
	// The protected DACL goes on every time, not only on a folder New made: a
	// root left by an older build or made by hand would otherwise keep its
	// inherited ACL. SecureDir sets the same two entries each time, so a second
	// call changes nothing.
	if err := casestore.SecureDir(root); err != nil {
		return nil, err
	}
	return &Store{opt: opt, root: root, open: map[casestore.CaseID]*caseState{}, erasing: map[casestore.CaseID]bool{}}, nil
}

// ErrLocked is casestore.ErrLocked: the case is sealed under the master key and the
// master key is locked or not set up.
var ErrLocked = casestore.ErrLocked

// lookup is the casestore.ProtectorLookup over this Store's options.
func (s *Store) lookup(kind string) (sealedfile.Protector, error) {
	switch kind {
	case casestore.KindDPAPI:
		return s.opt.Protector, nil
	case casestore.KindMasterKey:
		if s.opt.Protectors != nil {
			return s.opt.Protectors(kind)
		}
	}
	return nil, ErrLocked
}

// protector returns the protector of kind bound to label, or ErrLocked.
func (s *Store) protector(kind, label string) (sealedfile.Protector, error) {
	return casestore.Resolve(s.lookup, kind, label)
}

// isLocked reports whether files sealed with kind cannot be opened now.
func (s *Store) isLocked(kind string) bool {
	p, err := s.lookup(kind)
	if err != nil || p == nil {
		return true
	}
	if l, ok := p.(interface{ Locked() bool }); ok {
		return l.Locked()
	}
	return false
}

// newKind is the kind Create seals with.
func (s *Store) newKind() string {
	if s.opt.NewCaseKind != nil && s.opt.NewCaseKind() == casestore.KindMasterKey {
		return casestore.KindMasterKey
	}
	return casestore.KindDPAPI
}

// caseKind is the protector kind a case folder is sealed with now: the manifest's,
// else the key file's, else DPAPI.
func caseKind(dir string) string {
	for _, name := range []string{manifestName, keyName} {
		if k, err := casestore.FileKind(filepath.Join(dir, name)); err == nil {
			return k
		}
	}
	return casestore.KindDPAPI
}

// fileSealer is the sealedfile.Protector readManifest and writeManifest take. It
// picks the protector from the kind recorded in the file, so one Store reads cases
// of both kinds, and writes a manifest in the kind the case is already sealed with.
type fileSealer struct {
	s    *Store
	dir  string
	kind string // required on read and used on write; "" = any on read, the case's current kind on write
}

// sealer is the manifest protector for the case folder dir. kind may be "".
func (s *Store) sealer(dir, kind string) fileSealer { return fileSealer{s: s, dir: dir, kind: kind} }

func (f fileSealer) Protect(plain []byte) ([]byte, error) {
	kind := f.kind
	if kind == "" {
		kind = caseKind(f.dir)
	}
	p, err := f.s.protector(kind, casestore.LabelManifest)
	if err != nil {
		return nil, err
	}
	blob, err := p.Protect(plain)
	if err != nil {
		return nil, err
	}
	return casestore.WrapSealed(kind, blob), nil
}

func (f fileSealer) Unprotect(raw []byte) ([]byte, error) {
	kind, blob, err := casestore.UnwrapSealed(raw)
	if err != nil {
		return nil, err
	}
	if f.kind != "" && kind != f.kind {
		return nil, casestore.ErrKeyFile
	}
	p, err := f.s.protector(kind, casestore.LabelManifest)
	if err != nil {
		return nil, err
	}
	return p.Unprotect(blob)
}

// Root is the absolute cases folder.
func (s *Store) Root() string { return s.root }

func (s *Store) now() time.Time { return s.opt.Now().UTC().Round(0) }

func (s *Store) logf(format string, args ...any) {
	if s.opt.Logf != nil {
		s.opt.Logf(format, args...)
	}
}

// caseDir joins the root and a validated ID and refuses any reparse point on the
// way (section 4 rule 7).
func (s *Store) caseDir(id casestore.CaseID) (string, error) {
	if !id.Valid() {
		return "", casestore.ErrBadID
	}
	dir := filepath.Join(s.root, string(id))
	if err := casestore.RejectReparse(s.root, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// cleanTitle trims the title and applies F0's rule (casestore.ValidTitle).
func cleanTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if !casestore.ValidTitle(title) {
		return "", casestore.ErrBadTitle
	}
	return title, nil
}

// Create makes a new case folder with its sealed key, an empty encrypted
// database and a manifest. The case is left closed (no handle held).
func (s *Store) Create(ctx context.Context, title string) (casestore.CaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return casestore.CaseInfo{}, err
	}
	title, err := cleanTitle(title)
	if err != nil {
		return casestore.CaseInfo{}, err
	}
	id, err := newID()
	if err != nil {
		return casestore.CaseInfo{}, err
	}
	dir := filepath.Join(s.root, string(id))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return casestore.CaseInfo{}, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = casestore.EraseKeyFile(filepath.Join(dir, keyName))
			_ = os.RemoveAll(dir)
		}
	}()
	if err := casestore.SecureDir(dir); err != nil {
		return casestore.CaseInfo{}, err
	}
	if err := casestore.RejectReparse(s.root, dir); err != nil {
		return casestore.CaseInfo{}, err
	}
	keys, err := casestore.NewKeys()
	if err != nil {
		return casestore.CaseInfo{}, err
	}
	defer keys.Zero()
	keys.CaseID = string(id)
	kind := s.newKind()
	base, err := s.lookup(kind)
	if err != nil || base == nil {
		return casestore.CaseInfo{}, ErrLocked
	}
	if err := casestore.WriteKeyFileKind(filepath.Join(dir, keyName), kind, base, keys); err != nil {
		return casestore.CaseInfo{}, err
	}
	now := s.now()
	if err := s.createDB(ctx, dir, keys, id, now); err != nil {
		return casestore.CaseInfo{}, err
	}
	man := manifest{CaseID: string(id), Title: title, Created: now, Imports: []manifestImport{}}
	if err := writeManifest(dir, s.sealer(dir, kind), man); err != nil {
		return casestore.CaseInfo{}, err
	}
	ok = true
	s.logf("case %s created", id)
	return casestore.CaseInfo{ID: id, Title: title, Created: now}, nil
}

// createDB builds the schema in a new database, then turns page checksums on
// while the file is still empty (it needs a VACUUM to retrofit).
func (s *Store) createDB(ctx context.Context, dir string, keys casestore.Keys, id casestore.CaseID, now time.Time) error {
	db, err := s.openDB(ctx, dir, keys, true)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, discoverSQL); err != nil {
		_ = tx.Rollback()
		return err
	}
	for k, v := range map[string]string{
		"schema_version": fmt.Sprint(schemaVersion),
		"case_id":        string(id),
		"created_utc":    now.Format(time.RFC3339),
	} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta(k, v) VALUES(?, ?)", k, v); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return conn.Raw(func(dc any) error {
		raw, ok := dc.(interface{ Raw() *sqlite3.Conn })
		if !ok {
			return errors.New("sqlite driver does not expose its connection")
		}
		return raw.Raw().EnableChecksums("main")
	})
}

// escapeURIPath makes a file path safe inside a "file:" URI. SQLite decodes %xx.
func escapeURIPath(p string) string {
	p = filepath.ToSlash(p)
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23", " ", "%20")
	return r.Replace(p)
}

// openDB opens case.db through the adiantum VFS. The key travels only through
// the connection hook, never the DSN, and an error from the key statement is
// replaced so it cannot carry the key text.
func (s *Store) openDB(ctx context.Context, dir string, keys casestore.Keys, create bool) (*sql.DB, error) {
	path := filepath.Join(dir, dbName)
	mode := "rw"
	if create {
		mode = "rwc"
	} else if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := "file:" + escapeURIPath(path) + "?vfs=adiantum&mode=" + mode
	hexKey := hex.EncodeToString(keys.DB)
	maxPages := s.opt.MaxDBBytes / pageSize
	if maxPages < 1 {
		maxPages = 1
	}
	db, err := driver.Open(dsn, func(c *sqlite3.Conn) error { return initConn(c, hexKey, maxPages) })
	if err != nil {
		return nil, errors.New("opening case database failed")
	}
	// Four connections, not one: an import keeps one dedicated connection (and
	// its transaction) for its whole run, and with a single connection every
	// read of the case (Events, Imports) would wait until the import finished.
	// Readers see the last commit (WAL). The key is held per connection, an
	// accepted cost. TestReadsDoNotWaitForImport pins this.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, mapOpenErr(err)
	}
	return db, nil
}

var errKeySetup = errors.New("setting the case database key failed")

// initConn is the per-connection hook: key first, then hardening (storage
// decision, "Hardening applied on every connection").
func initConn(c *sqlite3.Conn, hexKey string, maxPages int64) error {
	if err := c.Exec("PRAGMA hexkey='" + hexKey + "'"); err != nil {
		return errKeySetup
	}
	if _, err := c.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		return err
	}
	for _, stmt := range []string{
		"PRAGMA busy_timeout=10000",
		"PRAGMA trusted_schema=OFF",
		"PRAGMA secure_delete=ON",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cell_size_check=ON",
		fmt.Sprintf("PRAGMA max_page_count=%d", maxPages),
		"PRAGMA journal_mode=WAL",
		"PRAGMA journal_size_limit=67108864",
		"PRAGMA cache_size=-65536",
		"PRAGMA synchronous=NORMAL",
	} {
		if err := c.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// mapOpenErr turns a driver error into one of this package's errors. A wrong key
// surfaces as NOTADB on the first read.
func mapOpenErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errKeySetup):
		return errKeySetup
	case errors.Is(err, sqlite3.NOTADB):
		return ErrWrongKey
	case errors.Is(err, sqlite3.CORRUPT), errors.Is(err, sqlite3.IOERR_DATA):
		return ErrDamaged
	}
	return fmt.Errorf("opening case database: %w", err)
}

// List returns every case, oldest first (F0 contract). A folder whose manifest
// cannot be read is listed without a title, so it can still be erased. Bytes is
// the sum of the imported files, as Info reports it.
func (s *Store) List(ctx context.Context) ([]casestore.CaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := []casestore.CaseInfo{}
	for _, e := range entries {
		id := casestore.CaseID(e.Name())
		if !e.IsDir() || !validID(id) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir := filepath.Join(s.root, e.Name())
		if casestore.RejectReparse(s.root, dir) != nil {
			continue
		}
		info := casestore.CaseInfo{ID: id, Title: "(unreadable case)"}
		if erasePending(dir) {
			info.Title = "(erase pending)"
			out = append(out, info)
			continue
		}
		if m, err := readManifest(dir, s.sealer(dir, "")); err == nil && m.CaseID == string(id) {
			info.Title, info.Created, info.Closed, info.Imports = m.Title, m.Created, m.Closed, len(m.Imports)
			info.Bytes = manifestBytes(m)
		} else if err != nil && caseKind(dir) == casestore.KindMasterKey && s.isLocked(casestore.KindMasterKey) {
			info.Title = "(locked)" // nothing else about a locked case is shown
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Open returns a new handle on the case. All handles of a case in a Store share
// one database pool, which closes with the last handle; each Open must be paired
// with a Close.
func (s *Store) Open(ctx context.Context, id casestore.CaseID) (casestore.Case, error) {
	if !id.Valid() {
		return nil, casestore.ErrBadID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.erasing[id] {
		return nil, casestore.ErrNotFound // an Erase is in flight: never revive the case
	}
	if st := s.open[id]; st != nil {
		st.mu.Lock()
		st.refs++
		st.mu.Unlock()
		return &Case{caseState: st}, nil
	}
	st, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	s.open[id] = st
	return &Case{caseState: st}, nil
}

// Close closes every case this Store still holds open and zeroes their keys.
// Handles that were not closed answer ErrHandleClosed afterwards. It is for
// shutdown (and for tests that leave handles open); it never touches files.
func (s *Store) Close() error {
	s.mu.Lock()
	open := s.open
	s.open = map[casestore.CaseID]*caseState{}
	s.mu.Unlock()
	var errs []error
	for _, st := range open {
		errs = append(errs, st.shutdown())
	}
	return errors.Join(errs...)
}

// load opens, verifies and reconciles one case (storage decision, "On open").
func (s *Store) load(ctx context.Context, id casestore.CaseID) (*caseState, error) {
	dir, err := s.caseDir(id)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, casestore.ErrNotFound
	}
	s.healHalfRewrap(id, dir)
	keys, man, err := s.readCaseFiles(dir, id)
	if err != nil {
		return nil, err
	}
	db, err := s.openDB(ctx, dir, keys, false)
	if err != nil {
		keys.Zero()
		return nil, err
	}
	st := &caseState{s: s, id: id, dir: dir, db: db, keys: keys, man: man, refs: 1, stop: make(chan struct{}), quit: make(chan struct{})}
	if err := (&Case{caseState: st}).verify(ctx); err != nil {
		_ = db.Close()
		keys.Zero()
		return nil, err
	}
	if st.idxPending {
		// A case from before schema 3 has no series-key indexes. Build them now, in
		// the background, so the first series query does not stall on a large case;
		// a query that arrives meanwhile waits for the same build.
		go (&Case{caseState: st}).ensureAttrIndexes()
	}
	return st, nil
}

// readCaseFiles reads the key and manifest under the folder lock, so an Erase in
// another process (which holds the lock while it destroys the key) is either
// finished or not yet started. A pending-erase marker means the case is gone.
// Both the key blob and the manifest must name this case.
func (s *Store) readCaseFiles(dir string, id casestore.CaseID) (casestore.Keys, manifest, error) {
	release, err := sealedfile.Lock(dir)
	if err != nil {
		return casestore.Keys{}, manifest{}, err
	}
	defer release()
	if erasePending(dir) {
		return casestore.Keys{}, manifest{}, casestore.ErrNotFound
	}
	if err := requirePlainFiles(dir); err != nil {
		return casestore.Keys{}, manifest{}, err
	}
	keys, kind, err := casestore.ReadKeyFileAny(filepath.Join(dir, keyName), s.lookup)
	if errors.Is(err, os.ErrNotExist) {
		return casestore.Keys{}, manifest{}, casestore.ErrNotFound // the key is gone: the case is erased
	}
	if err != nil {
		return casestore.Keys{}, manifest{}, err
	}
	// The manifest must be sealed the same way as the key. A mix is a damaged case
	// (a re-wrap that was cut short), never silently repaired here.
	if mk, mkErr := casestore.FileKind(filepath.Join(dir, manifestName)); mkErr == nil && mk != kind {
		keys.Zero()
		if mk == casestore.KindMasterKey && kind == casestore.KindDPAPI {
			// The one shape a cut re-wrap leaves behind; Store.Rewrap finishes it.
			return casestore.Keys{}, manifest{}, casestore.ErrNeedsRewrap
		}
		return casestore.Keys{}, manifest{}, casestore.ErrKeyFile
	}
	man, err := readManifest(dir, s.sealer(dir, kind))
	if err != nil {
		keys.Zero()
		if errors.Is(err, os.ErrNotExist) {
			return casestore.Keys{}, manifest{}, ErrDamaged
		}
		return casestore.Keys{}, manifest{}, err
	}
	if man.CaseID != string(id) || keys.CaseID != string(id) {
		keys.Zero()
		return casestore.Keys{}, manifest{}, ErrDamaged
	}
	return keys, man, nil
}

// requirePlainFiles refuses a case whose key, database, WAL, SHM or manifest is
// a link or other non-regular file (section 4 rule 7). A missing file is left
// for the readers to report.
func requirePlainFiles(dir string) error {
	for _, name := range []string{keyName, dbName, dbName + "-wal", dbName + "-shm", manifestName} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return casestore.ErrReparsePoint
		}
	}
	return nil
}

// CloseCase starts the 30-day retention clock. It is idempotent.
func (s *Store) CloseCase(ctx context.Context, id casestore.CaseID) error {
	dir, err := s.caseDir(id)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return casestore.ErrNotFound
	}
	release, err := sealedfile.Lock(dir)
	if err != nil {
		return err
	}
	defer release()
	if erasePending(dir) {
		return casestore.ErrNotFound
	}
	s.mu.Lock()
	h := s.open[id]
	s.mu.Unlock()
	// The on-disk manifest is the starting point even when this Store holds the
	// case open: another Store on the folder may have mirrored imports since.
	if k := caseKind(dir); k == casestore.KindMasterKey && s.isLocked(k) {
		return ErrLocked
	}
	m, err := readManifest(dir, s.sealer(dir, ""))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrDamaged
		}
		return err
	}
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	if m.Closed != nil {
		return nil
	}
	now := s.now()
	m.Closed = &now
	if err := writeManifest(dir, s.sealer(dir, ""), m); err != nil {
		return err
	}
	if h != nil {
		h.man = m
	}
	s.logf("case %s closed", id)
	return nil
}

// CaseKind reports which protector seals the case's key file: casestore.KindDPAPI
// or casestore.KindMasterKey. It reads the file header only, so it works while the
// master key is locked and reveals nothing else.
func (s *Store) CaseKind(id casestore.CaseID) (string, error) {
	dir, err := s.caseDir(id)
	if err != nil {
		return "", err
	}
	if erasePending(dir) {
		return "", casestore.ErrNotFound
	}
	k, err := casestore.FileKind(filepath.Join(dir, keyName))
	if errors.Is(err, os.ErrNotExist) {
		return "", casestore.ErrNotFound
	}
	return k, err
}

// Rewrap moves one case from DPAPI sealing to the master key: case.key and
// manifest.dat are re-sealed (atomically, see casestore.Rewrap), case.db is never
// touched. It needs the master key unlocked (ErrLocked otherwise, nothing
// written), is a no-op success for a case already on the master key, and works
// while the case is open in this Store. A failed Rewrap leaves the old files
// readable.
func (s *Store) Rewrap(ctx context.Context, id casestore.CaseID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.caseDir(id)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return casestore.ErrNotFound
	}
	if s.isLocked(casestore.KindMasterKey) {
		return ErrLocked
	}
	to, err := s.lookup(casestore.KindMasterKey)
	if err != nil || to == nil {
		return ErrLocked
	}
	s.mu.Lock()
	if s.erasing[id] {
		s.mu.Unlock()
		return casestore.ErrNotFound
	}
	h := s.open[id]
	s.mu.Unlock()
	return s.rewrapDir(id, dir, to, h)
}

// NeedsRewrap reports whether the case is in the half re-wrapped shape: the
// manifest is already sealed with the master key and case.key still with
// Windows (a re-wrap cut short). Store.Rewrap finishes it, and Open does so by
// itself while the master key is unlocked. It reads headers only.
func (s *Store) NeedsRewrap(id casestore.CaseID) bool {
	dir, err := s.caseDir(id)
	if err != nil || erasePending(dir) {
		return false
	}
	return halfRewrapped(dir)
}

func halfRewrapped(dir string) bool {
	mk, err1 := casestore.FileKind(filepath.Join(dir, manifestName))
	kk, err2 := casestore.FileKind(filepath.Join(dir, keyName))
	return err1 == nil && err2 == nil && mk == casestore.KindMasterKey && kk == casestore.KindDPAPI
}

// healHalfRewrap finishes a re-wrap that was cut after the manifest replace, so
// the case opens instead of being refused as damaged. It runs under Store.mu
// (from Open, h is nil there) and does nothing unless the case has exactly that
// shape, the master key is unlocked and Windows sealing is available. Failure is
// not reported here: readCaseFiles then answers casestore.ErrNeedsRewrap and the
// person can retry from the status screen.
func (s *Store) healHalfRewrap(id casestore.CaseID, dir string) {
	if s.opt.Protector == nil || !halfRewrapped(dir) || s.isLocked(casestore.KindMasterKey) {
		return
	}
	to, err := s.lookup(casestore.KindMasterKey)
	if err != nil || to == nil {
		return
	}
	if err := s.rewrapDir(id, dir, to, nil); err != nil {
		s.logf("case %s re-wrap could not be finished", id)
		return
	}
	s.logf("case %s re-wrap finished on open", id)
}

// rewrapDir is the Rewrap work for one case folder. h is the open handle of the
// case in this Store, or nil.
func (s *Store) rewrapDir(id casestore.CaseID, dir string, to sealedfile.Protector, h *caseState) error {
	// Lock order everywhere is folder lock, then the case's mu (as CloseCase does).
	// Holding the case mu keeps this Store's own manifest writers out meanwhile.
	opt := casestore.RewrapOptions{Step: s.opt.rewrapStep, Enter: func() (func(), error) {
		if erasePending(dir) {
			return nil, casestore.ErrNotFound
		}
		if h == nil {
			return func() {}, nil
		}
		h.mu.Lock()
		return h.mu.Unlock, nil
	}}
	err := casestore.Rewrap(dir,
		casestore.Sealing{Kind: casestore.KindDPAPI, P: s.opt.Protector},
		casestore.Sealing{Kind: casestore.KindMasterKey, P: to}, opt)
	if err != nil {
		s.logf("case %s re-wrap failed", id)
		return err
	}
	s.logf("case %s re-wrapped", id)
	return nil
}

// eraseMarker is the durable "an erase has started" record. It is written before
// the key is destroyed and removed, with the folder, only after OnErase has
// succeeded. A folder that holds it is retried by Erase and Sweep, so a crash or
// a failing hook can never lose the OnErase step.
const eraseMarker = "erase.pending"

func erasePending(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, eraseMarker))
	return err == nil
}

// Erase destroys a case: write the pending-erase marker, close the handle,
// overwrite and delete case.key (crypto-erase), delete the remaining files, run
// OnErase, then remove the marker and the folder. It is idempotent. A failure is
// returned and leaves the marker, so a later Erase or Sweep finishes the job.
// While an Erase is in flight Open refuses the case.
func (s *Store) Erase(ctx context.Context, id casestore.CaseID) error {
	dir, err := s.caseDir(id)
	if err != nil {
		return err // ErrBadID for a malformed ID
	}
	s.mu.Lock()
	if s.erasing[id] {
		s.mu.Unlock()
		return fmt.Errorf("case %s: erase already in progress", id)
	}
	s.erasing[id] = true
	h := s.open[id]
	delete(s.open, id)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.erasing, id)
		s.mu.Unlock()
	}()
	if h != nil {
		h.mu.Lock()
		h.erased = true // open handles now answer ErrNotFound, not ErrHandleClosed
		h.mu.Unlock()
		_ = h.shutdown()
	}
	if s.opt.duringErase != nil {
		s.opt.duringErase()
	}
	var errs []error
	if _, statErr := os.Lstat(dir); statErr == nil {
		release, lockErr := sealedfile.Lock(dir)
		if lockErr != nil {
			return lockErr
		}
		markErr := markErase(dir)
		if markErr == nil {
			errs = append(errs, eraseFiles(dir))
		} else {
			errs = append(errs, markErr) // nothing destroyed yet: the case is intact
		}
		release()
		if markErr != nil {
			s.logf("case %s erase not started", id)
			return errors.Join(errs...)
		}
	}
	if s.opt.OnErase != nil {
		errs = append(errs, s.opt.OnErase(ctx, id))
	}
	if err := errors.Join(errs...); err != nil {
		s.logf("case %s erase incomplete", id)
		return err
	}
	if err := removeMarkerAndDir(dir); err != nil {
		s.logf("case %s erase incomplete", id)
		return err
	}
	s.logf("case %s erased", id)
	return nil
}

// markErase writes and flushes the pending-erase marker.
func markErase(dir string) error {
	f, err := os.OpenFile(filepath.Join(dir, eraseMarker), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("recording the pending erase: %w", err)
	}
	if _, err := f.WriteString("erase pending\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("recording the pending erase: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("recording the pending erase: %w", err)
	}
	return f.Close()
}

func removeMarkerAndDir(dir string) error {
	if err := os.Remove(filepath.Join(dir, eraseMarker)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(dir)
}

// eraseFiles is steps 2 and 3 of the erase order. The key goes first, so even a
// failure later leaves only ciphertext behind. Everything in the folder goes
// except the pending-erase marker, which the caller removes last.
func eraseFiles(dir string) error {
	var errs []error
	if err := casestore.EraseKeyFile(filepath.Join(dir, keyName)); err != nil {
		errs = append(errs, errors.New("destroying the case key failed"))
	}
	for _, name := range []string{dbName, dbName + "-wal", dbName + "-shm", manifestName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil {
		errs = append(errs, err)
	} else {
		for _, e := range entries {
			if e.Name() == eraseMarker {
				continue
			}
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Sweep erases every case closed more than 30 days before now, and finishes any
// erase that was started but not completed, and returns their IDs. One failure
// does not stop the others.
func (s *Store) Sweep(ctx context.Context, now time.Time) ([]casestore.CaseID, error) {
	s.sweepRestoreStaging(now)
	infos, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var erased []casestore.CaseID
	var errs []error
	for _, info := range infos {
		pending := erasePending(filepath.Join(s.root, string(info.ID)))
		if !pending && (info.Closed == nil || now.Sub(*info.Closed) <= retention) {
			continue
		}
		if err := s.Erase(ctx, info.ID); err != nil {
			errs = append(errs, fmt.Errorf("case %s: %w", info.ID, err))
			continue
		}
		erased = append(erased, info.ID)
	}
	sort.Slice(erased, func(i, j int) bool { return erased[i] < erased[j] })
	return erased, errors.Join(errs...)
}

// restoreStagingMaxAge is how old a restore-<hex>.part staging file must be
// before Sweep removes it. A restore writes the file continuously, so one being
// written keeps a fresh modification time; an hour with no write is a crash.
const restoreStagingMaxAge = time.Hour

// sweepRestoreStaging removes restore-<16 hex>.part files that casestore's restore
// left in the cases folder (the staged database of a restore that was cut off).
// Only plain files of exactly that name shape and older than
// restoreStagingMaxAge go; a file that is in use (or cannot be removed) stays and
// is retried by the next Sweep. Nothing is reported: this is housekeeping.
func (s *Store) sweepRestoreStaging(now time.Time) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !isRestoreStaging(name) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(s.root, name))
		if err != nil || !fi.Mode().IsRegular() || now.Sub(fi.ModTime()) <= restoreStagingMaxAge {
			continue
		}
		if os.Remove(filepath.Join(s.root, name)) == nil {
			s.logf("removed a leftover restore staging file")
		}
	}
}

// isRestoreStaging matches "restore-" + 16 lower-case hex digits + ".part".
func isRestoreStaging(name string) bool {
	hexPart, ok := strings.CutPrefix(name, "restore-")
	if !ok {
		return false
	}
	hexPart, ok = strings.CutSuffix(hexPart, ".part")
	if !ok || len(hexPart) != 16 {
		return false
	}
	for _, c := range hexPart {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// dirBytes sums the regular files directly inside dir (database, WAL, SHM, key,
// manifest).
func dirBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
	}
	return n
}

// totalBytes sums every case folder under the root.
func (s *Store) totalBytes() int64 {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if e.IsDir() && validID(casestore.CaseID(e.Name())) {
			n += dirBytes(filepath.Join(s.root, e.Name()))
		}
	}
	return n
}
