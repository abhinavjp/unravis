//go:build windows

package settingsfile

import (
	"errors"
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

// A file whose access list was widened after Save (someone else was given access) is not trusted: Load says corrupt and code mode stays
// off. A list that only holds the owner, SYSTEM and Administrators still loads.
func TestLoadRefusesAFileWithAWidenedAccessList(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if set, err := s.Load(); err != nil || !set.Enabled {
		t.Fatalf("a file Save wrote must load: %+v %v", set, err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	// Add Everyone: read to the saved list (the owner and SYSTEM entries stay).
	sd, err := windows.GetNamedSecurityInfo(s.Path(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(s.Path(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if set, err := s.Load(); !errors.Is(err, ErrCorrupt) || set.Enabled {
		t.Fatalf("a widened list must not be trusted: %+v %v", set, err)
	}
	if s.Settings().Enabled {
		t.Fatal("code mode must stay off for a widened file")
	}
	// Saving again puts the owner-only list back and the file loads.
	if err := s.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if set, err := s.Load(); err != nil || !set.Enabled {
		t.Fatalf("after a fresh save: %+v %v", set, err)
	}
}
