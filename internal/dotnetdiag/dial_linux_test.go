//go:build linux

package dotnetdiag

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLinuxSocketNameFromProcStat(t *testing.T) {
	// A real stat line has 52 fields; field 22 is the start time. The command
	// name may hold spaces and parentheses.
	tail := "S 1 100 100 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 100 18446744073709551615"
	for _, comm := range []string{"(app)", "(my app)", "(a) b (c)", "()"} {
		got, ok := parseStartKey("4242 " + comm + " " + tail)
		if !ok || got != 987654 {
			t.Errorf("comm %s: got %d %v, want 987654", comm, got, ok)
		}
	}
	for _, bad := range []string{"", "4242 (app", "4242 (app) S 1", "4242 (app) " + strings.Replace(tail, "987654", "x", 1), "4242 (app) " + strings.Replace(tail, "987654", "0", 1)} {
		if _, ok := parseStartKey(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := socketName(4242, 987654); got != "dotnet-diagnostic-4242-987654-socket" {
		t.Fatal(got)
	}
	// Our own stat parses.
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Skip(err)
	}
	if k, ok := parseStartKey(string(raw)); !ok || k == 0 {
		t.Fatalf("own stat did not parse: %q", raw)
	}
}

func startKeyOf(t *testing.T, pid int) int64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	k, ok := parseStartKey(string(raw))
	if !ok {
		t.Fatalf("stat of %d: %q", pid, raw)
	}
	return k
}

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	c := exec.Command("sleep", "60")
	if err := c.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	return c
}

// TestLinuxDialPeerCred: a listener named like the real socket of a child
// process, but owned by THIS process. SO_PEERCRED names this process, not the
// child, so the dial is refused and nothing is written.
func TestLinuxDialPeerCred(t *testing.T) {
	child := startSleeper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, socketName(pid32(child.Process.Pid), startKeyOf(t, child.Process.Pid)))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	got := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- -1
			return
		}
		defer func() { _ = c.Close() }()
		n, _ := c.Read(make([]byte, 64)) // returns when the client closes
		got <- n
	}()
	d := &socketDialer{dir: dir}
	_, _, derr := d.Dial(context.Background(), Target{PID: pid32(child.Process.Pid)})
	if reasonOf(t, derr) != ReasonPeerMismatch {
		t.Fatalf("got %v, want peer-mismatch", derr)
	}
	select {
	case n := <-got:
		if n != 0 {
			t.Fatalf("server saw %d bytes", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the client hang up")
	}
}

func TestLinuxDialAbsentAndGone(t *testing.T) {
	child := startSleeper(t)
	d := &socketDialer{dir: t.TempDir()}
	if _, _, err := d.Dial(context.Background(), Target{PID: pid32(child.Process.Pid)}); reasonOf(t, err) != ReasonDisabled {
		t.Fatalf("live process without a socket: %v", err)
	}
	if _, _, err := d.Dial(context.Background(), Target{PID: pid32(child.Process.Pid), StartTicks: 1}); reasonOf(t, err) != ReasonPeerMismatch {
		t.Fatalf("wrong start ticks: %v", err)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if _, _, err := d.Dial(context.Background(), Target{PID: pid32(child.Process.Pid)}); reasonOf(t, err) != ReasonNotRunning {
		t.Fatalf("exited process: %v", err)
	}
	for _, pid := range []uint32{0, pid32(os.Getpid())} {
		if _, _, err := d.Dial(context.Background(), Target{PID: pid}); reasonOf(t, err) != ReasonPeerMismatch {
			t.Fatalf("pid %d: %v", pid, err)
		}
	}
}

func TestLinuxDialRefusesNonSocket(t *testing.T) {
	child := startSleeper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, socketName(pid32(child.Process.Pid), startKeyOf(t, child.Process.Pid)))
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &socketDialer{dir: dir}
	if _, _, err := d.Dial(context.Background(), Target{PID: pid32(child.Process.Pid)}); reasonOf(t, err) != ReasonPeerMismatch {
		t.Fatalf("regular file under the socket name: %v", err)
	}
}

// helperEnv switches this test binary into a fake diagnostic server process.
const helperEnv = "UNRAVIS_DIAG_TEST_HELPER_DIR"

// TestLinuxHelperServer is not a test: when helperEnv is set the test binary,
// re-run as a child process, listens on its own diagnostic socket and plays an
// honest runtime. That gives the dialer a real other process to check with
// SO_PEERCRED.
func TestLinuxHelperServer(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	pid := os.Getpid()
	path := filepath.Join(dir, socketName(pid32(pid), startKeyOf(t, pid)))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, _ = os.Stdout.WriteString("ready\n")
	h := newHonest(7, pid32(pid))
	deadline := time.Now().Add(20 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(i int) { defer func() { _ = c.Close() }(); h.handle(i, c) }(i)
		if i == 1 {
			time.Sleep(500 * time.Millisecond)
			return
		}
	}
}

// startHelperServer re-runs this test binary as a child process that serves its
// own diagnostic socket under dir and plays an honest runtime. It returns the
// child's pid once the socket is listening.
func startHelperServer(t *testing.T, dir string) uint32 {
	t.Helper()
	// The binary is this test binary itself (os.Args[0]) with a fixed -test.run.
	child := exec.Command(os.Args[0], "-test.run=^TestLinuxHelperServer$", "-test.v") //nolint:gosec // re-runs the running test binary
	child.Env = append(os.Environ(), helperEnv+"="+dir)
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	ready := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == "ready" {
				ready <- true
				break
			}
		}
		for sc.Scan() {
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("helper server did not start")
	}
	return pid32(child.Process.Pid)
}

