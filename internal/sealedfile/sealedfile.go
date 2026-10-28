// Package sealedfile holds the small pieces shared by every file Unravis keeps
// sealed for the current user: the Protector interface, the Windows DPAPI
// implementation, a per-path lock and an atomic file replace.
package sealedfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var shared struct {
	mu   sync.RWMutex
	root string
}

// SetSharedRoot names the shared data folder (P14). Lock serialises every path inside it across all the people signed in to this PC,
// not only within one person's session. An empty root turns that off.
func SetSharedRoot(root string) {
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(abs)
		}
	}
	shared.mu.Lock()
	shared.root = root
	shared.mu.Unlock()
}

// underShared reports whether an absolute path is the shared folder or inside it.
func underShared(canonical string) bool {
	shared.mu.RLock()
	root := shared.root
	shared.mu.RUnlock()
	if root == "" {
		return false
	}
	c, r := strings.ToLower(filepath.Clean(canonical)), strings.ToLower(root)
	return c == r || strings.HasPrefix(c, strings.TrimSuffix(r, string(filepath.Separator))+string(filepath.Separator))
}

// maxBlob is a sanity bound on what the platform sealer may hand back. Callers
// apply their own, smaller caps to the files they read and write.
const maxBlob = 64 << 20

// ErrUnavailable means sealing is not available or a file could not be written.
var ErrUnavailable = errors.New("sealed file storage is unavailable")

// Protector encrypts and decrypts a complete document.
type Protector interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}

// WriteAtomic writes data to path through a temporary file in the same folder:
// the file is created 0600, flushed to disk, then moved over path, so a reader
// sees the old or the new content and never a partial file. tempPattern is
// passed to os.CreateTemp and should end in ".tmp".
func WriteAtomic(path, tempPattern string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the replace has moved it
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return Replace(tmpName, path)
}
