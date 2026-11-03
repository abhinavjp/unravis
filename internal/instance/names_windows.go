//go:build windows

package instance

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CurrentNames returns the names for app with the SID of the current process
// token user.
func CurrentNames(app string) (Names, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return Names{}, fmt.Errorf("instance: open process token: %w", err)
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return Names{}, fmt.Errorf("instance: read token user: %w", err)
	}
	return NewNames(app, tu.User.Sid.String())
}

// OwnedByCurrentUser reports whether the kernel object h is owned by the
// current user, or by the token's default owner (an elevated administrator's
// objects belong to the Administrators group).
func OwnedByCurrentUser(h windows.Handle) (bool, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("instance: read object owner: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, fmt.Errorf("instance: read object owner: %w", err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false, fmt.Errorf("instance: open process token: %w", err)
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return false, fmt.Errorf("instance: read token user: %w", err)
	}
	var n uint32
	err = windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &n)
	if err != windows.ERROR_INSUFFICIENT_BUFFER || n < uint32(unsafe.Sizeof(uintptr(0))) {
		return false, fmt.Errorf("instance: size token owner: %w", err)
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buf[0], n, &n); err != nil {
		return false, fmt.Errorf("instance: read token owner: %w", err)
	}
	defaultOwner := *(**windows.SID)(unsafe.Pointer(&buf[0]))
	return ownerMatches(owner, tu.User.Sid, defaultOwner), nil
}

// ownerMatches is true when owner is the user or the default owner.
func ownerMatches(owner, user, defaultOwner *windows.SID) bool {
	if owner == nil {
		return false
	}
	if user != nil && windows.EqualSid(owner, user) {
		return true
	}
	return defaultOwner != nil && windows.EqualSid(owner, defaultOwner)
}

// ownerCheck is the seam that the guard and the signal functions call; their
// tests swap it to simulate a squatter.
var ownerCheck = OwnedByCurrentUser
