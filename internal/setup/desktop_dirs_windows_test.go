//go:build windows

package setup

import "testing"

func TestDesktopInsideProfile(t *testing.T) {
	const prof = `C:\Users\ana`
	for _, c := range []struct {
		name, desktop string
		want          bool
	}{
		{"inside", `C:\Users\ana\Desktop`, true},
		{"inside OneDrive", `C:\Users\ana\OneDrive\Desktop`, true},
		{"equal to the profile", `C:\Users\ana`, false},
		{"outside", `C:\Users\bob\Desktop`, false},
		{"share", `\\server\share\Desktop`, false},
		{"relative", `Desktop`, false},
		{"different drive", `D:\Users\ana\Desktop`, false},
		{"empty", ``, false},
	} {
		if got := desktopInsideProfile(prof, c.desktop); got != c.want {
			t.Errorf("%s: desktopInsideProfile(%q) = %v, want %v", c.name, c.desktop, got, c.want)
		}
	}
}

func TestOSDirsDesktopIsEmptyOrInsideTheProfile(t *testing.T) {
	d, err := OSDirs()
	if err != nil {
		t.Fatal(err)
	}
	if d.Desktop != "" && !desktopInsideProfile(d.UserProfile, d.Desktop) {
		t.Fatalf("Desktop %q is not inside the profile %q", d.Desktop, d.UserProfile)
	}
}
