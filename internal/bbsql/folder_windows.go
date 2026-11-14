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
//	 0 ACCESS_ALLOWED_ACE                 grants, SID at SidStart
//	 1 ACCESS_DENIED_ACE                  denies: not a privacy leak
//	 5 ACCESS_ALLOWED_OBJECT_ACE          grants, but the SID sits behind a
//	                                      GUID, so SidStart is not a SID
//	 6 ACCESS_DENIED_OBJECT_ACE           denies, same layout
//	 8 ACCESS_DENIED_CALLBACK_ACE         denies
//	 9 ACCESS_ALLOWED_CALLBACK_ACE        grants, SID at SidStart
//	10 ACCESS_ALLOWED_CALLBACK_OBJECT_ACE grants, SID not at SidStart
//	11 ACCESS_DENIED_CALLBACK_OBJECT_ACE  denies
const (
	aceAllowed         uint8 = 0
	aceDenied          uint8 = 1
	aceDeniedObject    uint8 = 6
	aceAllowedCallback uint8 = 9
	aceDeniedCallback  uint8 = 8
	aceDeniedCBObject  uint8 = 11
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
	dacl, present, err := sd.DACL()
	if err != nil {
		return folderACL{}, err
	}
	out := folderACL{
		Present:   present,
		Protected: ctrl&windows.SE_DACL_PROTECTED != 0,
	}
	if !present {
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

// folderSelfSID is the account running this process, which is the SQL service's
// own virtual account when Windows started it as a service. Empty means the
// check cannot name the third allowed trustee, and then it refuses.
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
