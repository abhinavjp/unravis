package bbsql

import (
	"errors"
	"strings"
	"testing"
)

const testSelf = "S-1-5-80-3-142520817-3865290043-129737760-3809223833"

func lockedACL() folderACL {
	return folderACL{
		Present:   true,
		Protected: true,
		Grantees:  []string{sidSystem, sidAdministrators, testSelf},
	}
}

func TestFolderCheckAcceptsLockedFolder(t *testing.T) {
	if err := lockedACL().check(testSelf); err != nil {
		t.Fatalf("the install-sql DACL refused: %v", err)
	}
	// Order and case are not the point: the same three trustees written by a
	// different tool must still pass.
	acl := folderACL{Present: true, Protected: true, Grantees: []string{
		strings.ToLower(testSelf), sidAdministrators, sidSystem}}
	if err := acl.check(testSelf); err != nil {
		t.Fatalf("reordered lowercase DACL refused: %v", err)
	}
	// A folder locked to only the service account is not readable by strangers.
	only := folderACL{Present: true, Protected: true, Grantees: []string{testSelf}}
	if err := only.check(testSelf); err != nil {
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

	for name, tc := range map[string]struct {
		acl  folderACL
		self string
	}{
		"builtin users granted": {withUsers, testSelf},
		"everyone granted":      {withEveryone, testSelf},
		"local users granted":   {func() folderACL { a := lockedACL(); a.Grantees = append(a.Grantees, "S-1-5-21-1-1-1-1001"); return a }(), testSelf},
		"unprotected DACL":      {unprotected, testSelf},
		"inherited ace":         {inherited, testSelf},
		"unclassifiable ace":    {unknownGrant, testSelf},
		"empty DACL":            {empty, testSelf},
		"no dacl at all":        {folderACL{Present: false, Protected: true, Grantees: []string{sidSystem}}, testSelf},
		"nil grantees":          {folderACL{Present: true, Protected: true}, testSelf},
		"own sid unreadable":    {lockedACL(), ""},
		"own sid blank":         {lockedACL(), "   "},
	} {
		if err := tc.acl.check(tc.self); !errors.Is(err, errFolderLock) {
			t.Errorf("%s: check = %v, want the folder-lock refusal", name, err)
		}
	}
}

func TestFolderLockErrorIsFixedAndNamesNothing(t *testing.T) {
	// C10: one fixed sentence, and it must not name the folder, the account or
	// the SID that broke the rule.
	if errFolderLock.Code != CodeFolder || errFolderLock.Message == "" {
		t.Fatalf("errFolderLock = %#v", errFolderLock)
	}
	for _, banned := range []string{"\\", "S-1-5", errFolderLock.Code} {
		if strings.Contains(errFolderLock.Message, banned) {
			t.Errorf("the message contains %q: %q", banned, errFolderLock.Message)
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
