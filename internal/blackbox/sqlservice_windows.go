//go:build windows

package blackbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SecureSQLDir gives SYSTEM, Administrators and the SQL service SID full control
// of dir and removes every inherited permission: no ordinary user can read the
// SQL data. W7-03: it first PROVES the folder is safe to adopt — trusted owner,
// trusted ancestors, no reparse points anywhere in the chain, and every
// existing child a regular file with a trusted owner — and only then applies
// the protected DACL to the folder AND each existing child (replacing explicit
// child ACEs, which inheritance alone would never remove). If any check fails
// nothing is mutated, so pre-existing data keeps exactly what it had.
func (OSEnv) SecureSQLDir(dir, sid string) error { return secureSQLDir(dir, sid) }

// GrantSQLLaunchAccess gives the SQL service SID the least-privilege rights it
// needs to start (W7-01): read/execute on the installed program, read on the
// settings file, and traverse on the folders that hold them. It only APPENDS
// allow ACEs to the existing DACLs: write, owner and DACL-change rights stay
// with SYSTEM and Administrators, the ACEs are not inheritable, and nothing is
// granted to ordinary users. The service never runs as SYSTEM.
func (OSEnv) GrantSQLLaunchAccess(exePath, configPath, sid string) error {
	return grantSQLLaunchAccess(exePath, configPath, sid)
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
	// 3. Apply the protected DACL: the folder first, then each verified child,
	// so explicit/protected child ACEs are replaced, not merely supplemented.
	if err := setProtectedDACL(dir, sid); err != nil {
		return err
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
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// grantSQLLaunchAccess appends the least-privilege launch ACEs (W7-01).
func grantSQLLaunchAccess(exePath, configPath, sid string) error {
	sidObj, err := windows.StringToSid(sid)
	if err != nil {
		return err
	}
	if err := appendAllowACE(exePath, sidObj, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE); err != nil {
		return err
	}
	if err := appendAllowACE(configPath, sidObj, windows.FILE_GENERIC_READ); err != nil {
		return err
	}
	// Traversal on the program folder and on every folder leading to the
	// settings file (the service must reach both). Traverse-only, no listing,
	// no inheritance, no write.
	traverse := windows.ACCESS_MASK(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	seen := map[string]bool{}
	for _, d := range []string{filepath.Dir(exePath), filepath.Dir(configPath)} {
		for p := filepath.Clean(d); ; p = filepath.Dir(p) {
			if !seen[p] {
				seen[p] = true
				if err := appendAllowACE(p, sidObj, traverse); err != nil {
					return err
				}
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	return nil
}

// appendAllowACE adds one non-inheritable access-allowed ACE for sid to path's
// existing DACL, leaving every other ACE, the owner and the protection state
// untouched. A missing (NULL) DACL is refused rather than silently replaced:
// the host install always writes an explicit one, and a NULL DACL means the
// program/settings are world-writable — a broken install, not a grant target.
func appendAllowACE(path string, sid *windows.SID, mask windows.ACCESS_MASK) error {
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
	buf[off] = windows.ACCESS_ALLOWED_ACE_TYPE
	buf[off+1] = 0 // no inheritance: the grant stays on this object only
	binary.LittleEndian.PutUint16(buf[off+2:], uint16(aceSize))
	binary.LittleEndian.PutUint32(buf[off+4:], uint32(mask))
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

// trustedOwner reports whether owner is SYSTEM, Administrators, or the
// installing administrator's own token user (a folder this admin created is
// theirs to lock). Any other owner — an ordinary user who pre-created the
// folder — is untrusted and refuses adoption (W7-03).
func trustedOwner(owner, self string) bool {
	return strings.EqualFold(owner, "S-1-5-18") ||
		strings.EqualFold(owner, "S-1-5-32-544") ||
		(self != "" && strings.EqualFold(owner, self))
}

// ownerSID reads the owner SID of a file or folder.
func ownerSID(path string) (string, error) {
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