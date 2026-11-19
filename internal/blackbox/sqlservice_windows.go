//go:build windows

package blackbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"api-audit-explorer/internal/bbsql"

	"golang.org/x/sys/windows"
)

// The pure launch plan (sqlLaunchGrants) names the rights as literals so it can
// be decided and tested off Windows. These compile-time checks refuse the build
// the moment a literal stops matching the Windows constant it stands for, in
// either direction: a silent drift would hand the service rights nobody decided,
// or take away ones it needs.
var (
	_ [int(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE) - int(maskSQLProgram)]struct{}
	_ [int(maskSQLProgram) - int(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE)]struct{}
	_ [int(windows.FILE_GENERIC_READ) - int(maskSQLSettings)]struct{}
	_ [int(maskSQLSettings) - int(windows.FILE_GENERIC_READ)]struct{}
	_ [int(windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE) - int(maskSQLTraverse)]struct{}
	_ [int(maskSQLTraverse) - int(windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE)]struct{}
	_ [int(windows.ACCESS_ALLOWED_ACE_TYPE) - int(aceTypeAllow)]struct{}
	_ [int(aceTypeAllow) - int(windows.ACCESS_ALLOWED_ACE_TYPE)]struct{}
)

// SecureSQLDir gives SYSTEM, Administrators and the SQL service SID full control
// of dir and removes every inherited permission: no ordinary user can read the
// SQL data. W7-03: it first PROVES the folder is safe to adopt — trusted owner,
// trusted ancestors, no reparse points anywhere in the chain, and every
// existing child a regular file with a trusted owner — and only then assigns
// the folder its OWNER and its protected DACL in one write (R1-s1: the sampler's
// own folder rule refuses a folder nobody pinned, so an install that left the
// creator's owner in place would brick the DB lane at the first reading) and
// applies the protected DACL to each existing child (replacing explicit child
// ACEs, which inheritance alone would never remove). If any check fails
// nothing is mutated, so pre-existing data keeps exactly what it had, and a
// folder whose owner could not be assigned is refused (undoing the install)
// rather than reported as locked.
func (OSEnv) SecureSQLDir(dir, sid string) error { return secureSQLDir(dir, sid) }

// GrantSQLLaunchAccess gives the SQL service SID the least-privilege rights it
// needs to start (W7-01): read/execute on the installed program, read on the
// settings file, and traverse on the folders that hold them. It only APPENDS
// allow ACEs to the existing DACLs: write, owner and DACL-change rights stay
// with SYSTEM and Administrators, the ACEs are not inheritable, and nothing is
// granted to ordinary users. The service never runs as SYSTEM.
//
// SEC-6: an entry an object already holds exactly is not added again, so a
// re-install leaves one entry per object instead of another copy, and the grant
// stops at the two folders that hold the files — a service account holds bypass
// traverse checking, so the folders it only passes through need nothing.
// RevokeSQLLaunchGrants takes the same entries back on remove-sql.
func (OSEnv) GrantSQLLaunchAccess(exePath, configPath, sid string) error {
	return grantSQLLaunchAccess(exePath, configPath, sid)
}

// VerifyAdminOnlyFile proves a settings file is administrator-locked (SEC-1): an
// owner of SYSTEM or BUILTIN\Administrators — the two trustedFileOwner accepts,
// which the installer ASSIGNS (R1-s2) rather than waits to inherit, so the
// account that ran the install is no longer trusted here — a DACL that grants
// change rights only to SYSTEM and Administrators, and no reparse point in the
// file or any ancestor. Every read failure is a refusal:
// an unreadable or malformed ACL means the file is NOT proven locked, never
// that it is fine. The DACL rule itself is decided in pure Go (adminOnlyDACL).
func (OSEnv) VerifyAdminOnlyFile(path string) error { return verifyAdminOnlyFile(path) }

