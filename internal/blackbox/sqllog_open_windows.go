//go:build windows

package blackbox

import (
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// openLogHandle opens path read only, with share-delete so SQL Server can still
// cycle (rename) the ERRORLOG while it is open, and without following a reparse
// point (link or junction) in the last part of the name. The opened handle is then
// checked: a plain disk file, not a directory or reparse point, and its final path
// is the path asked for, so a junction in a parent folder is refused too.
func openLogHandle(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	bad := func() (*os.File, error) { _ = windows.CloseHandle(h); return nil, fs.ErrInvalid }
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK {
		return bad()
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return bad()
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		return bad()
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || int(n) >= len(buf) {
		return bad()
	}
	final := strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`)
	if !strings.EqualFold(final, path) {
		return bad() // the name leads through a junction or a link in a parent folder
	}
	return os.NewFile(uintptr(h), path), nil
}
