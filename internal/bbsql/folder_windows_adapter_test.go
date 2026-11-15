//go:build windows

package bbsql

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// applySDDL sets an explicit DACL on a folder the test owns, the same way
// install-sql does. Nothing outside the test's own temp folder is touched.
func applySDDL(t *testing.T, dir, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("parse %q: %v", sddl, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("dacl of %q: %v", sddl, err)
	}
	// Honour the SDDL's own protection flag: forcing PROTECTED here would make an
	// intentionally unprotected descriptor impossible to test.
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatalf("control of %q: %v", sddl, err)
	}
	var info windows.SECURITY_INFORMATION = windows.DACL_SECURITY_INFORMATION
	if ctrl&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, info,
		nil, nil, dacl, nil); err != nil {
		t.Fatalf("apply %q: %v", sddl, err)
	}
}

// setFolderOwner points the folder's owner at sid. Setting the owner to the
// process's own token user needs only WRITE_OWNER, which the test holds on its
// own temp folder; nothing outside the temp folder is touched.
func setFolderOwner(t *testing.T, dir, sid string) {
	t.Helper()
	s, err := windows.StringToSid(sid)
	if err != nil {
		t.Fatalf("parse owner sid: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION, s, nil, nil, nil); err != nil {
		t.Fatalf("set owner of test folder: %v", err)
	}
}

// directOwnerSID reads the owner through a second, independent descriptor so
// the adapter test does not prove the reader against itself.
func directOwnerSID(t *testing.T, dir string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read owner of test folder: %v", err)
	}
	sid, _, err := sd.Owner()
	if err != nil || sid == nil {
		t.Fatalf("owner of test folder: sid=%v err=%v", sid, err)
	}
	return sid.String()
}

// W10-01 regression: the installer's own explicitly locked folder must be
// ACCEPTED. x/sys DACL() returns "defaulted", not "present"; reading that as
// presence made every correct install look unlocked and refused startup. The
// pre-existing smoke test only asserted refusal of a temp folder, so it passed
// even while the reader always refused.
func TestFolderAdapterAcceptsInstallerLock(t *testing.T) {
	dir := t.TempDir()
	// SYSTEM + Administrators + the PINNED SQL service SID, protected, nothing
	// inherited: the exact shape install-sql writes (W10-11).
	applySDDL(t, dir, "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+sqlServiceSID+")")

	acl, err := readFolderACL(dir)
	if err != nil {
		t.Fatalf("readFolderACL: %v", err)
	}
	if !acl.Present || !acl.Protected || acl.Unknown || acl.Inherited {
		t.Fatalf("adapter read %+v, want present+protected, nothing unknown or inherited", acl)
	}
	if len(acl.Grantees) != 3 {
		t.Fatalf("grantees = %v, want the three trustees", acl.Grantees)
	}
	// The reader must report the owner Windows actually records, not a guess.
	if acl.Owner == "" || !strings.EqualFold(acl.Owner, directOwnerSID(t, dir)) {
		t.Fatalf("owner = %q, want the folder's real owner", acl.Owner)
	}
	if allowedTrustee(acl.Owner) {
		// Elevated runs own their temp folders as Administrators: the full
		// locked shape must pass check(), and the production identity check
		// must still refuse this non-service process with the fixed identity
		// sentence (W10-11).
		if err := acl.check(); err != nil {
			t.Fatalf("the installer's locked DACL was refused: %v", err)
		}
		if err := CheckFolderAccess(dir); !errors.Is(err, errFolderIdentity) {
			t.Fatalf("CheckFolderAccess outside the service = %v, want the identity refusal", err)
		}
	} else {
		// Non-elevated runs own their temp folders as the plain user: the
		// owner rule must refuse even though the DACL is exactly right.
		if err := acl.check(); !errors.Is(err, errFolderLock) {
			t.Fatalf("user-owned temp folder check = %v, want the folder-lock refusal", err)
		}
		if err := CheckFolderAccess(dir); !errors.Is(err, errFolderLock) {
			t.Fatalf("CheckFolderAccess on a user-owned folder = %v, want the folder-lock refusal", err)
		}
	}
}

