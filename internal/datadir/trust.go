package datadir

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The shared folder is trusted only when nobody but the PC's administrators and services controls it. Machine-scope sealing (every
// account on the PC can unseal) makes the folder's owner and ACL the only guard of the cases inside, and by default any person may
// create a folder under C:\ProgramData and own it. So existence alone never turns shared mode on: the folder and its cases folder must
// be owned by SYSTEM, Administrators or TrustedInstaller, must not be a link elsewhere, and must not let anyone else change their
// permissions, take ownership, or delete or replace what is in them.

// ErrNotShared means there is no shared folder: per-user mode, nothing to report.
var ErrNotShared = errors.New("no shared data folder")

// Access bits that hand control of a folder to whoever holds them.
const (
	rightDelete      = 0x00010000
	rightWriteDAC    = 0x00040000
	rightWriteOwner  = 0x00080000
	rightGenericAll  = 0x10000000
	rightDeleteChild = 0x00000040 // FILE_DELETE_CHILD: delete (and so replace) anything inside, whatever its own ACL says
	controlRights    = rightDelete | rightWriteDAC | rightWriteOwner | rightGenericAll | rightDeleteChild

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

// FolderSecurity is what the trust check reads from a folder.
type FolderSecurity struct {
	Owner    string
	NullDACL bool
	Reparse  bool
	ACEs     []FolderACE
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
		if a.Mask&controlRights != 0 {
			return fmt.Errorf("%s lets %s change its permissions, take it over or delete what is in it", name, a.SID)
		}
	}
	return nil
}

// TrustSharedFolder checks the shared folder root and its cases folder with read, which returns ErrNotShared when the root is absent.
// The cases folder must exist (the installer makes it): one created later would belong to whoever created it.
func TrustSharedFolder(root string, read func(string) (FolderSecurity, error)) error {
	fs, err := read(root)
	if err != nil {
		if errors.Is(err, ErrNotShared) {
			return ErrNotShared
		}
		return fmt.Errorf("%s could not be checked: %v", root, err)
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
	return CheckFolder(cases, cfs)
}
