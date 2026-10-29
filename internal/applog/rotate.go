package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// rotateWriter is a size-rotated file writer kept in-process: no external
// dependency and nothing to install. The current file is unravis.log; older
// files are unravis.1.log, unravis.2.log, ... up to maxFiles files in total.
// A write that would push the current file past maxSize rotates first, so one
// file never grows much beyond maxSize (a single line larger than maxSize is
// still written whole, to a fresh file).
type rotateWriter struct {
	mu       sync.Mutex
	dir      string
	name     string
	maxSize  int64
	maxFiles int
	secure   func(string) error // restricts the fresh file to this user; may be nil
	f        *os.File
	size     int64
}

func newRotateWriter(dir, name string, maxSize int64, maxFiles int, secure func(string) error) (*rotateWriter, error) {
	if maxSize <= 0 {
		maxSize = MaxSize
	}
	if maxFiles < 1 {
		maxFiles = 1
	}
	w := &rotateWriter{dir: dir, name: name, maxSize: maxSize, maxFiles: maxFiles, secure: secure}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotateWriter) path() string { return filepath.Join(w.dir, w.name) }

// backup is the path of the i-th rotated file (1 is the newest backup).
func (w *rotateWriter) backup(i int) string {
	ext := filepath.Ext(w.name)
	stem := strings.TrimSuffix(w.name, ext)
	return filepath.Join(w.dir, fmt.Sprintf("%s.%d%s", stem, i, ext))
}

func (w *rotateWriter) open() error {
	f, err := os.OpenFile(w.path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if w.secure != nil {
		// The log sits in a per-user folder, but restrict the file itself too,
		// like every other per-user file Unravis writes.
		_ = w.secure(w.path())
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f, w.size = f, info.Size()
	return nil
}

// rotate shifts every backup one place older, dropping the oldest, and starts
// a fresh current file.
func (w *rotateWriter) rotate() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	if w.maxFiles > 1 {
		// Remove before rename: Windows refuses to rename over an existing file.
		_ = os.Remove(w.backup(w.maxFiles - 1))
		for i := w.maxFiles - 2; i >= 1; i-- {
			_ = os.Rename(w.backup(i), w.backup(i+1))
		}
		_ = os.Rename(w.path(), w.backup(1))
	} else {
		_ = os.Remove(w.path())
	}
	return w.open()
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
