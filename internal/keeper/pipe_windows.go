//go:build windows

package keeper

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/keeperwire"
)

const pipeBuffer = keeperwire.PipeBuffer

// pipeSDDL is the pipe's security: network logons are denied; LocalSystem, Administrators and the keeper's own account have full
// access; people signed in to this PC (INTERACTIVE, which includes Remote Desktop) may read and write but not create pipe instances
// (0x120183 leaves out FILE_CREATE_PIPE_INSTANCE), so nobody else can serve an instance under the keeper's name while it runs.
func pipeSDDL() (string, error) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + u.User.Sid.String() + ")(A;;0x120183;;;IU)", nil
}

// PipeListenOptions configures ListenPipe.
type PipeListenOptions struct {
	// SDDL replaces the default security descriptor.
	SDDL string
}

type pipeListener struct {
	name    *uint16
	sa      *windows.SecurityAttributes
	closeEv windows.Handle
	closed  atomic.Bool

	mu        sync.Mutex
	pending   windows.Handle
	accepting bool
}

// ListenPipe serves the named pipe name. The first instance is created with FILE_FLAG_FIRST_PIPE_INSTANCE, so the call fails when
// anything already serves that name; one instance is always kept listening, so a client finds the pipe busy, never missing, while the
// keeper runs.
func ListenPipe(name string, o PipeListenOptions) (Listener, error) {
	sddl := o.SDDL
	if sddl == "" {
		var err error
		if sddl, err = pipeSDDL(); err != nil {
			return nil, err
		}
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	l := &pipeListener{name: namep, sa: sa, closeEv: ev}
	h, err := l.instance(true)
	if err != nil {
		windows.CloseHandle(ev)
		return nil, err
	}
	l.pending = h
	return l, nil
}

func (l *pipeListener) instance(first bool) (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(l.name, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, l.sa)
}

func (l *pipeListener) Accept() (Conn, error) {
	for {
		l.mu.Lock()
		if l.closed.Load() || l.pending == 0 {
			l.mu.Unlock()
			return nil, ErrListenerClosed
		}
		h := l.pending
		l.accepting = true
		l.mu.Unlock()

		connected, err := l.connect(h)

		l.mu.Lock()
		l.accepting = false
		if l.closed.Load() {
			l.pending = 0
			l.mu.Unlock()
			windows.CloseHandle(h)
			return nil, ErrListenerClosed
		}
		if err != nil {
			l.mu.Unlock()
			return nil, err
		}
		if !connected {
			// The client went away before we saw it: reuse the instance.
			windows.DisconnectNamedPipe(h)
			l.mu.Unlock()
			continue
		}
		next, nerr := l.instance(false)
		if nerr != nil {
			l.pending = 0
		} else {
			l.pending = next
		}
		l.mu.Unlock()
		c, cerr := keeperwire.NewPipeConn(h, true)
		if cerr != nil {
			windows.CloseHandle(h)
			return nil, cerr
		}
		if nerr != nil {
			c.Close()
			return nil, nerr
		}
		return c, nil
	}
}

// connect waits for a client on h, or for Close.
func (l *pipeListener) connect(h windows.Handle) (bool, error) {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(ev)
	ov := &windows.Overlapped{HEvent: ev}
	err = windows.ConnectNamedPipe(h, ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return true, nil
	case errors.Is(err, windows.ERROR_NO_DATA):
		return false, nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return false, err
	}
	idx, werr := windows.WaitForMultipleObjects([]windows.Handle{ev, l.closeEv}, false, windows.INFINITE)
	var n uint32
	if werr != nil || idx != windows.WAIT_OBJECT_0 {
		windows.CancelIoEx(h, ov)
		windows.GetOverlappedResult(h, ov, &n, true)
		runtime.KeepAlive(ov)
		return false, ErrListenerClosed
	}
	err = windows.GetOverlappedResult(h, ov, &n, true)
	runtime.KeepAlive(ov)
	if err != nil {
		if errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_BROKEN_PIPE) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (l *pipeListener) Close() error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	windows.SetEvent(l.closeEv)
	l.mu.Lock()
	if !l.accepting && l.pending != 0 {
		windows.CloseHandle(l.pending)
		l.pending = 0
	}
	l.mu.Unlock()
	return nil
}
