//go:build windows

package pipesec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procWaitNamedPipeW = modkernel32.NewProc("WaitNamedPipeW")

// DialVerified opens the pipe and checks the server end (session, image path, install dir) before it returns, so nothing is sent to an
// impostor. It waits while every instance is busy, until ctx ends (then ErrPipeBusy). The returned Peer describes the server.
func DialVerified(ctx context.Context, name string, sp ServerPolicy) (net.Conn, Peer, error) {
	if !strings.HasPrefix(name, PipePrefix) {
		return nil, Peer{}, fmt.Errorf("pipesec: pipe name must start with %s", PipePrefix)
	}
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, Peer{}, err
	}
	var h windows.Handle
	for {
		h, err = windows.CreateFile(namep, clientAccess, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, Peer{}, fmt.Errorf("pipesec: open %s: %w", name, err)
		}
		if ctx.Err() != nil {
			return nil, Peer{}, fmt.Errorf("%w: %w", ErrPipeBusy, ctx.Err())
		}
		wait := uint32(250)
		if d, ok := ctx.Deadline(); ok {
			wait = uint32(max(1, min(250, time.Until(d).Milliseconds())))
		}
		procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(namep)), uintptr(wait))
	}
	srv, release, err := identify(h, false)
	release()
	if err == nil {
		err = sp.check(srv)
	}
	if err != nil {
		windows.CloseHandle(h)
		if errors.Is(err, ErrServerMismatch) {
			return nil, srv, err
		}
		return nil, srv, fmt.Errorf("%w: %w", ErrServerMismatch, err)
	}
	c, err := newPipeConn(h, name, srv, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, srv, err
	}
	return c, srv, nil
}
