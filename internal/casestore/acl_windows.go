//go:build windows

package casestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// ErrReparsePoint means a case path component is a symlink, junction or other
// reparse point. Case folders are never followed through one (§4 rule 7).
var ErrReparsePoint = errors.New("case path contains a reparse point")

// SecureDir gives dir a protected DACL for the current user and SYSTEM only.
// Inheritance from the parent is cut, and both entries inherit downwards, so the
// key, database, WAL and manifest created inside are covered too.
func SecureDir(dir string) error {
	return secureWithDACL(dir, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

// SecureFile gives one file a protected DACL for the current user and SYSTEM
// only, with no inheritance from its folder. A later rename keeps the DACL.
func SecureFile(path string) error {
	return secureWithDACL(path, windows.NO_INHERITANCE)
}

func secureWithDACL(path string, inheritance uint32) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("reading current user: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("building SYSTEM sid: %w", err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(system),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("building case ACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}

// RejectReparse refuses root and every folder from root down to path if any of
// them is a reparse point. path must be root or lie beneath it. A component
// that does not exist yet is skipped: the caller creates it afterwards and may
// call again.
func RejectReparse(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrReparsePoint
	}
	parts := []string{root}
	if rel != "." {
		cur := root
		for _, p := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, p)
			parts = append(parts, cur)
		}
	}
	for _, p := range parts {
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return ErrReparsePoint
		}
		attrs, err := windows.GetFileAttributes(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
				continue
			}
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return ErrReparsePoint
		}
	}
	return nil
}
