//go:build windows

package datadir

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ReadFolderSecurity reads a folder's owner, access list and link flag from Windows. It only reads; it never changes a permission.
func ReadFolderSecurity(path string) (FolderSecurity, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FolderSecurity{}, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return FolderSecurity{}, ErrNotShared
		}
		return FolderSecurity{}, err
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return FolderSecurity{}, errors.New("it is not a folder")
	}
	fs := FolderSecurity{Reparse: attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fs, err
	}
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
