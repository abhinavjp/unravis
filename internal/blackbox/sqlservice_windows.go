//go:build windows

package blackbox

import "golang.org/x/sys/windows"

// SecureSQLDir gives SYSTEM, Administrators and the SQL service SID full control
// of dir and removes every inherited permission: no ordinary user can read the
// SQL data. What is already inside gets the same rights.
func (OSEnv) SecureSQLDir(dir, sid string) error { return secureSQLDir(dir, sid) }

// secureSQLDir applies sqlSDDL(sid) to dir as a protected DACL, the way SecureDir
// applies secureSDDL. It never grants BUILTIN\Users or Everyone anything.
func secureSQLDir(dir, sid string) error {
	sd, err := windows.SecurityDescriptorFromString(sqlSDDL(sid))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}
