// folder.go is the H3 control of the wave-10 review: the SQL service proves its
// own data folder is locked before it writes a sample into it. install-sql is
// what writes the folder's DACL AND, since R1-s1, its owner (folderOwnerSID),
// but the folder comes from config, and "folder" +
// "apply" can point the service at a path whose permissions were inherited from
// the parent - which on a normal server means BUILTIN\Users can read every
// sample. A sample folder anyone can read is a privacy failure, so the service
// refuses to write rather than trusting how the folder came to exist.
//
// W10-11: the third allowed trustee is PINNED to the UnravisBlackboxSql service
// SID, never "whatever account runs this check", and the check also verifies
// the process actually IS that service identity. A console or helper process
// running as an administrator is refused: the hidden sql-service console mode
// is diagnostic wiring only, and production sampling runs exclusively inside
// the virtual-account service under the same controls.
//
// The rule is decided here, in pure Go over a described DACL and owner, so it
// is testable on any platform; reading a real DACL and owner is the platform
// job in folder_windows.go / folder_other.go.
package bbsql

import (
	"strings"
)

// CodeFolder is the fixed C10 code for "the sample folder is not locked to the
// accounts allowed to read it". The message never names the folder, the SID or
// the account that broke the rule: the owner sees which folder by reading the
// config, not a log line.
const CodeFolder = "folder-lock"

// errFolderLock is the one sentence this failure ever produces.
var errFolderLock = &Error{CodeFolder, "The SQL sample folder is not locked to the accounts allowed to read it. Re-run unravis-blackbox install-sql with the folder in the settings file."}

// errFolderIdentity refuses a process that is not the SQL service itself
// (W10-11). Fixed sentence, no account or SID named.
var errFolderIdentity = &Error{CodeFolder, "The SQL sampler must run as the UnravisBlackboxSql service, not from a console or another account."}

// The two well-known principals that are always allowed: NT AUTHORITY\SYSTEM
// and BUILTIN\Administrators.
const (
	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
)

// folderOwnerSID is the owner a SQL sample folder must have, and the one the
// elevated install assigns it (R1-s1). It is BUILTIN\Administrators, which is
// already a trustee of the DACL install-sql writes, so the owner needs no grant
// of its own. The write is not this package's job (bbsql reads descriptors and
// never writes one): internal/blackbox/sqlservice_windows.go names this exact SID
// through FolderOwnerSID in the same SetNamedSecurityInfo call that writes the
// DACL, so the installer can never produce a folder this rule refuses.
const folderOwnerSID = sidAdministrators

// FolderOwnerSID is the SID the install must make the SQL sample folder's owner.
// Exported so the folder rule and the installer agree by construction instead of
// by two copies of the same literal (R1-s1): a folder owned by whoever ran the
// install is refused here, and that refusal bricks the DB lane at every start.
func FolderOwnerSID() string { return folderOwnerSID }

// sqlServiceSID is the PINNED per-service SID of NT SERVICE\UnravisBlackboxSql
// (S-1-5-80 + SHA-1 of the upper-case service name in UTF-16LE, five
// little-endian uint32s — the same algorithm blackbox.sqlServiceSID uses,
// pinned there by TestSQLServiceSID against `sc showsid`). It is a constant,
// not the current token: a folder locked to some OTHER account, or a check run
// by some other account, can never pass itself off as the SQL service
// (W10-11).
const sqlServiceSID = "S-1-5-80-690519117-884339739-239568777-2365834119-2194647489"

