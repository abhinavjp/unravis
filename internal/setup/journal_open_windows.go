//go:build windows

package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/datadir"
)

// afterProofHook is a test hook: it runs after the file is proven and before its bytes are read, so a test can try to change the file
// in that gap. Nil in production.
var afterProofHook func()

// ReadFileProven opens path once without following a link and proves it on that handle before reading a byte: a plain disk file,
// not a folder, not a link, one name only (a hard link would let a person's own file stand here), at the path asked for, owned by the
// system, the administrators or the account running, and writable by nobody else. The bytes (at most max) come from the same handle;
// the path is never opened again. The file is opened share-read only, so nobody can write, rename or delete it while it is read.
func (osSecurity) ReadFileProven(path string, max int64) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// GENERIC_READ includes READ_CONTROL, which the owner and list need.
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	// From here the handle is owned by f (os.NewFile) once step 6 starts; until then it is closed on every refusal.
	closed := false
	defer func() {
		if !closed {
			windows.CloseHandle(h)
		}
	}()
	refuse := func(why string) ([]byte, error) {
		return nil, fmt.Errorf("%w: %s", ErrNotProven, why)
	}
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK {
		return refuse("it is not a file on a disk")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, err
	}
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		return refuse("it is a folder")
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return refuse("it is a link")
	case info.NumberOfLinks != 1:
		return refuse(fmt.Sprintf("it has %d names", info.NumberOfLinks))
	}
	if size := int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow); size > max {
		return refuse(fmt.Sprintf("it is too large (%d bytes, at most %d)", size, max))
	}
	if final, err := finalPathOf(h); err != nil {
		return nil, err
	} else if !samePath(final, path) {
		return refuse("it is reached through a link (it is really " + final + ")")
	}
	fs, err := datadir.SecurityOfHandle(h)
	if err != nil {
		return nil, err
	}
	self, _ := CurrentUserSID()
	if !journalFileOwnerOK(fs.Owner, self) {
		return refuse(fmt.Sprintf("it is owned by %s", fs.Owner))
	}
	if err := checkNoOthersWrite(path, fs); err != nil {
		return refuse(err.Error())
	}
	if afterProofHook != nil {
		afterProofHook()
	}
	f := os.NewFile(uintptr(h), path)
	closed = true // f.Close closes the handle, once
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return refuse(fmt.Sprintf("it is too large (more than %d bytes)", max))
	}
	return data, nil
}

// finalPathOf is the real path of an open handle, without the \\?\ prefix; a network path keeps its \\server\share form.
func finalPathOf(h windows.Handle) (string, error) {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) > len(buf) {
			buf = make([]uint16, n+1)
			continue
		}
		s := windows.UTF16ToString(buf[:n])
		if rest, ok := strings.CutPrefix(s, `\\?\UNC\`); ok {
			return `\\` + rest, nil
		}
		return strings.TrimPrefix(s, `\\?\`), nil
	}
}
