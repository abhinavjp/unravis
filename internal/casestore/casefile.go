package casestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum" // registers the "adiantum" VFS

	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// A case file (.unravis-case) carries one case from one PC to another. It holds
// no plaintext and nothing tied to the exporting machine:
//
//	offset  size  field
//	0       8     magic "UNRVCASE"
//	8       2     format version (big endian), 1
//	10      1     KDF id, 1 = Argon2id
//	11      4     Argon2id time cost
//	15      4     Argon2id memory, KiB
//	19      1     Argon2id parallelism
//	20      1     salt length, 16
//	21      16    salt, fresh for every file
//	37      4     header ciphertext length
//	41      n     header: AES-256-GCM (masterkey.SealWith) of a JSON document
//	41+n    m     case.db, byte for byte (already encrypted under the DB key)
//
// The header key is Argon2id(passphrase, salt). Bytes 0..40 are bound into the
// AEAD as additional data, so a flipped bit anywhere in them, in the salt, the
// cost parameters, the version or the length, makes the header fail to open. The
// header holds the case's DB and pseudonym keys, the original id and title, the
// database size and its SHA-256, so the database part is verified before it is
// used. The passphrase is the master key of the exporting PC: a case file needs
// the master key it was exported with, not the destination's.
//
// Restore always makes a new case with a new id; nothing is ever merged into or
// written over an existing case.

// CaseFileExt is the extension of an exported case.
const CaseFileExt = ".unravis-case"

// CaseFileMessage is the only text a failure to open a case file produces.
const CaseFileMessage = "That file could not be opened with this master key."

// ErrCaseFile is returned for every reason a case file cannot be opened: wrong
// passphrase, damaged or tampered header, wrong magic or version, truncated or
// extended file, database bytes that do not match their hash. It reveals which of
// those it was to no one.
var ErrCaseFile = errors.New(CaseFileMessage)

var (
	// ErrCaseFileExists means the export destination already exists. Export never
	// overwrites.
	ErrCaseFileExists = errors.New("case file already exists")
	// ErrCaseFileChanged means the case database kept changing while it was read,
	// so no consistent copy could be made. The caller may try again.
	ErrCaseFileChanged = errors.New("case changed during export")
	// ErrCaseFileBusy means a consistent snapshot could not be taken in time.
	ErrCaseFileBusy = errors.New("case is busy")
)

const (
	caseFileMagic     = "UNRVCASE"
	caseFileVersion   = 1
	caseFileKDFArgon2 = 1
	caseFileSaltLen   = 16
	caseFilePrefixLen = 41
	// caseFileHeaderMax bounds the header ciphertext. A real one is under 1 KiB.
	caseFileHeaderMax = 64 << 10
	// MaxCaseFileDB bounds the database part of a case file: twice the default
	// case database cap. A larger claim is refused before any byte is written.
	MaxCaseFileDB = 8 << 30
	// caseFileLabel is the start of the AEAD label; the 41 prefix bytes follow.
	caseFileLabel = "unravis/casefile/header/v1\x00"

	caseFileCopyBuf = 1 << 20
	// caseFileAttempts is how often export re-reads a database that changed under
	// it before giving up.
	caseFileAttempts = 3
)

// caseFileHeader is the sealed JSON document.
type caseFileHeader struct {
	V         int        `json:"v"`
	CaseID    string     `json:"caseId"`
	Title     string     `json:"title"`
	Created   time.Time  `json:"created"`
	Closed    *time.Time `json:"closed,omitempty"`
	DBKey     string     `json:"dbKey"`
	PseudoKey string     `json:"pseudoKey"`
	DBBytes   int64      `json:"dbBytes"`
	DBSHA256  string     `json:"dbSha256"`
}

func caseFilePrefix(p masterkey.Params, salt []byte, headerLen int) []byte {
	b := make([]byte, caseFilePrefixLen)
	copy(b, caseFileMagic)
	binary.BigEndian.PutUint16(b[8:], caseFileVersion)
	b[10] = caseFileKDFArgon2
	binary.BigEndian.PutUint32(b[11:], p.Time)
	binary.BigEndian.PutUint32(b[15:], p.Memory)
	b[19] = p.Threads
	b[20] = byte(len(salt))
	copy(b[21:37], salt)
	binary.BigEndian.PutUint32(b[37:], uint32(headerLen))
	return b
}

func caseFileAAD(prefix []byte) string { return caseFileLabel + string(prefix) }

