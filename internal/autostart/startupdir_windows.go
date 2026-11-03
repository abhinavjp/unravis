//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows"
)

// StartupDir is the current user's Startup folder, asked from Windows (it may be redirected, for example to OneDrive).
func StartupDir() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Startup, 0)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", errors.New("autostart: Windows returned an empty Startup folder")
	}
	return dir, nil
}
