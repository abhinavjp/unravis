//go:build !windows

package settingsfile

import "os"

// ownerOnly reports whether no group or other bit is set. Save writes 0600; a file anyone else can write could have been changed by
// them, so it is not trusted (code mode stays off until the owner saves again). The open file is only used on Windows.
func ownerOnly(_ *os.File, fi os.FileInfo) bool { return fi.Mode().Perm()&0o077 == 0 }
