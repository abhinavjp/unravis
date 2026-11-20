//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// openHeld opens path for reading with sharing for other readers only: while the handle is open nobody can write, rename or delete
// the file. A link is not followed (it would hash as a different file and fail the check).
func openHeld(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
