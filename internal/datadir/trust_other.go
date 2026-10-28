//go:build !windows

package datadir

// ReadFolderSecurity is Windows only: elsewhere there is no shared folder.
func ReadFolderSecurity(string) (FolderSecurity, error) { return FolderSecurity{}, ErrNotShared }

// ReadAnchor is Windows only: elsewhere there is no installer record.
func ReadAnchor() (Anchor, error) { return Anchor{}, ErrNoAnchor }