// LockSQLInstallMarker locks the SQL install record to SYSTEM and
// Administrators only (SEC-3): a protected DACL with full control for SY/BA
// and nothing for anyone else — not even the SQL service SID, which never
// reads the record. The record is OWNED by BUILTIN\Administrators because the
// installer says so in the same single descriptor write as the DACL (R1-s2),
// never because the account that created it happens to be an administrator:
// the owner holds implicit WRITE_DAC, so an owner of the installer's choosing
// could re-open the record this proof is anchored to. verifyAdminOnlyFile
// proves both halves. No inheritance: the record's lock is its own.
func (OSEnv) LockSQLInstallMarker(path string) error { return lockSQLInstallMarker(path) }

// LockSQLSettingsFile assigns the settings file the owner verifyAdminOnlyFile
// demands and the administrator-only protected DACL, in the same single
// descriptor write the install record uses (R1-s2, REG-1). The settings file
// needs this because nothing else in the install ever gives a file an owner:
// SaveConfig writes through a temp file and os.Chmod, and a rename keeps the
// descriptor of the temp file, so the file is owned by the account that ran the
// install — the owner SEC-1's rule refuses on purpose. Windows does not fix
// that by inheritance: an OICI ACE propagates access, never ownership.
//
// The DACL this writes does not carry the SQL service's own read entry, so it
// must be applied BEFORE grantSQLLaunchAccess adds it; install-sql keeps that
// order, and the grant is idempotent, so re-installing leaves one entry per
// trustee.
func (OSEnv) LockSQLSettingsFile(path string) error { return lockSQLSettingsFile(path) }

// lockSQLSettingsFile is the real Windows settings-file lock: the same owner and
// the same protected DACL as the install record, named once here so the two
// halves of the rule cannot drift.
func lockSQLSettingsFile(path string) error { return lockAdminOnlyFile(path) }

// lockSQLInstallMarker applies the admin-only owner and protected DACL to the
// record in one write.
func lockSQLInstallMarker(path string) error { return lockAdminOnlyFile(path) }

// lockAdminOnlyFile is the one descriptor every object the installer claims for
// administrators gets: a protected DACL that leaves change rights with SYSTEM
// and Administrators alone, and BUILTIN\Administrators as the owner, both in a
// single write so there is no moment when the file is locked but still owned by
// whoever created it.
func lockAdminOnlyFile(path string) error {
	sd, err := windows.SecurityDescriptorFromString("D:PAI(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	owner, err := windows.StringToSid(bbsql.FolderOwnerSID())
	if err != nil {
		return err
	}
	return writeOwnerAndProtectedDACL(path, owner, dacl)
}

// verifyAdminOnlyFile is the real Windows proof behind OSEnv.VerifyAdminOnlyFile.
func verifyAdminOnlyFile(path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("blackbox: the settings file path must be absolute")
	}
	// The whole ancestor chain must be free of reparse points (a junction could
	// redirect the privileged service at another file) and be real directories.
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		attrs, err := fileAttributes(p)
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("blackbox: refusing a reparse point in the settings file chain")
		}
		if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return fmt.Errorf("blackbox: the settings file chain contains a non-directory")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	attrs, err := fileAttributes(path)
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("blackbox: refusing a reparse point as the settings file")
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fmt.Errorf("blackbox: the settings file is a directory")
	}
	// The owner holds implicit WRITE_DAC no matter what the DACL says, so an
	// ordinary owner could re-grant themselves write at any time: refuse. Since
	// R1-s2 the installer OWNS this file as Administrators in the same write that
	// locked it (lockSQLInstallMarker), so the account that happened to create it
	// is no longer accepted here; the folder chain in secureSQLDir still uses
	// trustedOwner, which does, because the installer cannot re-own folders that
	// belong to the operator. The split is written down on trustedFileOwner.
	owner, err := ownerSID(path)
	if err != nil {
		return err
	}
	if !trustedFileOwner(owner) {
		return fmt.Errorf("blackbox: the settings file has an untrusted owner")
	}
	present, aces, err := describeAdminDACL(path)
	if err != nil {
		return err
	}
	return adminOnlyDACL(present, aces)
}

// describeAdminDACL reads the file's DACL and describes every ACE for the pure
// rule. A missing (NULL) DACL is reported as not present (the rule refuses it).
// A malformed or truncated entry is an error, never a skipped entry.
func describeAdminDACL(path string) (bool, []fileACE, error) {
	return describeFileDACL(path, "the settings file")
}

