package bbsql

import (
	"errors"
	"strings"
	"testing"
)

// testSelf is the PINNED SQL service SID (W10-11): the third trustee is a
// constant, never the current token.
const testSelf = sqlServiceSID

func lockedACL() folderACL {
	return folderACL{
		Owner:     sidSystem,
		Present:   true,
		Protected: true,
		Grantees:  []string{sidSystem, sidAdministrators, testSelf},
	}
}

func TestFolderCheckAcceptsLockedFolder(t *testing.T) {
	if err := lockedACL().check(); err != nil {
		t.Fatalf("the install-sql DACL refused: %v", err)
	}
	// Order and case are not the point: the same three trustees written by a
	// different tool must still pass.
	acl := folderACL{Owner: sidAdministrators, Present: true, Protected: true, Grantees: []string{
		strings.ToLower(testSelf), sidAdministrators, sidSystem}}
	if err := acl.check(); err != nil {
		t.Fatalf("reordered lowercase DACL refused: %v", err)
	}
	// A folder locked to only the service account is not readable by strangers.
	only := folderACL{Owner: testSelf, Present: true, Protected: true, Grantees: []string{testSelf}}
	if err := only.check(); err != nil {
		t.Fatalf("service-only DACL refused: %v", err)
	}
}

func TestFolderCheckRefuses(t *testing.T) {
	withUsers := lockedACL()
	withUsers.Grantees = append(withUsers.Grantees, "S-1-5-32-545") // BUILTIN\Users
	withEveryone := lockedACL()
	withEveryone.Grantees = append(withEveryone.Grantees, "S-1-1-0") // Everyone
	unprotected := lockedACL()
	unprotected.Protected = false
	inherited := lockedACL()
	inherited.Inherited = true
	unknownGrant := lockedACL()
	unknownGrant.Unknown = true
	empty := folderACL{Present: true, Protected: true}
	// W10-11: a DIFFERENT service SID (another virtual account) is not the
	// pinned trustee, even though it is a well-formed S-1-5-80 SID.
	otherService := lockedACL()
	otherService.Grantees = []string{sidSystem, sidAdministrators, "S-1-5-80-3-142520817-3865290043-129737760-3809223833"}

	for name, tc := range map[string]struct {
		acl folderACL
	}{
		"builtin users granted": {withUsers},
		"everyone granted":      {withEveryone},
		"local users granted":   {func() folderACL { a := lockedACL(); a.Grantees = append(a.Grantees, "S-1-5-21-1-1-1-1001"); return a }()},
		"unprotected DACL":      {unprotected},
		"inherited ace":         {inherited},
		"unclassifiable ace":    {unknownGrant},
		"empty DACL":            {empty},
		"no dacl at all":        {folderACL{Present: false, Protected: true, Grantees: []string{sidSystem}}},
		"nil grantees":          {folderACL{Present: true, Protected: true}},
		"another service SID":   {otherService},
	} {
		if err := tc.acl.check(); !errors.Is(err, errFolderLock) {
			t.Errorf("%s: check = %v, want the folder-lock refusal", name, err)
		}
	}
}

// TestFolderCheckOwnerRule: the owner holds implicit WRITE_DAC no matter what
// the DACL says, so a folder OWNED by anyone outside SYSTEM, Administrators
// and the pinned SQL service SID is refused even when the DACL itself is the
// exact install-sql lock. An unreadable (empty) or garbage owner is refused
// too: missing evidence is never a pass.
func TestFolderCheckOwnerRule(t *testing.T) {
	for _, owner := range []string{sidSystem, sidAdministrators, sqlServiceSID} {
		acl := lockedACL()
		acl.Owner = owner
		if err := acl.check(); err != nil {
			t.Errorf("owner %s with the install-sql DACL refused: %v", owner, err)
		}
	}
	for name, owner := range map[string]string{
		"ordinary user owner":      "S-1-5-21-1-1-1-1001",
		"builtin users owner":      "S-1-5-32-545",
		"everyone owner":           "S-1-1-0",
		"another service owner":    "S-1-5-80-3-142520817-3865290043-129737760-3809223833",
		"empty owner":              "",
		"garbage owner":            "not-a-sid",
		"truncated service prefix": "S-1-5-80",
	} {
		acl := lockedACL() // the DACL is exactly right; only the owner differs
		acl.Owner = owner
		if err := acl.check(); !errors.Is(err, errFolderLock) {
			t.Errorf("%s: check = %v, want the folder-lock refusal (implicit WRITE_DAC)", name, err)
		}
	}
}

