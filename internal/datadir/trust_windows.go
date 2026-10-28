//go:build windows

package datadir

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ReadFolderSecurity reads a folder's file ID, owner, access list and link flag from Windows, all from one handle so they describe the
// same folder. It only reads; it never changes a permission.
func ReadFolderSecurity(path string) (FolderSecurity, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FolderSecurity{}, err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return FolderSecurity{}, ErrNotShared
		}
		return FolderSecurity{}, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return FolderSecurity{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return FolderSecurity{}, errors.New("it is not a folder")
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return FolderSecurity{}, err
	}
	fs, err := securityOf(sd)
	fs.ID = fmt.Sprintf("%08x-%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	fs.Reparse = info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	return fs, err
}

// securityOf turns a security descriptor into the owner and access entries the trust check looks at.
func securityOf(sd *windows.SECURITY_DESCRIPTOR) (FolderSecurity, error) {
	var fs FolderSecurity
	if owner, _, err := sd.Owner(); err == nil && owner != nil {
		fs.Owner = owner.String()
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		// No DACL at all (or one Windows will not hand over): treat as open to everyone.
		fs.NullDACL = true
		return fs, nil
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fs, err
		}
		entry := FolderACE{Type: ace.Header.AceType, Flags: ace.Header.AceFlags, Mask: uint32(ace.Mask)}
		switch entry.Type {
		case aceAllowed, 1: // allowed, denied: the SID follows the mask
			entry.SID = (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		case 5, 9, 11: // object, callback and callback-object allows: kept with no SID, so they never count as a trusted account
			entry.Type = aceAllowed
		default: // denies of other shapes take nothing away from the check
			continue
		}
		fs.ACEs = append(fs.ACEs, entry)
	}
	return fs, nil
}

// AnchorKey is the installer's registry record of the shared folder, under HKEY_LOCAL_MACHINE. The installer (P11) writes it; Unravis
// and the key keeper only read it.
const AnchorKey = `SOFTWARE\Unravis`

// Value names in AnchorKey.
const (
	AnchorRootValue    = "SharedRoot"
	AnchorRootIDValue  = "SharedRootID"
	AnchorCasesIDValue = "SharedCasesID"
)

// ReadAnchor reads the installer's record of the shared folder and the record's own owner and access list. It opens the key for reading
// only; it never creates or changes anything in the registry.
func ReadAnchor() (Anchor, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, AnchorKey, registry.QUERY_VALUE|windows.READ_CONTROL|registry.WOW64_64KEY)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return Anchor{}, ErrNoAnchor
		}
		return Anchor{}, err
	}
	defer k.Close()
	var a Anchor
	for _, v := range []struct {
		name string
		into *string
	}{{AnchorRootValue, &a.Root}, {AnchorRootIDValue, &a.RootID}, {AnchorCasesIDValue, &a.CasesID}} {
		s, typ, err := k.GetStringValue(v.name)
		if err != nil {
			if errors.Is(err, registry.ErrNotExist) {
				return Anchor{}, ErrNoAnchor
			}
			return Anchor{}, err
		}
		if typ != registry.SZ {
			return Anchor{}, fmt.Errorf("%s is not a plain text value", v.name)
		}
		*v.into = s
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(k), windows.SE_REGISTRY_KEY, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return Anchor{}, err
	}
	if a.Security, err = securityOf(sd); err != nil {
		return Anchor{}, err
	}
	return a, nil
}
