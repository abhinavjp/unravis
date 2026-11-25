//go:build windows

package settingsfile

import "os"

// ownerOnly is always true here: Windows has no group or other bits, and the owner-only rule is the DACL that casestore.SecureFile sets
// when Save writes the file.
func ownerOnly(os.FileInfo) bool { return true }
