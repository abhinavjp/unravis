//go:build windows

package dotnetdiag

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32        = windows.NewLazySystemDLL("kernel32.dll")
	procWaitNamedPipeW = modkernel32.NewProc("WaitNamedPipeW")
)

const (
	stillActive        = 259 // STILL_ACTIVE
	pipeIOBuffer       = 4096
	defaultDialTimeout = 5 * time.Second
	waitNamedPipeSlice = 250 * time.Millisecond
	pipeNamePrefix     = `\\.\pipe\dotnet-diagnostic-`
	connectAccess      = windows.GENERIC_READ | windows.GENERIC_WRITE
	// SECURITY_EFFECTIVE_ONLY: of the identity the server may query, only the
	// enabled groups and privileges are visible, not the disabled ones. Identification
	// level already forbids impersonation; this narrows what the server can learn.
	connectFlagsNoShare = windows.FILE_FLAG_OVERLAPPED | windows.SECURITY_SQOS_PRESENT |
		windows.SECURITY_IDENTIFICATION | windows.SECURITY_EFFECTIVE_ONLY
)

// pipeName builds the diagnostic pipe name from a number only; no string from
// any caller is ever part of it.
func pipeName(pid uint32) string {
	return pipeNamePrefix + strconv.FormatUint(uint64(pid), 10)
}

// pipeDialer connects to \\.\pipe\dotnet-diagnostic-<pid>. nameFor and
// allowSelf are test seams: only tests set them, so CI can talk to an
// in-process server over a real named pipe.
type pipeDialer struct {
	nameFor     func(pid uint32) string
	allowSelf   bool
	dialTimeout time.Duration
	// ownerOverride replaces the read of the pipe's owner SID (tests only).
	ownerOverride func(h windows.Handle) (*windows.SID, error)
}

func platformDialer() Dialer { return &pipeDialer{} }

func errno(err error) uint32 {
	var en syscall.Errno
	if errors.As(err, &en) {
		return uint32(en)
	}
	return 0
}

// Dial follows spec 3.4, Windows: check the process (open with query-limited
// rights only), connect with SQOS identification (the server can learn who we
// are but cannot impersonate us), then check that the server of the pipe is the
// process asked for. No byte is written before that last check.
func (d *pipeDialer) Dial(ctx context.Context, t Target) (Conn, Peer, error) {
	if t.PID == 0 || (!d.allowSelf && t.PID == ownPID()) {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	// 1-2. The process: alive, and created when the caller expects. While this
	// handle is open the process id cannot be reused.
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, t.PID)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return nil, Peer{}, newErr(ReasonNotRunning, 0)
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return nil, Peer{}, newErr(ReasonDenied, uint32(windows.ERROR_ACCESS_DENIED))
		}
		return nil, Peer{}, newErr(ReasonOther, errno(err))
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	var exit uint32
	if err := windows.GetExitCodeProcess(ph, &exit); err != nil {
		return nil, Peer{}, newErr(ReasonOther, errno(err))
	}
	if exit != stillActive {
		return nil, Peer{}, newErr(ReasonNotRunning, 0)
	}
	var created, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(ph, &created, &e, &k, &u); err != nil {
		return nil, Peer{}, newErr(ReasonOther, errno(err))
	}
	ticks := int64(created.HighDateTime)<<32 | int64(created.LowDateTime)
	if t.StartTicks != 0 && t.StartTicks != ticks {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}

	// 3. Connect. Busy is waited out until the dial deadline.
	name := pipeName(t.PID)
	if d.nameFor != nil {
		name = d.nameFor(t.PID)
	}
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, Peer{}, newErr(ReasonOther, 0)
	}
	timeout := d.dialTimeout
	if timeout <= 0 {
		timeout = defaultDialTimeout
	}
	deadline := time.Now().Add(timeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(deadline) {
		deadline = cd
	}
	var h windows.Handle
	for {
		// Non-inheritable (nil security attributes), read and write only.
		h, err = windows.CreateFile(name16, connectAccess, 0, nil, windows.OPEN_EXISTING, connectFlagsNoShare, 0)
		if err == nil {
			break
		}
		switch {
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
			return nil, Peer{}, newErr(ReasonDisabled, errno(err))
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return nil, Peer{}, newErr(ReasonDenied, uint32(windows.ERROR_ACCESS_DENIED))
		case errors.Is(err, windows.ERROR_PIPE_BUSY):
			left := time.Until(deadline)
			if ctx.Err() != nil || left <= 0 {
				return nil, Peer{}, newErr(ReasonBusy, uint32(windows.ERROR_PIPE_BUSY))
			}
			if left > waitNamedPipeSlice {
				left = waitNamedPipeSlice
			}
			ms := uint32(left.Milliseconds()) //nolint:gosec // left is between 0 and 250 ms here
			if ms == 0 {
				ms = 1
			}
			_, _, _ = procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(name16)), uintptr(ms)) //nolint:gosec // WaitNamedPipeW takes the NUL-terminated name pointer; name16 outlives the call
			continue
		}
		return nil, Peer{}, newErr(ReasonOther, errno(err))
	}

	// 4. THE PEER CHECK. The server of this pipe must be the process asked for.
	var server uint32
	if err := windows.GetNamedPipeServerProcessId(h, &server); err != nil || server != t.PID {
		_ = windows.CloseHandle(h)
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	// 5. That pid is only the creator's pid at the time the instance was made.
	// Check the pipe's owner and session against the real target process.
	if e := d.checkServerIdentity(h, ph, t.PID); e != nil {
		_ = windows.CloseHandle(h)
		return nil, Peer{}, e
	}
	c, err := newPipeConn(h)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, Peer{}, newErr(ReasonOther, errno(err))
	}
	return c, Peer{PID: server, StartTicks: ticks}, nil
}

