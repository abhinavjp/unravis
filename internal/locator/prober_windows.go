//go:build windows

package locator

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type winProber struct{}

// SystemProber is the real prober: OpenProcess plus GetProcessTimes for the pid, WaitNamedPipe for the pipe.
func SystemProber() Prober { return winProber{} }

func (winProber) ProcessStart(pid int) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return time.Time{}, err
	}
	const stillActive = 259
	if code != stillActive {
		return time.Time{}, errors.New("locator: process has exited")
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, creation.Nanoseconds()).UTC(), nil
}

// PipeExists asks the pipe file system without connecting: "no such pipe" is the only answer that means absent. A busy pipe (all
// instances in use) or an access error still means the name is taken by a live server.
func (winProber) PipeExists(name string) bool {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	r, _, callErr := procWaitNamedPipe.Call(uintptr(unsafe.Pointer(p)), 1)
	if r != 0 {
		return true
	}
	return !errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(callErr, windows.ERROR_PATH_NOT_FOUND)
}

var procWaitNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

// SelfStart is this process's creation time, the value the owner writes into the locator.
func SelfStart() (time.Time, error) { return winProber{}.ProcessStart(os.Getpid()) }

// StartDetached launches exe with args in its own process group and no console window, and does not wait for it.
func StartDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", exe, err)
	}
	return cmd.Process.Release()
}
