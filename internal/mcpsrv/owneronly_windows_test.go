//go:build windows

package mcpsrv

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertOwnerOnly checks the real protection on Windows, the DACL (unix mode bits do not exist here): every entry is an
// allow entry for the current user or SYSTEM, both are present, and nobody else (Everyone, Users, Authenticated Users) is
// named. wantProtected also requires the DACL to be cut from its parent, which is what casestore.SecureDir and SecureFile
// write; files made later inside the folder (-wal, -shm) only inherit the folder's two entries, so they pass false.
func assertOwnerOnly(t *testing.T, path string, wantProtected bool) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("%s: reading the DACL: %v", path, err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if wantProtected && ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s: the DACL still inherits from its parent", path)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("%s: no DACL (a null DACL grants everyone): %v", path, err)
	}
	seenUser, seenSystem := false, false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("%s: unexpected ACE type %d", path, ace.Header.AceType)
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid):
			seenUser = true
		case sid.Equals(system):
			seenSystem = true
		default:
			t.Errorf("%s: unexpected ACE for %s", path, sid.String())
		}
	}
	if !seenUser || !seenSystem {
		t.Errorf("%s: user=%v system=%v, want both", path, seenUser, seenSystem)
	}
}
