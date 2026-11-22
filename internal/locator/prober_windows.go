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

// PipeExists asks the pipe file system without connecting. Success, a busy pipe (all instances in use), a wait timeout (a server with
// no free instance) and an access error mean the name is taken by a live server. Any other error fails closed: the pipe is reported
// absent, so an unexpected failure never makes a dead owner look alive. This is existence only, not server identity (see the package
// comment).
func (winProber) PipeExists(name string) bool {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	r, _, callErr := procWaitNamedPipe.Call(uintptr(unsafe.Pointer(p)), 1)
	if r != 0 {
		return true
	}
	return errors.Is(callErr, windows.ERROR_SEM_TIMEOUT) || errors.Is(callErr, windows.ERROR_PIPE_BUSY) || errors.Is(callErr, windows.ERROR_ACCESS_DENIED)
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