/* ---------------------------------------------------------------- export */

// CaseFileSource is what WriteCaseFile writes.
type CaseFileSource struct {
	// DBPath is the case.db to copy, as it is on disk.
	DBPath string
	CaseID string
	Title  string
	// Created and Closed are informational: restore starts a fresh case.
	Created time.Time
	Closed  *time.Time
	// Keys are the case's DB and pseudonym keys. WriteCaseFile does not zero them.
	Keys Keys
	// Hold, when set, runs before the database is read and the function it returns
	// runs after (HoldCaseDB). It makes the on-disk file a consistent snapshot
	// while the case may be open.
	Hold func() (release func(), err error)
}

// WriteCaseFile writes the case file for src to dst, which must end in
// CaseFileExt and must not exist. pass is the exporting PC's master key
// passphrase; it is not modified. The file is written under a temporary name and
// moved into place only when complete, and nothing is left behind on failure. It
// returns the size of the file.
//
// The database is read twice: once to hash it (the hash is in the header, which
// precedes the data) and once to copy it, hashing again. If the two hashes
// differ the case changed under the reader, and the read is repeated.
func WriteCaseFile(ctx context.Context, dst string, src CaseFileSource, pass []byte, params masterkey.Params) (int64, error) {
	if !strings.EqualFold(filepath.Ext(dst), CaseFileExt) {
		return 0, errors.New("case file name must end in " + CaseFileExt)
	}
	if params == (masterkey.Params{}) {
		params = masterkey.DefaultParams()
	}
	if !params.Validate() || len(src.Keys.DB) != 32 || len(src.Keys.Pseudo) != 32 || !CaseID(src.CaseID).Valid() || len(pass) == 0 {
		return 0, errors.New("case file source is invalid")
	}
	salt := make([]byte, caseFileSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return 0, err
	}
	key, err := masterkey.DeriveKey(pass, salt, params)
	if err != nil {
		return 0, err
	}
	defer clear(key)

	// Reserve the name first: O_EXCL makes "refuses to overwrite" atomic. The
	// placeholder is replaced by the finished file, or removed on any failure.
	hold, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, ErrCaseFileExists
		}
		return 0, err
	}
	_ = hold.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "unravis-case-*.tmp")
	if err != nil {
		_ = os.Remove(dst)
		return 0, err
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			_ = os.Remove(dst)
		}
	}()
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return 0, err
	}

	if src.Hold != nil {
		release, err := src.Hold()
		if err != nil {
			return 0, err
		}
		defer release()
	}
	var total int64
	for attempt := 0; ; attempt++ {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if err := tmp.Truncate(0); err != nil {
			return 0, err
		}
		total, err = writeCaseFileOnce(ctx, tmp, src, key, salt, params)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrCaseFileChanged) || attempt+1 >= caseFileAttempts {
			return 0, err
		}
	}
	if err := tmp.Sync(); err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := sealedfile.Replace(tmpName, dst); err != nil {
		return 0, err
	}
	done = true
	return total, nil
}

