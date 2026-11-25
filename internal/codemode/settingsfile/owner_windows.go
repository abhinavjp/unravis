//go:build windows

package settingsfile

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownerOnly reads the access list back from the open handle (what casestore.SecureFile set when Save wrote the file) and says whether
// it grants anything to someone other than the owner's account, SYSTEM and the Administrators group. A file whose list was widened, or
// that has no list at all (everyone), is not trusted: code mode stays off until the owner saves again. Deny entries only take access
// away, so they are not counted. Any problem reading the list means not trusted.
func ownerOnly(f *os.File, _ os.FileInfo) bool {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil { // a missing list gives everyone access
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return false
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
				continue
			}
			return false // an entry kind this check does not understand is not trusted
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(admins) {
			return false
		}
	}
	return true
}
