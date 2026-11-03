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
	closed   bool // set by Close: later writes are refused, never reopen the file

	// A rotation that fails (the file is held open by a viewer or a scanner)
	// must not eat the older backups, so the writer keeps appending and tries
	// again only once the pending write would pass noRotateBelow.
	noRotateBelow int64
	lastRotateErr error // the most recent failed rotation; nil after a good one
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

// stagingPath is where the current file waits while the backups shift, so the
// oldest backup is only deleted once the current file is known to be movable.
func (w *rotateWriter) stagingPath() string { return w.path() + ".rotating" }

// rotate shifts every backup one place older, dropping the oldest, and starts
// a fresh current file. When the current file cannot be moved (another program
// holds it open), nothing is deleted or shifted: the writer reopens the same
// file, records the error and backs off for one more maxSize of data.
func (w *rotateWriter) rotate() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	if err := w.shiftBackups(); err != nil {
		w.lastRotateErr = err
		w.noRotateBelow = w.size + w.maxSize
	} else {
		w.lastRotateErr = nil
		w.noRotateBelow = 0
	}
	return w.open()
}

// shiftBackups moves the current file out of the way first and only then drops
// the oldest backup and shifts the rest. A failure puts the current file back.
func (w *rotateWriter) shiftBackups() error {
	if w.maxFiles <= 1 {
		if err := os.Remove(w.path()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	staging := w.stagingPath()
	_ = os.Remove(staging) // a leftover from a crashed rotation; Windows will not rename over it
	if err := os.Rename(w.path(), staging); err != nil {
		return err
	}
	// Remove before rename: Windows refuses to rename over an existing file.
	if err := os.Remove(w.backup(w.maxFiles - 1)); err != nil && !os.IsNotExist(err) {
		_ = os.Rename(staging, w.path())
		return err
	}
	for i := w.maxFiles - 2; i >= 1; i-- {
		if err := os.Rename(w.backup(i), w.backup(i+1)); err != nil && !os.IsNotExist(err) {
			_ = os.Rename(staging, w.path())
			return err
		}
	}
	if err := os.Rename(staging, w.backup(1)); err != nil {
		_ = os.Rename(staging, w.path())
		return err
	}
	return nil
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if next := w.size + int64(len(p)); w.size > 0 && next > w.maxSize && next > w.noRotateBelow {
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
	w.closed = true
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
