//go:build windows

package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/keeper"
)

// The real elevation. NOT RUN: needs a clean VM (it shows the Windows approval prompt and starts an administrator process).
//
// The window makes a pipe with a random name that only its own account, SYSTEM and Administrators may open (remote clients are
// refused), then starts "setup.exe --worker --pipe <name> --parent <window pid>" with the verb runas. It accepts exactly one client and
// only when the client is the process Windows just started; then it closes the pipe name, so nothing else can connect. The worker, for
// its part, checks that the pipe is served by the process it was told about and that that process is this same setup program, and
// opens the pipe at identification level, so the window cannot act as the administrator.

// OSElevator starts the elevated worker.
type OSElevator struct {
	// SetupExe is the running setup program (os.Executable).
	SetupExe string
	// Timeout is how long to wait for the worker to connect once the prompt is approved (0 is two minutes).
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

type peerer interface {
	Peer() (pid, session uint32, ok bool)
}

// workerConn closes the worker's process handle with the pipe.
type workerConn struct {
	keeper.Conn
	proc windows.Handle
}

func (c *workerConn) Close() error {
	err := c.Conn.Close()
	windows.CloseHandle(c.proc)
	return err
}

// Start shows the approval prompt and returns the pipe to the worker. No at the prompt is ErrUACCancelled.
func (e OSElevator) Start(ctx context.Context) (io.ReadWriteCloser, error) {
	sid, err := CurrentUserSID()
	if err != nil {
		return nil, err
	}
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	name := `\\.\pipe\unravis-setup-` + hex.EncodeToString(rnd[:])
	ln, err := keeper.ListenPipe(name, keeper.PipeListenOptions{SDDL: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + sid + ")"})
	if err != nil {
		return nil, fmt.Errorf("could not make the pipe to the administrator part: %w", err)
	}
	defer ln.Close() // one client only: the name goes away as soon as the worker is connected (or not)

	params := "--worker --pipe " + windows.EscapeArg(name) + " --parent " + strconv.Itoa(os.Getpid())
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(e.SetupExe)
	if err != nil {
		return nil, err
	}
	p, _ := windows.UTF16PtrFromString(params)
	info := shellExecuteInfo{fMask: seeMaskNoCloseProcess, lpVerb: verb, lpFile: file, lpParameters: p, nShow: windows.SW_HIDE}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return nil, ErrUACCancelled
		}
		return nil, callErr
	}
	if info.hProcess == 0 {
		return nil, errors.New("Windows started the administrator part without saying which process it is")
	}
	keep := false
	defer func() {
		if !keep {
			windows.CloseHandle(info.hProcess)
		}
	}()
	workerPID, err := windows.GetProcessId(info.hProcess)
	if err != nil {
		return nil, err
	}

	type accepted struct {
		c   keeper.Conn
		err error
	}
	got := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		got <- accepted{c, err}
	}()
	exited := make(chan struct{})
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		for {
			ev, _ := windows.WaitForSingleObject(info.hProcess, 250)
			if ev == windows.WAIT_OBJECT_0 {
				close(exited)
				return
			}
			select {
			case <-stopWatch:
				return
			default:
			}
		}
	}()
	limit := e.Timeout
	if limit == 0 {
		limit = 2 * time.Minute
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case a := <-got:
		if a.err != nil {
			return nil, a.err
		}
		pr, ok := a.c.(peerer)
		pid, _, known := uint32(0), uint32(0), false
		if ok {
			pid, _, known = pr.Peer()
		}
		if !known || pid != workerPID {
			a.c.Close()
			return nil, errors.New("something other than setup's administrator part connected to it")
		}
		keep = true
		return &workerConn{Conn: a.c, proc: info.hProcess}, nil
	case <-exited:
		return nil, errors.New("the administrator part of setup ended before it connected")
	case <-timer.C:
		_ = windows.TerminateProcess(info.hProcess, 1)
		return nil, errors.New("the administrator part of setup did not connect in time")
	case <-ctx.Done():
		_ = windows.TerminateProcess(info.hProcess, 1) // it has not been sent anything, so it has changed nothing
		return nil, ErrCancelled
	}
}

// WorkerPipe is the worker's end, with what Windows says about the window on the other end.
type WorkerPipe struct {
	Conn io.ReadWriteCloser
	// SID and Profile are the account of the setup window and its profile folder (ProfileList), both read from Windows.
	SID, Profile string
}

// DialWorkerPipe is the worker's side of OSElevator.Start.
func DialWorkerPipe(name string, parentPID uint32) (*WorkerPipe, error) {
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	// Identification level: the window may learn who the worker is, never act as it.
	h, err := windows.CreateFile(namep, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		return nil, fmt.Errorf("could not open the pipe to the setup window: %w", err)
	}
	fail := func(err error) (*WorkerPipe, error) {
		windows.CloseHandle(h)
		return nil, err
	}
	var server uint32
	if err := windows.GetNamedPipeServerProcessId(h, &server); err != nil || server != parentPID {
		return fail(errors.New("the pipe is not served by the setup window that started this worker"))
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, server)
	if err != nil {
		return fail(err)
	}
	defer windows.CloseHandle(proc)
	if self, err := os.Executable(); err != nil || !samePath(imagePath(server), self) {
		return fail(errors.New("the pipe is served by a program other than this setup"))
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &tok); err != nil {
		return fail(err)
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return fail(err)
	}
	sid := u.User.Sid.String()
	profile, err := ProfileOf(sid)
	if err != nil {
		return fail(fmt.Errorf("Windows has no profile folder for the account running setup: %w", err))
	}
	f := os.NewFile(uintptr(h), name) // overlapped handle: reads and writes run at once
	if f == nil {
		return fail(errors.New("could not use the pipe"))
	}
	return &WorkerPipe{Conn: f, SID: sid, Profile: profile}, nil
}
