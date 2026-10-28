package keeper

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// Caller is who sent a request, as Windows reports it for the pipe client. It is filled by the transport, never by the request.
type Caller struct {
	SID     string `json:"sid"`
	Name    string `json:"name"`
	PID     uint32 `json:"pid"`
	Session uint32 `json:"session"`
}

// Conn is the keeper's side of one connection.
type Conn interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
	// Identify asks the operating system who the client is. On the named pipe it must be called after the first frame was read
	// (Windows only lets a server look at the client token once data has arrived).
	Identify() (Caller, error)
}

// Listener accepts connections.
type Listener interface {
	Accept() (Conn, error)
	Close() error
}

// ClientConn is the client's side of one connection.
type ClientConn interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
}

// Dialer opens one connection to the keeper. A dialer whose keeper is not there returns an error that matches ErrNotRunning.
type Dialer interface {
	Dial(ctx context.Context) (ClientConn, error)
}

// ErrNotRunning means nothing is listening on the keeper's pipe: the service is stopped or not installed.
var ErrNotRunning = errors.New("the Unravis Key Keeper service is not running")

// ErrImpostor means something answered on the keeper's pipe name that is not a Windows service (its process is not in session 0).
var ErrImpostor = errors.New("the program answering on the key keeper's pipe is not the Unravis Key Keeper service")

// ErrListenerClosed is returned by Accept after Close.
var ErrListenerClosed = errors.New("keeper listener closed")

/* ------------------------------------------------------------- memory */

// MemoryListener is an in-process transport for tests: each Dial gives a net.Pipe whose keeper side reports the caller set on the dialer.
type MemoryListener struct {
	ch     chan Conn
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	closed bool
}

// NewMemoryListener makes an empty in-process listener.
func NewMemoryListener() *MemoryListener {
	return &MemoryListener{ch: make(chan Conn), done: make(chan struct{})}
}

// Accept waits for the next Dial.
func (l *MemoryListener) Accept() (Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, ErrListenerClosed
	}
}

// Close stops the listener; later dials report ErrNotRunning.
func (l *MemoryListener) Close() error {
	l.once.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		close(l.done)
	})
	return nil
}

// Dialer returns a dialer whose connections are made as caller.
func (l *MemoryListener) Dialer(caller Caller) Dialer { return memoryDialer{l: l, caller: caller} }

type memoryDialer struct {
	l      *MemoryListener
	caller Caller
}

func (d memoryDialer) Dial(ctx context.Context) (ClientConn, error) {
	d.l.mu.Lock()
	closed := d.l.closed
	d.l.mu.Unlock()
	if closed {
		return nil, ErrNotRunning
	}
	server, client := net.Pipe()
	select {
	case d.l.ch <- &memoryConn{Conn: server, caller: d.caller}:
		return client, nil
	case <-d.l.done:
		server.Close()
		client.Close()
		return nil, ErrNotRunning
	case <-ctx.Done():
		server.Close()
		client.Close()
		return nil, ctx.Err()
	}
}

type memoryConn struct {
	net.Conn
	caller Caller
}

func (c *memoryConn) Identify() (Caller, error) { return c.caller, nil }

// StoppedDialer always reports that the keeper is not running (the UI state "keeper stopped").
type StoppedDialer struct{}

func (StoppedDialer) Dial(context.Context) (ClientConn, error) { return nil, ErrNotRunning }
