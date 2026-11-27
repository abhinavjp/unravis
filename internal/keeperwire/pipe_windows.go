//go:build windows

package keeperwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	modadvapi32                    = windows.NewLazySystemDLL("advapi32.dll")
	procWaitNamedPipeW             = modkernel32.NewProc("WaitNamedPipeW")
	procImpersonateNamedPipeClient = modadvapi32.NewProc("ImpersonateNamedPipeClient")
)

// PipeBuffer is the size of the pipe's buffers and of the conn's copy buffers.
const PipeBuffer = 64 << 10

/* ---------------------------------------------------------------- conn */

type PipeConn struct {
	h      windows.Handle
	server bool
	closed atomic.Bool

	rmu  sync.Mutex
	rov  *windows.Overlapped
	rbuf []byte
	wmu  sync.Mutex
	wov  *windows.Overlapped
	wbuf []byte

	dmu      sync.Mutex
	deadline time.Time
}

func NewPipeConn(h windows.Handle, server bool) (*PipeConn, error) {
	re, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	we, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(re)
		return nil, err
	}
	return &PipeConn{h: h, server: server, rov: &windows.Overlapped{HEvent: re}, wov: &windows.Overlapped{HEvent: we}, rbuf: make([]byte, PipeBuffer), wbuf: make([]byte, PipeBuffer)}, nil
}

// SetDeadline applies to reads and writes started after it.
func (c *PipeConn) SetDeadline(t time.Time) error {
	c.dmu.Lock()
	c.deadline = t
	c.dmu.Unlock()
	return nil
}

func (c *PipeConn) waitMillis() (uint32, bool) {
	c.dmu.Lock()
	d := c.deadline
	c.dmu.Unlock()
	if d.IsZero() {
		return windows.INFINITE, true
	}
	left := time.Until(d)
	if left <= 0 {
		return 0, false
	}
	ms := left.Milliseconds()
	if ms >= int64(windows.INFINITE) {
		ms = int64(windows.INFINITE) - 1
	}
	if ms == 0 {
		ms = 1
	}
	return uint32(ms), true
}

// finish waits for an overlapped operation started with err (ERROR_IO_PENDING, or nil when it completed at once).
func (c *PipeConn) finish(ov *windows.Overlapped, started error, n uint32) (uint32, error) {
	if started != nil && !errors.Is(started, windows.ERROR_IO_PENDING) {
		return 0, c.mapErr(started)
	}
	if c.closed.Load() {
		windows.CancelIoEx(c.h, ov)
	}
	ms, ok := c.waitMillis()
	timedOut := !ok
	if ok {
		r, err := windows.WaitForSingleObject(ov.HEvent, ms)
		if err != nil {
			windows.CancelIoEx(c.h, ov)
		} else if r == uint32(windows.WAIT_TIMEOUT) {
			timedOut = true
		}
	}
	if timedOut {
		windows.CancelIoEx(c.h, ov)
	}
	err := windows.GetOverlappedResult(c.h, ov, &n, true)
	runtime.KeepAlive(ov)
	if err == nil {
		return n, nil
	}
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		if c.closed.Load() {
			return n, os.ErrClosed
		}
		if timedOut {
			return n, os.ErrDeadlineExceeded
		}
	}
	return n, c.mapErr(err)
}

func (c *PipeConn) mapErr(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED), errors.Is(err, windows.ERROR_NO_DATA):
		return io.EOF
	case c.closed.Load():
		return os.ErrClosed
	}
	return err
}

func (c *PipeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.closed.Load() {
			return 0, os.ErrClosed
		}
		want := len(p)
		if want > len(c.rbuf) {
			want = len(c.rbuf)
		}
		windows.ResetEvent(c.rov.HEvent)
		var n uint32
		err := windows.ReadFile(c.h, c.rbuf[:want], &n, c.rov)
		n, err = c.finish(c.rov, err, n)
		if n > 0 {
			copy(p, c.rbuf[:n])
			return int(n), nil
		}
		if err != nil {
			return 0, err
		}
		// A zero-length message: read again.
	}
}

func (c *PipeConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		if c.closed.Load() {
			return written, os.ErrClosed
		}
		chunk := copy(c.wbuf, p)
		windows.ResetEvent(c.wov.HEvent)
		var n uint32
		err := windows.WriteFile(c.h, c.wbuf[:chunk], &n, c.wov)
		n, err = c.finish(c.wov, err, n)
		written += int(n)
		if err != nil {
			return written, err
		}
		if int(n) < chunk {
			return written, io.ErrShortWrite
		}
		p = p[chunk:]
	}
	return written, nil
}

func (c *PipeConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	windows.CancelIoEx(c.h, nil)
	c.rmu.Lock()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	defer c.rmu.Unlock()
	err := windows.CloseHandle(c.h)
	windows.CloseHandle(c.rov.HEvent)
	windows.CloseHandle(c.wov.HEvent)
	return err
}

