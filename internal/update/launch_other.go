//go:build !windows

package update

import "errors"

// CanLaunch is false off Windows: the installer is a Windows program.
const CanLaunch = false

// LaunchSetup does not exist off Windows.
func LaunchSetup(path, sha256hex, version string) error {
	return errors.New("installing an update works on Windows only")
}

// LaunchRollback does not exist off Windows.
func LaunchRollback(path, sha256hex, version string) error {
	return errors.New("going back to the old version works on Windows only")
}

// StartVerified does not exist off Windows.
func StartVerified(path, sha256hex string, args ...string) error {
	return errors.New("starting a verified program works on Windows only")
}
