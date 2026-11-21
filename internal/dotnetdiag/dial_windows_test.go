//go:build windows

package dotnetdiag

// These tests use real Windows named pipes made in this process, so the real
// SQOS flags, GetNamedPipeServerProcessId, overlapped I/O and CancelIoEx run.
// They run on the Windows CI job only. The server is an in-process fake: they
// say nothing about a real .NET runtime or its pipe DACL.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var testPipeSeq atomic.Int64

func uniquePipeName() string {
	return fmt.Sprintf(`\\.\pipe\unravis-dotnetdiag-test-%d-%d-%d`, os.Getpid(), time.Now().UnixNano(), testPipeSeq.Add(1))
}

// srvConn is the server end of an accepted instance as a net.Conn (synchronous
// handle; deadlines are not used by the fake servers).
type srvConn struct {
	h    windows.Handle
	once sync.Once
}

func (c *srvConn) Read(p []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(c.h, p, &n, nil)
	if err != nil {
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) || errors.Is(err, windows.ERROR_NO_DATA) {
			return int(n), io.EOF
		}
		return int(n), err
	}
	return int(n), nil
}

func (c *srvConn) Write(p []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(c.h, p, &n, nil)
	return int(n), err
}

func (c *srvConn) Close() error {
	c.once.Do(func() { windows.CloseHandle(c.h) })
	return nil
}
func (c *srvConn) LocalAddr() net.Addr              { return nil }
func (c *srvConn) RemoteAddr() net.Addr             { return nil }
func (c *srvConn) SetDeadline(time.Time) error      { return nil }
func (c *srvConn) SetReadDeadline(time.Time) error  { return nil }
func (c *srvConn) SetWriteDeadline(time.Time) error { return nil }

// pipeSrv serves a named pipe: one pending instance is always listening (like
// the runtime), every accepted connection runs handler(i, conn).
type pipeSrv struct {
	name   string
	name16 *uint16
	sa     *windows.SecurityAttributes
	max    uint32
	closed atomic.Bool
	noWake bool // a client can never connect (denied), so the pending instance cannot be released
	done   chan struct{}
	wg     sync.WaitGroup
}

func (s *pipeSrv) create() (windows.Handle, error) {
	return windows.CreateNamedPipe(s.name16, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, s.max, 65536, 65536, 0, s.sa)
}

func startPipeSrv(t *testing.T, name string, sa *windows.SecurityAttributes, max uint32, handler func(i int, c net.Conn)) *pipeSrv {
	t.Helper()
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	s := &pipeSrv{name: name, name16: name16, sa: sa, max: max, done: make(chan struct{})}
	h, err := s.create()
	if err != nil {
		t.Fatalf("CreateNamedPipe: %v", err)
	}
	go func() {
		defer close(s.done)
		for i := 0; ; i++ {
			err := windows.ConnectNamedPipe(h, nil)
			if (err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED)) || s.closed.Load() {
				windows.CloseHandle(h)
				return
			}
			next, nerr := s.create()
			s.wg.Add(1)
			go func(i int, h windows.Handle) {
				defer s.wg.Done()
				c := &srvConn{h: h}
				defer c.Close()
				handler(i, c)
			}(i, h)
			if nerr != nil {
				return
			}
			h = next
		}
	}()
	t.Cleanup(s.close)
	return s
}

