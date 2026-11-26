//go:build windows

package blackbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noOrdinaryUsers fails if a permission list lets anyone but SYSTEM, administrators
// or the running account in, or inherits from above.
func noOrdinaryUsers(t *testing.T, what, dacl string) {
	t.Helper()
	if !strings.Contains(dacl, "D:P") {
		t.Errorf("%s: list is not protected: %s", what, dacl)
	}
	for _, sid := range []string{";;;BU)", ";;;WD)", ";;;AU)", ";;;IU)", ";;;LU)"} {
		if strings.Contains(dacl, sid) {
			t.Errorf("%s: an entry for ordinary users (%s): %s", what, sid, dacl)
		}
	}
	if !strings.Contains(dacl, ";;;SY)") {
		t.Errorf("%s: SYSTEM missing: %s", what, dacl)
	}
}

func TestCrashACLIsPrivateForAFolderAndAFile(t *testing.T) {
	for _, elevated := range []bool{true, false} {
		dir := filepath.Join(longTempDir(t), "crash")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		giveBack(t, dir)
		f := filepath.Join(dir, "crash-x-1-go.txt")
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := applyCrashACL(dir, elevated, true); err != nil {
			t.Fatal(err)
		}
		d := daclText(t, dir)
		noOrdinaryUsers(t, "folder", d)
		if elevated && !strings.Contains(d, ";;;BA)") {
			t.Errorf("administrators missing: %s", d)
		}
		if err := applyCrashACL(f, elevated, false); err != nil {
			t.Fatal(err)
		}
		noOrdinaryUsers(t, "file", daclText(t, f))
	}
}

// The real securing, end to end: a started capture leaves a private folder and a
// private raw dump file, whatever the recordings folder allowed.
func TestStartedCrashCaptureLeavesPrivateFolderAndFile(t *testing.T) {
	folder := longTempDir(t)
	giveBack(t, folder)
	c := startCrashCapture(folder, func(f string, a ...any) { t.Logf(f, a...) })
	if c == nil {
		t.Fatal("capture did not start")
	}
	pending := c.Path()
	t.Cleanup(c.Close)
	noOrdinaryUsers(t, "crash folder", daclText(t, CrashDir(folder)))
	noOrdinaryUsers(t, "raw dump file", daclText(t, pending))
}