func writeCaseFileOnce(ctx context.Context, out *os.File, src CaseFileSource, key, salt []byte, params masterkey.Params) (int64, error) {
	db, err := os.Open(src.DBPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	fi, err := db.Stat()
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() || fi.Size() <= 0 || fi.Size() > MaxCaseFileDB {
		return 0, errors.New("case database cannot be exported")
	}
	// Pass 1: the hash for the header.
	h := sha256.New()
	n, err := copyCtx(ctx, h, db)
	if err != nil {
		return 0, err
	}
	if n != fi.Size() {
		return 0, ErrCaseFileChanged
	}
	want := h.Sum(nil)

	hdr := caseFileHeader{
		V: 1, CaseID: src.CaseID, Title: src.Title, Created: src.Created.UTC(), Closed: src.Closed,
		DBKey: hex.EncodeToString(src.Keys.DB), PseudoKey: hex.EncodeToString(src.Keys.Pseudo),
		DBBytes: n, DBSHA256: hex.EncodeToString(want),
	}
	plain, err := json.Marshal(hdr)
	if err != nil {
		return 0, err
	}
	defer clear(plain)
	hdr.DBKey, hdr.PseudoKey = "", ""
	// The ciphertext length is known before sealing: version byte, nonce, tag.
	ctLen := 1 + 12 + len(plain) + 16
	prefix := caseFilePrefix(params, salt, ctLen)
	ct, err := masterkey.SealWith(key, caseFileAAD(prefix), plain)
	if err != nil {
		return 0, err
	}
	if len(ct) != ctLen {
		return 0, errors.New("case file header length mismatch")
	}
	if _, err := out.Write(prefix); err != nil {
		return 0, err
	}
	if _, err := out.Write(ct); err != nil {
		return 0, err
	}
	// Pass 2: copy, hashing what is actually written.
	if _, err := db.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	h2 := sha256.New()
	copied, err := copyCtx(ctx, io.MultiWriter(out, h2), db)
	if err != nil {
		return 0, err
	}
	if copied != n || !bytes.Equal(h2.Sum(nil), want) {
		return 0, ErrCaseFileChanged
	}
	// The file must not have grown or been replaced while it was read.
	if st, err := os.Stat(src.DBPath); err != nil || st.Size() != n {
		return 0, ErrCaseFileChanged
	}
	return int64(caseFilePrefixLen) + int64(ctLen) + n, nil
}

// copyCtx copies r to w in 1 MiB steps and stops early if ctx is done. The
// buffer is the only memory it holds.
func copyCtx(ctx context.Context, w io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, caseFileCopyBuf)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, rerr := r.Read(buf)
		if m > 0 {
			if _, werr := w.Write(buf[:m]); werr != nil {
				return n, werr
			}
			n += int64(m)
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

/* --------------------------------------------------------------- reading */

// CaseFileInfo is what a case file says about the case it holds.
type CaseFileInfo struct {
	CaseID  string
	Title   string
	Created time.Time
	Closed  *time.Time
	DBBytes int64
}

// CaseFile is an opened case file: header verified, database part not yet read.
type CaseFile struct {
	f     *os.File
	hdr   caseFileHeader
	keys  Keys
	dbOff int64
	size  int64
}

// OpenCaseFile opens path and verifies the header with pass, the master key
// passphrase the file was exported with. Every failure is ErrCaseFile. pass is
// not modified. The caller must Close the result.
func OpenCaseFile(path string, pass []byte) (*CaseFile, error) {
	li, err := os.Lstat(path)
	if err != nil || !li.Mode().IsRegular() {
		return nil, ErrCaseFile
	}
	if li.Size() < caseFilePrefixLen+1+12+16 || li.Size() > caseFilePrefixLen+caseFileHeaderMax+MaxCaseFileDB {
		return nil, ErrCaseFile
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrCaseFile
	}
	cf, err := openCaseFile(f, li.Size(), pass)
	if err != nil {
		_ = f.Close()
		return nil, ErrCaseFile
	}
	return cf, nil
}

func openCaseFile(f *os.File, size int64, pass []byte) (*CaseFile, error) {
	prefix := make([]byte, caseFilePrefixLen)
	if _, err := io.ReadFull(f, prefix); err != nil {
		return nil, ErrCaseFile
	}
	if string(prefix[:8]) != caseFileMagic || binary.BigEndian.Uint16(prefix[8:]) != caseFileVersion || prefix[10] != caseFileKDFArgon2 {
		return nil, ErrCaseFile
	}
	params := masterkey.Params{
		Time:    binary.BigEndian.Uint32(prefix[11:]),
		Memory:  binary.BigEndian.Uint32(prefix[15:]),
		Threads: prefix[19],
	}
	ctLen := int64(binary.BigEndian.Uint32(prefix[37:]))
	if !params.Validate() || prefix[20] != caseFileSaltLen || ctLen < 1+12+16 || ctLen > caseFileHeaderMax || caseFilePrefixLen+ctLen > size {
		return nil, ErrCaseFile
	}
	ct := make([]byte, ctLen)
	if _, err := io.ReadFull(f, ct); err != nil {
		return nil, ErrCaseFile
	}
	key, err := masterkey.DeriveKey(pass, prefix[21:37], params)
	if err != nil {
		return nil, ErrCaseFile
	}
	defer clear(key)
	plain, err := masterkey.OpenWith(key, caseFileAAD(prefix), ct)
	if err != nil {
		return nil, ErrCaseFile
	}
	defer clear(plain)
	var hdr caseFileHeader
	if json.Unmarshal(plain, &hdr) != nil {
		return nil, ErrCaseFile
	}
	dbKey, err1 := hex.DecodeString(hdr.DBKey)
	pseudo, err2 := hex.DecodeString(hdr.PseudoKey)
	sum, err3 := hex.DecodeString(hdr.DBSHA256)
	dbOff := int64(caseFilePrefixLen) + ctLen
	bad := hdr.V != 1 || err1 != nil || err2 != nil || err3 != nil || len(dbKey) != 32 || len(pseudo) != 32 || len(sum) != sha256.Size ||
		!CaseID(hdr.CaseID).Valid() || !ValidTitle(hdr.Title) ||
		hdr.DBBytes <= 0 || hdr.DBBytes > MaxCaseFileDB || dbOff+hdr.DBBytes != size
	if bad {
		clear(dbKey)
		clear(pseudo)
		return nil, ErrCaseFile
	}
	hdr.DBKey, hdr.PseudoKey = "", ""
	return &CaseFile{f: f, hdr: hdr, keys: Keys{DB: dbKey, Pseudo: pseudo}, dbOff: dbOff, size: size}, nil
}

// Info describes the case in the file.
func (c *CaseFile) Info() CaseFileInfo {
	return CaseFileInfo{CaseID: c.hdr.CaseID, Title: c.hdr.Title, Created: c.hdr.Created, Closed: c.hdr.Closed, DBBytes: c.hdr.DBBytes}
}

// Keys returns a copy of the case's keys, with CaseID set to id. The caller zeroes it.
func (c *CaseFile) Keys(id string) Keys {
	return Keys{DB: append([]byte(nil), c.keys.DB...), Pseudo: append([]byte(nil), c.keys.Pseudo...), CaseID: id}
}

// Close zeroes the keys and closes the file.
func (c *CaseFile) Close() error {
	c.keys.Zero()
	return c.f.Close()
}

// ExtractDB streams the database part to dst, which must not exist, and checks
// it against the SHA-256 in the header. dst is created exclusively and removed
// if the check (or anything else) fails, so a partial file is never left. The
// caller moves it to its final name afterwards. Any content failure is
// ErrCaseFile; a failure to write is returned as is.
func (c *CaseFile) ExtractDB(ctx context.Context, dst string) (err error) {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(dst)
		}
	}()
	if _, err = c.f.Seek(c.dbOff, io.SeekStart); err != nil {
		return ErrCaseFile
	}
	h := sha256.New()
	buf := make([]byte, caseFileCopyBuf)
	remaining := c.hdr.DBBytes
	for remaining > 0 {
		if err = ctx.Err(); err != nil {
			return err
		}
		chunk := buf
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		m, rerr := io.ReadFull(c.f, chunk)
		if m > 0 {
			h.Write(chunk[:m])
			if _, werr := out.Write(chunk[:m]); werr != nil {
				err = werr
				return err
			}
			remaining -= int64(m)
		}
		if rerr != nil && remaining > 0 {
			err = ErrCaseFile
			return err
		}
	}
	// Nothing may follow the database.
	var one [1]byte
	if n, _ := c.f.Read(one[:]); n != 0 {
		err = ErrCaseFile
		return err
	}
	want, _ := hex.DecodeString(c.hdr.DBSHA256)
	if !bytes.Equal(h.Sum(nil), want) {
		err = ErrCaseFile
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

/* ------------------------------------------------------- database helpers */

// escapeCaseURIPath makes a file path safe inside a "file:" URI.
func escapeCaseURIPath(p string) string {
	p = filepath.ToSlash(p)
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23", " ", "%20").Replace(p)
}

var errCaseDB = errors.New("case database cannot be used")

// openCaseDB opens a case database with its key. The key travels only through
// the connection hook, never the DSN, and an error from the key statement is
// replaced so it cannot carry the key text.
func openCaseDB(ctx context.Context, dbPath string, dbKey []byte) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	dsn := "file:" + escapeCaseURIPath(dbPath) + "?vfs=adiantum&mode=rw"
	hexKey := hex.EncodeToString(dbKey)
	db, err := driver.Open(dsn, func(c *sqlite3.Conn) error {
		if err := c.Exec("PRAGMA hexkey='" + hexKey + "'"); err != nil {
			return errCaseDB
		}
		if _, err := c.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
			return err
		}
		for _, stmt := range []string{"PRAGMA busy_timeout=10000", "PRAGMA trusted_schema=OFF", "PRAGMA temp_store=MEMORY"} {
			if err := c.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, errCaseDB
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errCaseDB
	}
	return db, nil
}

// HoldCaseDB makes the database file at dbPath a consistent snapshot for the
// caller to copy, whether or not the case is open elsewhere in the process. It
// checkpoints the write-ahead log into the main file until nothing is left in
// it, then holds a read transaction, which stops any other connection from
// copying later commits into the file. The returned function releases the hold.
// The case is not modified: a checkpoint moves committed pages into the file and
// changes nothing a reader can see.
func HoldCaseDB(ctx context.Context, dbPath string, dbKey []byte) (release func(), err error) {
	db, err := openCaseDB(ctx, dbPath, dbKey)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, errCaseDB
	}
	fail := func(e error) (func(), error) {
		_ = conn.Close()
		_ = db.Close()
		return nil, e
	}
	// A busy checkpoint (another connection is mid-transaction) leaves part of the
	// log unwritten, so retry for a few seconds.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var busy, logFrames, checkpointed int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
			return fail(errCaseDB)
		}
		if busy == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fail(ErrCaseFileBusy)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fail(errCaseDB)
	}
	var one int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM meta").Scan(&one); err != nil {
		_ = tx.Rollback()
		return fail(errCaseDB)
	}
	return func() {
		_ = tx.Rollback()
		_ = conn.Close()
		_ = db.Close()
	}, nil
}

