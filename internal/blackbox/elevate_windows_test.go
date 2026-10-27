//go:build windows

package blackbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The folder an elevated answer is written to is private (F4): protected from
// inherited entries, and no entry for ordinary users or Everyone.
func TestMakePrivateDirIsProtectedAndOurs(t *testing.T) {
	dir, err := os.MkdirTemp("", "unravis-bb-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := makePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	// We can still write in it.
	if err := os.WriteFile(filepath.Join(dir, "answer.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	if !strings.Contains(s, "D:P") {
		t.Errorf("the entries are not protected from inheritance: %s", s)
	}
	for _, bad := range []string{";;;BU)", ";;;WD)", ";;;AU)"} {
		if strings.Contains(s, bad) {
			t.Errorf("the folder is open to a wider group (%s): %s", bad, s)
		}
	}
}
