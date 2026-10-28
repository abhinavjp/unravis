//go:build windows

package server

import (
	"testing"

	"golang.org/x/sys/windows"
)

// shortPathOf returns the 8.3 form of an existing path, or "" when the volume
// keeps no short names.
func shortPathOf(t *testing.T, p string) string {
	t.Helper()
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
