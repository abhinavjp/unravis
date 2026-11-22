//go:build windows

package pipesec

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	modadvapi32                       = windows.NewLazySystemDLL("advapi32.dll")
	procGetNamedPipeClientSessionID   = modkernel32.NewProc("GetNamedPipeClientSessionId")
	procGetNamedPipeServerSessionID   = modkernel32.NewProc("GetNamedPipeServerSessionId")
	procImpersonateNamedPipeClientAPI = modadvapi32.NewProc("ImpersonateNamedPipeClient")
)

// peerCheckHook, when set by a test, runs while the peer's process handle is held and before the checks end.
var peerCheckHook atomic.Pointer[func(proc windows.Handle)]

func pipeSession(proc *windows.LazyProc, h windows.Handle) (uint32, error) {
	var s uint32
	if r, _, e := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&s))); r == 0 {
		return 0, e
	}
	return s, nil
}

func imagePath(proc windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(proc, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

// tokenSIDs reads the user SID and the logon SID of tok.
func tokenSIDs(tok windows.Token) (user, logon string, err error) {
	tu, err := tok.GetTokenUser()
	if err != nil {
		return "", "", err
	}
	ls, err := logonSID(tok)
	if err != nil {
		return "", "", err
	}
	return tu.User.Sid.String(), ls.String(), nil
}

// impersonatedSIDs reads the token the client connected with. The thread is unlocked only after RevertToSelf succeeds; if reverting
// fails the goroutine ends still locked and Go discards that thread. ok is false when impersonation is not possible (for example
// before the client has sent anything), which is not an error: the process token is the fallback.
func impersonatedSIDs(h windows.Handle) (user, logon string, ok bool) {
	type res struct {
		user, logon string
		ok          bool
	}
	ch := make(chan res, 1)
	go func() {
		runtime.LockOSThread()
		if r, _, _ := procImpersonateNamedPipeClientAPI.Call(uintptr(h)); r == 0 {
			runtime.UnlockOSThread()
			ch <- res{}
			return
		}
		var tok windows.Token
		oerr := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok)
		if rerr := windows.RevertToSelf(); rerr != nil {
			if oerr == nil {
				tok.Close()
			}
			ch <- res{}
			return
		}
		runtime.UnlockOSThread()
		if oerr != nil {
			ch <- res{}
			return
		}
		defer tok.Close()
		u, l, err := tokenSIDs(tok)
		ch <- res{u, l, err == nil}
	}()
	r := <-ch
	return r.user, r.logon, r.ok
}

// identify describes the other end of the pipe handle h. client is true on the server side (the other end is the client). The
// process handle stays open until the returned release func runs, so the PID cannot be reused while the checks run. The PID is read before OpenProcess, so a tiny reuse window remains before the handle is taken.
func identify(h windows.Handle, client bool) (Peer, func(), error) {
	var p Peer
	var err error
	if client {
		err = windows.GetNamedPipeClientProcessId(h, &p.PID)
	} else {
		err = windows.GetNamedPipeServerProcessId(h, &p.PID)
	}
	if err != nil {
		return p, func() {}, fmt.Errorf("pipe process id: %w", err)
	}
	sess := procGetNamedPipeServerSessionID
	if client {
		sess = procGetNamedPipeClientSessionID
	}
	if p.Session, err = pipeSession(sess, h); err != nil {
		return p, func() {}, fmt.Errorf("pipe session id: %w", err)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.PID)
	if err != nil {
		return p, func() {}, fmt.Errorf("open peer process: %w", err)
	}
	release := func() { windows.CloseHandle(proc) }
	if p.Image, err = imagePath(proc); err != nil {
		return p, release, fmt.Errorf("peer image path: %w", err)
	}
	if hook := peerCheckHook.Load(); hook != nil {
		(*hook)(proc)
	}
	var tok windows.Token
	if err = windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &tok); err != nil {
		return p, release, fmt.Errorf("peer token: %w", err)
	}
	p.UserSID, p.LogonSID, err = tokenSIDs(tok)
	tok.Close()
	if err != nil {
		return p, release, fmt.Errorf("peer sids: %w", err)
	}
	if client {
		// The pipe connection's own token wins when it can be read.
		if u, l, ok := impersonatedSIDs(h); ok {
			p.UserSID, p.LogonSID = u, l
		}
	}
	return p, release, nil
}

// selfIdentity is the server's baseline.
func selfIdentity() (self, error) {
	var me self
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &me.Session); err != nil {
		return me, err
	}
	sid, err := logonSID(windows.GetCurrentProcessToken())
	if err != nil {
		return me, err
	}
	me.LogonSID = sid.String()
	return me, nil
}
