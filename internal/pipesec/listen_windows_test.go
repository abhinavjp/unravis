//go:build windows

package pipesec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/pipesec/pipesectest"
)

// selfPolicy allows exactly this test binary.
func selfPolicy(t *testing.T) PeerPolicy {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return PeerPolicy{InstallDir: filepath.Dir(exe), AllowedBase: []string{filepath.Base(exe)}, RequireSameLogon: true}
}

func selfServerPolicy(t *testing.T) ServerPolicy {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var s uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &s); err != nil {
		t.Fatal(err)
	}
	return ServerPolicy{ExpectedImage: exe, InstallDir: filepath.Dir(exe), ExpectedSession: s}
}

func listen(t *testing.T, name string, pol PeerPolicy, max int) net.Listener {
	t.Helper()
	l, err := Listen(ListenConfig{Name: name, Policy: pol, MaxInstances: max})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// echo answers every accepted connection with what it reads.
func echo(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() { defer c.Close(); io.Copy(c, c) }()
	}
}

func roundTrip(t *testing.T, name string, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := DialVerified(ctx, name, selfServerPolicy(t))
	if err != nil {
		t.Fatalf("DialVerified: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("echo: %q %v", buf, err)
	}
}

func TestListenDialRoundTripAndReuse(t *testing.T) {
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 2)
	go echo(l)
	// More sequential connections than instances proves instances are reused after Close.
	for range 6 {
		roundTrip(t, name, "hello")
	}
}

