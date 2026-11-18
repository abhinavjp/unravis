//go:build !windows

package blackbox

import (
	"errors"
	"os"
)

// OpenAnswerFile opens an existing answer file for writing. It never creates the
// file and refuses a symbolic link or anything that is not a plain file.
func OpenAnswerFile(path string) (*os.File, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("the answer location is a link or not a plain file")
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