/* ---------------------------------------------------------------- conn */

// pipeConn is an overlapped pipe handle. Every read and write is bounded by the
// deadline; SetDeadline and Close also act on an operation already waiting
// (they signal a wake event), so a cancelled context ends a blocked read.
type pipeConn struct {
	h      windows.Handle
	closed atomic.Bool

	rmu  sync.Mutex
	rov  *windows.Overlapped
	rbuf []byte
	wake [2]windows.Handle // read, write
	wmu  sync.Mutex
	wov  *windows.Overlapped
	wbuf []byte

	dmu      sync.Mutex
	deadline time.Time
}

func newPipeConn(h windows.Handle) (*pipeConn, error) {
	var ev [4]windows.Handle
	for i := range ev {
		e, err := windows.CreateEvent(nil, 1, 0, nil) // manual reset, not signalled, not inheritable
		if err != nil {
			for _, p := range ev[:i] {
				_ = windows.CloseHandle(p)
			}
			return nil, err
		}
		ev[i] = e
	}
	return &pipeConn{
		h:    h,
		rov:  &windows.Overlapped{HEvent: ev[0]},
		wov:  &windows.Overlapped{HEvent: ev[1]},
		wake: [2]windows.Handle{ev[2], ev[3]},
		rbuf: make([]byte, pipeIOBuffer),
		wbuf: make([]byte, pipeIOBuffer),
	}, nil
}

func (c *pipeConn) SetDeadline(t time.Time) error {
	c.dmu.Lock()
	c.deadline = t
	c.dmu.Unlock()
	if !c.closed.Load() {
		_ = windows.SetEvent(c.wake[0])
		_ = windows.SetEvent(c.wake[1])
	}
	return nil
}

func (c *pipeConn) waitMillis() (uint32, bool) {
	c.dmu.Lock()
	d := c.deadline
	c.dmu.Unlock()
	if d.IsZero() {
		return windows.INFINITE - 1, true
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

// finish waits for one overlapped operation (started with err: ERROR_IO_PENDING,
// or nil when it completed at once), re-reading the deadline whenever it moves.
// On deadline, close or a wait failure it cancels the operation and still waits
// for the cancel to finish before it returns, so the buffer is never in use.
func (c *pipeConn) finish(ov *windows.Overlapped, wake windows.Handle, started error) (uint32, error) {
	if started != nil && !errors.Is(started, windows.ERROR_IO_PENDING) {
		return 0, c.mapErr(started)
	}
	completed, timedOut := false, false
	for !completed {
		if c.closed.Load() {
			break
		}
		ms, ok := c.waitMillis()
		if !ok {
			timedOut = true
			break
		}
		r, err := windows.WaitForMultipleObjects([]windows.Handle{ov.HEvent, wake}, false, ms)
		if err != nil {
			break
		}
		switch r {
		case windows.WAIT_OBJECT_0:
			completed = true
		case windows.WAIT_OBJECT_0 + 1:
			_ = windows.ResetEvent(wake)
		case uint32(windows.WAIT_TIMEOUT):
			timedOut = true
		}
		if timedOut {
			break
		}
	}
	if !completed {
		_ = windows.CancelIoEx(c.h, ov)
	}
	var n uint32
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

func (c *pipeConn) mapErr(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED), errors.Is(err, windows.ERROR_NO_DATA):
		return io.EOF
	case c.closed.Load():
		return os.ErrClosed
	}
	return err
}

func (c *pipeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if c.closed.Load() {
		return 0, os.ErrClosed
	}
	want := len(p)
	if want > len(c.rbuf) {
		want = len(c.rbuf)
	}
	_ = windows.ResetEvent(c.rov.HEvent)
	var n uint32
	err := windows.ReadFile(c.h, c.rbuf[:want], &n, c.rov)
	n, err = c.finish(c.rov, c.wake[0], err)
	if n > 0 {
		copy(p, c.rbuf[:n])
		return int(n), nil
	}
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (c *pipeConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	total := 0
	for len(p) > 0 {
		if c.closed.Load() {
			return total, os.ErrClosed
		}
		chunk := p
		if len(chunk) > len(c.wbuf) {
			chunk = chunk[:len(c.wbuf)]
		}
		copy(c.wbuf, chunk)
		_ = windows.ResetEvent(c.wov.HEvent)
		var n uint32
		err := windows.WriteFile(c.h, c.wbuf[:len(chunk)], &n, c.wov)
		n, err = c.finish(c.wov, c.wake[1], err)
		total += int(n)
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// Close wakes any waiting operation, waits for it to unwind, then releases the
// pipe handle and the events. Safe to call twice.
func (c *pipeConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = windows.SetEvent(c.wake[0])
	_ = windows.SetEvent(c.wake[1])
	c.rmu.Lock()
	c.wmu.Lock()
	_ = windows.CloseHandle(c.h)
	for _, e := range []windows.Handle{c.rov.HEvent, c.wov.HEvent, c.wake[0], c.wake[1]} {
		_ = windows.CloseHandle(e)
	}
	c.wmu.Unlock()
	c.rmu.Unlock()
	return nil
}
