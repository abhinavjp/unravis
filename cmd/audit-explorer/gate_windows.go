//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// TOKEN_ELEVATION_TYPE values from winnt.h.
const (
	tokenElevationTypeDefault = 1 // UAC off, or a user with no split token
	tokenElevationTypeFull    = 2 // started with "Run as administrator"
	tokenElevationTypeLimited = 3 // the filtered token of a normal start
)

// isElevated is true only when the process token is elevated and its elevation
// type is full. Where UAC is off the type is "default", so such a user is not
// locked out.
func isElevated() bool {
	token := windows.GetCurrentProcessToken()
	var flag, kind, size uint32
	err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&flag)), uint32(unsafe.Sizeof(flag)), &size)
	if err == nil {
		err = windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&kind)), uint32(unsafe.Sizeof(kind)), &size)
	}
	return elevationDecision(flag != 0, kind, err)
}

// elevationDecision fails closed: an unreadable token counts as elevated, so a
// possibly elevated copy is refused rather than allowed to take the name.
func elevationDecision(elevated bool, kind uint32, err error) bool {
	if err != nil {
		return true
	}
	return elevated && kind == tokenElevationTypeFull
}
