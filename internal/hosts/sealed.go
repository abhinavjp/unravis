package hosts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"api-audit-explorer/internal/sealedfile"
)

// maxSealedBytes caps hosts.dat. The plaintext is at most MaxFileBytes; the
// rest is headroom for the platform sealer's envelope.
const maxSealedBytes = 2 * MaxFileBytes

// Sealed is the host map stored as hosts.dat: hosts.json encrypted for the
// current user and written atomically with mode 0600. Safe for concurrent use.
type Sealed struct {
	path string
	seal sealedfile.Protector

	mu  sync.RWMutex
	cur *Map
}

// OpenSealed opens the sealed file at path. A missing file starts an empty
// map and writes nothing. A file that cannot be decrypted or validated is never
// overwritten: OpenSealed returns an error and leaves it as it is.
func OpenSealed(path string, protector sealedfile.Protector) (*Sealed, error) {
	if path == "" || protector == nil {
		return nil, errors.New("the sealed hosts file needs a file path and a protector")
	}
	unlock, err := sealedfile.Lock(path)
	if err != nil {
		return nil, fmt.Errorf("the hosts file %s could not be locked: %w", path, err)
	}
	defer unlock()
	m, err := readSealed(path, protector)
	if err != nil {
		return nil, err
	}
	return &Sealed{path: path, seal: protector, cur: m}, nil
}

// readSealed returns an empty map when the file does not exist.
func readSealed(path string, protector sealedfile.Protector) (*Map, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return build(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("the hosts file %s could not be read: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSealedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("the hosts file %s could not be read: %w", path, err)
	}
	if len(data) == 0 || len(data) > maxSealedBytes {
		return nil, fmt.Errorf("the hosts file %s is empty or larger than the %d byte limit and was left untouched", path, maxSealedBytes)
	}
	plain, err := protector.Unprotect(data)
	if err != nil || len(plain) == 0 || len(plain) > MaxFileBytes {
		return nil, fmt.Errorf("the hosts file %s could not be decrypted (damaged, or sealed for another user or machine) and was left untouched", path)
	}
	m, err := decode(plain)
	if err != nil {
		return nil, fmt.Errorf("the hosts file %s is not a valid hosts file and was left untouched: %w", path, err)
	}
	return m, nil
}

// Map returns the current host map. It is never nil and never changes; Save
// swaps in a new one.
func (s *Sealed) Map() *Map {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Save seals m, replaces hosts.dat atomically and then makes m the current
// map. On any error the file and the current map are unchanged.
func (s *Sealed) Save(m *Map) error {
	if m == nil {
		return errors.New("no host map to save")
	}
	plain, err := m.Marshal()
	if err != nil {
		return err
	}
	if len(plain) > MaxFileBytes {
		return fmt.Errorf("the hosts file would be larger than the %d byte limit", MaxFileBytes)
	}
	sealed, err := s.seal.Protect(plain)
	if err != nil {
		return err
	}
	if len(sealed) == 0 || len(sealed) > maxSealedBytes {
		return fmt.Errorf("the sealed hosts file would be larger than the %d byte limit", maxSealedBytes)
	}
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return fmt.Errorf("the hosts file %s could not be locked: %w", s.path, err)
	}
	defer unlock()
	if err := sealedfile.WriteAtomic(s.path, ".hosts-*.tmp", sealed); err != nil {
		return fmt.Errorf("the hosts file %s could not be written: %w", s.path, err)
	}
	s.mu.Lock()
	s.cur = m
	s.mu.Unlock()
	return nil
}
