//go:build windows

package pipesec

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pipeMode is byte mode, blocking, and no remote clients.
const pipeMode = windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS

type listener struct {
	name   string
	namep  *uint16
	sd     *windows.SECURITY_DESCRIPTOR
	sa     *windows.SecurityAttributes
	policy PeerPolicy
	me     self

	closeEv  windows.Handle
	done     chan struct{}
	accepted chan *pipeConn

	mu     sync.Mutex
	closed bool
	all    sync.WaitGroup // instance goroutines in the connect phase
}

// Listen serves the pipe cfg.Name. All instances are created before it returns; the first carries FILE_FLAG_FIRST_PIPE_INSTANCE, so it
// fails when anything else already serves that name, and the full pool makes a later CreateNamedPipe of the same name fail with
// ERROR_PIPE_BUSY. Accept returns only peers that passed the policy; others are disconnected, counted (see Metrics) and the instance
// is reused. Accept returns Conn values.
func Listen(cfg ListenConfig) (net.Listener, error) {
	if !strings.HasPrefix(cfg.Name, PipePrefix) || len(cfg.Name) == len(PipePrefix) {
		return nil, fmt.Errorf("pipesec: pipe name must start with %s", PipePrefix)
	}
	n := cfg.MaxInstances
	if n == 0 {
		n = DefaultMaxInstances
	}
	if n < 1 || n > 254 {
		return nil, fmt.Errorf("pipesec: MaxInstances %d out of range 1..254", n)
	}
	pol := cfg.Policy
	if pol.InstallDir == "" && len(pol.AllowedBase) == 0 && !pol.RequireSameLogon {
		pol = DefaultPeerPolicy()
	}
	me, err := selfIdentity()
	if err != nil {
		return nil, fmt.Errorf("pipesec: own identity: %w", err)
	}
	sddl, err := pipeSDDL()
	if err != nil {
		return nil, fmt.Errorf("pipesec: build DACL: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("pipesec: parse DACL: %w", err)
	}
	namep, err := windows.UTF16PtrFromString(cfg.Name)
	if err != nil {
		return nil, err
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	l := &listener{name: cfg.Name, namep: namep, sd: sd, policy: pol, me: me, closeEv: ev,
		done: make(chan struct{}), accepted: make(chan *pipeConn)}
	l.sa = &windows.SecurityAttributes{SecurityDescriptor: sd}
	l.sa.Length = uint32(unsafe.Sizeof(*l.sa))

	handles := make([]windows.Handle, 0, n)
	for i := range n {
		h, err := l.create(i == 0, uint32(n))
		if err != nil {
			for _, o := range handles {
				windows.CloseHandle(o)
			}
			windows.CloseHandle(ev)
			return nil, fmt.Errorf("pipesec: create pipe instance %d of %s: %w", i+1, cfg.Name, err)
		}
		handles = append(handles, h)
	}
	l.all.Add(len(handles))
	for _, h := range handles {
		go l.serve(h)
	}
	return l, nil
}

func (l *listener) create(first bool, max uint32) (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	h, err := windows.CreateNamedPipe(l.namep, flags, pipeMode, max, pipeBuffer, pipeBuffer, 0, l.sa)
	runtime.KeepAlive(l.sd)
	return h, err
}

// serve is one instance's connect phase. It ends when the instance is handed to Accept (the conn then owns it and release restarts the
// phase) or the listener closes.
func (l *listener) serve(h windows.Handle) {
	defer l.all.Done()
	for {
		if l.isClosed() {
			windows.CloseHandle(h)
			return
		}
		ok, err := l.connect(h)
		switch {
		case l.isClosed():
			windows.CloseHandle(h)
			return
		case err != nil:
			windows.DisconnectNamedPipe(h)
			time.Sleep(100 * time.Millisecond)
			continue
		case !ok:
			windows.DisconnectNamedPipe(h)
			continue
		}
		peer, release, err := identify(h, true)
		if err == nil {
			err = l.policy.check(peer, l.me)
		}
		release()
		if err != nil {
			denied.Add(1)
			windows.DisconnectNamedPipe(h)
			continue
		}
		c, err := newPipeConn(h, l.name, peer, func() { l.release(h) })
		if err != nil {
			windows.DisconnectNamedPipe(h)
			continue
		}
		select {
		case l.accepted <- c:
			return
		case <-l.done:
			c.onDone = nil
			c.Close()
			return
		}
	}
}

// release takes an instance back from a closed connection: flush what the server wrote (bounded), disconnect, listen again.
func (l *listener) release(h windows.Handle) {
	flushed := make(chan struct{})
	go func() { windows.FlushFileBuffers(h); close(flushed) }()
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
	}
	windows.DisconnectNamedPipe(h)
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		windows.CloseHandle(h)
		return
	}
	l.all.Add(1)
	l.mu.Unlock()
	go l.serve(h)
}

func (l *listener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// connect waits for a client on h, or for Close.
func (l *listener) connect(h windows.Handle) (bool, error) {
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
		return false, net.ErrClosed
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

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.accepted:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close stops listening and returns once every idle instance is closed. Connections already accepted stay open; their instances close
// when they do.
func (l *listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.done)
	l.mu.Unlock()
	windows.SetEvent(l.closeEv)
	l.all.Wait()
	windows.CloseHandle(l.closeEv)
	return nil
}

func (l *listener) Addr() net.Addr { return pipeAddr(l.name) }
