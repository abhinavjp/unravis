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
	// finished is set once the elevated process has ended. Until then the folder is
	// not removed: the process could still write the answer, which can hold the key.
	finished := false
	defer func() {
		if finished {
			os.RemoveAll(dir)
		}
	}()
	if err := makePrivateDir(dir); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
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
	closeProc := true
	defer func() {
		if closeProc {
			windows.CloseHandle(info.hProcess)
		}
	}()
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
			closeProc = false
			lateCleanup(info.hProcess, dir)
			return nil, errors.New("the Blackbox program did not finish in time")
		}
		finished = true
	case <-ctx.Done():
		closeProc = false
		lateCleanup(info.hProcess, dir)
		return nil, ctx.Err()
	}
	return os.ReadFile(outPath)
}

// lateCleanup removes the answer folder after an elevated process that Unravis
// stopped waiting for has ended, and closes its handle. A process that is still
// running could write the answer (and the key in it) after the folder was removed.
func lateCleanup(proc windows.Handle, dir string) {
	go func() {
		defer windows.CloseHandle(proc)
		windows.WaitForSingleObject(proc, lateCleanupWait)
		os.RemoveAll(dir)
	}()
}

// lateCleanupWait is how long the late cleanup waits for the process to end.
const lateCleanupWait = uint32(30 * 60 * 1000)

// makePrivateDir limits dir to the signed-in user, administrators and the system,
// with no inherited entries, so another account on the PC cannot read an answer
// that holds a read key. (An administrator who approves the prompt with other
// credentials still can write it.)
func makePrivateDir(dir string) error {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	sddl := "D:PAI(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
