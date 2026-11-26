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

// LaunchSilent does not exist off Windows.
func LaunchSilent(path, sha256hex, version string) error {
	return errors.New("installing an update works on Windows only")
}

// LaunchRecover does not exist off Windows.
func LaunchRecover(path string) error {
	return errors.New("recovering a cut-off update works on Windows only")
}

// LaunchSilentReopen does not exist off Windows.
func LaunchSilentReopen(path, sha256hex, version string) error {
	return errors.New("installing an update works on Windows only")
}
