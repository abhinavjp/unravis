//go:build windows

package blackbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// giveBack lets this account delete what a test locked down, so the temporary
// folder can be removed afterwards.
func giveBack(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		u, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return
		}
		acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(u.User.Sid)},
		}}, nil)
		if err != nil {
			return
		}
		_ = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	})
}

func daclText(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

// SEC-FIX-06: the permission list is applied through the folder's handle, is
// protected, and reaches what is already inside.
func TestSecureDirHandleAppliesThePermissionList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bb")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "existing.bbx")
	if err := os.WriteFile(child, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	giveBack(t, dir)
	if err := secureDirHandle(dir, true, false); err != nil {
		t.Fatal(err)
	}
	s := daclText(t, dir)
	if !strings.Contains(s, "D:P") || !strings.Contains(s, ";;;SY)") || !strings.Contains(s, ";;;BA)") || !strings.Contains(s, ";;;BU)") {
		t.Errorf("folder list is not the secure one: %s", s)
	}
	cs := daclText(t, child)
	if !strings.Contains(cs, ";;;SY)") || !strings.Contains(cs, ";;;BA)") || !strings.Contains(cs, "ID;") {
		t.Errorf("the file inside did not get the inherited list: %s", cs)
	}
	// Without usersRead there is no entry for ordinary users.
	if err := secureDirHandle(dir, false, false); err != nil {
		t.Fatal(err)
	}
	if s := daclText(t, dir); strings.Contains(s, ";;;BU)") {
		t.Errorf("ordinary users still have an entry: %s", s)
	}
}

// SEC-FIX-06: a folder swapped for a junction is never the target.
func TestSecureDirHandleRefusesAJunction(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	before := daclText(t, target)
	link := filepath.Join(base, "link")
	if err := makeJunction(link, target); err != nil {
		t.Skipf("cannot make a junction here: %v", err)
	}
	err := secureDirHandle(link, true, false)
	if !errors.Is(err, ErrFolderChanged) {
		t.Fatalf("a junction was accepted: %v", err)
	}
	if after := daclText(t, target); after != before {
		t.Fatalf("the junction target was changed: %s -> %s", before, after)
	}
}

func TestSecureDirHandleRefusesAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := secureDirHandle(f, true, false); !errors.Is(err, ErrFolderChanged) {
		t.Fatalf("a file was accepted as the folder: %v", err)
	}
}

// SEC-FIX-06: the handle must lead to the place that was checked. A short (8.3)
// spelling leads to the same folder but is not the name that was judged.
func TestSecureDirHandleRefusesAPlaceThatWasNotChecked(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "A Long Folder Name")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p, _ := windows.UTF16PtrFromString(dir)
	buf := make([]uint16, 512)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skip("no short name for this folder")
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, dir) {
		t.Skip("8.3 names are off on this drive")
	}
	if err := secureDirHandle(short, true, false); !errors.Is(err, ErrFolderChanged) {
		t.Fatalf("a different spelling of the path was accepted: %v", err)
	}
}

// SEC-FIX-06: the folder is made with the list already on it, and an existing
// name is refused, not reused.
func TestCreateSecureDirMakesItProtectedAndRefusesAnExistingName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bb")
	var env OSEnv
	if err := env.CreateSecureDir(dir, false); err != nil {
		t.Fatal(err)
	}
	giveBack(t, dir)
	if s := daclText(t, dir); !strings.Contains(s, "D:P") || strings.Contains(s, ";;;BU)") || !strings.Contains(s, ";;;SY)") {
		t.Errorf("not protected at birth: %s", s)
	}
	err := env.CreateSecureDir(dir, false)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("an existing folder was not refused: %v", err)
	}
}
