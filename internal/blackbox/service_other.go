//go:build !windows

package blackbox

import "errors"

// IsAdmin is false off Windows: the service only exists on Windows.
func (OSEnv) IsAdmin() bool { return false }

// WinService does not exist off Windows; every call says so.
type WinService struct{}

var errNoService = errors.New("the Blackbox service is Windows only")

// State reports that there is no service.
func (WinService) State(string) (SvcState, error) { return SvcNotInstalled, errNoService }

// Create is not available.
func (WinService) Create(ServiceSpec) error { return errNoService }

// Start is not available.
func (WinService) Start(string) error { return errNoService }

// Stop is not available.
func (WinService) Stop(string) error { return errNoService }

// Delete is not available.
func (WinService) Delete(string) error { return errNoService }
