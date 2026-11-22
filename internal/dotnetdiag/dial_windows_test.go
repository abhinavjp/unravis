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
	c.once.Do(func() { _ = windows.CloseHandle(c.h) })
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
				_ = windows.CloseHandle(h)
				return
			}
			next, nerr := s.create()
			s.wg.Add(1)
			go func(i int, h windows.Handle) {
				defer s.wg.Done()
				c := &srvConn{h: h}
				defer func() { _ = c.Close() }()
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
			_ = windows.CloseHandle(h)
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
		Target{PID: pid32(os.Getpid())}
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
	pid := pid32(os.Getpid())
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
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	// The expected server is the child; the pipe server is this process.
	d := &pipeDialer{nameFor: func(uint32) string { return name }, dialTimeout: 500 * time.Millisecond}
	res, err := Probe(context.Background(), d, Target{PID: pid32(child.Process.Pid)}, winOptions())
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
	t.Cleanup(func() { _ = alive.Process.Kill(); _ = alive.Wait() })
	_, _, err := d.Dial(context.Background(), Target{PID: pid32(alive.Process.Pid)})
	if reasonOf(t, err) != ReasonDisabled {
		t.Fatalf("live process without a pipe: %v", err)
	}

	gone := exec.Command("cmd", "/c", "exit", "0")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	_, _, err = d.Dial(context.Background(), Target{PID: pid32(gone.Process.Pid)})
	if reasonOf(t, err) != ReasonNotRunning {
		t.Fatalf("exited process: %v", err)
	}
}

func TestWindowsDialRefusesBadTargets(t *testing.T) {
	d := &pipeDialer{dialTimeout: 200 * time.Millisecond}
	for _, pid := range []uint32{0, pid32(os.Getpid())} {
		if _, _, err := d.Dial(context.Background(), Target{PID: pid}); reasonOf(t, err) != ReasonPeerMismatch {
			t.Fatalf("pid %d: %v", pid, err)
		}
	}
	child := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := child.Start(); err != nil {
		t.Skipf("cannot start ping: %v", err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	// A start time that is not the process's: refused before any pipe is opened.
	if _, _, err := d.Dial(context.Background(), Target{PID: pid32(child.Process.Pid), StartTicks: 1}); reasonOf(t, err) != ReasonPeerMismatch {
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
	t.Cleanup(func() { _ = windows.CloseHandle(holder) })

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
	r, _, err := procGetProcessHandleCount.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n))) //nolint:gosec // out-parameter of GetProcessHandleCount
	if r == 0 {
		t.Fatalf("GetProcessHandleCount: %v", err)
	}
	return int(n)
}

func TestWindowsReadDeadline(t *testing.T) {
	name := uniquePipeName()
	// The server reads the start message and never answers.
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		_, _ = io.Copy(io.Discard, c)
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
	// The Go runtime starts OS threads (each holds a few handles) for the test
	// server's blocking pipe calls, so the count can step up by a thread's
	// worth (+6 and +12 were seen on CI) at any point, and that step does not
	// depend on how many probes ran. A real leak differs: it adds handles on
	// EVERY probe, so every round of probes grows the count. So the test runs
	// several rounds after a settling window and fails only if every round
	// grew by more than a few handles. The check of the pipe owner and session
	// is also tested on its own, with no goroutine involved, by
	// TestWindowsIdentityCheckLeaksNoHandle.
	for i := 0; i < 5; i++ {
		run()
	}
	time.Sleep(300 * time.Millisecond)
	const rounds, perRound, noise = 4, 15, 4
	prev := handleCount(t)
	minGrowth := 1 << 30
	var growth []int
	for r := 0; r < rounds; r++ {
		for i := 0; i < perRound; i++ {
			run()
		}
		time.Sleep(200 * time.Millisecond)
		now := handleCount(t)
		growth = append(growth, now-prev)
		if now-prev < minGrowth {
			minGrowth = now - prev
		}
		prev = now
	}
	t.Logf("handle growth per round of %d probes: %v", perRound, growth)
	if minGrowth > noise {
		t.Fatalf("handle count grew by more than %d in every round (%v): a handle leaks", noise, growth)
	}
}