// describeFileDACL describes an object's access list for a pure rule, with the
// same fail-closed reads as the settings-file proof: an unreadable, absent or
// malformed ACL is an error, never a shorter list.
func describeFileDACL(path, what string) (bool, []fileACE, error) {
	list, err := describeACEs(path, what)
	if err != nil {
		return false, nil, err
	}
	aces := make([]fileACE, 0, len(list.entries))
	for _, e := range list.entries {
		aces = append(aces, e.ace)
	}
	return list.present, aces, nil
}

// describedDACL is one object's access list in every form the ACL writers need:
// the entries as the pure rules judge them, their raw bytes (so a rebuilt list
// carries the entries this code was never asked to touch, unchanged) and the
// header bytes (so a rebuilt list keeps the revision the original declared).
type describedDACL struct {
	present bool
	header  []byte
	entries []describedACE
}

// describedACE is one access entry: its description and its exact bytes.
type describedACE struct {
	ace fileACE
	raw []byte
}

// describeACEs reads path's DACL and describes every entry. Every read failure
// is returned: an entry that cannot be parsed is never dropped, and a list that
// cannot be read is never treated as an empty one (SEC-1, SEC-6). what names the
// object in an error message.
func describeACEs(path, what string) (describedDACL, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return describedDACL{}, err
	}
	defer runtime.KeepAlive(sd)
	dacl, present, err := sd.DACL()
	if err != nil {
		return describedDACL{}, err
	}
	if !present || dacl == nil {
		return describedDACL{}, nil
	}
	// The ACL header is 8 bytes: revision, a spare byte, the total size, the
	// entry count and a final spare word. x/sys keeps all but the count
	// unexported, so the header is taken straight from the bytes.
	hdr := make([]byte, 8)
	copy(hdr, unsafe.Slice((*byte)(unsafe.Pointer(dacl)), 8))
	list := describedDACL{present: true, header: hdr, entries: make([]describedACE, 0, int(dacl.AceCount))}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return describedDACL{}, err
		}
		size := int(ace.Header.AceSize)
		if size < 8 {
			return describedDACL{}, fmt.Errorf("blackbox: a truncated access entry on %s", what)
		}
		// Copy so the SID is aligned and owned; String() returns "" for an
		// invalid SID, which the rules treat as an untrusted trustee.
		raw := make([]byte, size)
		copy(raw, unsafe.Slice((*byte)(unsafe.Pointer(ace)), size))
		a, err := describeACEBytes(raw, what)
		if err != nil {
			return describedDACL{}, err
		}
		list.entries = append(list.entries, describedACE{ace: a, raw: raw})
		runtime.KeepAlive(ace)
	}
	return list, nil
}

// describeACEBytes describes one access entry from its bytes. The trustee SID
// starts at offset 8 for plain and callback ACEs; an object ACE carries a flags
// word and up to two GUIDs before the SID.
func describeACEBytes(raw []byte, what string) (fileACE, error) {
	a := fileACE{Type: raw[0], Flags: raw[1], Mask: binary.LittleEndian.Uint32(raw[4:8])}
	sidOff := 8
	switch a.Type {
	case 0x5, 0x6, 0x7, 0x8, 0xB, 0xC, 0xF, 0x10:
		if len(raw) < 12 {
			return a, fmt.Errorf("blackbox: a truncated object access entry on %s", what)
		}
		objFlags := binary.LittleEndian.Uint32(raw[8:12])
		sidOff = 12
		if objFlags&0x1 != 0 { // ACE_OBJECT_TYPE_PRESENT
			sidOff += 16
		}
		if objFlags&0x2 != 0 { // ACE_INHERITED_OBJECT_TYPE_PRESENT
			sidOff += 16
		}
	}
	if sidOff+8 > len(raw) {
		return a, fmt.Errorf("blackbox: a truncated access entry on %s", what)
	}
	sidLen := 8 + 4*int(raw[sidOff+1]) // revision + count + authority + sub-authorities
	if sidOff+sidLen > len(raw) {
		return a, fmt.Errorf("blackbox: a malformed trustee on %s", what)
	}
	a.Trustee = (*windows.SID)(unsafe.Pointer(&raw[sidOff])).String()
	return a, nil
}

