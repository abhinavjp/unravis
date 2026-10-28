//go:build !windows

package server

import (
	"path/filepath"
	"syscall"
)

// finalFolder resolves symlinks.
func finalFolder(path string) (string, error) { return filepath.EvalSymlinks(path) }

// driveIsRemote has no drive types off Windows: nothing is refused as remote.
func driveIsRemote(string) (bool, error) { return false, nil }

// archiveFreeBytes is the space available to the current user on the volume that
// holds path.
func archiveFreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