// The owner and session check opens a process token and reads a security
// descriptor. This runs it many times on one pipe handle, on this goroutine,
// for each way it can end (trusted, foreign owner, owner unreadable). No
// thread or goroutine is started, so the handle count must not move: a token
// handle left open would add one per call.
func TestWindowsIdentityCheckLeaksNoHandle(t *testing.T) {
	name := uniquePipeName()
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		_, _ = io.Copy(io.Discard, c)
	})
	d, tg := selfTarget(t, name)
	conn, _, err := d.Dial(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	pc, ok := conn.(*pipeConn)
	if !ok {
		t.Fatalf("conn is %T", conn)
	}
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, tg.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		owner func(windows.Handle) (*windows.SID, error)
		want  Reason
	}{
		{"real owner", nil, ""},
		{"foreign owner", func(windows.Handle) (*windows.SID, error) { return everyone, nil }, ReasonPeerMismatch},
		{"owner unreadable", func(windows.Handle) (*windows.SID, error) { return nil, windows.ERROR_ACCESS_DENIED }, ReasonPeerMismatch},
	}
	for _, c := range cases {
		d.ownerOverride = c.owner
		check := func() {
			e := d.checkServerIdentity(pc.h, ph, tg.PID)
			var got Reason
			if e != nil {
				got = e.Reason
			}
			if got != c.want {
				t.Fatalf("%s: got %q want %q", c.name, got, c.want)
			}
		}
		for i := 0; i < 20; i++ {
			check()
		}
		before := handleCount(t)
		for i := 0; i < 300; i++ {
			check()
		}
		after := handleCount(t)
		if after > before+2 {
			t.Fatalf("%s: handle count went from %d to %d over 300 checks: a handle leaks", c.name, before, after)
		}
	}
}

func TestWindowsCancelEndsBlockedRead(t *testing.T) {
	name := uniquePipeName()
	h := newHonest(7, pid32(os.Getpid()))
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

// The pipe owner must be the target token's user or its default owner.
func TestOwnerTrustedTable(t *testing.T) {
	sid := func(s string) *windows.SID {
		x, err := windows.StringToSid(s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	user := sid("S-1-5-21-1111-2222-3333-1001")
	admins := sid("S-1-5-32-544")
	other := sid("S-1-5-21-1111-2222-3333-1002")
	everyone := sid("S-1-1-0")
	cases := []struct {
		name  string
		owner *windows.SID
		want  bool
	}{
		{"the user", sid("S-1-5-21-1111-2222-3333-1001"), true},
		{"the default owner (Administrators)", sid("S-1-5-32-544"), true},
		{"another user", other, false},
		{"Everyone", everyone, false},
		{"no owner", nil, false},
	}
	for _, c := range cases {
		if got := ownerTrusted(c.owner, user, admins); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if ownerTrusted(user, nil, nil) {
		t.Error("with no token SIDs nothing is trusted")
	}
}

// A pipe whose server pid is the target but whose owner is someone else is
// refused before any byte is written (the pid-reuse hand-over case).
func TestWindowsDialRejectsForeignOwner(t *testing.T) {
	name := uniquePipeName()
	var got atomic.Int64
	seen := make(chan struct{}, 4)
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		n, _ := io.Copy(io.Discard, c)
		got.Add(n)
		seen <- struct{}{}
	})
	d, tg := selfTarget(t, name)
	d.ownerOverride = func(windows.Handle) (*windows.SID, error) {
		return windows.StringToSid("S-1-5-21-1-2-3-4242") // not the target's user or owner
	}
	res, err := Probe(context.Background(), d, tg, winOptions())
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

// An owner that cannot be read is refused, not skipped.
func TestWindowsDialRefusesUnreadableOwner(t *testing.T) {
	name := uniquePipeName()
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		_, _ = io.Copy(io.Discard, c)
	})
	d, tg := selfTarget(t, name)
	d.ownerOverride = func(windows.Handle) (*windows.SID, error) { return nil, windows.ERROR_ACCESS_DENIED }
	if _, _, err := d.Dial(context.Background(), tg); reasonOf(t, err) != ReasonPeerMismatch {
		t.Fatalf("got %v, want peer-mismatch", err)
	}
}

// The real owner of a pipe created by this process is trusted for this
// process's own token. This runs the real GetSecurityInfo and token reads.
func TestWindowsRealOwnerIsTrustedForOwnToken(t *testing.T) {
	name := uniquePipeName()
	startPipeSrv(t, name, nil, windows.PIPE_UNLIMITED_INSTANCES, func(i int, c net.Conn) {
		_, _ = io.Copy(io.Discard, c)
	})
	name16, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateFile(name16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	owner, err := pipeOwner(h)
	if err != nil {
		t.Fatalf("pipeOwner: %v", err)
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tok.Close() }()
	user, defOwner, err := tokenSIDs(tok)
	if err != nil {
		t.Fatalf("tokenSIDs: %v", err)
	}
	if !ownerTrusted(owner, user, defOwner) {
		t.Fatalf("owner %s is neither the token user %s nor its default owner %s", owner, user, defOwner)
	}
}
