//go:build windows

package update

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

// anyServiceInstalled says whether one of the named Windows services exists. A manager that cannot be opened or asked counts as
// "yes": the cautious answer sends the person to the bar instead of raising a prompt nobody can answer.
func anyServiceInstalled(names ...string) bool {
	m, err := mgr.Connect()
	if err != nil {
		return true
	}
	defer m.Disconnect()
	for _, n := range names {
		s, err := m.OpenService(n)
		if err == nil {
			s.Close()
			return true
		}
		if err != windows.ERROR_SERVICE_DOES_NOT_EXIST {
			return true
		}
	}
	return false
}
