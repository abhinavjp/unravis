//go:build windows

package blackbox

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// WinService is the real ServiceControl: the Windows service manager. It is
// never used by tests; it needs an elevated process and changes the machine.
type WinService struct{}

// IsAdmin says whether this process runs elevated.
func (OSEnv) IsAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// secureSDDL is the permission list SecureDir sets: protected (nothing
// inherited), full control for SYSTEM and Administrators, and with usersRead
// read and list for ordinary users.
func secureSDDL(usersRead bool) string {
	sddl := "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if usersRead {
		sddl += "(A;OICI;GRGX;;;BU)"
	}
	return sddl
}

// SecureDir makes Administrators the owner of dir (SEC-03: an owner can always
// change the permissions back, so a folder another account made would stay that
// account's), gives SYSTEM and Administrators full control, removes every
// inherited permission, and, with usersRead, lets ordinary users read and list
// it. What is already inside gets the same rights. The folder is opened once
// without following a link and everything is applied through that handle
// (SEC-FIX-06); a folder owned by another account is refused (SEC-FIX-05).
func (OSEnv) SecureDir(dir string, usersRead bool) error {
	return secureDirHandle(dir, usersRead, true)
}

func connect() (*mgr.Mgr, error) { return mgr.Connect() }

func mapState(s svc.State) SvcState {
	switch s {
	case svc.Stopped:
		return SvcStopped
	case svc.StartPending:
		return SvcStartPending
	case svc.Running, svc.ContinuePending, svc.PausePending, svc.Paused:
		return SvcRunning
	case svc.StopPending:
		return SvcStopPending
	}
	return SvcStopped
}

// State reports the service state, or SvcNotInstalled when there is none.
// State asks for the state with read-only handles, so it works without administrator rights. mgr.Connect and mgr.OpenService ask for full
// access, which a normal user is refused, so the Where step could never tell whether a Blackbox is installed.
func (WinService) State(name string) (SvcState, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return SvcNotInstalled, err
	}
	defer windows.CloseServiceHandle(m)
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return SvcNotInstalled, err
	}
	s, err := windows.OpenService(m, n, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return SvcNotInstalled, nil
		}
		return SvcNotInstalled, err
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(s, &st); err != nil {
		return SvcNotInstalled, err
	}
	return mapState(svc.State(st.CurrentState)), nil
}

// recoverySetter is the part of a Windows service handle setRecovery uses.
type recoverySetter interface {
	SetRecoveryActions([]mgr.RecoveryAction, uint32) error
	SetRecoveryActionsOnNonCrashFailures(bool) error
}

// recoveryActions restart the service after a failure: quickly the first two
// times, then after a minute; the count resets after a day.
var recoveryActions = []mgr.RecoveryAction{
	{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
	{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
}

// setRecovery makes Windows restart the service after a crash and also after it
// exits with an error (a clean exit with a non-zero code, such as a failed
// start-up, counts as a non-crash failure and is ignored without the flag).
func setRecovery(s recoverySetter) error {
	if err := s.SetRecoveryActions(recoveryActions, 86400); err != nil {
		return err
	}
	return s.SetRecoveryActionsOnNonCrashFailures(true)
}

// Create registers the service to start automatically and to restart itself
// after a crash (the recorder must keep recording). It runs as LocalSystem
// unless spec.Account names another account; no password is ever set.
func (WinService) Create(spec ServiceSpec) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	c := mgr.Config{
		DisplayName:      spec.DisplayName,
		Description:      spec.Description,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: spec.Account, // empty: LocalSystem, as before
	}
	if spec.SIDUnrestricted {
		c.SidType = windows.SERVICE_SID_TYPE_UNRESTRICTED
	}
	s, err := m.CreateService(spec.Name, spec.ExePath, c, spec.Args...)
	if err != nil {
		return err
	}
	defer s.Close()
	_ = setRecovery(s) // best effort: the service still installs without it
	return nil
}

func (WinService) open(name string, f func(s *mgr.Service) error) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	return f(s)
}

// Start starts the service.
func (w WinService) Start(name string) error {
	return w.open(name, func(s *mgr.Service) error { return s.Start() })
}

// Stop asks the service to stop.
func (w WinService) Stop(name string) error {
	return w.open(name, func(s *mgr.Service) error {
		_, err := s.Control(svc.Stop)
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return err
	})
}

// Delete marks the service for deletion.
func (w WinService) Delete(name string) error {
	return w.open(name, func(s *mgr.Service) error { return s.Delete() })
}
