//go:build !windows

package blackbox

import (
	"os"
	"path/filepath"
)

// InspectFolder reports what is in dir and whether dir is a link. Other systems
// have no owner or drive-type rule here: the folder is taken as owned and fixed.
func (OSEnv) InspectFolder(dir string) (FolderInfo, error) {
	fi := FolderInfo{Fixed: true, OwnerTrusted: true}
	clean := filepath.Clean(dir)
	st, err := os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return fi, nil
		}
		return fi, err
	}
	fi.Exists = true
	fi.Reparse = st.Mode()&os.ModeSymlink != 0
	ents, err := os.ReadDir(clean)
	if err != nil {
		return fi, err
	}
	for i, e := range ents {
		if i >= 2000 {
			break
		}
		fi.Entries = append(fi.Entries, e.Name())
	}
	return fi, nil
}