// Peer reads the client's process id and Windows session from the OS. It works as soon as the client has connected, before it sends.
func (c *PipeConn) Peer() (uint32, uint32, bool) {
	if !c.server {
		return 0, 0, false
	}
	var pid, session uint32
	if err := windows.GetNamedPipeClientProcessId(c.h, &pid); err != nil {
		return 0, 0, false
	}
	if err := windows.ProcessIdToSessionId(pid, &session); err != nil {
		return pid, 0, false
	}
	return pid, session, true
}

// Identify reads who is on the other end from the OS: the token of the client's own connection (impersonated at identification level
// only, then reverted at once), its process id and its Windows session. Nothing the client sends is trusted for this.
func (c *PipeConn) Identify() (Caller, error) {
	if !c.server {
		return Caller{}, errors.New("only the keeper end identifies its caller")
	}
	var caller Caller
	if err := windows.GetNamedPipeClientProcessId(c.h, &caller.PID); err == nil {
		_ = windows.ProcessIdToSessionId(caller.PID, &caller.Session)
	}
	type result struct {
		sid *windows.SID
		err error
	}
	ch := make(chan result, 1)
	go func() {
		// Impersonation changes the OS thread. The thread is unlocked only after RevertToSelf succeeds; if reverting fails the
		// goroutine ends still locked, and Go throws that thread away rather than reuse it.
		runtime.LockOSThread()
		r, _, e := procImpersonateNamedPipeClient.Call(uintptr(c.h))
		if r == 0 {
			runtime.UnlockOSThread()
			ch <- result{err: fmt.Errorf("impersonate: %w", e)}
			return
		}
		var tok windows.Token
		oerr := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok)
		if rerr := windows.RevertToSelf(); rerr != nil {
			if oerr == nil {
				tok.Close()
			}
			ch <- result{err: fmt.Errorf("revert: %w", rerr)}
			return
		}
		runtime.UnlockOSThread()
		if oerr != nil {
			ch <- result{err: fmt.Errorf("token: %w", oerr)}
			return
		}
		defer tok.Close()
		tu, terr := tok.GetTokenUser()
		if terr != nil {
			ch <- result{err: terr}
			return
		}
		sid, cerr := tu.User.Sid.Copy()
		ch <- result{sid: sid, err: cerr}
	}()
	res := <-ch
	if res.err != nil {
		return caller, res.err
	}
	caller.SID = res.sid.String()
	if acct, dom, _, err := res.sid.LookupAccount(""); err == nil {
		caller.Name = acct
		if dom != "" {
			caller.Name = dom + `\` + acct
		}
	}
	return caller, nil
}

/* -------------------------------------------------------------- dialer */

// PipeDialer connects to the keeper's pipe.
type PipeDialer struct {
	Name string
	// RequireSession0 refuses a pipe whose server is not in session 0, where services run. Only tests turn it off; no environment
	// variable can.
	RequireSession0 bool
	// RequireKeeperOwner refuses a pipe whose owner is not a service identity (see TrustedPipeOwner). Session 0 alone is not enough: a
	// standard user can run a program there (a scheduled task set to run while signed out) and serve the keeper's pipe name while the
	// service is stopped. The owner of a pipe is the creator's token owner, which a standard user cannot set to SYSTEM, Administrators,
	// LocalService, NetworkService or the keeper's own service SID. Only tests turn it off.
	RequireKeeperOwner bool
	// ExtraOwners are owner SIDs a test also trusts (its own account).
	ExtraOwners []string
}

// NewPipeDialer is the production dialer for the pipe name.
func NewPipeDialer(name string) PipeDialer {
	return PipeDialer{Name: name, RequireSession0: true, RequireKeeperOwner: true}
}

func (d PipeDialer) Dial(ctx context.Context) (ClientConn, error) {
	namep, err := windows.UTF16PtrFromString(d.Name)
	if err != nil {
		return nil, err
	}
	var h windows.Handle
	for {
		h, err = windows.CreateFile(namep, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			break
		}
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, ErrNotRunning
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, fmt.Errorf("open the key keeper pipe: %w", err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(namep)), 500)
	}
	if d.RequireSession0 {
		var pid, session uint32
		if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil || windows.ProcessIdToSessionId(pid, &session) != nil || session != 0 {
			windows.CloseHandle(h)
			return nil, ErrImpostor
		}
	}
	if d.RequireKeeperOwner {
		owner, err := PipeOwner(h)
		if err != nil || !TrustedPipeOwner(owner, d.ExtraOwners) {
			windows.CloseHandle(h)
			return nil, ErrImpostor
		}
	}
	c, err := NewPipeConn(h, false)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return c, nil
}

// PipeOwner reads the owner SID of the pipe the client opened (READ_CONTROL is part of the access the pipe grants its callers).
func PipeOwner(h windows.Handle) (string, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return "", errors.New("the pipe has no owner")
	}
	return owner.String(), nil
}
