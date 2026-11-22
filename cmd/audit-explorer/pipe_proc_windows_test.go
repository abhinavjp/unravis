//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// listeningPIDs returns the owning PIDs of every listening TCP socket (IPv4 and IPv6) on this PC, from GetExtendedTcpTable.
func listeningPIDs(t *testing.T) map[uint32]bool {
	t.Helper()
	proc := windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
	const tcpTableOwnerPIDListener = 3
	out := map[uint32]bool{}
	for _, f := range []struct {
		family, rowSize, pidOffset uintptr
	}{{windows.AF_INET, 24, 20}, {windows.AF_INET6, 56, 52}} {
		var size uint32
		var buf []byte
		for range 5 {
			var p unsafe.Pointer
			if len(buf) > 0 {
				p = unsafe.Pointer(&buf[0])
			}
			r, _, _ := proc.Call(uintptr(p), uintptr(unsafe.Pointer(&size)), 0, f.family, tcpTableOwnerPIDListener, 0)
			if r == 0 {
				break
			}
			if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
				t.Fatalf("GetExtendedTcpTable family %d: error %d", f.family, r)
			}
			buf = make([]byte, size)
		}
		if len(buf) < 4 {
			continue
		}
		n := *(*uint32)(unsafe.Pointer(&buf[0]))
		for i := uintptr(0); i < uintptr(n); i++ {
			off := 4 + i*f.rowSize + f.pidOffset
			if off+4 > uintptr(len(buf)) {
				break
			}
			out[*(*uint32)(unsafe.Pointer(&buf[off]))] = true
		}
	}
	return out
}

// TestNoTCPListenerInPipeMode starts the real exe as --pipe-only and asks the OS (GetExtendedTcpTable) whether its pid owns a listening
// socket. This catches any net.Listen anywhere in the startup path, not only the listenTCP seam. It also checks the pipes appear and
// that no session.json is written.
func TestNoTCPListenerInPipeMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the exe")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "unravis.exe")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "--pipe-only", "--demo")
	cmd.Env = append(os.Environ(), "AUDIT_CONFIG_DIR="+data)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	ui, ag := regexp.MustCompile(`^unravis\.ui\.[0-9a-f]{32}$`), regexp.MustCompile(`^unravis\.ag\.[0-9a-f]{32}$`)
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case err := <-exited:
			t.Fatalf("exe exited early: %v", err)
		default:
		}
		var gotUI, gotAG bool
		entries, _ := os.ReadDir(`\\.\pipe\`)
		for _, e := range entries {
			gotUI = gotUI || ui.MatchString(e.Name())
			gotAG = gotAG || ag.MatchString(e.Name())
		}
		if gotUI && gotAG {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ui and ag pipes did not appear")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if listeningPIDs(t)[uint32(cmd.Process.Pid)] {
		t.Fatalf("pid %d owns a listening TCP socket in --pipe-only mode", cmd.Process.Pid)
	}
	_ = filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), "session.json") {
			t.Errorf("session file written: %s", p)
		}
		return nil
	})
	if t.Failed() {
		return
	}
}
