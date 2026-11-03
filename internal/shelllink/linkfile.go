package shelllink

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The shared rules for deciding whether a shortcut file on disk is one of ours. The app (internal/autostart) and the installer
// (internal/setup) both use them, so a fix to the size cap, the regular-file check or the target comparison lands in one place.

// MaxFileSize is the largest file read as a shortcut; a real one is a few hundred bytes.
const MaxFileSize = 64 << 10

// BackgroundArg is the argument the Startup shortcut passes so the app starts in the background (tray only, no browser).
const BackgroundArg = "--background"

// ErrNotShortcutFile means the path is not a regular file, or is larger than MaxFileSize. It was not read.
var ErrNotShortcutFile = errors.New("not a regular shortcut file")

// ReadFile reads the shortcut file at path when it is a regular file (not a link, junction or directory) of at most MaxFileSize. A
// missing file gives the os error (errors.Is os.ErrNotExist). A file that is not regular, or too large, gives ErrNotShortcutFile and is
// not read. read is the file reader (nil means os.ReadFile); setup tests swap it.
func ReadFile(path string, read func(string) ([]byte, error)) ([]byte, error) {
	if read == nil {
		read = os.ReadFile
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrNotShortcutFile, path)
	}
	if fi.Size() > MaxFileSize {
		return nil, fmt.Errorf("%w: %s is too large to be a shortcut", ErrNotShortcutFile, path)
	}
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize { // it grew between the stat and the read
		return nil, fmt.Errorf("%w: %s is too large to be a shortcut", ErrNotShortcutFile, path)
	}
	return data, nil
}

// SamePath compares two Windows paths the way the file system does (case does not matter). An empty path matches nothing.
func SamePath(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// TargetIs reports whether data is a shortcut whose target is exe: the "is this link ours" test.
func TargetIs(data []byte, exe string) bool {
	target, err := ReadShortcutTarget(data)
	return err == nil && SamePath(target, exe)
}
