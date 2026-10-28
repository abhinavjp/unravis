package casestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/masterkey"
)

// Writing and checking case files for the archive (P3). The format is the one
// WriteCaseFile makes (v1); what differs is who supplies the key. An archive file
// is sealed under a key the caller already holds (the master key, whose salt and
// cost parameters go into the prefix so the file opens with the typed master
// passphrase on any PC) or a key derived from a passphrase typed for that file.
// Nothing here stores or logs a key.

// KeyDeriver supplies the header key of a case file from the salt and cost
// parameters its prefix records. The caller of a derive function owns the slice it
// returns; the reader clears it.
type KeyDeriver func(salt []byte, p masterkey.Params) ([]byte, error)

// FileKey is the key a case file is sealed under, with the salt and parameters the
// prefix records. WriteArchivePart does not clear Key.
type FileKey struct {
	Key    []byte
	Salt   []byte
	Params masterkey.Params
}

// FixedKey is a KeyDeriver that returns a copy of key whatever the salt, for a
// file sealed under a key this PC holds already. A file made under another key
// fails to open, which is ErrCaseFile.
func FixedKey(key []byte) KeyDeriver {
	return func([]byte, masterkey.Params) ([]byte, error) { return append([]byte(nil), key...), nil }
}

// PassphraseKey is a KeyDeriver that runs Argon2id over pass with the file's own
// salt and parameters.
func PassphraseKey(pass []byte) KeyDeriver {
	return func(salt []byte, p masterkey.Params) ([]byte, error) { return masterkey.DeriveKey(pass, salt, p) }
}

// partProbe counts the bytes a writer or reader handles (step) and hashes the
// file as it is written (sum). A nil probe does nothing.
type partProbe struct {
	step func(int64)
	sum  hash.Hash
}

type stepWriter struct {
	w    io.Writer
	step func(int64)
}

func (s stepWriter) Write(b []byte) (int, error) {
	n, err := s.w.Write(b)
	if n > 0 && s.step != nil {
		s.step(int64(n))
	}
	return n, err
}

// counting wraps w so that every write is counted.
func (p *partProbe) counting(w io.Writer) io.Writer {
	if p == nil || p.step == nil {
		return w
	}
	return stepWriter{w: w, step: p.step}
}

// tee wraps w so that every write is counted and added to the file hash.
func (p *partProbe) tee(w io.Writer) io.Writer {
	if p == nil {
		return w
	}
	if p.sum != nil {
		w = io.MultiWriter(w, p.sum)
	}
	return p.counting(w)
}

func (p *partProbe) sumOnly(b []byte) {
	if p != nil && p.sum != nil {
		p.sum.Write(b)
	}
}

// ArchiveWritten is what WriteArchivePart reports about the file it made.
type ArchiveWritten struct {
	Bytes  int64
	SHA256 string // hex SHA-256 of the whole file, as written
}

// WriteArchivePart writes the case file for src to part, which must not exist, and
// leaves it in place, flushed to disk, for the caller to read back and move to its
// final name. step is told how many bytes each pass handled (the database is read
// twice, so a file of n database bytes reports about 2n; the caller adds its own
// read back). On any failure part is removed. Cancelling ctx stops within one
// buffer.
func WriteArchivePart(ctx context.Context, part string, src CaseFileSource, fk FileKey, step func(int64)) (ArchiveWritten, error) {
	if !fk.Params.Validate() || len(fk.Key) != masterkey.KeyLen || len(fk.Salt) != caseFileSaltLen ||
		len(src.Keys.DB) != 32 || len(src.Keys.Pseudo) != 32 || !CaseID(src.CaseID).Valid() {
		return ArchiveWritten{}, errors.New("case file source is invalid")
	}
	out, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ArchiveWritten{}, ErrCaseFileExists
		}
		return ArchiveWritten{}, err
	}
	done := false
	defer func() {
		if !done {
			_ = out.Close()
			_ = os.Remove(part)
		}
	}()
	if src.Hold != nil {
		release, err := src.Hold()
		if err != nil {
			return ArchiveWritten{}, err
		}
		defer release()
	}
	probe := &partProbe{step: step, sum: sha256.New()}
	var total int64
	for attempt := 0; ; attempt++ {
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			return ArchiveWritten{}, err
		}
		if err := out.Truncate(0); err != nil {
			return ArchiveWritten{}, err
		}
		probe.sum.Reset()
		total, err = writeCaseFileOnce(ctx, out, src, fk.Key, fk.Salt, fk.Params, probe)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrCaseFileChanged) || attempt+1 >= caseFileAttempts {
			return ArchiveWritten{}, err
		}
	}
	if err := out.Sync(); err != nil {
		return ArchiveWritten{}, err
	}
	if err := out.Close(); err != nil {
		return ArchiveWritten{}, err
	}
	done = true
	return ArchiveWritten{Bytes: total, SHA256: hex.EncodeToString(probe.sum.Sum(nil))}, nil
}