// secureSQLDir applies sqlSDDL(sid) to dir as a protected DACL after the
// adoption checks, and re-secures every existing child file the same way.
func secureSQLDir(dir, sid string) error {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("blackbox: the SQL data folder path must be absolute")
	}
	self := currentTokenSID()
	// 1. The whole ancestor chain must be free of reparse points (a junction
	// could redirect the privileged ACL mutation) and have a trusted owner.
	for p := dir; ; p = filepath.Dir(p) {
		attrs, err := fileAttributes(p)
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("blackbox: refusing a reparse point in the SQL data folder chain")
		}
		if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return fmt.Errorf("blackbox: the SQL data folder chain contains a non-directory")
		}
		owner, err := ownerSID(p)
		if err != nil {
			return err
		}
		if !trustedOwner(owner, self) {
			return fmt.Errorf("blackbox: the SQL data folder chain has an untrusted owner")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	// 2. Enumerate and verify every existing child BEFORE mutating anything:
	// only regular files with trusted owners are adopted; a subfolder, a
	// reparse point or an unowned file refuses the whole operation (safe
	// rollback: nothing has been changed yet).
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var children []string
	for _, e := range entries {
		child := filepath.Join(dir, e.Name())
		attrs, err := fileAttributes(child)
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("blackbox: refusing a reparse point inside the SQL data folder")
		}
		if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return fmt.Errorf("blackbox: the SQL data folder holds an unexpected subfolder")
		}
		owner, err := ownerSID(child)
		if err != nil {
			return err
		}
		if !trustedOwner(owner, self) {
			return fmt.Errorf("blackbox: an existing SQL data file has an untrusted owner")
		}
		children = append(children, child)
	}
	// 3. Lock the folder and give it its owner in ONE write (R1-s1), then prove
	// the owner landed, then re-lock each verified child, so explicit/protected
	// child ACEs are replaced, not merely supplemented. A child keeps its own
	// owner: the sampler's rule is about the folder it is handed.
	if err := lockAndOwnSQLDir(dir, sid); err != nil {
		return err
	}
	folderOwner, err := ownerSID(dir)
	if err != nil {
		return err
	}
	if !trustedSQLFolderOwner(folderOwner, sid) {
		return fmt.Errorf("%w (the folder kept a different owner)", errSQLFolderOwner)
	}
	for _, child := range children {
		if err := setProtectedDACL(child, sid); err != nil {
			return err
		}
	}
	return nil
}

// setProtectedDACL writes sqlSDDL(sid) as the object's protected DACL.
func setProtectedDACL(path, sid string) error {
	sd, err := windows.SecurityDescriptorFromString(sqlSDDL(sid))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return setNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// Privilege names, as Windows spells them (x/sys has no constant for either).
const (
	seRestorePrivilege       = "SeRestorePrivilege"
	seTakeOwnershipPrivilege = "SeTakeOwnershipPrivilege"
)

// errPrivilegeNotHeld is the one answer this package gives for a token that was
// asked to hold a privilege it cannot. A bare windows.Errno carries no context
// once it reaches the sentence the person reads, and the identity of the code is
// what a test checks the refusal for, so the read-back reports through this
// variable.
var errPrivilegeNotHeld = windows.ERROR_NOT_ALL_ASSIGNED

// enablePrivilege turns one named privilege on for this process and returns the
// function that puts the token back the way it was.
//
// Naming an owner that is not the caller is what SeRestorePrivilege (or
// SeTakeOwnershipPrivilege) lets a process do, and an elevated install token
// carries both present-but-disabled, so they are enabled for the length of one
// descriptor write and taken back straight after it instead of being left on for
// the rest of the install.
//
// A privilege the token does not hold is a failure, never a success. Windows
// answers that case with ERROR_NOT_ALL_ASSIGNED and returns TRUE, which x/sys
// reports as no error at all - measured on this host's filtered token, where the
// call "succeeded" and the owner write afterwards failed with WIN32 1307. So the
// token is read back and the assignment is proved, not trusted: an install that
// cannot hold the privilege refuses, instead of attempting a write it has no
// right to make.
var enablePrivilege = enablePrivilegeOnToken

func enablePrivilegeOnToken(name string) (func(), error) {
	sysName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, sysName, &luid); err != nil {
		return nil, err
	}
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return nil, err
	}
	// What the token says about itself before anything changes is what it has to
	// say afterwards. PrevState cannot carry that: measured on this host, enabling
	// SeChangeNotifyPrivilege - held and switched on by default - came back with
	// PrivilegeCount 0, no entry for the privilege at all, and a restore computed
	// from that turns off a privilege the process was already using.
	wasOn, err := tokenPrivilegeEnabled(tok, luid)
	if err != nil {
		_ = tok.Close()
		return nil, err
	}
	on := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}}}
	if err := windows.AdjustTokenPrivileges(tok, false, &on, 0, nil, nil); err != nil {
		_ = tok.Close()
		return nil, err
	}
	if err := holdsPrivilege(tok, luid); err != nil {
		_ = tok.Close()
		return nil, err
	}
	back := attributesToRestore(wasOn)
	restore := func() {
		state := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: back}}}
		_ = windows.AdjustTokenPrivileges(tok, false, &state, 0, nil, nil)
		_ = tok.Close()
	}
	return restore, nil
}

