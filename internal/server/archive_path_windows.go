//go:build windows

package server

import (
	"golang.org/x/sys/windows"
)

// finalFolder returns the final name of an existing folder: 8.3 short names are
// expanded and symlinks and junctions are followed, as Windows itself resolves
// them (GetFinalPathNameByHandle). filepath.EvalSymlinks does not follow a
// junction, so it cannot be used here. The result may carry the \?\ prefix;
// the caller strips it and checks the shape again.
func finalFolder(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}

// driveIsRemote reports whether the volume root (such as `C:\`) is a network or
// mapped drive. An unknown or missing drive is an error, which the caller treats
// as refused.
func driveIsRemote(volume string) (bool, error) {
	p, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return false, err
	}
	switch windows.GetDriveType(p) {
	case windows.DRIVE_REMOTE:
		return true, nil
	case windows.DRIVE_UNKNOWN, windows.DRIVE_NO_ROOT_DIR:
		return false, errDriveUnknown
	}
	return false, nil
}

// archiveFreeBytes is the space available to the current user on the volume that
// holds path.
func archiveFreeBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