// HashFile reads path from disk and returns its size and hex SHA-256: the read
// back of a finished archive. step counts the bytes read.
func HashFile(ctx context.Context, path string, step func(int64)) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	p := &partProbe{step: step}
	n, err := copyCtx(ctx, p.counting(h), f)
	if err != nil {
		return n, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// CountCaseDB opens the case database at dbPath with its key and counts what the
// archive record states: imports, sources and events.
func CountCaseDB(ctx context.Context, dbPath string, dbKey []byte) (Counts, error) {
	db, err := openCaseDB(ctx, dbPath, dbKey)
	if err != nil {
		return Counts{}, err
	}
	defer db.Close()
	var c Counts
	err = db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM imports), (SELECT COUNT(*) FROM case_sources), (SELECT COUNT(*) FROM events)`).
		Scan(&c.Imports, &c.Sources, &c.Events)
	if err != nil {
		if ctx.Err() != nil {
			return Counts{}, ctx.Err()
		}
		// A case made before sources existed has no such table.
		if strings.Contains(err.Error(), "no such table") {
			err = db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM imports), 0, (SELECT COUNT(*) FROM events)`).
				Scan(&c.Imports, &c.Sources, &c.Events)
		}
		if err != nil {
			return Counts{}, errCaseDB
		}
	}
	return c, nil
}

// CheckedCaseFile is what a successful test restore found.
type CheckedCaseFile struct {
	Info   CaseFileInfo
	Counts Counts
}

// CheckCaseFile restores the case file at path into a scratch folder under
// scratchDir, as a real restore would: the header opens with derive, the database
// part is extracted and checked against its SHA-256, then opened with the case's
// own key and counted. The scratch folder is removed before it returns, whatever
// the result; a failure to remove it is returned as cleanup so the caller can
// retry and show "cleanup pending". Content failures are ErrCaseFile; a failure to
// write or read is returned as is. It changes no case.
func CheckCaseFile(ctx context.Context, path, scratchDir string, derive KeyDeriver, step func(int64)) (res CheckedCaseFile, cleanup error, err error) {
	cf, err := OpenCaseFileWith(path, derive)
	if err != nil {
		return CheckedCaseFile{}, nil, err
	}
	defer cf.Close()
	if err := os.MkdirAll(scratchDir, 0o700); err != nil {
		return CheckedCaseFile{}, nil, err
	}
	dir, err := os.MkdirTemp(scratchDir, "verify-")
	if err != nil {
		return CheckedCaseFile{}, nil, err
	}
	defer func() {
		if rerr := removeAllRetry(dir); rerr != nil {
			cleanup = rerr
		}
	}()
	dbPath := filepath.Join(dir, "case.db")
	if err := cf.extractDB(ctx, dbPath, step); err != nil {
		return CheckedCaseFile{}, nil, err
	}
	keys := cf.Keys(cf.hdr.CaseID)
	defer keys.Zero()
	counts, err := CountCaseDB(ctx, dbPath, keys.DB)
	if err != nil {
		if ctx.Err() != nil {
			return CheckedCaseFile{}, nil, ctx.Err()
		}
		return CheckedCaseFile{}, nil, ErrCaseFile
	}
	return CheckedCaseFile{Info: cf.Info(), Counts: counts}, nil, nil
}

// removeAllRetry removes dir, trying a few times: on Windows a file that was just
// closed can be held for a moment by a scanner.
func removeAllRetry(dir string) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
	}
	return err
}