// attributesToRestore is the state enablePrivilege puts the token back into: the
// enabled bit the privilege had before the write, and nothing else. One that was
// already enabled stays enabled (switching it off would take a right the process
// was using before the install), and one that was off goes off again, so the
// privilege lives only for the single write that needed it.
func attributesToRestore(wasEnabled bool) uint32 {
	if wasEnabled {
		return windows.SE_PRIVILEGE_ENABLED
	}
	return 0
}

// tokenPrivilegeEnabled reports whether this token's own privilege list has the
// named privilege switched on right now.
func tokenPrivilegeEnabled(tok windows.Token, luid windows.LUID) (bool, error) {
	var n uint32
	// Ask for the size first: the call fails with ERROR_INSUFFICIENT_BUFFER and
	// reports how large the privilege list is.
	if err := windows.GetTokenInformation(tok, windows.TokenPrivileges, nil, 0, &n); err != nil && err != windows.ERROR_INSUFFICIENT_BUFFER {
		return false, err
	}
	if n < uint32(unsafe.Sizeof(windows.Tokenprivileges{})) {
		return false, nil
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(tok, windows.TokenPrivileges, &buf[0], n, &n); err != nil {
		return false, err
	}
	ps := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	for _, p := range ps.AllPrivileges() {
		if p.Luid == luid {
			runtime.KeepAlive(buf)
			return p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0, nil
		}
	}
	runtime.KeepAlive(buf)
	return false, nil
}

// holdsPrivilege refuses unless the token's own privilege list has the named
// privilege switched on right now.
//
// Nothing here trusts a successful call: Windows answers AdjustTokenPrivileges
// with TRUE and ERROR_NOT_ALL_ASSIGNED when the token does not hold the
// privilege, and x/sys reports an error only when the call returns FALSE
// (measured on this host's token, where enabling "succeeded" and the owner write
// afterwards failed with WIN32 1307). So the assignment is proved against the
// token itself, and an install that cannot hold the privilege refuses instead of
// attempting a write it has no right to make.
func holdsPrivilege(tok windows.Token, luid windows.LUID) error {
	on, err := tokenPrivilegeEnabled(tok, luid)
	if err != nil {
		return err
	}
	if !on {
		return errPrivilegeNotHeld
	}
	return nil
}

// writeOwnerAndProtectedDACL is the one Windows call that assigns an owner and a
// protected DACL together, holding the privilege that makes naming another owner
// possible for the length of that single write and no longer. Windows refusing
// the owner (the measured WIN32 1307 of a token that cannot hold it) is the
// install's own sentence, because a folder that kept its creator as owner is the
// defect R1-s1 reported.
func writeOwnerAndProtectedDACL(path string, owner *windows.SID, dacl *windows.ACL) error {
	restore, err := enablePrivilege(seRestorePrivilege)
	if err != nil {
		restore, err = enablePrivilege(seTakeOwnershipPrivilege)
		if err != nil {
			return fmt.Errorf("%w: this process cannot enable either %s or %s (%w)", errSQLFolderOwner, seRestorePrivilege, seTakeOwnershipPrivilege, err)
		}
	}
	defer restore()
	if err := setNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil); err != nil {
		return fmt.Errorf("%w: Windows refused the owner write (%w)", errSQLFolderOwner, err)
	}
	return nil
}