// rebindBatch is how many event rows one rewrite statement moves. The whole
// rewrite is still one transaction; batching only bounds the size of the
// statement's working set.
const rebindBatch = 20000

// RebindCaseDB makes the database at dbPath belong to the case newID. The id sits
// in two places inside the encrypted database: meta.case_id, and the first part
// of every events.evidence_id ("ev:<first 8 of the id>:<importer>:<hash>", which
// erase uses to strip ledger references). Both are rewritten in one transaction;
// either all of it happens or none. It returns the number of events rewritten.
// The old id must match meta.case_id or nothing is changed.
func RebindCaseDB(ctx context.Context, dbPath string, dbKey []byte, oldID, newID string) (int64, error) {
	if !CaseID(oldID).Valid() || !CaseID(newID).Valid() {
		return 0, ErrBadID
	}
	db, err := openCaseDB(ctx, dbPath, dbKey)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, errCaseDB
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, errCaseDB
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	var cur string
	if err := conn.QueryRowContext(ctx, "SELECT v FROM meta WHERE k='case_id'").Scan(&cur); err != nil || cur != oldID {
		return 0, errCaseDB
	}
	res, err := conn.ExecContext(ctx, "UPDATE meta SET v=? WHERE k='case_id'", newID)
	if err != nil {
		return 0, errCaseDB
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, errCaseDB
	}
	var moved int64
	oldTag, newTag := "ev:"+oldID[:8], "ev:"+newID[:8]
	if oldTag != newTag {
		for {
			res, err := conn.ExecContext(ctx,
				`UPDATE events SET evidence_id = ? || substr(evidence_id, 12) WHERE evidence_id IN
				 (SELECT evidence_id FROM events WHERE evidence_id >= ? AND evidence_id < ? LIMIT ?)`,
				newTag, oldTag+":", oldTag+";", rebindBatch)
			if err != nil {
				return 0, errCaseDB
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				break
			}
			moved += n
		}
	}
	if oldTag != newTag {
		// The case's own theories ledger (table ledger_blob, absent in older cases)
		// refers to events by the same prefix; keep those references pointing at them.
		var body []byte
		switch err := conn.QueryRowContext(ctx, "SELECT body FROM ledger_blob WHERE id = 1").Scan(&body); {
		case err == nil:
			rewritten := bytes.ReplaceAll(body, []byte(oldTag+":"), []byte(newTag+":"))
			if _, err := conn.ExecContext(ctx, "UPDATE ledger_blob SET body = ? WHERE id = 1", rewritten); err != nil {
				return 0, errCaseDB
			}
		case errors.Is(err, sql.ErrNoRows), strings.Contains(err.Error(), "no such table"):
		default:
			return 0, errCaseDB
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, errCaseDB
	}
	committed = true
	// Leave the main file self-contained; best effort.
	var busy, logFrames, checkpointed int
	_ = conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed)
	return moved, nil
}

