//go:build windows

package update

import (
	"golang.org/x/sys/windows"
)

// anyServiceInstalled says whether one of the named Windows services exists. It asks with the lowest rights (connect to the service
// manager, query one service), so it works for the person's own unelevated process; the full-rights connect of the mgr package is
// refused there. A manager that cannot be asked counts as "yes": the cautious answer sends the person to the bar instead of raising
// a prompt nobody can answer.
func anyServiceInstalled(names ...string) bool {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return true
	}
	defer windows.CloseServiceHandle(h)
	for _, n := range names {
		p, err := windows.UTF16PtrFromString(n)
		if err != nil {
			return true
		}
		s, err := windows.OpenService(h, p, windows.SERVICE_QUERY_STATUS)
		if err == nil {
			windows.CloseServiceHandle(s)
			return true
		}
		if err != windows.ERROR_SERVICE_DOES_NOT_EXIST {
			return true
		}
	}
	return false
}
