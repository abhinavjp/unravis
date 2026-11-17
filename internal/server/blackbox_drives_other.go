//go:build !windows

package server

// listDrives is empty off Windows: there are no Windows drive letters to
// report, and no local paths are exposed instead.
func listDrives() []bbDriveInfo { return []bbDriveInfo{} }
