//go:build !windows

package mcpsrv

import (
	"os"
	"testing"
)

// assertOwnerOnly checks the unix mode: no group or other bits. wantProtected is the Windows DACL flag and means nothing here.
func assertOwnerOnly(t *testing.T, path string, _ bool) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("%s mode %v", path, st.Mode())
	}
}
