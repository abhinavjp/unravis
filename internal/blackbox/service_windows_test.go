//go:build windows

package blackbox

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The real SecureDir changes permissions and is never run by tests; this checks
// that the permission text it sets is valid and has no write access for users.
func TestSecureDirPermissionTextIsValidAndGivesUsersNoWrite(t *testing.T) {
	for _, usersRead := range []bool{false, true} {
		text := secureSDDL(usersRead)
		sd, err := windows.SecurityDescriptorFromString(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if _, _, err := sd.DACL(); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if !strings.HasPrefix(text, "D:P") {
			t.Fatalf("%q must be protected from inherited permissions", text)
		}
		if strings.Contains(text, ";FA;;;BU") || strings.Contains(text, ";FA;;;AU") || strings.Contains(text, "WD)") {
			t.Fatalf("%q gives ordinary users write access", text)
		}
		if usersRead != strings.Contains(text, ";;;BU)") {
			t.Fatalf("%q", text)
		}
	}
}