func TestLinuxDialHappy(t *testing.T) {
	dir := t.TempDir()
	pid := startHelperServer(t, dir)
	res, perr := Probe(context.Background(), &socketDialer{dir: dir}, Target{PID: pid}, testOptions())
	if perr != nil || res.Reason != "" {
		t.Fatalf("err %v result %+v", perr, res)
	}
	if res.ServerPID != pid || !res.PreambleOK || !res.StopAck || !res.StopSession || res.Transport != "socket" || res.ServerStart == 0 {
		t.Fatalf("result %+v", res)
	}
}

// PID reuse: after the connect and the SO_PEERCRED check, the start key of the
// pid is read again. Here the second reading differs, as it would if the pid
// now belonged to another process. The dial must be refused, with the
// connection closed and nothing written.
func TestLinuxDialRechecksStartKeyAfterPeerCheck(t *testing.T) {
	dir := t.TempDir()
	pid := startHelperServer(t, dir)
	calls := 0
	d := &socketDialer{dir: dir, startKey: func(p uint32) (int64, *Error) {
		calls++
		k, e := procStartKey(p)
		if e == nil && calls >= 2 {
			k++ // another process started later under the same pid
		}
		return k, e
	}}
	_, _, err := d.Dial(context.Background(), Target{PID: pid})
	if reasonOf(t, err) != ReasonPeerMismatch {
		t.Fatalf("got %v, want peer-mismatch", err)
	}
	if calls != 2 {
		t.Fatalf("the start key was read %d times, want 2 (before and after the peer check)", calls)
	}
	// The same dialer with an unchanged key still works: the check is not a
	// blanket refusal.
	d.startKey = nil
	conn, peer, err := d.Dial(context.Background(), Target{PID: pid})
	if err != nil {
		t.Fatalf("unchanged key: %v", err)
	}
	_ = conn.Close()
	if peer.PID != pid || peer.StartTicks == 0 {
		t.Fatalf("peer %+v", peer)
	}
}

func TestLinuxStartKeyReadFailureAfterConnectIsRefused(t *testing.T) {
	dir := t.TempDir()
	pid := startHelperServer(t, dir)
	calls := 0
	d := &socketDialer{dir: dir, startKey: func(p uint32) (int64, *Error) {
		calls++
		if calls >= 2 {
			return 0, newErr(ReasonNotRunning, 0)
		}
		return procStartKey(p)
	}}
	if _, _, err := d.Dial(context.Background(), Target{PID: pid}); reasonOf(t, err) != ReasonPeerMismatch {
		t.Fatalf("got %v, want peer-mismatch", err)
	}
}

// $TMPDIR is for the command line only. A dialer not made by platformDialer
// (a future service path) never reads it.
func TestLinuxTMPDIRIsOnlyForTheCommandLine(t *testing.T) {
	env := t.TempDir()
	t.Setenv("TMPDIR", env)
	if got := (&socketDialer{}).socketDir(); got != "/tmp" {
		t.Fatalf("a dialer not made for the command line looked in %q, want /tmp", got)
	}
	if got := (&socketDialer{fromEnv: true}).socketDir(); got != env {
		t.Fatalf("the command-line dialer looked in %q, want %q", got, env)
	}
	if got := (&socketDialer{dir: "/x", fromEnv: true}).socketDir(); got != "/x" {
		t.Fatalf("an explicit directory must win, got %q", got)
	}
	cli, ok := DefaultDialer().(*socketDialer)
	if !ok || !cli.fromEnv {
		t.Fatalf("DefaultDialer() is %T, want the command-line socketDialer", DefaultDialer())
	}
	// And in effect: a socket under $TMPDIR is found by the command-line
	// dialer but not by a plain one.
	pid := startHelperServer(t, env)
	if _, _, err := (&socketDialer{}).Dial(context.Background(), Target{PID: pid}); reasonOf(t, err) != ReasonDisabled {
		t.Fatalf("plain dialer: %v, want diag-disabled", err)
	}
	conn, _, err := cli.Dial(context.Background(), Target{PID: pid})
	if err != nil {
		t.Fatalf("command-line dialer: %v", err)
	}
	_ = conn.Close()
}
