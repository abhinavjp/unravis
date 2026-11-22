//go:build linux

package dotnetdiag

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// socketDialer connects to the diagnostic Unix socket of a .NET process on
// Linux. It is a real, peer-checked transport, used as the local proof tool on
// a Linux box; it is not a product path. dir and allowSelf are test seams.
type socketDialer struct {
	dir       string // default $TMPDIR, then /tmp
	allowSelf bool
}

func platformDialer() Dialer { return &socketDialer{} }

// procStartKey returns field 22 of /proc/<pid>/stat (start time in jiffies).
// The command name (field 2) sits in parentheses and may hold spaces and
// parentheses, so the fields are counted after the LAST ')'.
func parseStartKey(stat string) (uint64, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state); field 22 is f[19].
	if len(f) < 20 {
		return 0, false
	}
	v, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return v, true
}

// socketName is dotnet-diagnostic-<pid>-<key>-socket, built exactly (no globbing).
func socketName(pid uint32, key uint64) string {
	return "dotnet-diagnostic-" + strconv.FormatUint(uint64(pid), 10) + "-" + strconv.FormatUint(key, 10) + "-socket"
}

func (d *socketDialer) Dial(ctx context.Context, t Target) (Conn, Peer, error) {
	if t.PID == 0 || (!d.allowSelf && t.PID == uint32(os.Getpid())) {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	pidDir := "/proc/" + strconv.FormatUint(uint64(t.PID), 10)
	st, err := os.Stat(pidDir)
	if err != nil {
		return nil, Peer{}, newErr(ReasonNotRunning, 0)
	}
	procUID := uint32(0)
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		procUID = sys.Uid
	}
	raw, err := os.ReadFile(pidDir + "/stat")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, Peer{}, newErr(ReasonDenied, uint32(syscall.EACCES))
		}
		return nil, Peer{}, newErr(ReasonNotRunning, 0)
	}
	key, ok := parseStartKey(string(raw))
	if !ok {
		return nil, Peer{}, newErr(ReasonOther, 0)
	}
	if t.StartTicks != 0 && t.StartTicks != int64(key) {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	dir := d.dir
	if dir == "" {
		if dir = os.Getenv("TMPDIR"); dir == "" {
			dir = "/tmp"
		}
	}
	path := filepath.Join(dir, socketName(t.PID, key))
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, Peer{}, newErr(ReasonDenied, uint32(syscall.EACCES))
		}
		return nil, Peer{}, newErr(ReasonDisabled, 0)
	}
	// Must be a socket, owned by the process's user: not a file or link someone
	// else left under that name.
	if fi.Mode()&os.ModeSocket == 0 {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	if sys, ok := fi.Sys().(*syscall.Stat_t); !ok || sys.Uid != procUID {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	var dl net.Dialer
	dctx, cancel := context.WithTimeout(ctx, defaultDialTimeoutLinux)
	defer cancel()
	nc, err := dl.DialContext(dctx, "unix", path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrPermission):
			return nil, Peer{}, newErr(ReasonDenied, uint32(syscall.EACCES))
		case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ENOENT):
			return nil, Peer{}, newErr(ReasonDisabled, 0)
		case isTimeout(err):
			return nil, Peer{}, newErr(ReasonBusy, 0)
		}
		return nil, Peer{}, newErr(ReasonOther, 0)
	}
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		nc.Close()
		return nil, Peer{}, newErr(ReasonOther, 0)
	}
	// THE PEER CHECK. The process at the other end must be the one asked for.
	// No byte has been written.
	peerPID, err := peerCredPID(uc)
	if err != nil || peerPID != t.PID {
		uc.Close()
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	return uc, Peer{PID: peerPID, StartTicks: int64(key)}, nil
}

const defaultDialTimeoutLinux = 5 * time.Second

func peerCredPID(c *net.UnixConn) (uint32, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var serr error
	if err := rc.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if serr != nil || cred == nil || cred.Pid <= 0 {
		return 0, errors.Join(serr, errors.New("no peer credentials"))
	}
	return uint32(cred.Pid), nil
}
