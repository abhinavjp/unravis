// folder.go is the H3 control of the wave-10 review: the SQL service proves its
// own data folder is locked before it writes a sample into it. install-sql is
// what writes the DACL, but the folder comes from config, and "folder" +
// "apply" can point the service at a path whose permissions were inherited from
// the parent - which on a normal server means BUILTIN\Users can read every
// sample. A sample folder anyone can read is a privacy failure, so the service
// refuses to write rather than trusting how the folder came to exist.
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

// The two well-known principals that are always allowed: NT AUTHORITY\SYSTEM
// and BUILTIN\Administrators. The service's own account is allowed by being the
// process that runs this check.
const (
	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
)

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
// one to start recording into.
func (a folderACL) check(self string) error {
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
	if strings.TrimSpace(self) == "" {
		// Without our own SID the allow-list cannot be completed, and guessing
		// that the service runs as one of the two well-known accounts would
		// fail open for a service moved to another virtual account.
		return errFolderLock
	}
	for _, g := range a.Grantees {
		if !strings.EqualFold(g, sidSystem) &&
			!strings.EqualFold(g, sidAdministrators) &&
			!strings.EqualFold(g, self) {
			return errFolderLock
		}
	}
	return nil
}

// CheckFolderAccess reports whether dir may hold SQL samples. It fails closed:
// a folder it cannot read, a DACL it cannot classify and a DACL that grants
// access to anyone outside SYSTEM, Administrators and this account all refuse.
func CheckFolderAccess(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errFolderLock
	}
	acl, err := readFolderACL(dir)
	if err != nil {
		return errFolderLock
	}
	return acl.check(folderSelfSID())
}
