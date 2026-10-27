//go:build windows

package blackbox

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
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

type fakeRecovery struct {
	actions  []mgr.RecoveryAction
	reset    uint32
	nonCrash bool
	setErr   error
}

func (f *fakeRecovery) SetRecoveryActions(a []mgr.RecoveryAction, reset uint32) error {
	f.actions, f.reset = a, reset
	return f.setErr
}
func (f *fakeRecovery) SetRecoveryActionsOnNonCrashFailures(b bool) error {
	f.nonCrash = b
	return nil
}

// Review F6.4: a clean exit with an error (exit code 2 when start-up fails) is
// restarted only when the non-crash flag is set.
func TestServiceRecoveryAlsoRestartsAfterNonCrashFailures(t *testing.T) {
	f := &fakeRecovery{}
	if err := setRecovery(f); err != nil {
		t.Fatal(err)
	}
	if !f.nonCrash {
		t.Fatal("restart after an error exit is not switched on")
	}
	if len(f.actions) != 3 || f.reset != 86400 {
		t.Fatalf("%+v %d", f.actions, f.reset)
	}
	for _, a := range f.actions {
		if a.Type != mgr.ServiceRestart {
			t.Fatalf("%+v", a)
		}
	}
	f2 := &fakeRecovery{setErr: errors.New("denied")}
	if err := setRecovery(f2); err == nil || f2.nonCrash {
		t.Fatal("flag set although the actions were refused")
	}
}