// folderACL is a DACL and owner described in the only terms the rule needs.
type folderACL struct {
	// Owner is the folder's owner SID as a string. The owner always holds
	// implicit WRITE_DAC and READ_CONTROL even when the DACL names nobody it
	// recognises, so an owner outside the trustee set can rewrite the DACL and
	// grant itself access. Empty means the owner could not be read: refuse.
	Owner string
	// Present is false when the object reports no DACL at all.
	Present bool
	// Protected is the SE_DACL_PROTECTED flag: the DACL holds only what was
	// written on this folder, nothing inherited from its parent.
	Protected bool
	// Grantees is one string SID per ACE that grants access. Deny ACEs are not
	// grants and are not listed; an ACE this check cannot classify sets Unknown
	// instead of being skipped.
	Grantees []string
	// Inherited is true when any ACE arrived through inheritance, whatever the
	// protection flag says.
	Inherited bool
	// Unknown is true when an ACE form could not be classified as grant or deny.
	Unknown bool
}

// allowedTrustee reports whether sid is one of the three pinned principals:
// SYSTEM, Administrators or the SQL service SID (W10-11). It gates BOTH the
// DACL grantees and the owner, so the two halves can never drift apart. The owner
// is named through folderOwnerSID, the constant the installer writes with.
//
// The owner rule exists for the case the grantee rule cannot see: an owner
// outside the DACL holds implicit WRITE_DAC whatever is written down, so a folder
// owned by an ordinary account can always be re-opened by that account. Both
// owners this accepts are also granted full control by install-sql's DACL, so
// their implicit WRITE_DAC adds no right the folder did not already give them.
// The SQL service SID stays an accepted owner on purpose (R1-s1): when the service
// creates <folder>\sql itself, after an operator deletes it or repoints Folder,
// its owner is its own service SID, and refusing that would demand a second
// install-sql for an ordinary restart.
func allowedTrustee(sid string) bool {
	return strings.EqualFold(sid, sidSystem) ||
		strings.EqualFold(sid, folderOwnerSID) ||
		strings.EqualFold(sid, sqlServiceSID)
}

// check applies the rule: a readable-by-strangers folder is refused, and so is
// anything the check cannot prove. An empty DACL (nobody has access) is refused
// too - the service could not write into it, and a folder that strange is not
// one to start recording into. The only trustees are SYSTEM, Administrators
// and the pinned SQL service SID (W10-11).
//
// The owner must be one of those three as well: Windows gives the owner
// implicit WRITE_DAC and READ_CONTROL even when the DACL names none of its
// rights, so a folder OWNED by an ordinary user can have its DACL rewritten by
// that user no matter how locked the DACL looks today. An owner that is empty
// or unrecognised is refused, never treated as safe. Nothing inherits or guesses
// an owner here: the contract is that internal/blackbox/sqlservice_windows.go
// assigns folderOwnerSID itself (OWNER|DACL|PROTECTED in one
// SetNamedSecurityInfo) and reads the owner back before the service exists,
// which is the only way a folder this rule accepts can come into existence
// without an operator editing security (R1-s1).
func (a folderACL) check() error {
	switch {
	case !a.Present:
		return errFolderLock
	case a.Unknown:
		return errFolderLock
	case !a.Protected:
		return errFolderLock
	case a.Inherited:
		return errFolderLock
	case len(a.Grantees) == 0:
		return errFolderLock
	case !allowedTrustee(a.Owner):
		return errFolderLock
	}
	for _, g := range a.Grantees {
		if !allowedTrustee(g) {
			return errFolderLock
		}
	}
	return nil
}

// CheckFolderAccess reports whether dir may hold SQL samples. It fails closed:
// a folder it cannot read, a DACL it cannot classify, a DACL that grants
// access to anyone outside SYSTEM, Administrators and the pinned SQL service
// SID, an owner outside that same set (implicit WRITE_DAC), and a process that
// is not that service identity all refuse (W10-11).
func CheckFolderAccess(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errFolderLock
	}
	acl, err := readFolderACL(dir)
	if err != nil {
		return errFolderLock
	}
	if err := acl.check(); err != nil {
		return err
	}
	// Production identity: the process must BE the SQL service. Off Windows
	// folderSelfSID is empty and the check refuses, the same fail-closed shape
	// as the missing DACL model.
	if !strings.EqualFold(folderSelfSID(), sqlServiceSID) {
		return errFolderIdentity
	}
	return nil
}
