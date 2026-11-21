//go:build windows

package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// CanLaunch says whether this build can hand a downloaded setup to the installer.
const CanLaunch = true

// launchGrace is how long a started setup must stay up to count as started.
const launchGrace = 3 * time.Second

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// openHeld opens path for reading with sharing that allows other readers only: nobody can write, rename or delete the file while
// the handle is open. Hash from this handle and start the process before closing it, and what was hashed is what starts.
func openHeld(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// LaunchSetup starts the downloaded setup program as an upgrade, in its own window, apart from this program. It passes the
// hash and version the signed manifest promised, and the setup refuses to run if its own file or contents differ.
// path must be an absolute path to the file this program downloaded. The file is held open with no write, rename or delete sharing
// from the moment it is hashed until the process has started, so nothing can swap it in between.
func LaunchSetup(path, sha256hex, version string) error {
	return launchSetup(path, sha256hex, version, nil)
}

// LaunchRollback starts the old setup kept by the last upgrade, to go back to version. It is the same hand-off as LaunchSetup, with
// the one flag that lets setup install an older version over a newer one, and it is silent too (no window, the app is closed and
// started again by the setup program). The caller passes the path, hash and version from the
// marker the installer wrote; nothing from the network reaches this call.
func LaunchRollback(path, sha256hex, version string) error {
	extra := []string{"--allow-downgrade"}
	// The kept setup is the OLD version's. Only one that knows --close-app can go back silently; an older one does it in its window.
	if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte(closeAppMarker)) {
		extra = append(extra, "--silent", "--close-app")
	}
	return launchSetup(path, sha256hex, version, extra)
}

// closeAppMarker is text that only a setup program that knows --close-app contains (the flag's help line). "close-app" alone is also a
// step name in every older setup, so it cannot tell them apart.
const closeAppMarker = "what an automatic update does"

// LaunchSilent starts the downloaded setup with no window of any kind: --silent --upgrade --close-app. The setup program asks this
// app to quit, replaces the program files, starts the app again (the old version again if the update had to be undone) and leaves a
// plain note of the result for the app to show. The same hash and version checks as LaunchSetup apply.
func LaunchSilent(path, sha256hex, version string) error {
	return launchSetup(path, sha256hex, version, []string{"--silent", "--close-app"})
}

func launchSetup(path, sha256hex, version string, extra []string) error {
	if !filepath.IsAbs(path) || !sha256RE.MatchString(sha256hex) {
		return errors.New("the setup file or its hash is not valid")
	}
	if v, err := ParseVersion(version); err != nil || v.String() != version {
		return errors.New("the version is not valid")
	}
	f, err := openHeld(path)
	if err != nil {
		return errors.New("could not open the setup file")
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return errors.New("could not read the setup file")
	}
	if hex.EncodeToString(h.Sum(nil)) != sha256hex {
		return errors.New("the setup file changed after it was checked; it was not started")
	}
	cmd := exec.Command(path, append([]string{"--upgrade", "--expect-sha256", sha256hex, "--expect-version", version}, extra...)...)
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: hidden(extra)}
	if err := cmd.Start(); err != nil {
		return errors.New("could not start the setup program")
	}
	// The setup has no console, so if it refuses to run (its own file or version check) nobody would see why. A setup that is still
	// running after a few seconds is up; one that stopped at once is reported.
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	select {
	case <-exited:
		return errors.New("the setup program closed right away; its log is in the setup-logs folder under Unravis in your local app data")
	case <-time.After(launchGrace):
	}
	return nil
}

// StartVerified starts the program at path with args, detached from this process, only if the file's SHA-256 is sha256hex. The file is
// held open with no write, rename or delete sharing while it is hashed and started, so the bytes that were checked are the bytes that
// run. It does not wait for the program: the Blackbox service starts its update helper this way, and the helper stops that service.
func StartVerified(path, sha256hex string, args ...string) error {
	if !filepath.IsAbs(path) || !sha256RE.MatchString(sha256hex) {
		return errors.New("the program file or its hash is not valid")
	}
	f, err := openHeld(path)
	if err != nil {
		return errors.New("could not open the program file")
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return errors.New("could not read the program file")
	}
	if hex.EncodeToString(h.Sum(nil)) != sha256hex {
		return errors.New("the program file changed after it was checked; it was not started")
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return errors.New("could not start the program")
	}
	return cmd.Process.Release()
}

// hidden says the setup runs silently: nothing it starts may show a window.
func hidden(extra []string) bool {
	for _, a := range extra {
		if a == "--silent" {
			return true
		}
	}
	return false
}

// LaunchRecover starts `setup --recover` from the program folder: no window, detached from this program. The setup puts back what a
// cut-off run changed, asks this program to quit if it must, and starts it again. path must be an absolute path to the regular file
// unravis-setup.exe next to this program; it is the same trust as this program's own folder. It does not wait for the result.
func LaunchRecover(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("the setup file is not valid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("the setup file is not a plain file")
	}
	cmd := exec.Command(path, "--recover")
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return errors.New("could not start the setup program")
	}
	return cmd.Process.Release()
}