func TestAcceptedConnCarriesPeer(t *testing.T) {
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 0)
	got := make(chan Peer, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		got <- c.(Conn).Peer()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, srv, err := DialVerified(ctx, name, selfServerPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if srv.PID != uint32(os.Getpid()) || srv.LogonSID == "" || srv.UserSID == "" {
		t.Fatalf("server peer: %+v", srv)
	}
	select {
	case p := <-got:
		if p.PID != uint32(os.Getpid()) || !strings.HasPrefix(p.LogonSID, "S-1-5-5-") {
			t.Fatalf("client peer: %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no accept")
	}
}

// TestFirstInstanceWins: a squatter that already serves the name (max 16, one instance, so the pool limit does not get in the way) must
// make Listen fail on instance 1 with ERROR_ACCESS_DENIED. Without FILE_FLAG_FIRST_PIPE_INSTANCE Listen would take instance 1 and
// only fail later, on the pool limit (ERROR_PIPE_BUSY).
func TestFirstInstanceWins(t *testing.T) {
	name := pipesectest.Name(t)
	namep, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateNamedPipe(namep, windows.PIPE_ACCESS_DUPLEX, pipeMode, DefaultMaxInstances, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatalf("squatter: %v", err)
	}
	defer windows.CloseHandle(h)
	l, err := Listen(ListenConfig{Name: name, Policy: selfPolicy(t)})
	if err == nil {
		l.Close()
		t.Fatal("Listen took a name that something else already serves")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("want ERROR_ACCESS_DENIED from the first-instance flag, got %v", err)
	}
}

func TestSecondCreateIsBusy(t *testing.T) {
	name := pipesectest.Name(t)
	listen(t, name, selfPolicy(t), 0)
	namep, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateNamedPipe(namep, windows.PIPE_ACCESS_DUPLEX, pipeMode, DefaultMaxInstances, 4096, 4096, 0, nil)
	if err == nil {
		windows.CloseHandle(h)
		t.Fatal("same-user CreateNamedPipe succeeded although the pool is full")
	}
	if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
		t.Fatalf("want ERROR_PIPE_BUSY, got %v", err)
	}
}

// openLoopbackUNC opens the pipe through the SMB loopback path, which is how a remote client reaches a pipe.
func openLoopbackUNC(name string) error {
	unc, _ := windows.UTF16PtrFromString(`\\localhost\pipe\` + strings.TrimPrefix(name, PipePrefix))
	h, err := windows.CreateFile(unc, clientAccess, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		windows.CloseHandle(h)
	}
	return err
}

// TestRejectRemoteClients creates an instance through the listener's own create path, with a test-only DACL that lets Everyone in, so
// the DACL cannot be the reason a network open fails. A control pipe made without PIPE_REJECT_REMOTE_CLIENTS must be reachable the same
// way; if it is not (SMB loopback or the Server service is unavailable) the test cannot say anything and skips.
func TestRejectRemoteClients(t *testing.T) {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))

	ctl := pipesectest.Name(t) + "-ctl"
	ctlp, _ := windows.UTF16PtrFromString(ctl)
	hc, err := windows.CreateNamedPipe(ctlp, windows.PIPE_ACCESS_DUPLEX, pipeMode&^windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, sa)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(hc)
	if err := openLoopbackUNC(ctl); err != nil {
		skipOrFail(t, "control pipe not reachable through \\\\localhost, cannot test the reject flag: "+err.Error())
		return
	}

	name := pipesectest.Name(t)
	namep, _ := windows.UTF16PtrFromString(name)
	l := &listener{name: name, namep: namep, sd: sd, sa: sa}
	h, err := l.create(true, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := openLoopbackUNC(name); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("network-path open of a listener instance: want ERROR_ACCESS_DENIED, got %v", err)
	}
}

// skipOrFail skips locally but fails on CI, where a skip would hide a test that never runs.
func skipOrFail(t *testing.T, msg string) {
	t.Helper()
	// The local runner (LOCAL_CI_RUNNER) is not an elevated machine: it skips here, and GitHub, which is, still
	// fails the test, so a skip never hides a break on the elevated runner.
	if os.Getenv("CI") != "" && os.Getenv("LOCAL_CI_RUNNER") == "" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func TestDaclNoEveryone(t *testing.T) {
	name := pipesectest.Name(t)
	listen(t, name, selfPolicy(t), 0)
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read DACL: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("no DACL (null DACL means Everyone): %v", err)
	}
	if dacl.AceCount != 2 {
		t.Fatalf("want the logon SID ACE and the OWNER RIGHTS ACE, got %d ACEs", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || uint32(ace.Mask) != 0x12019f {
		t.Fatalf("ACE type %d mask %#x", ace.Header.AceType, ace.Mask)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	want, err := logonSID(windows.GetCurrentProcessToken())
	if err != nil {
		t.Fatal(err)
	}
	if !sid.Equals(want) {
		t.Fatalf("ACE SID %s is not the logon SID %s", sid, want)
	}
	for _, bad := range []string{"S-1-1-0", "S-1-5-7", "S-1-5-2"} { // Everyone, Anonymous, Network
		b, _ := windows.StringToSid(bad)
		if sid.Equals(b) {
			t.Fatalf("ACE grants %s", bad)
		}
	}
	var oace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 1, &oace); err != nil {
		t.Fatal(err)
	}
	ow, _ := windows.StringToSid("S-1-3-4") // OWNER RIGHTS
	osid := (*windows.SID)(unsafe.Pointer(&oace.SidStart))
	if oace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !osid.Equals(ow) || uint32(oace.Mask) != ownerMask {
		t.Fatalf("second ACE: type %d sid %s mask %#x", oace.Header.AceType, osid, oace.Mask)
	}
}

// TestOwnerCannotWriteDAC: the owner's implicit WRITE_DAC must be gone, or the same user in another logon session could rewrite the DACL.
func TestOwnerCannotWriteDAC(t *testing.T) {
	name := pipesectest.Name(t)
	listen(t, name, selfPolicy(t), 0)
	namep, _ := windows.UTF16PtrFromString(name)
	for _, access := range []uint32{windows.WRITE_DAC, windows.WRITE_OWNER} {
		h, err := windows.CreateFile(namep, access, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(h)
			t.Fatalf("pipe opened with access %#x", access)
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("access %#x: want ERROR_ACCESS_DENIED, got %v", access, err)
		}
	}
}

// TestDeadlineWakesPendingRead: net/http sets a deadline in the past to stop a read that is already blocked.
func TestDeadlineWakesPendingRead(t *testing.T) {
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 1)
	srv := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			srv <- c
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cc, _, err := DialVerified(ctx, name, selfServerPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	var sc net.Conn
	select {
	case sc = <-srv:
	case <-time.After(10 * time.Second):
		t.Fatal("no accept")
	}
	defer sc.Close()
	for _, c := range []net.Conn{sc, cc} {
		res := make(chan error, 1)
		go func() { _, err := c.Read(make([]byte, 1)); res <- err }()
		time.Sleep(200 * time.Millisecond) // let the read block with no deadline set
		c.SetReadDeadline(time.Now().Add(-time.Second))
		select {
		case err := <-res:
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("want os.ErrDeadlineExceeded, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a deadline set during a blocked Read did not wake it")
		}
		// Clear the deadline again.
		c.SetReadDeadline(time.Time{})
	}
}

// expectDenied connects, then expects the server to drop the connection and count a denial.
func expectDenied(t *testing.T, pol PeerPolicy) {
	t.Helper()
	name := pipesectest.Name(t)
	l := listen(t, name, pol, 2)
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
			accepted <- struct{}{}
		}
	}()
	before := Metrics()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := DialVerified(ctx, name, selfServerPolicy(t))
	if err == nil {
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, rerr := c.Read(make([]byte, 1)); rerr == nil {
			t.Fatal("denied peer was served")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for Metrics() == before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if Metrics() <= before {
		t.Fatal("denial was not counted")
	}
	select {
	case <-accepted:
		t.Fatal("denied peer reached Accept")
	default:
	}
}

func TestImageNotInInstallDirDenied(t *testing.T) {
	pol := selfPolicy(t)
	pol.InstallDir = t.TempDir()
	expectDenied(t, pol)
}

func TestImageNameNotAllowedDenied(t *testing.T) {
	pol := selfPolicy(t)
	pol.AllowedBase = []string{"unravis.exe"}
	expectDenied(t, pol)
}

func TestPeerHandleHeldDuringCheck(t *testing.T) {
	type seen struct {
		pid uint32
		err error
	}
	ch := make(chan seen, 8)
	hook := func(proc windows.Handle) {
		pid, err := windows.GetProcessId(proc)
		var code uint32
		if err == nil {
			err = windows.GetExitCodeProcess(proc, &code)
		}
		ch <- seen{pid, err}
	}
	peerCheckHook.Store(&hook)
	defer peerCheckHook.Store(nil)
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 1)
	go echo(l)
	roundTrip(t, name, "x")
	select {
	case s := <-ch:
		if s.err != nil || s.pid != uint32(os.Getpid()) {
			t.Fatalf("handle not valid during the check: pid=%d err=%v", s.pid, s.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook never ran")
	}
}

func TestDialMismatchedServerRefused(t *testing.T) {
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 0)
	go echo(l)
	sp := selfServerPolicy(t)
	sp.ExpectedImage = filepath.Join(filepath.Dir(sp.ExpectedImage), "unravis.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, _, err := DialVerified(ctx, name, sp); !errors.Is(err, ErrServerMismatch) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("want ErrServerMismatch, got %v", err)
	}
	sp = selfServerPolicy(t)
	sp.ExpectedSession += 1000
	if c, _, err := DialVerified(ctx, name, sp); !errors.Is(err, ErrServerMismatch) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("session: want ErrServerMismatch, got %v", err)
	}
}

func TestDialBusyHonoursContext(t *testing.T) {
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 1)
	_ = l // nobody calls Accept: the only instance is served to the first client and stays busy
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c1, _, err := DialVerified(ctx, name, selfServerPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel2()
	if c, _, err := DialVerified(ctx2, name, selfServerPolicy(t)); !errors.Is(err, ErrPipeBusy) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("want ErrPipeBusy, got %v", err)
	}
}

func TestOtherLogonDenied(t *testing.T) {
	const user = "unravisptest"
	pass := fmt.Sprintf("Zq9!Aa%08d", time.Now().UnixNano()%100000000) // 14 characters: no "longer than 14" prompt
	exec.Command("net", "user", user, "/delete").Run()                 // a leftover from an aborted run
	if out, err := exec.Command("net", "user", user, pass, "/add", "/Y").CombinedOutput(); err != nil {
		skipOrFail(t, fmt.Sprintf("cannot create a second local user (needs an elevated runner); run on the live PC instead: %v %s", err, out))
	}
	t.Cleanup(func() { exec.Command("net", "user", user, "/delete").Run() })

	name := pipesectest.Name(t)
	listen(t, name, selfPolicy(t), 0)

	type result struct {
		skip string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		u, _ := windows.UTF16PtrFromString(user)
		d, _ := windows.UTF16PtrFromString(".")
		p, _ := windows.UTF16PtrFromString(pass)
		var tok windows.Token
		logon := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")
		r, _, e := logon.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)), 2 /*INTERACTIVE*/, 0, uintptr(unsafe.Pointer(&tok)))
		if r == 0 {
			runtime.UnlockOSThread()
			ch <- result{skip: "LogonUser refused for the second user: " + e.Error()}
			return
		}
		defer tok.Close()
		imp := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser")
		if r, _, e := imp.Call(uintptr(tok)); r == 0 {
			runtime.UnlockOSThread()
			ch <- result{skip: "ImpersonateLoggedOnUser refused: " + e.Error()}
			return
		}
		namep, _ := windows.UTF16PtrFromString(name)
		h, err := windows.CreateFile(namep, clientAccess, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(h)
		}
		if rerr := windows.RevertToSelf(); rerr != nil {
			ch <- result{err: rerr} // thread stays locked and is discarded by Go
			return
		}
		runtime.UnlockOSThread()
		ch <- result{err: err}
	}()
	r := <-ch
	if r.skip != "" {
		skipOrFail(t, r.skip+" (then verify with runas /user on the live PC)")
	}
	if !errors.Is(r.err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("second logon: want ERROR_ACCESS_DENIED, got %v", r.err)
	}
}
