// folder.go is the H3 control of the wave-10 review: the SQL service proves its
// own data folder is locked before it writes a sample into it. install-sql is
// what writes the DACL, but the folder comes from config, and "folder" +
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
// The rule is decided here, in pure Go over a described DACL, so it is testable
// on any platform; reading a real DACL is the platform job in
// folder_windows.go / folder_other.go.
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

// sqlServiceSID is the PINNED per-service SID of NT SERVICE\UnravisBlackboxSql
// (S-1-5-80 + SHA-1 of the upper-case service name in UTF-16LE, five
// little-endian uint32s — the same algorithm blackbox.sqlServiceSID uses,
// pinned there by TestSQLServiceSID against `sc showsid`). It is a constant,
// not the current token: a folder locked to some OTHER account, or a check run
// by some other account, can never pass itself off as the SQL service
// (W10-11).
const sqlServiceSID = "S-1-5-80-690519117-884339739-239568777-2365834119-2194647489"

// folderACL is a DACL described in the only terms the rule needs.
type folderACL struct {
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

// check applies the rule: a readable-by-strangers folder is refused, and so is
// anything the check cannot prove. An empty DACL (nobody has access) is refused
// too - the service could not write into it, and a folder that strange is not
// one to start recording into. The only trustees are SYSTEM, Administrators
// and the pinned SQL service SID (W10-11).
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
	}
	for _, g := range a.Grantees {
		if !strings.EqualFold(g, sidSystem) &&
			!strings.EqualFold(g, sidAdministrators) &&
			!strings.EqualFold(g, sqlServiceSID) {
			return errFolderLock
		}
	}
	return nil
}

// CheckFolderAccess reports whether dir may hold SQL samples. It fails closed:
// a folder it cannot read, a DACL it cannot classify, a DACL that grants
// access to anyone outside SYSTEM, Administrators and the pinned SQL service
// SID, and a process that is not that service identity all refuse (W10-11).
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