/* ------------------------------------------------------ export and restore */

// ExportCaseFile writes the case file for the case id under root to dst. The
// case may be open. pass is the master key passphrase of this PC. lookup opens
// the case's key file (the master key, or DPAPI for a case not yet re-wrapped).
// The case itself is not changed. params are the Argon2id costs for the file
// header key; the zero value means masterkey.DefaultParams.
func ExportCaseFile(ctx context.Context, root string, id CaseID, title string, created time.Time, closed *time.Time,
	lookup ProtectorLookup, dst string, pass []byte, params masterkey.Params) (int64, error) {
	if !id.Valid() {
		return 0, ErrBadID
	}
	dir := filepath.Join(root, string(id))
	if err := RejectReparse(root, dir); err != nil {
		return 0, err
	}
	keys, _, err := ReadKeyFileAny(filepath.Join(dir, KeyFileName), lookup)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	defer keys.Zero()
	if keys.CaseID != string(id) {
		return 0, ErrKeyFile
	}
	dbPath := filepath.Join(dir, "case.db")
	return WriteCaseFile(ctx, dst, CaseFileSource{
		DBPath: dbPath, CaseID: string(id), Title: title, Created: created, Closed: closed, Keys: keys,
		Hold: func() (func(), error) { return HoldCaseDB(ctx, dbPath, keys.DB) },
	}, pass, params)
}

