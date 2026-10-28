package datadir

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The shared folder is trusted only when the installer made it and nobody but the PC's administrators and services controls it.
// Machine-scope sealing (every account on the PC can unseal) makes the folder's owner and ACL the only guard of the cases inside, and by
// default any person may create a folder under C:\ProgramData and own it. Owner and ACL alone are not enough either: a person can move
// an existing folder that the system owns but that lets everyone change it (several programs leave one under C:\ProgramData) to the
// shared folder's name, then rewrite its ACL with entries the check does not count. So trust is anchored in something only an
// administrator can set: the installer's record under HKLM\SOFTWARE\Unravis (read through ReadAnchor, never written by Unravis), which
// names the folder and the file IDs (volume serial and file index) of the folder and its cases folder. A folder moved, copied or
// replaced has another path or another ID and is not used; with no record there is no shared mode. On top of that the folder and its
// cases folder must be owned by SYSTEM, Administrators or TrustedInstaller, must not be a link elsewhere, and must not let anyone else
// change their permissions, take ownership, or delete or replace what is in them; and the record itself must not be changeable by anyone
// but them.

// ErrNotShared means there is no shared folder: per-user mode, nothing to report.
var ErrNotShared = errors.New("no shared data folder")

// ErrNoAnchor means the installer's record of the shared folder is absent.
var ErrNoAnchor = errors.New("no installer record of the shared folder")

// Access bits that hand control of a folder to whoever holds them.
const (
	rightDelete      = 0x00010000
	rightWriteDAC    = 0x00040000
	rightWriteOwner  = 0x00080000
	rightGenericAll  = 0x10000000
	rightDeleteChild = 0x00000040 // FILE_DELETE_CHILD: delete (and so replace) anything inside, whatever its own ACL says
	controlRights    = rightDelete | rightWriteDAC | rightWriteOwner | rightGenericAll | rightDeleteChild

	// The same for the installer's registry record: set a value, add a subkey or a link, or write generically.
	keySetValue     = 0x0002
	keyCreateSubKey = 0x0004
	keyCreateLink   = 0x0020
	rightGenericW   = 0x40000000
	keyControl      = keySetValue | keyCreateSubKey | keyCreateLink | rightDelete | rightWriteDAC | rightWriteOwner | rightGenericAll | rightGenericW

	aceAllowed     = 0
	aceInheritOnly = 0x08
)

// FolderACE is one access-control entry as read from the folder.
type FolderACE struct {
	Type  uint8
	Flags uint8
	Mask  uint32
	SID   string
}

// FolderSecurity is what the trust check reads from a folder (or, for the anchor, from its registry key).
type FolderSecurity struct {
	// ID is the folder's file ID on its volume, "<volume serial>-<file index>" in hex: it survives a rename but not a copy, and a move
	// keeps it only together with a new path.
	ID       string
	Owner    string
	NullDACL bool
	Reparse  bool
	ACEs     []FolderACE
}

// Anchor is the installer's record of the shared folder: the folder it made, the IDs of that folder and of its cases folder, and the
// record's own owner and ACL.
type Anchor struct {
	Root     string
	RootID   string
	CasesID  string
	Security FolderSecurity
}

const trustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"

// adminOwner reports whether sid may own the shared folder.
func adminOwner(sid string) bool {
	return sid == "S-1-5-18" || sid == "S-1-5-32-544" || sid == trustedInstallerSID
}

// mayControl reports whether sid may hold control rights on the shared folder: the owners above, any service SID (S-1-5-80-..., which
// only the service control manager hands out, so the keeper qualifies), CREATOR OWNER on the folder itself (it grants nothing there)
// and OWNER RIGHTS (it applies to the owner, already checked).
func mayControl(sid string) bool {
	return adminOwner(sid) || strings.HasPrefix(sid, "S-1-5-80-") || sid == "S-1-3-0" || sid == "S-1-3-4"
}

// CheckFolder decides whether a folder may hold shared data. name says which folder for the reason.
func CheckFolder(name string, fs FolderSecurity) error {
	if fs.Reparse {
		return fmt.Errorf("%s is a link to another place", name)
	}
	return checkSecurity(name, fs, controlRights)
}

// checkSecurity refuses an owner outside the administrators and the system, a missing access list, and any allow entry that hands
// someone else one of the rights in mask.
func checkSecurity(name string, fs FolderSecurity, mask uint32) error {
	if !adminOwner(fs.Owner) {
		return fmt.Errorf("%s is owned by %s, not by the PC's administrators or the system", name, fs.Owner)
	}
	if fs.NullDACL {
		return fmt.Errorf("%s has no access list, so everyone controls it", name)
	}
	for _, a := range fs.ACEs {
		if a.Type != aceAllowed || a.Flags&aceInheritOnly != 0 || mayControl(a.SID) {
			continue
		}
		if a.Mask&mask != 0 {
			return fmt.Errorf("%s lets %s change its permissions, take it over or delete what is in it", name, a.SID)
		}
	}
	return nil
}

// samePath compares two Windows paths the way the file system does (case does not matter).
func samePath(a, b string) bool {
	return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// TrustSharedFolder checks the shared folder root and its cases folder with read, which returns ErrNotShared when the root is absent,
// against the installer's record from anchor, which returns ErrNoAnchor when there is none. The record is read only once the folder is
// known to exist, so a PC without the shared folder never looks at it. The cases folder must exist (the installer makes it): one created
// later would belong to whoever created it, and would not carry the recorded ID.
func TrustSharedFolder(root string, read func(string) (FolderSecurity, error), anchor func() (Anchor, error)) error {
	fs, err := read(root)
	if err != nil {
		if errors.Is(err, ErrNotShared) {
			return ErrNotShared
		}
		return fmt.Errorf("%s could not be checked: %v", root, err)
	}
	if anchor == nil {
		return fmt.Errorf("%s was not made by the Unravis installer: there is no installer record of it", root)
	}
	a, err := anchor()
	if err != nil {
		if errors.Is(err, ErrNoAnchor) {
			return fmt.Errorf("%s was not made by the Unravis installer: there is no installer record of it", root)
		}
		return fmt.Errorf("the installer's record of %s could not be read: %v", root, err)
	}
	if err := checkSecurity("the installer's record of "+root, a.Security, keyControl); err != nil {
		return err
	}
	if !samePath(a.Root, root) {
		return fmt.Errorf("%s is not the folder the installer made (its record names %s)", root, a.Root)
	}
	if a.RootID == "" || fs.ID != a.RootID {
		return fmt.Errorf("%s is not the folder the installer made (it was moved, copied or replaced)", root)
	}
	if err := CheckFolder(root, fs); err != nil {
		return err
	}
	cases := filepath.Join(root, "cases")
	cfs, err := read(cases)
	if err != nil {
		if errors.Is(err, ErrNotShared) {
			return fmt.Errorf("%s has no cases folder; the installer makes it", root)
		}
		return fmt.Errorf("%s could not be checked: %v", cases, err)
	}
	if a.CasesID == "" || cfs.ID != a.CasesID {
		return fmt.Errorf("%s is not the cases folder the installer made (it was moved, copied or replaced)", cases)
	}
	return CheckFolder(cases, cfs)
}
