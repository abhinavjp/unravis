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

// Mandatory integrity levels (the last sub-authority of the label SID).
const integrityHigh = 0x3000

// isElevated is true when the process token is a full elevated administrator
// token: TokenElevationType is full and either TokenElevation is set or the
// integrity level is High or above. Where UAC is off the type is "default", so
// such an administrator is allowed on purpose (spec 01, BL-304). An unreadable
// token counts as elevated.
func isElevated() bool {
	token := windows.GetCurrentProcessToken()
	var flag, kind, size uint32
	err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&flag)), uint32(unsafe.Sizeof(flag)), &size)
	if err == nil {
		err = windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&kind)), uint32(unsafe.Sizeof(kind)), &size)
	}
	var level uint32
	if err == nil {
		level, err = integrityLevel(token)
	}
	return elevationDecision(flag != 0, kind, level, err)
}

// integrityLevel reads the token's mandatory integrity level.
func integrityLevel(token windows.Token) (uint32, error) {
	var need uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &need)
	if err != windows.ERROR_INSUFFICIENT_BUFFER || need == 0 {
		if err == nil {
			err = windows.ERROR_INVALID_DATA
		}
		return 0, err
	}
	buf := make([]byte, need)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], need, &need); err != nil {
		return 0, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	sid := label.Label.Sid
	count := uint32(sid.SubAuthorityCount())
	if count == 0 {
		return 0, windows.ERROR_INVALID_DATA
	}
	return sid.SubAuthority(count - 1), nil
}

// elevationDecision fails closed: an unreadable token counts as elevated, so a
// possibly elevated copy is refused rather than allowed to take the name. Only
// a full token is refused; type default (UAC off, or no split token) is allowed
// whatever its flag or integrity level.
func elevationDecision(elevated bool, kind, integrity uint32, err error) bool {
	if err != nil {
		return true
	}
	return kind == tokenElevationTypeFull && (elevated || integrity >= integrityHigh)
}
