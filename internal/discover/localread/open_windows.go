//go:build windows

package localread

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

type systemOpener struct{}

const (
	volumeNameDOS          = 0x0 // VOLUME_NAME_DOS | FILE_NAME_NORMALIZED
	threadModeBackgroundOn = 0x00010000
	threadModeBackgroundOf = 0x00020000
)

var procSetThreadPriority = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadPriority")

// fileSource is an opened disk file. ReadAt on an *os.File over a synchronous
// handle is a plain ReadFile with an offset.
type fileSource struct{ f *os.File }

func (s fileSource) ReadAt(p []byte, off int64) (int, error) { return s.f.ReadAt(p, off) }
func (s fileSource) Close() error                            { return s.f.Close() }

// OpenSource implements Opener. See the package comment and the discovery
// design spec section 2.2 for the rules enforced here, in this order:
//
//  1. shape of the canonical path (no UNC, ADS, "..", long-path prefix);
//  2. the root is a fixed local drive;
//  3. an 8.3 alias segment is refused;
//  4. no component, file included, is a reparse point; the leaf is not a directory;
//  5. CreateFile: GENERIC_READ, share read|write|delete, OPEN_EXISTING,
//     SEQUENTIAL_SCAN and OPEN_REPARSE_POINT, never BACKUP_SEMANTICS;
//  6. on the handle: disk file, not a directory, not a reparse point, final
//     path equals the canonical path (case-insensitive), identity equals expect.
func (systemOpener) OpenSource(canonical string, expect FileID) (Source, Info, error) {
	if err := CheckShape(canonical); err != nil {
		return nil, Info{}, err
	}
	if err := checkDrive(canonical); err != nil {
		return nil, Info{}, err
	}
	if err := checkShortName(canonical); err != nil {
		return nil, Info{}, err
	}
	if err := checkComponents(canonical); err != nil {
		return nil, Info{}, err
	}

	name, err := windows.UTF16PtrFromString(canonical)
	if err != nil {
		return nil, Info{}, unsafe("bad characters in path")
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_SEQUENTIAL_SCAN|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, Info{}, mapErr(err)
	}
	info, err := checkHandle(h, canonical, expect)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, Info{}, err
	}
	return fileSource{os.NewFile(uintptr(h), info.Base)}, info, nil
}

func mapErr(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return ErrAccessDenied
	}
	var en windows.Errno
	if errors.As(err, &en) {
		return fmt.Errorf("localread: open failed: %w", en)
	}
	return fmt.Errorf("localread: open failed: %w", err)
}

func checkDrive(p string) error {
	root, err := windows.UTF16PtrFromString(p[:3])
	if err != nil {
		return unsafe("bad characters in path")
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return unsafe("not a fixed local drive")
	}
	return nil
}

// checkShortName refuses a path with a segment containing '~' when the long
// form of the path differs: that segment is an 8.3 alias.
func checkShortName(p string) error {
	if !strings.Contains(p, "~") {
		return nil
	}
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return unsafe("bad characters in path")
	}
	buf := make([]uint16, len(p)+MaxPath)
	n, err := windows.GetLongPathName(in, &buf[0], uint32(len(buf)))
	if err != nil {
		return mapErr(err)
	}
	if int(n) > len(buf) {
		return unsafe("path changes under long-name expansion")
	}
	if !strings.EqualFold(windows.UTF16ToString(buf[:n]), p) {
		return unsafe("8.3 alias in path")
	}
	return nil
}

// checkComponents walks from the first directory under the drive root down to
// the file and refuses a reparse point at any level, a non-directory parent
// and a directory leaf.
func checkComponents(p string) error {
	segs := strings.Split(p[3:], "\\")
	cur := p[:2] // "C:"
	for i, seg := range segs {
		cur += "\\" + seg
		w, err := windows.UTF16PtrFromString(cur)
		if err != nil {
			return unsafe("bad characters in path")
		}
		attrs, err := windows.GetFileAttributes(w)
		if err != nil {
			return mapErr(err)
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return unsafe("reparse point in path")
		}
		isDir := attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0
		if i < len(segs)-1 && !isDir {
			return unsafe("parent is not a directory")
		}
		if i == len(segs)-1 && isDir {
			return unsafe("not a regular file")
		}
	}
	return nil
}

func checkHandle(h windows.Handle, canonical string, expect FileID) (Info, error) {
	ft, err := windows.GetFileType(h)
	if err != nil || ft != windows.FILE_TYPE_DISK {
		return Info{}, unsafe("not a disk file")
	}
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return Info{}, mapErr(err)
	}
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return Info{}, unsafe("not a regular file")
	}
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return Info{}, unsafe("file is a reparse point")
	}
	final, err := finalPath(h)
	if err != nil {
		return Info{}, mapErr(err)
	}
	if !strings.EqualFold(final, canonical) {
		return Info{}, unsafe("final path differs from the expected path")
	}
	id := FileID{VolumeSerial: bi.VolumeSerialNumber, FileIndex: uint64(bi.FileIndexHigh)<<32 | uint64(bi.FileIndexLow)}
	if !expect.IsZero() && id != expect {
		return Info{}, unsafe("file identity changed since the scan")
	}
	return Info{
		ID:      id,
		Size:    int64(bi.FileSizeHigh)<<32 | int64(bi.FileSizeLow),
		ModTime: time.Unix(0, bi.LastWriteTime.Nanoseconds()).UTC(),
		Base:    baseName(canonical),
	}, nil
}

// finalPath returns the DOS-form final path of an open handle without the
// `\\?\` prefix. A UNC result is refused.
func finalPath(h windows.Handle) (string, error) {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS)
		if err != nil {
			return "", err
		}
		if int(n) > len(buf) {
			buf = make([]uint16, n)
			continue
		}
		s := windows.UTF16ToString(buf[:n])
		if strings.HasPrefix(s, `\\?\UNC\`) {
			return "", unsafe("file is on a network path")
		}
		return strings.TrimPrefix(s, `\\?\`), nil
	}
}

// EnterBackground puts the calling goroutine's OS thread into background
// processing mode (low CPU and I/O priority, THREAD_MODE_BACKGROUND_BEGIN) and
// locks the goroutine to it. Call it from the goroutine that reads, and call
// the returned function when done. Failure to enter the mode is not an error:
// the returned function is then only an unlock.
func EnterBackground() (leave func()) {
	runtime.LockOSThread()
	th, err := windows.GetCurrentThread()
	if err != nil {
		return runtime.UnlockOSThread
	}
	r, _, _ := procSetThreadPriority.Call(uintptr(th), uintptr(threadModeBackgroundOn))
	if r == 0 {
		return runtime.UnlockOSThread
	}
	return func() {
		procSetThreadPriority.Call(uintptr(th), uintptr(threadModeBackgroundOf))
		runtime.UnlockOSThread()
	}
}
