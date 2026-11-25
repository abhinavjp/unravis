//go:build windows

package settingsfile

import "os"

// ownerOnly is always true here: Windows has no group or other bits, and Load does not read the DACL back. The owner-only rule on Windows is
// only what casestore.SecureFile sets when Save writes the file; a file someone else widened is not detected (BL-574 follow-up).
func ownerOnly(os.FileInfo) bool { return true }
