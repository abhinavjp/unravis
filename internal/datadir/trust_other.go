//go:build !windows

package datadir

// ReadFolderSecurity is Windows only: elsewhere there is no shared folder.
func ReadFolderSecurity(string) (FolderSecurity, error) { return FolderSecurity{}, ErrNotShared }
