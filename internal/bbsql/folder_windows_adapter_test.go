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
	if err := setFolderOwnerErr(t, dir, sid); err != nil {
		t.Fatalf("set owner of test folder: %v", err)
	}
}

// setFolderOwnerErr is setFolderOwner without t.Fatalf, so a caller can tell
// "this harness cannot hold that owner" apart from "the code is wrong". Only that
// first case may ever skip: a skipped assertion once let the installer's own
// folder shape go untested (R1-c5).
func setFolderOwnerErr(t *testing.T, dir, sid string) error {
	t.Helper()
	s, err := windows.StringToSid(sid)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION, s, nil, nil, nil)
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

// W10-01 regression: the reader must describe the installer's own explicitly
// locked folder as it is written. x/sys DACL() returns "defaulted", not "present";
// reading that as presence made every correct install look unlocked and refused
// startup. The pre-existing smoke test only asserted refusal of a temp folder, so
// it passed even while the reader always refused.
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
	// This test proves the READER (W10-01): the DACL and owner of a correctly
	// locked folder come back as they are written. What the rule then decides for
	// a given owner is proved unconditionally, never behind a branch on who owns
	// the temp folder: acceptance of the owner the installer assigns is
	// TestFolderAdapterAcceptsAdministratorsOwnedLock, and refusal of a
	// user-owned folder is TestFolderAdapterRefusesUserOwnedLockedFolder (R1-c5).
	//
	// Only the identity half is decided here, and it holds whatever the owner
	// turns out to be: this process is not the SQL service, so it must be refused
	// (W10-11).
	if err := CheckFolderAccess(dir); err == nil {
		t.Fatal("a process that is not the SQL service was accepted")
	}
}

// W10-01 + R1-s1 + R2-A regression: a folder carrying the exact install-sql lock
// AND the owner install-sql assigns is ACCEPTED - unconditionally, on every
// Windows edition, with no skip on either path. The owner comes from the shared
// FolderOwnerSID constant, so the rule and the installer agree by construction.
//
// One field cannot be staged by a token that holds no SeRestorePrivilege: the
// owner itself (measured: WIN32 1307 for S-1-5-32-544). That is reported, never
// skipped around - the reader's half of the assertions runs against the real
// folder either way, and the ACCEPT always runs against the descriptor the reader
// produced, with the owner the installer assigns. A run that quietly gave up on
// staging and asserted the opposite is how R1-s1 shipped.
func TestFolderAdapterAcceptsAdministratorsOwnedLock(t *testing.T) {
	dir := t.TempDir()
	ownerErr := setFolderOwnerErr(t, dir, FolderOwnerSID())
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
	if ownerErr == nil {
		// The reader saw the owner Windows actually records, on a folder really
		// owned by the pinned trustee.
		if !strings.EqualFold(acl.Owner, FolderOwnerSID()) {
			t.Fatalf("owner = %q, want the owner the installer assigns (%s)", acl.Owner, FolderOwnerSID())
		}
	} else {
		var e windows.Errno
		if !errors.As(ownerErr, &e) {
			t.Fatalf("unexpected set-owner failure: %v", ownerErr)
		}
		t.Logf("this harness cannot own as %s: WIN32 %d - the elevated installer can, through SeRestorePrivilege; the owner is named here from the same constant the install writes", FolderOwnerSID(), uint32(e))
		acl.Owner = FolderOwnerSID()
	}
	// THE ACCEPT: always runs, on a folder whose DACL the reader read from disk.
	if err := acl.check(); err != nil {
		t.Fatalf("the installer's locked, administrator-owned folder was refused: %v", err)
	}
	// The whole path, on the folder on disk: what may still refuse it is the
	// identity of this process (never the service, so always refused here) or,
	// when the owner could not be staged, the owner itself - and never the lock,
	// which this folder carries exactly.
	err = CheckFolderAccess(dir)
	if err == nil {
		t.Fatal("CheckFolderAccess accepted a folder this process is not the SQL service for")
	}
	if ownerErr == nil && !errors.Is(err, errFolderIdentity) {
		t.Fatalf("CheckFolderAccess on the accepted folder = %v, want the identity refusal and nothing else", err)
	}
	if ownerErr != nil && !errors.Is(err, errFolderLock) {
		t.Fatalf("CheckFolderAccess on a folder this token could not own = %v, want the owner refusal", err)
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
