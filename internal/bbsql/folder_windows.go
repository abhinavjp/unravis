//go:build windows

package bbsql

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ACE types (winnt.h). x/sys/windows defines only the two basic ones, so the
// set is spelled out here: what is not listed is treated as unclassifiable and
// refuses, because the check must not guess about a folder ACL it cannot read.
//
//	 0 ACCESS_ALLOWED_ACE                  grants, SID at SidStart
//	 1 ACCESS_DENIED_ACE                   denies: not a privacy leak
//	 5 ACCESS_ALLOWED_OBJECT_ACE           grants, but the SID sits behind a
//	                                       GUID, so SidStart is not a SID
//	 6 ACCESS_DENIED_OBJECT_ACE            denies, same layout
//	 8 SYSTEM_ALARM_OBJECT_ACE             audit/alarm: unsupported, refuse
//	 9 ACCESS_ALLOWED_CALLBACK_ACE         grants, SID at SidStart
//	10 ACCESS_DENIED_CALLBACK_ACE          denies
//	11 ACCESS_ALLOWED_CALLBACK_OBJECT_ACE  grants, SID not at SidStart: refuse
//	12 ACCESS_DENIED_CALLBACK_OBJECT_ACE   denies
//
// Only the four deny forms are ignored. Every allow form whose SID cannot be read
// at SidStart, and every audit/alarm form, falls through to Unknown and refuses:
// an unreadable grant must never be mistaken for a deny (W10-12).
const (
	aceAllowed         uint8 = 0
	aceDenied          uint8 = 1
	aceDeniedObject    uint8 = 6
	aceAllowedCallback uint8 = 9
	aceDeniedCallback  uint8 = 10
	aceDeniedCBObject  uint8 = 12
)

// readFolderACL describes the DACL of dir in the terms folderACL.check needs.
// Anything it cannot read comes back as an error, which the caller refuses.
func readFolderACL(dir string) (folderACL, error) {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return folderACL{}, err
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		return folderACL{}, err
	}
	// DACL's second return is DEFAULTED, not present (x/sys v0.48.0): a missing
	// DACL comes back as ERROR_OBJECT_NOT_FOUND. Reading it as "present" made
	// every explicitly locked folder look unlocked, so the service refused to
	// start on a correct install (W10-01).
	dacl, defaulted, err := sd.DACL()
	if err != nil {
		return folderACL{}, err
	}
	out := folderACL{
		Present:   true,
		Protected: ctrl&windows.SE_DACL_PROTECTED != 0,
	}
	if defaulted {
		// A defaulted DACL is not the explicit lock install-sql sets. Refuse
		// rather than trust a DACL nobody chose.
		out.Unknown = true
		return out, nil
	}
	if dacl == nil {
		// A present but NULL DACL grants access to everyone. It is reported as
		// unclassifiable so the rule refuses it: it is the least locked folder
		// there is.
		out.Unknown = true
		return out, nil
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return folderACL{}, err
		}
		if ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			out.Inherited = true
		}
		switch ace.Header.AceType {
		case aceAllowed, aceAllowedCallback:
			// The SID of an access-allowed ACE sits right after the mask.
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			out.Grantees = append(out.Grantees, sid.String())
		case aceDenied, aceDeniedObject, aceDeniedCallback, aceDeniedCBObject:
			// A deny ACE writes down who may NOT have access. It cannot make a
			// folder readable by a stranger, so it is not a grantee.
		default:
			// Object-form and unknown grant types put their SID elsewhere (or
			// nowhere): the grantee cannot be identified, so it cannot be allowed.
			out.Unknown = true
		}
	}
	return out, nil
}

// folderSelfSID is the account running this process. CheckFolderAccess uses it
// ONLY to verify the production identity: it must equal the pinned SQL service
// SID (W10-11). It is never an allowed trustee by itself — a folder locked to
// the current console account, or a check run by that account, still refuses.
// Empty means the identity cannot be proven, and then the check refuses.
func folderSelfSID() string {
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return ""
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return ""
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil || user == nil {
		return ""
	}
	return user.User.Sid.String()
}