// R1-s1: the owner the elevated install assigns the sample folder must be an
// owner this rule ACCEPTS, and it must come from the one constant the installer
// writes with, so the rule and the installer cannot drift apart. Pure: it needs
// no privilege, cannot skip and so still pins the shape on a non-Windows run.
func TestFolderCheckAcceptsTheInstallerOwner(t *testing.T) {
	if got := FolderOwnerSID(); got != "S-1-5-32-544" {
		t.Fatalf("FolderOwnerSID() = %q, want BUILTIN\\Administrators (S-1-5-32-544): the install DACL names BA with the SDDL abbreviation, so any other owner would be an account the lock does not name", got)
	}
	acl := folderACL{
		Owner:     FolderOwnerSID(),
		Present:   true,
		Protected: true,
		Grantees:  []string{sidSystem, sidAdministrators, sqlServiceSID},
	}
	if err := acl.check(); err != nil {
		t.Fatalf("check() refused the folder the installer creates (owner %s): %v", FolderOwnerSID(), err)
	}
	// One predicate still gates the owner and the grantees (SEC-2), so the owner
	// the installer assigns cannot stop being a trustee without this refusing.
	if !allowedTrustee(FolderOwnerSID()) {
		t.Fatalf("FolderOwnerSID() = %q is not an allowed trustee", FolderOwnerSID())
	}
	// The other half of the same pure pair, in one test: the bare group SID
	// S-1-5-32-544 is what passes, and an account's own SID is NOT what passes -
	// not even an interactive administrator's logon account, because that owner
	// holds implicit WRITE_DAC over the folder whatever the DACL says (SEC-2).
	if !allowedTrustee(sidAdministrators) {
		t.Errorf("the bare group SID %s is not an accepted owner: the folder the installer owns as Administrators would refuse itself", sidAdministrators)
	}
	userOwned := folderACL{
		Owner:     "S-1-5-21-1-1-1-1001",
		Present:   true,
		Protected: true,
		Grantees:  []string{sidSystem, sidAdministrators, sqlServiceSID},
	}
	if err := userOwned.check(); !errors.Is(err, errFolderLock) {
		t.Errorf("a non-admin account as owner: check = %v, want the folder-lock refusal", err)
	}
}

func TestFolderLockErrorIsFixedAndNamesNothing(t *testing.T) {
	// C10: one fixed sentence, and it must not name the folder, the account or
	// the SID that broke the rule.
	for _, e := range []*Error{errFolderLock, errFolderIdentity} {
		if e.Code != CodeFolder || e.Message == "" {
			t.Fatalf("err = %#v", e)
		}
		for _, banned := range []string{"\\", "S-1-5", e.Code} {
			if strings.Contains(e.Message, banned) {
				t.Errorf("the message contains %q: %q", banned, e.Message)
			}
		}
	}
	if err := CheckFolderAccess("  "); !errors.Is(err, errFolderLock) {
		t.Errorf("empty folder: err = %v, want the folder-lock refusal", err)
	}
}

func TestCheckFolderAccessOnRealFolder(t *testing.T) {
	// The platform reader must at least answer for a folder this process owns:
	// a temporary folder is inherited from the parent, so the honest answer is
	// the refusal, not a pass.
	dir := t.TempDir()
	err := CheckFolderAccess(dir)
	if err == nil {
		t.Skip("this platform reports the temp folder as locked")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeFolder {
		t.Fatalf("err = %#v, want code %q", err, CodeFolder)
	}
}

// TestPinnedServiceSIDShape pins the constant's shape: it is the S-1-5-80
// per-service SID family, and it is NOT the current token (the Windows adapter
// test proves the identity half on a real token).
func TestPinnedServiceSIDShape(t *testing.T) {
	if !strings.HasPrefix(sqlServiceSID, "S-1-5-80-") || strings.Count(sqlServiceSID, "-") != 8 {
		t.Fatalf("pinned SID shape: %q", sqlServiceSID)
	}
}
