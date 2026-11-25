//go:build !windows

package blackbox

import (
	"io/fs"
	"os"
)

// openLogHandle opens path read only and refuses it unless the opened file is the
// same plain file the path names (no link in the last part of the path).
func openLogHandle(path string) (*os.File, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !li.Mode().IsRegular() {
		return nil, fs.ErrInvalid
	}
	f, err := os.Open(path) //nolint:gosec // path comes from the SQL instance registry key and is checked by sqlLogPathOK
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(li, fi) {
		_ = f.Close()
		return nil, fs.ErrInvalid
	}
	return f, nil
}