// SEC-2: an ordinary user who OWNS the folder holds implicit WRITE_DAC even
// when the DACL is the exact install-sql lock, so the check must refuse. The
// test reproduces the attack shape: claim ownership first (while the inherited
// DACL still allows it), then write the perfect locked DACL - which succeeds
// precisely BECAUSE the owner keeps implicit WRITE_DAC.
func TestFolderAdapterRefusesUserOwnedLockedFolder(t *testing.T) {
	self := folderSelfSID()
	if self == "" {
		t.Fatal("the test process token has no readable user SID")
	}
	if allowedTrustee(self) {
		t.Skip("running as a trusted trustee itself (SYSTEM or the SQL service); the user-owner case cannot be staged")
	}
	dir := t.TempDir()
	setFolderOwner(t, dir, self)
	applySDDL(t, dir, "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+sqlServiceSID+")")

	acl, err := readFolderACL(dir)
	if err != nil {
		t.Fatalf("readFolderACL: %v", err)
	}
	if !strings.EqualFold(acl.Owner, self) {
		t.Fatalf("owner = %q, want the test user", acl.Owner)
	}
	if !acl.Present || !acl.Protected || acl.Unknown || acl.Inherited || len(acl.Grantees) != 3 {
		t.Fatalf("DACL shape %+v, want the exact install-sql lock", acl)
	}
	if err := acl.check(); !errors.Is(err, errFolderLock) {
		t.Fatalf("user-owned folder with a perfect DACL: check = %v, want the folder-lock refusal", err)
	}
	if err := CheckFolderAccess(dir); !errors.Is(err, errFolderLock) {
		t.Fatalf("CheckFolderAccess on a user-owned folder = %v, want the folder-lock refusal", err)
	}
}

// A folder the reader cannot open at all is an error, which CheckFolderAccess
// turns into the fixed refusal - never a zero-value ACL that looks healthy.
func TestFolderAdapterRefusesUnreadableFolder(t *testing.T) {
	dir := t.TempDir()
	if _, err := readFolderACL(dir + `\does-not-exist`); err == nil {
		t.Fatal("a nonexistent folder read back without error")
	}
}

// W10-12: an allow form whose SID is not at SidStart must refuse, never be
// mistaken for a deny and silently ignored. Type 11 is
// ACCESS_ALLOWED_CALLBACK_OBJECT_ACE, not a deny.
func TestFolderAdapterRefusesUnreadableGrantForms(t *testing.T) {
	base := "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sqlServiceSID + ")"
	for _, tc := range []struct {
		name string
		sddl string
	}{
		// Conditional object allow (type 11): a grant this check cannot read.
		{"callback object allow", base + "(ZA;;FR;00112233-4455-6677-8899-aabbccddeeff;;WD;(Member_of {SID(S-1-5-32-544)}))"},
		// A plain grant to Everyone is outside the trustee set.
		{"everyone grant", base + "(A;OICI;FA;;;WD)"},
		// An empty DACL grants nobody: the service could not write either.
		{"empty dacl", "D:PAI"},
		// A grant to the CURRENT token instead of the pinned service SID is
		// outside the trustee set too (W10-11): the rule no longer trusts
		// "whatever account runs the check".
		{"current-token grant", "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + folderSelfSID() + ")"},
	} {
		if tc.name == "current-token grant" && strings.EqualFold(folderSelfSID(), sqlServiceSID) {
			continue // running as the actual service: the grant IS the pinned SID
		}
		dir := t.TempDir()
		applySDDL(t, dir, tc.sddl)
		acl, err := readFolderACL(dir)
		if err != nil {
			t.Fatalf("%s: readFolderACL: %v", tc.name, err)
		}
		if err := acl.check(); err == nil {
			t.Errorf("%s: accepted, want refusal", tc.name)
		}
	}
}

// An unprotected DACL is refused even when its trustees are exactly right.
func TestFolderAdapterRefusesUnprotected(t *testing.T) {
	dir := t.TempDir()
	applySDDL(t, dir, "D:AI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+sqlServiceSID+")")
	acl, err := readFolderACL(dir)
	if err != nil {
		t.Fatalf("readFolderACL: %v", err)
	}
	if err := acl.check(); err == nil {
		t.Fatal("an unprotected DACL was accepted")
	}
}
