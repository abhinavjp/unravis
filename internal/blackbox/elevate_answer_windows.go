//go:build windows

package blackbox

import (
	"os"

	"golang.org/x/sys/windows"
)

// OpenAnswerFile is how the elevated helper opens the file its answer goes to
// (SEC-FIX-04). Unravis made the file before the prompt, so the helper opens an
// existing file only: it never creates a path, and it refuses a link (reparse
// point) or a folder, so an administrator is never led onto another file. The
// file is emptied before it is written.
func OpenAnswerFile(path string) (*os.File, error) {
	f, err := openHeld(path, false, false, windows.GENERIC_WRITE)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
