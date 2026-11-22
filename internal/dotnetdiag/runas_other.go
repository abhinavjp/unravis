//go:build !windows

package dotnetdiag

import "os"

// RunAs says which kind of account this process runs as: "elevated" for root,
// "user" for any other user, "unknown" where the platform has no user id.
func RunAs() string {
	switch uid := os.Geteuid(); {
	case uid == 0:
		return "elevated"
	case uid > 0:
		return "user"
	}
	return "unknown"
}
