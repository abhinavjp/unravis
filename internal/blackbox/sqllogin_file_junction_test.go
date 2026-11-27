//go:build windows

package blackbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A junction named like a login folder must be skipped by the start-up sweep, and what it points to must be left untouched.
func TestSweepLeavesAJunctionAndItsTargetAlone(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, "login.json")
	if err := os.WriteFile(keep, []byte(`{"password":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "unravis-sqllogin-junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil { //nolint:gosec // G204: a fixed mklink call on paths this test made in its own temp dir
		t.Skipf("could not make a junction here: %v %s", err, out)
	}
	if n := SweepSQLLoginFiles(root); n != 0 {
		t.Errorf("the sweep removed %d entries, want 0", n)
	}
	b, err := os.ReadFile(keep) //nolint:gosec // G304: a path this test made in its own temp dir
	if err != nil || string(b) != `{"password":"x"}` {
		t.Fatalf("the junction target was touched: %q %v", b, err)
	}
}
