//go:build windows

package blackbox

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// helper is started directly (never through cmd.exe: a command interpreter would
// run whatever the user's AutoRun registry entry names, with administrator
// rights) with the verb runas and the extra argument "--answer-file <path>".
// The helper writes its answer there; the file is read and deleted at once. The
// file sits in a folder made for this one call under the user's temp folder. It
// can hold the read key for a moment, so it is removed on every path, including
// a cancelled prompt.
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
	// Started directly, so shell symbols such as & | < > ^ % are plain text. Only
	// control characters and a NUL cannot be carried on a command line.
	for _, a := range append([]string{exe}, args...) {
		if strings.ContainsAny(a, "\x00\r\n") {
			return nil, fmt.Errorf("a control character in %q cannot be passed to the helper", a)
		}
	}
	dir, err := os.MkdirTemp("", "unravis-bb-out-")
	if err != nil {
		return nil, err
	}
	// started is set once the elevated process exists; finished once it has
	// ended. Until it ends the folder is not removed: the process could still
	// write the answer, which can hold the key. Before it starts, every path
	// (a declined prompt included) removes the folder at once.
	started, finished := false, false
	var slot *answerSlot
	defer func() {
		if !started || finished {
			slot.Close()
			os.RemoveAll(dir)
		}
	}()
	// SEC-FIX-04: Unravis makes the answer file itself and keeps both it and
	// its folder open without the delete share right, so nobody can swap
	// either for a link while the prompt is open. The elevated helper only
	// opens that existing file; it never creates a path an administrator
	// would follow.
	slot, err = openAnswerSlot(dir)
	if err != nil {
		return nil, err
	}
	params := elevatedParams(args, slot.Path)

	verb, _ := windows.UTF16PtrFromString("runas")
	file, ferr := windows.UTF16PtrFromString(exe)
	if ferr != nil {
		return nil, ferr
	}
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
	started = true
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
			lateCleanup(info.hProcess, dir, slot)
			return nil, errors.New("the Blackbox program did not finish in time")
		}
		finished = true
	case <-ctx.Done():
		closeProc = false
		lateCleanup(info.hProcess, dir, slot)
		return nil, ctx.Err()
	}
	return slot.Read()
}

// elevatedParams is the command line given to the helper: its own arguments, each
// quoted for the Windows command line, then the answer file.
func elevatedParams(args []string, outPath string) string {
	parts := make([]string, 0, len(args)+2)
	for _, a := range args {
		parts = append(parts, windows.EscapeArg(a))
	}
	parts = append(parts, AnswerFileFlag, windows.EscapeArg(outPath))
	return strings.Join(parts, " ")
}

// lateCleanup removes the answer folder after an elevated process that Unravis
// stopped waiting for has ended, and closes its handle. A process that is still
// running could write the answer (and the key in it) after the folder was removed.
func lateCleanup(proc windows.Handle, dir string, slot *answerSlot) {
	go func() {
		defer windows.CloseHandle(proc)
		windows.WaitForSingleObject(proc, lateCleanupWait)
		slot.Close()
		os.RemoveAll(dir)
	}()
}

// lateCleanupWait is how long the late cleanup waits for the process to end.
const lateCleanupWait = uint32(30 * 60 * 1000)

// maxAnswerBytes bounds how much of the answer file is read.
const maxAnswerBytes = 1 << 20

// answerSlot is the answer folder and the pre-made answer file of one elevated
// call, held open with share modes that forbid delete and rename.
type answerSlot struct {
	Path string
	dir  *os.File
	file *os.File
}

// openHeld opens path without following a link, with read and write sharing but
// not delete, and refuses a reparse point or the wrong kind of object. create
// makes a new file (and fails if the name already exists).
func openHeld(path string, isDir, create bool, access uint32) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	disp := uint32(windows.OPEN_EXISTING)
	if isDir {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	} else {
		flags |= windows.FILE_ATTRIBUTE_NORMAL
		if create {
			disp = windows.CREATE_NEW
		}
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, disp, flags, 0)
	if err != nil {
		return nil, err
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != isDir {
		windows.CloseHandle(h)
		return nil, errors.New("the temporary answer location is a link or the wrong kind of item")
	}
	return os.NewFile(uintptr(h), path), nil
}

// openAnswerSlot holds dir, limits it to the user, administrators and the system
// through that handle, and creates answer.json in it.
func openAnswerSlot(dir string) (*answerSlot, error) {
	d, err := openHeld(dir, true, false, windows.GENERIC_READ|windows.WRITE_DAC)
	if err != nil {
		return nil, fmt.Errorf("unravis could not prepare a private place for the answer, so nothing was changed. Try again: %w", err)
	}
	if err := securePrivateHandle(windows.Handle(d.Fd())); err != nil {
		d.Close()
		return nil, fmt.Errorf("unravis could not make the answer folder private, so nothing was changed. Try again: %w", err)
	}
	path := filepath.Join(dir, "answer.json")
	f, err := openHeld(path, false, true, windows.GENERIC_READ|windows.GENERIC_WRITE)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("unravis could not make the answer file, so nothing was changed. Try again: %w", err)
	}
	return &answerSlot{Path: path, dir: d, file: f}, nil
}

// Read returns what the helper wrote, through the handle held since before the
// prompt. An empty file means the helper never answered.
func (s *answerSlot) Read() ([]byte, error) {
	if _, err := s.file.Seek(0, 0); err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(s.file, maxAnswerBytes))
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoAnswer
	}
	return out, nil
}

// Close releases both handles; it is safe on a nil slot.
func (s *answerSlot) Close() {
	if s == nil {
		return
	}
	if s.file != nil {
		s.file.Close()
	}
	if s.dir != nil {
		s.dir.Close()
	}
}

// privateSDDL limits a folder to the signed-in user, administrators and the
// system, with no inherited entries, so another account on the PC cannot read an
// answer that holds a read key. (An administrator who approves the prompt with
// other credentials still can write it.)
func privateSDDL() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return "D:PAI(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)", nil
}

// securePrivateHandle applies privateSDDL through an open handle, so the change
// lands on the object that was checked and never on a link put in its place.
func securePrivateHandle(h windows.Handle) error {
	sddl, err := privateSDDL()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
