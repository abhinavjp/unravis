//go:build !windows

package update

import "errors"

// CanLaunch is false off Windows: the installer is a Windows program.
const CanLaunch = false

// LaunchSetup does not exist off Windows.
func LaunchSetup(path, sha256hex, version string) error {
	return errors.New("installing an update works on Windows only")
}
