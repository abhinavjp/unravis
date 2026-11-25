//go:build windows

package settingsfile

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/codemode"
)

// The saved file has a protected DACL (no inheritance from the folder) with the current user and SYSTEM only, and the DACL survived the
// rename over the final name.
func TestSavedFileHasProtectedUserAndSystemOnlyDACL(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	sd, err := windows.GetNamedSecurityInfo(s.Path(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Error("DACL is not protected")
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
