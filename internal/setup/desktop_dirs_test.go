package setup

import "testing"

func TestCaptureDirsKeepsDesktop(t *testing.T) {
	d := captureDirs()
	if d.Desktop != `C:\Users\ana\Desktop` {
		t.Fatalf("Desktop = %q", d.Desktop)
	}
	if err := CheckUserDirs(d.User()); err != nil {
		t.Fatal(err)
	}
}