// setNamedSecurityInfo is the Win32 write behind every descriptor the installer
// makes, again a variable only so a test can read the exact request.
var setNamedSecurityInfo = windows.SetNamedSecurityInfo

// lockAndOwnSQLDir writes the install DACL AND the folder's owner in a single
// descriptor write (A1, R1-s1). The owner is the one SID bbsql's sample-folder
// rule accepts and the installer must produce, named through bbsql.FolderOwnerSID
// so the rule and the install cannot drift apart. Writing the two together leaves
// no moment where the folder is locked but still owned by whoever created it, and
// no half state to undo if the second write failed.
func lockAndOwnSQLDir(path, sid string) error {
	sd, err := windows.SecurityDescriptorFromString(sqlSDDL(sid))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	owner, err := windows.StringToSid(bbsql.FolderOwnerSID())
	if err != nil {
		return err
	}
	return writeOwnerAndProtectedDACL(path, owner, dacl)
}

// grantSQLLaunchAccess applies the least-privilege launch plan (W7-01), which
// sqlLaunchGrants decides in pure Go: the installed program, the bound settings
// file and the two folders that hold them — nothing above them, and never the
// same entry twice (SEC-6).
func grantSQLLaunchAccess(exePath, configPath, sid string) (err error) {
	// SEC-1: re-prove the settings lock here, at the real ACL boundary, so the
	// grant can never widen access on a user-writable config even if a caller
	// skipped the entry proof. Unverifiable means refused.
	if verr := verifyAdminOnlyFile(configPath); verr != nil {
		return verr
	}
	grants, err := sqlLaunchGrants(exePath, configPath, sid)
	if err != nil {
		return err
	}
	// PROVE FIRST, in the W7-03 order: every target's access list is read and the
	// entries that are genuinely missing are worked out BEFORE a single write. A
	// target whose list cannot be read therefore refuses the whole grant with
	// nothing written, and a re-install whose entries are all already present
	// writes nothing at all instead of another copy of each (SEC-6).
	var todo []sqlLaunchGrant
	for _, gr := range grants {
		aces, derr := describeGrantDACL(gr.Path)
		if derr != nil {
			return derr
		}
		if aceGrantNeeded(aces, gr.ACE) {
			todo = append(todo, gr)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	sidObj, err := windows.StringToSid(sid)
	if err != nil {
		return err
	}
	// A write that fails half way is taken back, so an install that did not
	// finish never leaves its rights on some of its objects and not on others.
	// The failure itself is still what the caller sees.
	var done []sqlLaunchGrant
	defer func() {
		if err != nil && len(done) > 0 {
			_ = revokeSQLLaunchGrants(done)
		}
	}()
	for _, gr := range todo {
		if aerr := appendAllowACE(gr.Path, sidObj, gr.ACE); aerr != nil {
			err = aerr
			return err
		}
		done = append(done, gr)
	}
	return nil
}

// describeGrantDACL reads the access list an object must already hold before the
// grant rule may touch it. Everything is fail-closed: an unreadable or malformed
// list is an error, and so is a missing (NULL) one — the host install always
// writes an explicit list, and a NULL list means the program or settings are
// world-writable, which is a broken install rather than a grant target. Unknown
// never becomes "empty", because an empty list would make every grant look missing.
func describeGrantDACL(path string) ([]fileACE, error) {
	list, err := describeACEs(path, "the SQL launch grant target")
	if err != nil {
		return nil, err
	}
	if !list.present {
		return nil, fmt.Errorf("blackbox: refusing to grant on an object with no access list")
	}
	aces := make([]fileACE, 0, len(list.entries))
	for _, e := range list.entries {
		aces = append(aces, e.ace)
	}
	return aces, nil
}

// appendAllowACE adds one entry to path's existing DACL, leaving every other
// entry, the owner and the protection state untouched. The caller decides first
// whether it is needed at all (aceGrantNeeded): an object that already holds
// exactly this entry is never handed a second copy (SEC-6).
// The owner this write leaves alone is NOT left to chance anywhere: it was
// assigned by lockAndOwnSQLDir for the SQL data folder and by
// lockSQLInstallMarker for the install record (R1-s1, R1-s2), and a DACL-only
// write is exactly the shape that used to let an owner drift unnoticed.
func appendAllowACE(path string, sid *windows.SID, want fileACE) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if old == nil {
		return fmt.Errorf("blackbox: refusing to grant on an object with no access list")
	}
	sidLen := int(sid.Len())
	aceSize := 8 + sidLen // ACE header (4) + access mask (4) + SID
	// The ACL's total size lives in its header (offset 2, little-endian
	// uint16); x/sys keeps that field unexported, so read it from the bytes.
	oldSize := int(binary.LittleEndian.Uint16(unsafe.Slice((*byte)(unsafe.Pointer(old)), 4)[2:4]))
	newSize := oldSize + aceSize
	if newSize > 0xFFFF {
		return fmt.Errorf("blackbox: the access list is too large to extend")
	}
	buf := make([]byte, newSize)
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(old)), oldSize))
	off := oldSize
	buf[off] = want.Type
	buf[off+1] = want.Flags // no inheritance: the grant stays on this object only
	binary.LittleEndian.PutUint16(buf[off+2:], uint16(aceSize))
	binary.LittleEndian.PutUint32(buf[off+4:], uint32(want.Mask))
	copy(buf[off+8:], unsafe.Slice((*byte)(unsafe.Pointer(sid)), sidLen))
	acl := (*windows.ACL)(unsafe.Pointer(&buf[0]))
	// Rewrite the header size in the bytes (the struct field is unexported).
	binary.LittleEndian.PutUint16(buf[2:4], uint16(newSize))
	acl.AceCount = old.AceCount + 1
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(buf)
	return err
}

