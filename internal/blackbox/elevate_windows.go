//go:build windows

package blackbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OSElevator is the real Elevator. It is never used by tests: it shows the
// Windows permission prompt and changes the machine. NOT RUN: needs a clean VM.
//
// An elevated process cannot pipe its output back to an ordinary one, so the
// helper is started as "cmd /c program args > file" with the verb runas, and the
// file is read and deleted at once. The file sits in a folder made for this one
// call under the user's temp folder. It can hold the read key for a moment, so it
// is removed on every path, including a cancelled prompt.
type OSElevator struct {
	// Timeout bounds one call (0 is two minutes: an install waits for the service to start).
	Timeout time.Duration
}

const seeMaskNoCloseProcess = 0x00000040

type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// Run runs exe with args and returns what it printed.
func (e OSElevator) Run(ctx context.Context, exe string, args []string, elevated bool) ([]byte, error) {
	if !elevated {
		cmd := exec.CommandContext(ctx, exe, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if len(out) > 0 {
			// The helper answers a failure with a JSON line and a non-zero exit.
			return out, nil
		}
		return nil, err
	}
	for _, a := range append([]string{exe}, args...) {
		if strings.ContainsAny(a, "&|<>^%\"\r\n") {
			return nil, fmt.Errorf("a character in %q cannot be passed to the helper", a)
		}
	}
	dir, err := os.MkdirTemp("", "unravis-bb-out-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	outPath := filepath.Join(dir, "answer.json")
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, windows.EscapeArg(exe))
	for _, a := range args {
		parts = append(parts, windows.EscapeArg(a))
	}
	params := `/c "` + strings.Join(parts, " ") + ` > ` + windows.EscapeArg(outPath) + `"`

	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString("cmd.exe")
	p, _ := windows.UTF16PtrFromString(params)
	info := shellExecuteInfo{fMask: seeMaskNoCloseProcess, lpVerb: verb, lpFile: file, lpParameters: p, nShow: windows.SW_HIDE}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return nil, ErrDeclined
		}
		return nil, callErr
	}
	defer windows.CloseHandle(info.hProcess)
	limit := e.Timeout
	if limit == 0 {
		limit = 2 * time.Minute
	}
	done := make(chan uint32, 1)
	go func() {
		ev, _ := windows.WaitForSingleObject(info.hProcess, uint32(limit/time.Millisecond))
		done <- ev
	}()
	select {
	case ev := <-done:
		if ev != windows.WAIT_OBJECT_0 {
			return nil, errors.New("the Blackbox program did not finish in time")
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return os.ReadFile(outPath)
}
