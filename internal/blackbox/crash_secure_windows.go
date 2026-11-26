//go:build windows

package blackbox

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// crashACEs is the permission list of the crash folder and its files, as the
// entries between "D:P" and the end: SYSTEM, and Administrators when this
// process is elevated (the service is) or the account running it when it is not
// (the console command run by an ordinary user). Nobody else: in particular no
// entry for ordinary users. inherit is true for the folder (new files inherit)
// and false for a file (explicit list, nothing inherited).
func crashACEs(elevated, inherit bool) (string, error) {
	flags := ""
	if inherit {
		flags = "OICI"
	}
	who := "(A;" + flags + ";FA;;;SY)"
	if elevated {
		return who + "(A;" + flags + ";FA;;;BA)", nil
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("reading the current account: %w", err)
	}
	return who + "(A;" + flags + ";FA;;;" + u.User.Sid.String() + ")", nil
}

// applyCrashACL sets a protected permission list (nothing inherited from above)
// on path.
func applyCrashACL(path string, elevated, inherit bool) error {
	aces, err := crashACEs(elevated, inherit)
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P" + aces)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// secureCrashDir makes the crash folder SYSTEM and administrators only. An
// elevated process uses the same handle-checked routine as the recordings
// folder (it refuses a link, checks the owner) with no read entry for users.
func secureCrashDir(dir string) error {
	if windows.GetCurrentProcessToken().IsElevated() {
		return secureDirHandle(dir, false, true)
	}
	return applyCrashACL(dir, false, true)
}

// secureCrashFile gives one crash file its own protected list, the same people
// as the folder, so it stays private if it is ever moved.
func secureCrashFile(path string) error {
	return applyCrashACL(path, windows.GetCurrentProcessToken().IsElevated(), false)
}