// RevokeSQLLaunchGrants takes back the launch entries install-sql added (SEC-6).
// It is handed the very entries of the install plan, so an entry this code did
// not add — an operator's grant, an inherited one, a deny, SYSTEM's or
// Administrators' full control — is never removed, and the folder's protection
// state and owner are left exactly as they are.
func (OSEnv) RevokeSQLLaunchGrants(grants []sqlLaunchGrant) error {
	return revokeSQLLaunchGrants(grants)
}

// revokeSQLLaunchGrants takes the planned entries back. Every target is read and
// its replacement list built BEFORE anything is written (the W7-03 prove-first
// order), so an object whose list cannot be read stops the whole revoke and no
// folder is stripped while another keeps its entry. What survives is written back
// byte for byte, in order, under the header the object itself declared, and only
// DACL_SECURITY_INFORMATION is ever set: the protection flag, the owner and the
// group stay exactly as they were. A write that fails is returned: what did go was
// one of this install's own read entries, so a part-finished revoke only ever
// narrows access — it never widens it and never rewrites an entry that is not
// ours — and the caller is told instead of being shown a clean removal.
func revokeSQLLaunchGrants(grants []sqlLaunchGrant) error {
	// One rewrite per object, even when two planned entries sit on the same
	// folder: the second removal is applied to the list the first one produced,
	// never to a stale copy that would write the entry back.
	type target struct {
		header  []byte
		entries []describedACE
		dirty   bool
	}
	byPath := make(map[string]*target, len(grants))
	var order []string
	for _, gr := range grants {
		t := byPath[gr.Path]
		if t == nil {
			list, err := describeACEs(gr.Path, "the SQL launch grant target")
			if err != nil {
				// The object is gone (a replaced program folder, an already
				// deleted settings file): its entries went with it, so there is
				// nothing left to revoke. Any other read failure is returned:
				// unknown is never treated as an empty access list.
				if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
					continue
				}
				return err
			}
			if !list.present {
				return fmt.Errorf("blackbox: refusing to revoke on an object with no access list")
			}
			t = &target{header: list.header, entries: list.entries}
			byPath[gr.Path] = t
			order = append(order, gr.Path)
		}
		kept, removed, err := dropGrantACEs(t.entries, gr.ACE)
		if err != nil {
			return err
		}
		if removed > 0 {
			t.entries, t.dirty = kept, true
		}
	}
	for _, path := range order {
		t := byPath[path]
		if !t.dirty {
			continue
		}
		raws := make([][]byte, len(t.entries))
		for i, e := range t.entries {
			raws[i] = e.raw
		}
		if err := writeDACL(path, t.header, raws); err != nil {
			return err
		}
	}
	return nil
}

