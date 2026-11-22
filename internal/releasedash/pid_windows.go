//go:build windows

package releasedash

import (
	"time"

	"golang.org/x/sys/windows"
)

func pidAlive(pid int, startedBy time.Time) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code != 259 { // STILL_ACTIVE
		return false
	}
	var created, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exit, &kernel, &user) == nil {
		if time.Unix(0, created.Nanoseconds()).After(startedBy.Add(5 * time.Second)) {
			return false // the pid was reused by a newer process
		}
	}
	return true
}
