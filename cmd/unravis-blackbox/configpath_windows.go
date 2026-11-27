//go:build windows

package main

import "golang.org/x/sys/windows"

// driveIsFixed is true for a drive built into this PC. A mapped network drive, a removable drive or a RAM disk is not.
func driveIsFixed(letter byte) bool {
	root, err := windows.UTF16PtrFromString(string([]byte{letter, ':', '\\'}))
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_FIXED
}