// dropGrantACEs returns the entries of a read list that survive revoking want and
// how many went. The entries dropped here and the ones the pure rule counted must
// agree; if they ever do not, the list is refused rather than rewritten from a
// count this code cannot account for.
func dropGrantACEs(list []describedACE, want fileACE) ([]describedACE, int, error) {
	described := make([]fileACE, len(list))
	for i, e := range list {
		described[i] = e.ace
	}
	keptAces, removed := aceGrantKept(described, want)
	if removed == 0 {
		return list, 0, nil
	}
	kept := make([]describedACE, 0, len(list))
	dropped := 0
	for _, e := range list {
		if dropped < removed && aceEqual(e.ace, want) {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) != len(keptAces) {
		return nil, 0, fmt.Errorf("blackbox: the access list of the SQL launch grant target could not be rebuilt safely")
	}
	return kept, removed, nil
}

// writeDACL replaces path's access list with exactly these entries, keeping the
// header the original list declared. Only DACL_SECURITY_INFORMATION is set, so
// the protection flag, the owner and the group stay as they were.
func writeDACL(path string, header []byte, entries [][]byte) error {
	size := 8
	for _, e := range entries {
		size += len(e)
	}
	if size > 0xFFFF {
		return fmt.Errorf("blackbox: the access list is too large to rewrite")
	}
	buf := make([]byte, size)
	copy(buf, header[:8])
	binary.LittleEndian.PutUint16(buf[2:4], uint16(size))
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(entries)))
	off := 8
	for _, e := range entries {
		copy(buf[off:], e)
		off += len(e)
	}
	acl := (*windows.ACL)(unsafe.Pointer(&buf[0]))
	err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(buf)
	return err
}

// trustedOwner reports whether owner is SYSTEM, Administrators, or the
// installing administrator's own token user (a folder this admin created is
// theirs to lock). Any other owner — an ordinary user who pre-created the
// folder — is untrusted and refuses adoption (W7-03).
//
// This is the rule for objects the installer ADOPTS: the folder chain above the
// SQL data folder and the files already inside it. They belong to whoever made
// them, and an install must not re-own an operator's folders to satisfy a proof,
// so the installing token counts here. The objects the installer CREATES are held
// to the stricter trustedFileOwner (in sqlservice.go), because there the install
// chooses the owner itself and must not leave it to chance.
func trustedOwner(owner, self string) bool {
	return strings.EqualFold(owner, sidSystemSID) ||
		strings.EqualFold(owner, "S-1-5-32-544") ||
		(self != "" && strings.EqualFold(owner, self))
}

// ownerSID reads the owner SID of a file or folder.
//
// It is a variable for the same reason the two writes above are: the install's
// owner read-back can only be watched end to end, with an operator-owned chain,
// by a test that answers the read instead of the machine — this host has one
// drive, its root is owned by NT SERVICE\TrustedInstaller, and W7-03 then refuses
// every absolute path before the folder step is reached. The production value is
// the Win32 read itself.
var ownerSID = ownerSIDFromWindows

func ownerSIDFromWindows(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	sid, _, err := sd.Owner()
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

// currentTokenSID is the installing process's token user, used only to decide
// whether an existing owner is the installing administrator themselves.
func currentTokenSID() string {
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return ""
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return ""
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil || user == nil {
		return ""
	}
	return user.User.Sid.String()
}

// fileAttributes reads the Win32 file attributes (reparse/directory flags).
func fileAttributes(path string) (uint32, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return 0, err
	}
	return attrs, nil
}
