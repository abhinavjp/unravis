//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openDirNoFollow opens dir as a directory without following a link, and with
// read and write sharing but not delete, so the folder cannot be renamed or
// removed while the handle is open. A reparse point (junction, symbolic link,
// mounted volume) or an item that is not a folder is refused.
func openDirNoFollow(dir string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		windows.CloseHandle(h)
		return 0, fmt.Errorf("%w: it is a link or not a folder", ErrFolderChanged)
	}
	return h, nil
}

// finalPath is the real path of an open handle, without the \\?\ prefix.
func finalPath(h windows.Handle) (string, error) {
	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n+1)
		if n, err = windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0); err != nil {
			return "", err
		}
	}
	p := windows.UTF16ToString(buf[:n])
	return strings.TrimPrefix(p, `\\?\`), nil
}

// enableOwnerPrivilege turns on the privilege that lets an administrator give a
// folder to the Administrators group. It is best effort: without it the open
// below simply does not ask for the right to change the owner.
func enableOwnerPrivilege() {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return
	}
	defer tok.Close()
	name, _ := windows.UTF16PtrFromString("SeTakeOwnershipPrivilege")
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	_ = windows.AdjustTokenPrivileges(tok, false, &tp, uint32(unsafe.Sizeof(tp)), nil, nil)
}

// secureDirHandle is SecureDir. It opens dir once, without following a link,
// checks that it is the folder the caller named and that it belongs to
// Administrators, SYSTEM, TrustedInstaller or the account running this
// program, and applies the owner and the protected permission list through that
// same handle, so a folder swapped for a link between the check and the change
// is never the target (SEC-FIX-05, SEC-FIX-06). setOwner false leaves the owner
// alone (used by tests that run without administrator rights).
func secureDirHandle(dir string, usersRead, setOwner bool) error {
	sd, err := windows.SecurityDescriptorFromString(secureSDDL(usersRead))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	var admins *windows.SID
	access := uint32(windows.READ_CONTROL | windows.WRITE_DAC)
	if setOwner {
		if admins, err = windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err != nil {
			return err
		}
		enableOwnerPrivilege()
	}
	h, err := openDirNoFollow(dir, access|windows.WRITE_OWNER)
	if err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The owner needs no extra right to change the list; only a change of
		// owner does, and that is needed only for a folder this program made.
		h, err = openDirNoFollow(dir, access)
		setOwner = false
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	if got, gerr := finalPath(h); gerr != nil || !samePath(got, dir) {
		return fmt.Errorf("%w: it is not at the place it was checked", ErrFolderChanged)
	}
	cur, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := cur.Owner()
	if err != nil {
		return err
	}
	trusted := trustedFolderOwner(owner)
	if !trusted && !ownedByThisAccount(owner) {
		return fmt.Errorf("%w: it belongs to another account", ErrFolderChanged)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var newOwner *windows.SID
	if setOwner && !trusted {
		info |= windows.OWNER_SECURITY_INFORMATION
		newOwner = admins
	} else if !trusted {
		// A folder of this account that cannot be handed to Administrators
		// stays its account's: refuse rather than leave it open to a handle.
		if admins != nil {
			return fmt.Errorf("%w: it could not be given to the Administrators group", ErrFolderChanged)
		}
	}
	err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, info, newOwner, nil, dacl, nil)
	runtime.KeepAlive(sd)
	return err
}

// ownedByThisAccount says whether sid is the account this program runs as.
func ownedByThisAccount(sid *windows.SID) bool {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	return err == nil && sid != nil && sid.Equals(u.User.Sid)
}

// CreateSecureDir makes dir (one level; the parent must exist) with its owner and
// protected permission list set in the create call itself, so there is no moment
// when the new folder has inherited rights an ordinary user could use
// (SEC-FIX-06). It fails when the name already exists.
func (OSEnv) CreateSecureDir(dir string, usersRead bool) error {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	var last error
	for _, text := range []string{"O:BA" + secureSDDL(usersRead), secureSDDL(usersRead)} {
		sd, err := windows.SecurityDescriptorFromString(text)
		if err != nil {
			return err
		}
		sa := windows.SecurityAttributes{SecurityDescriptor: sd}
		sa.Length = uint32(unsafe.Sizeof(sa))
		err = windows.CreateDirectory(p, &sa)
		runtime.KeepAlive(sd)
		if err == nil {
			return nil
		}
		last = err
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return err
		}
		// Otherwise try again without the owner: the owner is then set through
		// the folder's handle by SecureDir.
	}
	return last
}
