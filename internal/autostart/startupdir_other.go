//go:build !windows

package autostart

// StartupDir reports that there is no Startup folder off Windows.
func StartupDir() (string, error) { return "", ErrNoStartupDir }
