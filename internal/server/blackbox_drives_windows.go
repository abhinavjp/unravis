//go:build windows

package server

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// listDrives reads the drive letters Windows reports, then reads each volume's
// label, type and space. Failures affect only the field being read: a missing
// label stays empty and unreadable space stays null. No error text is returned.
func listDrives() []bbDriveInfo {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return []bbDriveInfo{}
	}

	systemLetter := ""
	if root := os.Getenv("SystemRoot"); len(root) >= 2 && root[1] == ':' {
		systemLetter = strings.ToUpper(root[:1])
	}

	drives := make([]bbDriveInfo, 0, 26)
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}

		drive := bbDriveInfo{Letter: letter, System: letter == systemLetter}
		switch windows.GetDriveType(rootPtr) {
		case windows.DRIVE_FIXED:
			drive.Type = "fixed"
		case windows.DRIVE_REMOVABLE:
			drive.Type = "removable"
		case windows.DRIVE_REMOTE:
			drive.Type = "network"
		default:
			drive.Type = "other"
		}

		// A network drive can block for many seconds when its server is away, and this
		// request runs on the Folder step. Its label and space are left out.
		if drive.Type == "network" {
			drives = append(drives, drive)
			continue
		}

		volumeName := make([]uint16, 261)
		if err := windows.GetVolumeInformation(rootPtr, &volumeName[0], uint32(len(volumeName)), nil, nil, nil, nil, 0); err == nil {
			drive.Label = windows.UTF16ToString(volumeName)
		}

		var free, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(rootPtr, &free, &total, &totalFree); err == nil {
			drive.FreeBytes = &free
			drive.TotalBytes = &total
		}
		drives = append(drives, drive)
	}
	return drives
}
