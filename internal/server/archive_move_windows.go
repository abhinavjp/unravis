//go:build windows

package server

import (
	"os"

	"golang.org/x/sys/windows"
)

// moveNoReplace renames from to to and fails when to exists. os.Rename replaces an
// existing file on Windows; MoveFileEx without MOVEFILE_REPLACE_EXISTING does not.
func moveNoReplace(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, 0); err != nil {
		if err == windows.ERROR_ALREADY_EXISTS || err == windows.ERROR_FILE_EXISTS {
			return os.ErrExist
		}
		return err
	}
	return nil
}
