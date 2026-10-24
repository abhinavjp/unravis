//go:build windows

package sqlite

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/casestore/storetest"
)

func daclProtected(t *testing.T, path string) bool {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	return ctrl&windows.SE_DACL_PROTECTED != 0
}

// New protects the cases folder whether it made the folder or found it: a root
// left by an older build, or made by hand, keeps no inherited ACL (batch-2 L5).
func TestNewSecuresExistingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cases")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if daclProtected(t, root) {
		t.Skip("a fresh folder here already has a protected DACL; the check proves nothing")
	}
	newStoreAt(t, root, storetest.RandomProtector())
	if !daclProtected(t, root) {
		t.Error("an existing root kept its inherited DACL after New")
	}
	// A second New over the same folder is a no-op, not an error.
	newStoreAt(t, root, storetest.RandomProtector())
	if !daclProtected(t, root) {
		t.Error("root lost its protected DACL on a second New")
	}
}
