//go:build windows

package casestore_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/casestore"
)

func TestSecureDirProtectedDACLWithUserAndSystemOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "case")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := casestore.SecureDir(dir); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "case.key")
	if err := os.WriteFile(child, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)

	check := func(path string, wantProtected bool) {
		t.Helper()
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		ctrl, _, err := sd.Control()
		if err != nil {
			t.Fatal(err)
		}
		if protected := ctrl&windows.SE_DACL_PROTECTED != 0; protected != wantProtected {
			t.Errorf("%s: DACL protected = %v, want %v", filepath.Base(path), protected, wantProtected)
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil {
			t.Fatalf("%s: no DACL: %v", filepath.Base(path), err)
		}
		seenUser, seenSystem := false, false
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			switch {
			case sid.Equals(user.User.Sid):
				seenUser = true
			case sid.Equals(system):
				seenSystem = true
			default:
				t.Errorf("%s: unexpected ACE for %s", filepath.Base(path), sid.String())
			}
		}
		if !seenUser || !seenSystem {
			t.Errorf("%s: user=%v system=%v, want both", filepath.Base(path), seenUser, seenSystem)
		}
	}
	check(dir, true)
	// Children inherit the same two entries; their DACL is inherited, not protected.
	check(child, false)
}

// SecureFile gives a file in an ordinary, inheriting folder a protected DACL
// with the current user and SYSTEM only, and the DACL survives a rename.
func TestSecureFileProtectedDACLSurvivesRename(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, ".session-1.tmp")
	if err := os.WriteFile(temp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := casestore.SecureFile(temp); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, "session.json")
	if err := os.Rename(temp, final); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	sd, err := windows.GetNamedSecurityInfo(final, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Error("the file DACL still inherits from its folder")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	seenUser, seenSystem := false, false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid):
			seenUser = true
		case sid.Equals(system):
			seenSystem = true
		default:
			t.Errorf("unexpected ACE for %s", sid.String())
		}
	}
	if !seenUser || !seenSystem {
		t.Errorf("user=%v system=%v, want both", seenUser, seenSystem)
	}
}

// makeJunction creates link -> target with mklink /J, which needs no privilege.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) }) // removes the junction, not the target
}

func TestRejectReparseRefusesJunction(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	target := filepath.Join(base, "target")
	for _, d := range []string{root, target} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := casestore.RejectReparse(root, real); err != nil {
		t.Fatalf("plain folder refused: %v", err)
	}
	if err := casestore.RejectReparse(root, filepath.Join(root, "not-yet")); err != nil {
		t.Fatalf("missing component refused: %v", err)
	}
	link := filepath.Join(root, "link")
	makeJunction(t, link, target)
	if err := casestore.RejectReparse(root, link); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Fatalf("junction as case folder = %v, want ErrReparsePoint", err)
	}
	if err := casestore.RejectReparse(root, filepath.Join(link, "deeper")); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Fatalf("path through a junction = %v, want ErrReparsePoint", err)
	}
	// The root itself being a junction is refused too.
	rootLink := filepath.Join(base, "rootlink")
	makeJunction(t, rootLink, root)
	if err := casestore.RejectReparse(rootLink, rootLink); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Fatalf("junction as root = %v, want ErrReparsePoint", err)
	}
	// A path outside root is refused.
	if err := casestore.RejectReparse(root, target); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Fatalf("path outside root = %v, want refusal", err)
	}
}
