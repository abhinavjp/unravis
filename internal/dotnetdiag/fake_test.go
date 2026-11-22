package dotnetdiag

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// recConn wraps the client end of a net.Pipe and records every byte written.
type recConn struct {
	net.Conn
	mu      sync.Mutex
	written []byte
	closed  bool
}

func (c *recConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.mu.Lock()
	c.written = append(c.written, b[:n]...)
	c.mu.Unlock()
	return n, err
}

func (c *recConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *recConn) Written() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.written...)
}

func (c *recConn) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// fakeDialer is an in-memory Dialer. handler(i, server) plays the runtime on
// dial number i (0 is the first connection). Nothing here touches the OS.
type fakeDialer struct {
	mu      sync.Mutex
	handler func(i int, server net.Conn)
	peer    func(i int) Peer  // nil: the target as asked
	dialErr func(i int) error // nil or returns nil: dial succeeds
	targets []Target
	conns   []*recConn
	panicOn func(i int) bool // wrap the client conn so Read panics after the reply
}

func (f *fakeDialer) Dial(ctx context.Context, t Target) (Conn, Peer, error) {
	f.mu.Lock()
	i := len(f.targets)
	f.targets = append(f.targets, t)
	f.mu.Unlock()
	if f.dialErr != nil {
		if err := f.dialErr(i); err != nil {
			return nil, Peer{}, err
		}
	}
	client, server := net.Pipe()
	rc := &recConn{Conn: client}
	f.mu.Lock()
	f.conns = append(f.conns, rc)
	f.mu.Unlock()
	go func() {
		defer server.Close()
		if f.handler != nil {
			f.handler(i, server)
		}
	}()
	peer := Peer{PID: t.PID, StartTicks: t.StartTicks}
	if f.peer != nil {
		peer = f.peer(i)
	}
	if f.panicOn != nil && f.panicOn(i) {
		return &panicConn{recConn: rc}, peer, nil
	}
	return rc, peer, nil
}

func (f *fakeDialer) dials() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.targets)
}

func (f *fakeDialer) conn(i int) *recConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.conns) {
		return nil
	}
	return f.conns[i]
}

// panicConn panics on the first Read after the start reply was consumed (the
// 28 reply bytes), so the panic happens in the stream phase.
type panicConn struct {
	*recConn
	mu   sync.Mutex
	read int
}

func (c *panicConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	c.read++
	n := c.read
	c.mu.Unlock()
	if n > 2 { // decodeReply reads 20 then 8; the third Read is the stream
		panic("fake conn panic")
	}
	return c.recConn.Read(b)
}

var errFakeRefused = errors.New("fake: refused")

// honest is the runtime playing by the book: on connection 0 it reads the
// start message, answers OK(session), sends a preamble and then chunks until
// the stop arrives on connection 1; on connection 1 it reads the stop message,
// answers OK(session) and closes the stream.
type honest struct {
	session uint64
	pid     uint32
	chunks  int           // chunks to send before waiting for stop (0 = 3)
	gap     time.Duration // between chunks
	stopped chan struct{}
	once    sync.Once
}

func newHonest(session uint64, pid uint32) *honest {
	return &honest{session: session, pid: pid, stopped: make(chan struct{})}
}

func okReply(session uint64) []byte {
	b, _ := hex.DecodeString(goldenOK)
	binary.LittleEndian.PutUint64(b[20:], session)
	return b
}

func (h *honest) handle(i int, s net.Conn) {
	switch i {
	case 0:
		if _, err := io.ReadFull(s, make([]byte, 136)); err != nil {
			return
		}
		if _, err := s.Write(okReply(h.session)); err != nil {
			return
		}
		if _, err := s.Write(buildPreamble(h.pid, 8, 4)); err != nil {
			return
		}
		n := h.chunks
		if n == 0 {
			n = 3
		}
		for k := 0; k < n; k++ {
			if _, err := s.Write(bytes.Repeat([]byte{0x55}, 1000)); err != nil {
				return
			}
			time.Sleep(h.gap)
		}
		select {
		case <-h.stopped:
		case <-time.After(3 * time.Second):
		}
	default:
		buf := make([]byte, 28)
		if _, err := io.ReadFull(s, buf); err != nil {
			return
		}
		if !bytes.Equal(buf, encodeStop(h.session)) {
			return
		}
		if _, err := s.Write(okReply(h.session)); err != nil {
			return
		}
		h.once.Do(func() { close(h.stopped) })
	}
}
