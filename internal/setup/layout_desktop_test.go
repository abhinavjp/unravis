package setup

import (
	"path/filepath"
	"testing"
)

func desktopTestDirs() Dirs {
	prof := `C:\Users\Ana`
	return Dirs{
		ProgramFiles: `C:\Program Files`, ProgramData: `C:\ProgramData`, CommonPrograms: `C:\ProgramData\Microsoft\Windows\Start Menu\Programs`,
		UserProfile: prof, LocalAppData: prof + `\AppData\Local`, AppData: prof + `\AppData\Roaming`,
		Programs: prof + `\AppData\Roaming\Microsoft\Windows\Start Menu\Programs`,
		Startup:  prof + `\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup`,
		Desktop:  prof + `\Desktop`,
	}
}

func TestNewLayoutHasDesktopAndQuitLinks(t *testing.T) {
	d := desktopTestDirs()
	for _, scope := range []Scope{ScopeUser, ScopeMachine} {
		l := NewLayout(Options{Scope: scope, Location: DefaultLocation(scope, d)}, d)
		if want := filepath.Join(d.Desktop, ShortcutName); l.DesktopLink != want {
			t.Errorf("scope %v DesktopLink = %q, want %q", scope, l.DesktopLink, want)
		}
		if filepath.Base(l.QuitLink) != QuitShortcutName || QuitShortcutName != "Quit Unravis.lnk" {
			t.Errorf("scope %v QuitLink = %q", scope, l.QuitLink)
		}
	}
}

func TestNewLayoutWithoutDesktopHasNoDesktopLink(t *testing.T) {
	d := desktopTestDirs()
	d.Desktop = ""
	l := NewLayout(Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}, d)
	if l.DesktopLink != "" {
		t.Errorf("DesktopLink = %q, want none", l.DesktopLink)
	}
	if l.QuitLink == "" {
		t.Error("the Quit link is missing")
	}
}

func TestQuitLinkSitsNextToTheStartMenuLink(t *testing.T) {
	d := desktopTestDirs()
	for _, scope := range []Scope{ScopeUser, ScopeMachine} {
		l := NewLayout(Options{Scope: scope, Location: DefaultLocation(scope, d)}, d)
		if filepath.Dir(l.QuitLink) != filepath.Dir(l.StartMenuLink) || l.QuitLink == l.StartMenuLink {
			t.Errorf("scope %v: %q is not next to %q", scope, l.QuitLink, l.StartMenuLink)
		}
	}
}

func TestUserDirsRoundTripKeepsDesktop(t *testing.T) {
	d := desktopTestDirs()
	u := d.User()
	if u.Desktop != d.Desktop {
		t.Errorf("User().Desktop = %q", u.Desktop)
	}
	back := Dirs{}.WithUser(u)
	if back.Desktop != d.Desktop {
		t.Errorf("WithUser Desktop = %q", back.Desktop)
	}
}

func TestCheckUserDirsAcceptsAnEmptyDesktop(t *testing.T) {
	d := desktopTestDirs()
	if err := CheckUserDirs(d.User()); err != nil {
		t.Fatalf("with a Desktop: %v", err)
	}
	u := d.User()
	u.Desktop = ""
	if err := CheckUserDirs(u); err != nil {
		t.Errorf("without a Desktop: %v", err)
	}
}

func TestCheckUserDirsRejectsADesktopWithTheWrongSuffix(t *testing.T) {
	d := desktopTestDirs()
	for _, bad := range []string{d.UserProfile + `\Documents`, d.UserProfile + `\Desktop\..\Documents`, `C:\Windows\Desktop`, `\server\share\Desktop`} {
		u := d.User()
		u.Desktop = bad
		if err := CheckUserDirs(u); err == nil {
			t.Errorf("Desktop %q accepted", bad)
		}
	}
}
