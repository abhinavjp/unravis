//go:build windows

package dotnetdiag

import "golang.org/x/sys/windows"

// RunAs says which kind of account this process runs as: "system" (LocalSystem),
// "elevated" (an administrator token with elevation), "user", or "unknown" when
// the token cannot be read. It is only for the probe's output.
func RunAs() string {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil || u == nil || u.User.Sid == nil {
		return "unknown"
	}
	if u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return "system"
	}
	if tok.IsElevated() {
		return "elevated"
	}
	return "user"
}
