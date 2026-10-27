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

// SecureDir gives SYSTEM and Administrators full control of dir, removes every
// inherited permission, and, with usersRead, lets ordinary users read and list
// it. What is already inside gets the same rights.
func (OSEnv) SecureDir(dir string, usersRead bool) error {
	sd, err := windows.SecurityDescriptorFromString(secureSDDL(usersRead))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
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
func (WinService) State(name string) (SvcState, error) {
	m, err := connect()
	if err != nil {
		return SvcNotInstalled, err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return SvcNotInstalled, nil
		}
		return SvcNotInstalled, err
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return SvcNotInstalled, err
	}
	return mapState(st.State), nil
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

// Create registers the service to start automatically as LocalSystem and to
// restart itself after a crash (the recorder must keep recording).
func (WinService) Create(spec ServiceSpec) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.CreateService(spec.Name, spec.ExePath, mgr.Config{
		DisplayName: spec.DisplayName,
		Description: spec.Description,
		StartType:   mgr.StartAutomatic,
	}, spec.Args...)
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
