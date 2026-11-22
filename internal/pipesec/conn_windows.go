//go:build windows

package pipesec

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const pipeBuffer = 64 << 10

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

// pipeConn is an overlapped named-pipe connection (same pattern as internal/keeper/pipe_windows.go, which stays untouched).
type pipeConn struct {
	h      windows.Handle
	name   string
	peer   Peer
	closed atomic.Bool
	onDone func() // server side: hands the instance back to its pool

	rmu  sync.Mutex
	rov  *windows.Overlapped
	rbuf []byte
	wmu  sync.Mutex
	wov  *windows.Overlapped
	wbuf []byte

	dmu    sync.Mutex
	rd, wd time.Time
}

func newPipeConn(h windows.Handle, name string, peer Peer, onDone func()) (*pipeConn, error) {
	re, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	we, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(re)
		return nil, err
	}
	return &pipeConn{h: h, name: name, peer: peer, onDone: onDone,
		rov: &windows.Overlapped{HEvent: re}, wov: &windows.Overlapped{HEvent: we},
		rbuf: make([]byte, pipeBuffer), wbuf: make([]byte, pipeBuffer)}, nil
}

func (c *pipeConn) Peer() Peer           { return c.peer }
func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.name) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.name) }

func (c *pipeConn) SetDeadline(t time.Time) error {
	c.dmu.Lock()
	c.rd, c.wd = t, t
	c.dmu.Unlock()
	return nil
}

func (c *pipeConn) SetReadDeadline(t time.Time) error {
	c.dmu.Lock()
	c.rd = t
	c.dmu.Unlock()
	return nil
}

func (c *pipeConn) SetWriteDeadline(t time.Time) error {
	c.dmu.Lock()
	c.wd = t
	c.dmu.Unlock()
	return nil
}

func (c *pipeConn) waitMillis(read bool) (uint32, bool) {
	c.dmu.Lock()
	d := c.wd
	if read {
		d = c.rd
	}
	c.dmu.Unlock()
	if d.IsZero() {
		return windows.INFINITE, true
	}
	left := time.Until(d)
	if left <= 0 {
		return 0, false
	}
	ms := max(left.Milliseconds(), 1)
	if ms >= int64(windows.INFINITE) {
		ms = int64(windows.INFINITE) - 1
	}
	return uint32(ms), true
}

// finish waits for an overlapped operation started with err (ERROR_IO_PENDING, or nil when it completed at once).
func (c *pipeConn) finish(ov *windows.Overlapped, started error, n uint32, read bool) (uint32, error) {
	if started != nil && !errors.Is(started, windows.ERROR_IO_PENDING) {
		return 0, c.mapErr(started)
	}
	if c.closed.Load() {
		windows.CancelIoEx(c.h, ov)
	}
	ms, ok := c.waitMillis(read)
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
	for {
		if c.closed.Load() {
			return 0, os.ErrClosed
		}
		want := min(len(p), len(c.rbuf))
		windows.ResetEvent(c.rov.HEvent)
		var n uint32
		err := windows.ReadFile(c.h, c.rbuf[:want], &n, c.rov)
		n, err = c.finish(c.rov, err, n, true)
		if n > 0 {
			copy(p, c.rbuf[:n])
			return int(n), nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func (c *pipeConn) Write(p []byte) (int, error) {
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
		n, err = c.finish(c.wov, err, n, false)
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

// Close ends the connection. A client end closes its handle. A server end keeps the instance: onDone hands it back to the pool, which
// flushes, disconnects and listens again (the pipe is never closed while serving, so the name stays taken).
func (c *pipeConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	windows.CancelIoEx(c.h, nil)
	c.rmu.Lock()
	c.wmu.Lock()
	windows.CloseHandle(c.rov.HEvent)
	windows.CloseHandle(c.wov.HEvent)
	c.wmu.Unlock()
	c.rmu.Unlock()
	if c.onDone != nil {
		c.onDone()
		return nil
	}
	return windows.CloseHandle(c.h)
}
