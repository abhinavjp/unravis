//go:build !windows && !plan9 && !js

package localci

import "syscall"

// freeDiskBytes is the space the current user can still use on the volume that holds dir.
func freeDiskBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:gosec,unconvert // a disk size fits in int64; the field types differ per OS
}
