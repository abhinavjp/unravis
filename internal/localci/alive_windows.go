//go:build windows

package localci

import "golang.org/x/sys/windows"

// processAlive reports whether a process with this id is still running.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // a process id fits in 32 bits
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED // it exists, we may not look at it
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}
