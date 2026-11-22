//go:build windows

package dotnetdiag

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetNamedPipeServerSessionID = modkernel32.NewProc("GetNamedPipeServerSessionId")

// tokenOwner is the TOKEN_OWNER structure: the SID new objects of the token
// get as owner by default.
type tokenOwner struct{ Owner *windows.SID }

// pipeOwner reads the owner SID of the pipe instance. GENERIC_READ, which the
// connect asked for, includes READ_CONTROL. The returned SID is a copy.
func pipeOwner(h windows.Handle) (*windows.SID, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, errors.New("no owner")
	}
	return owner.Copy()
}

// tokenSIDs returns the user SID and the default owner SID of a token (copies).
// An elevated administrator, or LocalSystem, creates objects owned by the
// Administrators group, not by the user, so both are listed.
func tokenSIDs(t windows.Token) (user, owner *windows.SID, err error) {
	u, err := t.GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	if user, err = u.User.Sid.Copy(); err != nil {
		return nil, nil, err
	}
	var n uint32
	// The first call only asks for the size; ERROR_INSUFFICIENT_BUFFER is expected.
	_ = windows.GetTokenInformation(t, windows.TokenOwner, nil, 0, &n)
	if n < uint32(unsafe.Sizeof(tokenOwner{})) || n > 1<<12 {
		return nil, nil, errors.New("token owner size")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenOwner, &buf[0], n, &n); err != nil {
		return nil, nil, err
	}
	to := (*tokenOwner)(unsafe.Pointer(&buf[0])) //nolint:gosec // buf holds a TOKEN_OWNER written by GetTokenInformation, sized by the first call
	if to.Owner == nil {
		return nil, nil, errors.New("no token owner")
	}
	owner, err = to.Owner.Copy()
	return user, owner, err
}

// ownerTrusted says whether the owner of a pipe instance is one the target
// process would own an object it created itself: the user of its token, or the
// default owner of its token.
func ownerTrusted(pipeOwner, tokenUser, tokenDefaultOwner *windows.SID) bool {
	if pipeOwner == nil {
		return false
	}
	return (tokenUser != nil && pipeOwner.Equals(tokenUser)) ||
		(tokenDefaultOwner != nil && pipeOwner.Equals(tokenDefaultOwner))
}

// checkServerIdentity runs after GetNamedPipeServerProcessId named the target
// and before any byte is written. That call returns the pid stored when the
// pipe instance was created, not who holds the handle now, so a low-privilege
// process could have created an instance under a pid that was later reused,
// and handed the handle on. Two more facts must then agree with the real
// target process (whose handle we hold, so its pid cannot be reused):
//   - the pipe's owner SID is the target token's user or default owner (a
//     low-privilege process cannot create an object owned by the target's user);
//   - the pipe server's session is the target's session.
//
// Anything that cannot be read is refused, never skipped.
func (d *pipeDialer) checkServerIdentity(h, process windows.Handle, pid uint32) *Error {
	var serverSession, processSession uint32
	r, _, _ := procGetNamedPipeServerSessionID.Call(uintptr(h), uintptr(unsafe.Pointer(&serverSession))) //nolint:gosec // out-parameter of GetNamedPipeServerSessionId
	if r == 0 {
		return newErr(ReasonPeerMismatch, 0)
	}
	if err := windows.ProcessIdToSessionId(pid, &processSession); err != nil || serverSession != processSession {
		return newErr(ReasonPeerMismatch, 0)
	}

	owner, err := d.ownerOf(h)
	if err != nil {
		return newErr(ReasonPeerMismatch, errno(err))
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &tok); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return newErr(ReasonDenied, uint32(windows.ERROR_ACCESS_DENIED))
		}
		return newErr(ReasonPeerMismatch, errno(err))
	}
	defer func() { _ = tok.Close() }()
	user, defOwner, err := tokenSIDs(tok)
	if err != nil {
		return newErr(ReasonPeerMismatch, errno(err))
	}
	if !ownerTrusted(owner, user, defOwner) {
		return newErr(ReasonPeerMismatch, 0)
	}
	return nil
}

func (d *pipeDialer) ownerOf(h windows.Handle) (*windows.SID, error) {
	if d.ownerOverride != nil {
		return d.ownerOverride(h)
	}
	return pipeOwner(h)
}