// close stops the accept loop (a throw-away client releases the pending
// instance) and waits for the handlers.
func (s *pipeSrv) close() {
	s.closed.Store(true)
	if s.noWake {
		return // its accept loop ends with the test process
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-s.done:
			s.wg.Wait()
			return
		case <-deadline:
			return
		default:
		}
		h, err := windows.CreateFile(s.name16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(h)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// selfTarget makes Probe and the dialer accept this very process as the target.
func selfTarget(t *testing.T, name string) (*pipeDialer, Target) {
	t.Helper()
	old := ownPID
	ownPID = func() uint32 { return 0xFFFFFFFE }
	t.Cleanup(func() { ownPID = old })
	return &pipeDialer{nameFor: func(uint32) string { return name }, allowSelf: true, dialTimeout: 500 * time.Millisecond},
		Target{PID: uint32(os.Getpid())}
}

func winOptions() Options {
	return Options{Duration: 100 * time.Millisecond, ReplyTimeout: 500 * time.Millisecond,
		StopTimeout: 500 * time.Millisecond, StreamCap: 1 << 20}
}

func TestWindowsPipeNameFromNumberOnly(t *testing.T) {
	if got := pipeName(4242); got != `\\.\pipe\dotnet-diagnostic-4242` {
		t.Fatal(got)
	}
}

func TestWindowsDialHappy(t *testing.T) {
	name := uniquePipeName()
	pid := uint32(os.Getpid())
	h := newHonest(7, pid)
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, h.handle)
	d, tg := selfTarget(t, name)
	res, err := Probe(context.Background(), d, tg, winOptions())
	if err != nil || res.Reason != "" {
		t.Fatalf("err %v result %+v", err, res)
	}
	if !res.PipeOpened || res.ServerPID != pid || !res.PreambleOK || !res.StopAck || !res.StopSession || res.SessionID != "7" {
		t.Fatalf("result %+v", res)
	}
	if res.ServerStart == 0 {
		t.Fatal("creation time not read")
	}
	if res.BytesBefore+res.BytesAfter < uint64(PreambleLen+3000) {
		t.Fatalf("bytes %d+%d", res.BytesBefore, res.BytesAfter)
	}
}

func TestWindowsDialRejectsForeignServer(t *testing.T) {
	name := uniquePipeName()
	var got atomic.Int64
	seen := make(chan struct{}, 4)
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		n, _ := io.Copy(io.Discard, c)
		got.Add(n)
		seen <- struct{}{}
	})
	child := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := child.Start(); err != nil {
		t.Skipf("cannot start ping: %v", err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	// The expected server is the child; the pipe server is this process.
	d := &pipeDialer{nameFor: func(uint32) string { return name }, dialTimeout: 500 * time.Millisecond}
	res, err := Probe(context.Background(), d, Target{PID: uint32(child.Process.Pid)}, winOptions())
	if reasonOf(t, err) != ReasonPeerMismatch || res.Reason != ReasonPeerMismatch {
		t.Fatalf("err %v result %+v", err, res)
	}
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("the server never saw the client hang up")
	}
	if got.Load() != 0 {
		t.Fatalf("server received %d bytes, want 0", got.Load())
	}
}

func TestWindowsDialDenied(t *testing.T) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:(D;;GA;;;" + u.User.Sid.String() + ")(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name := uniquePipeName()
	startPipeSrv(t, name, sa, 1, func(int, net.Conn) {}).noWake = true
	d, tg := selfTarget(t, name)
	res, perr := Probe(context.Background(), d, tg, winOptions())
	if reasonOf(t, perr) != ReasonDenied || res.Reason != ReasonDenied {
		t.Fatalf("err %v result %+v", perr, res)
	}
	if res.PipeOpened {
		t.Fatal("pipe reported opened")
	}
}

func TestWindowsDialAbsent(t *testing.T) {
	d := &pipeDialer{nameFor: func(uint32) string { return uniquePipeName() }, dialTimeout: 500 * time.Millisecond}

	alive := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := alive.Start(); err != nil {
		t.Skipf("cannot start ping: %v", err)
	}
	t.Cleanup(func() { alive.Process.Kill(); alive.Wait() })
	_, _, err := d.Dial(context.Background(), Target{PID: uint32(alive.Process.Pid)})
	if reasonOf(t, err) != ReasonDisabled {
		t.Fatalf("live process without a pipe: %v", err)
	}

	gone := exec.Command("cmd", "/c", "exit", "0")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	_, _, err = d.Dial(context.Background(), Target{PID: uint32(gone.Process.Pid)})
	if reasonOf(t, err) != ReasonNotRunning {
		t.Fatalf("exited process: %v", err)
	}
}

