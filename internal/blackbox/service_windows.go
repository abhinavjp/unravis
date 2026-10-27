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
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400)
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
