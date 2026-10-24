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
	"unicode"

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

	maxTitleRunes = 200

	// Defaults for the size caps (storage decision, "Size caps").
	defaultMaxFileBytes  = 2 << 30
	defaultMaxFileRows   = 10_000_000
	defaultMaxCaseBytes  = 4 << 30
	defaultMaxTotalBytes = 20 << 30
	defaultMinFreeBytes  = 2 << 30

	// expansionRatio is a deliberately high guess at database bytes per source
	// byte, used only for the pre-import size check when the body reports its size.
	expansionRatio = 2
)

// Options configure a Store. Zero values take the defaults noted per field.
type Options struct {
	// Root is the folder that holds one sub-folder per case. Required. Use
	// DefaultRoot for the per-user location.
	Root string
	// Protector seals case.key and manifest.dat. Default: DPAPI for the current user.
	Protector sealedfile.Protector
	// Now is the clock. Default: time.Now.
	Now func() time.Time
	// MaxFileBytes and MaxFileRows cap one imported file (default 2 GiB and 10M
	// rows). An importer's own, smaller limit applies first.
	MaxFileBytes, MaxFileRows int64
	// MaxCaseBytes caps one case database (default 4 GiB). It is enforced inside
	// SQLite with max_page_count as well as before each import.
	MaxCaseBytes int64
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

	afterCommit func() error // test hook, runs between commit and manifest mirror
	duringErase func()       // test hook, runs in Erase after the handle is released, before any file is touched
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
	open    map[casestore.CaseID]*Case
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
	if opt.MaxTotalBytes <= 0 {
		opt.MaxTotalBytes = defaultMaxTotalBytes
	}
	if opt.MinFreeBytes == 0 {
		opt.MinFreeBytes = defaultMinFreeBytes
	}
	_, statErr := os.Lstat(root)
	fresh := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if fresh {
		if err := casestore.SecureDir(root); err != nil {
			return nil, err
		}
	}
	// Root and every ancestor up to the volume must be plain folders.
	if err := casestore.RejectReparse(filepath.VolumeName(root)+string(filepath.Separator), root); err != nil {
		return nil, err
	}
	return &Store{opt: opt, root: root, open: map[casestore.CaseID]*Case{}, erasing: map[casestore.CaseID]bool{}}, nil
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
	if !validID(id) {
		return "", casestore.ErrNotFound
	}
	dir := filepath.Join(s.root, string(id))
	if err := casestore.RejectReparse(s.root, dir); err != nil {
		return "", err
	}
	return dir, nil
}

func cleanTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > maxTitleRunes {
		return "", fmt.Errorf("%w: case title must be 1 to %d characters", ErrInvalid, maxTitleRunes)
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: case title has control characters", ErrInvalid)
		}
	}
	return title, nil
}

// Create makes a new case folder with its sealed key, an empty encrypted
// database and a manifest. The case is left closed (no handle held).
func (s *Store) Create(ctx context.Context, title string) (casestore.CaseInfo, error) {
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
	if err := casestore.WriteKeyFile(filepath.Join(dir, keyName), s.opt.Protector, keys); err != nil {
		return casestore.CaseInfo{}, err
	}
	now := s.now()
	if err := s.createDB(ctx, dir, keys, id, now); err != nil {
		return casestore.CaseInfo{}, err
	}
	man := manifest{CaseID: string(id), Title: title, Created: now, Imports: []manifestImport{}}
	if err := writeManifest(dir, s.opt.Protector, man); err != nil {
		return casestore.CaseInfo{}, err
	}
	ok = true
	s.logf("case %s created", id)
	return casestore.CaseInfo{ID: id, Title: title, Created: now, Bytes: dirBytes(dir)}, nil
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
	maxPages := s.opt.MaxCaseBytes / pageSize
	if maxPages < 1 {
		maxPages = 1
	}
	db, err := driver.Open(dsn, func(c *sqlite3.Conn) error { return initConn(c, hexKey, maxPages) })
	if err != nil {
		return nil, errors.New("opening case database failed")
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
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

// List returns every readable case, newest first. A folder whose manifest cannot
// be read is listed without a title, so it can still be erased.
func (s *Store) List(ctx context.Context) ([]casestore.CaseInfo, error) {
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
		info := casestore.CaseInfo{ID: id, Title: "(unreadable case)", Bytes: dirBytes(dir)}
		if erasePending(dir) {
			info.Title = "(erase pending)"
			out = append(out, info)
			continue
		}
		if m, err := readManifest(dir, s.opt.Protector); err == nil && m.CaseID == string(id) {
			info.Title, info.Created, info.Closed, info.Imports = m.Title, m.Created, m.Closed, len(m.Imports)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Open returns the case handle. One handle exists per case in a Store, so the
// database keeps a single connection; each Open must be paired with a Close.
func (s *Store) Open(ctx context.Context, id casestore.CaseID) (casestore.Case, error) {
	if !validID(id) {
		return nil, casestore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.erasing[id] {
		return nil, casestore.ErrNotFound // an Erase is in flight: never revive the case
	}
	if c := s.open[id]; c != nil {
		c.mu.Lock()
		c.refs++
		c.mu.Unlock()
		return c, nil
	}
	c, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	s.open[id] = c
	return c, nil
}

// load opens, verifies and reconciles one case (storage decision, "On open").
func (s *Store) load(ctx context.Context, id casestore.CaseID) (*Case, error) {
	dir, err := s.caseDir(id)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, casestore.ErrNotFound
	}
	keys, man, err := s.readCaseFiles(dir, id)
	if err != nil {
		return nil, err
	}
	db, err := s.openDB(ctx, dir, keys, false)
	if err != nil {
		keys.Zero()
		return nil, err
	}
	c := &Case{s: s, id: id, dir: dir, db: db, keys: keys, man: man, refs: 1}
	if err := c.verify(ctx); err != nil {
		_ = db.Close()
		keys.Zero()
		return nil, err
	}
	return c, nil
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
	keys, err := casestore.ReadKeyFile(filepath.Join(dir, keyName), s.opt.Protector)
	if errors.Is(err, os.ErrNotExist) {
		return casestore.Keys{}, manifest{}, casestore.ErrNotFound // the key is gone: the case is erased
	}
	if err != nil {
		return casestore.Keys{}, manifest{}, err
	}
	man, err := readManifest(dir, s.opt.Protector)
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
	m, err := readManifest(dir, s.opt.Protector)
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
	if err := writeManifest(dir, s.opt.Protector, m); err != nil {
		return err
	}
	if h != nil {
		h.man = m
	}
	s.logf("case %s closed", id)
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
		if errors.Is(err, casestore.ErrNotFound) {
			return nil // not a case ID, so nothing of ours exists
		}
		return err
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