func TestWindowsDialRefusesBadTargets(t *testing.T) {
	d := &pipeDialer{dialTimeout: 200 * time.Millisecond}
	for _, pid := range []uint32{0, uint32(os.Getpid())} {
		if _, _, err := d.Dial(context.Background(), Target{PID: pid}); reasonOf(t, err) != ReasonPeerMismatch {
			t.Fatalf("pid %d: %v", pid, err)
		}
	}
	child := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := child.Start(); err != nil {
		t.Skipf("cannot start ping: %v", err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	// A start time that is not the process's: refused before any pipe is opened.
	if _, _, err := d.Dial(context.Background(), Target{PID: uint32(child.Process.Pid), StartTicks: 1}); reasonOf(t, err) != ReasonPeerMismatch {
		t.Fatalf("wrong start ticks: %v", err)
	}
}

func TestWindowsDialBusy(t *testing.T) {
	name := uniquePipeName()
	release := make(chan struct{})
	startPipeSrv(t, name, nil, 1, func(i int, c net.Conn) { <-release })
	t.Cleanup(func() { close(release) })
	// Hold the only instance with another client.
	name16, _ := windows.UTF16PtrFromString(name)
	holder, err := windows.CreateFile(name16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatalf("holder: %v", err)
	}
	t.Cleanup(func() { windows.CloseHandle(holder) })

	d, tg := selfTarget(t, name)
	start := time.Now()
	res, perr := Probe(context.Background(), d, tg, winOptions())
	if reasonOf(t, perr) != ReasonBusy || res.Reason != ReasonBusy {
		t.Fatalf("err %v result %+v", perr, res)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("busy took %v", el)
	}
}

var procGetProcessHandleCount = modkernel32.NewProc("GetProcessHandleCount")

func handleCount(t *testing.T) int {
	t.Helper()
	var n uint32
	r, _, err := procGetProcessHandleCount.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		t.Fatalf("GetProcessHandleCount: %v", err)
	}
	return int(n)
}

func TestWindowsReadDeadline(t *testing.T) {
	name := uniquePipeName()
	// The server reads the start message and never answers.
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		io.Copy(io.Discard, c)
	})
	d, tg := selfTarget(t, name)
	o := winOptions()
	o.ReplyTimeout = 200 * time.Millisecond

	run := func() {
		start := time.Now()
		res, err := Probe(context.Background(), d, tg, o)
		if reasonOf(t, err) != ReasonTimeout || res.Reason != ReasonTimeout {
			t.Fatalf("err %v result %+v", err, res)
		}
		if el := time.Since(start); el > o.ReplyTimeout+500*time.Millisecond {
			t.Fatalf("timeout took %v, want within %v", el, o.ReplyTimeout+500*time.Millisecond)
		}
	}
	// The Go runtime starts OS threads (each holds handles) for the test
	// server's blocking pipe calls, and CI showed a fixed +6 between two
	// readings that did not grow with the number of probes (5 or 15). So the
	// check looks at growth, not at the level: after a settling window, 30 more
	// probes must add (almost) nothing. A handle leaked once per probe would
	// add 30; the dialer holds 5 handles per connection.
	for i := 0; i < 10; i++ {
		run()
	}
	time.Sleep(300 * time.Millisecond)
	settled := handleCount(t)
	for i := 0; i < 30; i++ {
		run()
	}
	time.Sleep(300 * time.Millisecond)
	after := handleCount(t)
	t.Logf("handle count: %d after the settling window, %d after 30 more probes", settled, after)
	if after > settled+4 {
		t.Fatalf("handle count went from %d to %d over 30 probes: a handle leaks", settled, after)
	}
}

func TestWindowsCancelEndsBlockedRead(t *testing.T) {
	name := uniquePipeName()
	h := newHonest(7, uint32(os.Getpid()))
	h.chunks, h.gap = 100000, time.Millisecond
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, h.handle)
	d, tg := selfTarget(t, name)
	o := winOptions()
	o.Duration = 30 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	res, err := Probe(ctx, d, tg, o)
	if reasonOf(t, err) != ReasonCancelled || res.Reason != ReasonCancelled {
		t.Fatalf("err %v result %+v", err, res)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("cancel took %v", el)
	}
	if !res.StopAck || !res.StopSession {
		t.Fatalf("the stop path must still run after a cancel: %+v", res)
	}
}