// RestoreStore is the part of a case store RestoreCaseFile uses.
type RestoreStore interface {
	Create(ctx context.Context, title string) (CaseInfo, error)
	Open(ctx context.Context, id CaseID) (Case, error)
	Erase(ctx context.Context, id CaseID) error
	Root() string
}

// RestoreCaseFile reads the case file at path with pass (the master key passphrase
// of the exporting PC) and makes a new case from it in st, sealed under the
// destination PC's master key, which lookup must supply (unlocked). The new case
// has a new id; the id inside the database and in every event id is rewritten in
// one transaction, then the store's own open checks run. Nothing is merged or
// overwritten, so restoring one file twice makes two cases. On any failure the
// new case is erased and no folder is left. Content failures are ErrCaseFile.
func RestoreCaseFile(ctx context.Context, st RestoreStore, lookup ProtectorLookup, path string, pass []byte) (CaseInfo, error) {
	base, err := lookup(KindMasterKey)
	if err != nil || base == nil {
		return CaseInfo{}, ErrLocked
	}
	cf, err := OpenCaseFile(path, pass)
	if err != nil {
		return CaseInfo{}, err
	}
	defer cf.Close()
	info := cf.Info()

	// Stage and verify the database before any case exists, so a damaged file
	// creates nothing. The staging name is not a case id, so List ignores it.
	root := st.Root()
	var tag [8]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return CaseInfo{}, err
	}
	stage := filepath.Join(root, "restore-"+hex.EncodeToString(tag[:])+".part")
	if err := cf.ExtractDB(ctx, stage); err != nil {
		return CaseInfo{}, err
	}
	defer os.Remove(stage)

	created, err := st.Create(ctx, info.Title)
	if err != nil {
		if errors.Is(err, ErrBadTitle) {
			return CaseInfo{}, ErrCaseFile
		}
		return CaseInfo{}, err
	}
	id := created.ID
	ok := false
	defer func() {
		if ok {
			return
		}
		cleanup := context.WithoutCancel(ctx)
		if st.Erase(cleanup, id) != nil {
			// Last resort: no half-restored folder may stay.
			dir := filepath.Join(root, string(id))
			_ = EraseKeyFile(filepath.Join(dir, KeyFileName))
			_ = os.RemoveAll(dir)
		}
	}()
	dir := filepath.Join(root, string(id))
	if err := RejectReparse(root, dir); err != nil {
		return CaseInfo{}, err
	}
	dbPath := filepath.Join(dir, "case.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := sealedfile.Replace(stage, dbPath); err != nil {
		return CaseInfo{}, err
	}
	keys := cf.Keys(string(id))
	defer keys.Zero()
	if err := WriteKeyFileKind(filepath.Join(dir, KeyFileName), KindMasterKey, base, keys); err != nil {
		return CaseInfo{}, err
	}
	if _, err := RebindCaseDB(ctx, dbPath, keys.DB, info.CaseID, string(id)); err != nil {
		if ctx.Err() != nil {
			return CaseInfo{}, ctx.Err()
		}
		return CaseInfo{}, ErrCaseFile
	}
	c, err := st.Open(ctx, id)
	if err != nil {
		if errors.Is(err, ErrLocked) || ctx.Err() != nil {
			return CaseInfo{}, err
		}
		return CaseInfo{}, ErrCaseFile
	}
	out := c.Info()
	if err := c.Close(); err != nil {
		return CaseInfo{}, fmt.Errorf("closing restored case: %w", err)
	}
	ok = true
	return out, nil
}
