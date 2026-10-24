package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"api-audit-explorer/internal/sealedfile"
)

// maxSealedBytes caps the sealed file and its plaintext. MaxTotalBytes (8 MiB)
// bounds the entries; the rest is headroom for JSON structure and encoding.
const maxSealedBytes = 16 << 20

// OpenSealed returns a Store backed by the sealed file at path (the ledger JSON
// encrypted for the current user, written atomically with mode 0600). A missing
// file starts an empty ledger, or migrates legacyPath when that plain JSON file
// exists: the plain ledger is written sealed, read back and compared, and only
// then is the plain file deleted. If that check fails the plain file is kept,
// the new sealed file is removed and OpenSealed returns an error. A sealed file
// that cannot be decrypted or decoded is never overwritten: OpenSealed returns
// an error and leaves it as it is.
func OpenSealed(path, legacyPath string, protector sealedfile.Protector) (Store, error) {
	if path == "" || protector == nil {
		return nil, errors.New("the sealed ledger needs a file path and a protector")
	}
	s := &store{path: path, seal: protector, items: []Hypothesis{}}
	unlock, err := sealedfile.Lock(path)
	if err != nil {
		return nil, fmt.Errorf("the ledger file %s could not be locked: %w", path, err)
	}
	defer unlock()

	items, found, err := readSealed(path, protector)
	if err != nil {
		return nil, err
	}
	if found {
		s.items = items
		if legacyPath != "" {
			finishMigration(legacyPath, items)
		}
		return s, nil
	}
	if legacyPath == "" {
		return s, nil
	}
	plain, err := readLegacy(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.writeSealed(plain); err != nil {
		return nil, fmt.Errorf("the ledger at %s could not be migrated to the sealed file; the plain file was kept: %w", legacyPath, err)
	}
	back, ok, err := readSealed(path, protector)
	if err != nil || !ok || !sameLedger(plain, back) {
		// The sealed file is our own unverified output: remove it so the next
		// start retries the migration instead of failing on it.
		_ = os.Remove(path)
		cause := "it did not read back as the same ledger"
		if err != nil {
			cause = err.Error()
		}
		return nil, fmt.Errorf("the ledger at %s could not be migrated to the sealed file and the plain file was kept: %s", legacyPath, cause)
	}
	s.items = back
	// The sealed copy is verified. A failure to delete the plain file is not
	// fatal: the next start finds both files equal and tries again.
	_ = removePlain(legacyPath)
	return s, nil
}

// readSealed reads and decodes the sealed file. found is false when the file
// does not exist.
func readSealed(path string, protector sealedfile.Protector) (items []Hypothesis, found bool, err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("the ledger file %s could not be read: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSealedBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("the ledger file %s could not be read: %w", path, err)
	}
	if len(data) == 0 || len(data) > maxSealedBytes {
		return nil, false, fmt.Errorf("the ledger file %s is empty or larger than the %d byte limit and was left untouched", path, maxSealedBytes)
	}
	plain, err := protector.Unprotect(data)
	if err != nil || len(plain) == 0 || len(plain) > maxSealedBytes {
		return nil, false, fmt.Errorf("the ledger file %s could not be decrypted (damaged, or sealed for another user or machine) and was left untouched", path)
	}
	items, err = decode(plain)
	if err != nil {
		return nil, false, fmt.Errorf("the ledger file %s is not a valid ledger and was left untouched: %v", path, err)
	}
	return items, true, nil
}

// readLegacy decodes the plain ledger.json. A file that is not a valid ledger
// is left untouched and reported.
func readLegacy(path string) ([]Hypothesis, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("the ledger file %s could not be read: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSealedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("the ledger file %s could not be read: %w", path, err)
	}
	if len(data) > maxSealedBytes {
		return nil, fmt.Errorf("the ledger file %s is larger than the %d byte limit and was left untouched", path, maxSealedBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []Hypothesis{}, nil
	}
	items, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("the ledger file %s is not a valid ledger and was left untouched: %v", path, err)
	}
	return items, nil
}

// finishMigration completes a migration that stopped after the sealed file was
// written: when the plain file decodes to exactly the sealed ledger it is
// deleted. Anything else is left alone.
func finishMigration(legacyPath string, sealed []Hypothesis) {
	plain, err := readLegacy(legacyPath)
	if err != nil || !sameLedger(plain, sealed) {
		return
	}
	_ = removePlain(legacyPath)
}

func (s *store) saveSealed(items []Hypothesis) error {
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return saveErr(err)
	}
	defer unlock()
	if err := s.writeSealed(items); err != nil {
		return saveErr(err)
	}
	return nil
}

// writeSealed seals items and replaces the file atomically. The caller holds
// the file lock.
func (s *store) writeSealed(items []Hypothesis) error {
	plain, err := json.Marshal(fileData{Version: fileVersion, Hypotheses: items})
	if err != nil {
		return err
	}
	if len(plain) > maxSealedBytes {
		return fmt.Errorf("the ledger is larger than the %d byte file limit", maxSealedBytes)
	}
	sealed, err := s.seal.Protect(plain)
	if err != nil {
		return err
	}
	if len(sealed) == 0 || len(sealed) > maxSealedBytes {
		return fmt.Errorf("the sealed ledger is larger than the %d byte file limit", maxSealedBytes)
	}
	return sealedfile.WriteAtomic(s.path, ".ledger-*.tmp", sealed)
}

// sameLedger reports whether two ledgers serialise identically.
func sameLedger(a, b []Hypothesis) bool {
	x, errA := json.Marshal(fileData{Version: fileVersion, Hypotheses: a})
	y, errB := json.Marshal(fileData{Version: fileVersion, Hypotheses: b})
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// removePlain overwrites the plain ledger with zeros and deletes it. The
// overwrite is best effort only: on SSDs, with wear levelling, and on journaling
// or copy-on-write file systems the old bytes can survive on the medium, so
// secure deletion cannot be guaranteed. The sealed file is the protection going
// forward; this just avoids leaving the plain file readable in the folder.
func removePlain(path string) error {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			zeros := make([]byte, 32<<10)
			for left := info.Size(); left > 0; {
				n := int64(len(zeros))
				if left < n {
					n = left
				}
				if _, err := f.Write(zeros[:n]); err != nil {
					break
				}
				left -= n
			}
			_ = f.Sync()
			_ = f.Close()
		}
	}
	return os.Remove(path)
}
