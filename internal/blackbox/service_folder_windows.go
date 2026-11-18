//go:build windows

package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// maxFolderEntries bounds how many names InspectFolder reads.
const maxFolderEntries = 2000

// trustedInstallerSID is NT SERVICE\TrustedInstaller, the owner of Windows' own
// folders. Nothing of ours is expected to be owned by it; it is trusted all the same.
const trustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"

// InspectFolder reports who owns dir, whether it or a folder above it is a
// link, whether it is on a local hard drive, and what is inside it. A folder
// that does not exist yet is reported as not existing, with the links above it
// still checked.
func (OSEnv) InspectFolder(dir string) (FolderInfo, error) {
	var fi FolderInfo
	clean := filepath.Clean(dir)
	vol := filepath.VolumeName(clean)
	if vol == "" {
		return fi, errors.New("not a drive path")
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return fi, err
	}
	fi.Fixed = windows.GetDriveType(root) == windows.DRIVE_FIXED

	// Every existing folder from the drive root down to dir.
	rest := strings.Trim(strings.TrimPrefix(clean, vol), `\`)
	cur := vol + `\`
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, `\`)
	}
	for _, part := range parts {
		cur = filepath.Join(cur, part)
		attrs, aerr := windows.GetFileAttributes(mustUTF16(cur))
		if aerr != nil {
			if errors.Is(aerr, windows.ERROR_FILE_NOT_FOUND) || errors.Is(aerr, windows.ERROR_PATH_NOT_FOUND) {
				break
			}
			return fi, aerr
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			fi.Reparse = true
		}
		if samePath(cur, clean) {
			fi.Exists = true
		}
	}
	if !fi.Exists {
		return fi, nil
	}
	owner, err := folderOwnerSID(clean)
	if err != nil {
		return fi, err
	}
	fi.OwnerTrusted = trustedFolderOwner(owner)
	ents, err := os.ReadDir(clean)
	if err != nil {
		return fi, err
	}
	for i, e := range ents {
		if i >= maxFolderEntries {
			break
		}
		fi.Entries = append(fi.Entries, e.Name())
	}
	return fi, nil
}

func mustUTF16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("")
	}
	return p
}

func folderOwnerSID(dir string) (*windows.SID, error) {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return nil, err
	}
	return owner, nil
}

// trustedOwner says whether sid is Administrators, SYSTEM or TrustedInstaller.
func trustedFolderOwner(sid *windows.SID) bool {
	if sid == nil {
		return false
	}
	if sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.IsWellKnown(windows.WinLocalSystemSid) {
		return true
	}
	ti, err := windows.StringToSid(trustedInstallerSID)
	return err == nil && sid.Equals(ti)
}
