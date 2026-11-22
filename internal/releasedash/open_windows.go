//go:build windows

package releasedash

import (
	"os"

	"golang.org/x/sys/windows"
)

// openShared opens a file for reading the way a polite observer must: with every share flag, including delete. Go's own os.Open
// leaves out FILE_SHARE_DELETE, and Release.ps1 replaces state.json and lock-info.json with Move-Item. A poll that happened to be
// reading at that moment would make the release's move fail.
func openShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
