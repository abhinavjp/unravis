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
// a Linux box; it is not a product path. dir, allowSelf and startKey are test
// seams.
//
// Where the socket lives: dir if set; else $TMPDIR, but ONLY when fromEnv is
// true; else /tmp. fromEnv is set only by CLIDialer (through platformDialer),
// the command-line path (the person running the probe chose that environment).
// DefaultDialer and any other construction leave it false and never read the
// environment: a service must not take the socket directory from an
// environment variable that someone else can influence.
type socketDialer struct {
	dir       string
	fromEnv   bool
	allowSelf bool
	// startKey reads the start key of a process; nil means /proc.
	startKey func(pid uint32) (int64, *Error)
}

// platformDialer is the dialer of this platform. cli is true only for CLIDialer
// and lets the dialer use the caller's $TMPDIR.
func platformDialer(cli bool) Dialer { return &socketDialer{fromEnv: cli} }

// socketDir returns the directory to look in. The environment is read only for
// a dialer made by CLIDialer.
func (d *socketDialer) socketDir() string {
	if d.dir != "" {
		return d.dir
	}
	if d.fromEnv {
		if t := os.Getenv("TMPDIR"); t != "" {
			return t
		}
	}
	return "/tmp"
}

// parseStartKey returns field 22 of /proc/<pid>/stat (start time in jiffies).
// The command name (field 2) sits in parentheses and may hold spaces and
// parentheses, so the fields are counted after the LAST ')'.
func parseStartKey(stat string) (int64, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state); field 22 is f[19].
	if len(f) < 20 {
		return 0, false
	}
	v, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// socketName is dotnet-diagnostic-<pid>-<key>-socket, built exactly (no globbing).
func socketName(pid uint32, key int64) string {
	return "dotnet-diagnostic-" + strconv.FormatUint(uint64(pid), 10) + "-" + strconv.FormatInt(key, 10) + "-socket"
}

// procStartKey reads the start key of pid from /proc.
func procStartKey(pid uint32) (int64, *Error) {
	// The path is /proc/<number>/stat: built from a number only.
	raw, err := os.ReadFile("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/stat")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return 0, newErr(ReasonDenied, uint32(syscall.EACCES))
		}
		return 0, newErr(ReasonNotRunning, 0)
	}
	key, ok := parseStartKey(string(raw))
	if !ok {
		return 0, newErr(ReasonOther, 0)
	}
	return key, nil
}

func (d *socketDialer) key(pid uint32) (int64, *Error) {
	if d.startKey != nil {
		return d.startKey(pid)
	}
	return procStartKey(pid)
}

func (d *socketDialer) Dial(ctx context.Context, t Target) (Conn, Peer, error) {
	if t.PID == 0 || (!d.allowSelf && t.PID == ownPID()) {
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
	key, kerr := d.key(t.PID)
	if kerr != nil {
		return nil, Peer{}, kerr
	}
	if t.StartTicks != 0 && t.StartTicks != key {
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	path := filepath.Join(d.socketDir(), socketName(t.PID, key))
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
		_ = nc.Close()
		return nil, Peer{}, newErr(ReasonOther, 0)
	}
	// THE PEER CHECK. The process at the other end must be the one asked for.
	// No byte has been written.
	peerPID, err := peerCredPID(uc)
	if err != nil || peerPID != t.PID {
		_ = uc.Close()
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	// SO_PEERCRED names the pid recorded when the socket began to listen. If
	// that pid was reused since (or the socket handed to another process), the
	// process now running under it was started at another time: read the start
	// key again, after the connect, and refuse a change.
	if again, kerr := d.key(t.PID); kerr != nil || again != key {
		_ = uc.Close()
		return nil, Peer{}, newErr(ReasonPeerMismatch, 0)
	}
	return uc, Peer{PID: peerPID, StartTicks: key}, nil
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
