package pipesec

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestRandomNameShape(t *testing.T) {
	re := regexp.MustCompile(`^\\\\\.\\pipe\\unravis\.(ui|ag)\.[0-9a-f]{32}$`)
	seen := map[string]bool{}
	for _, k := range []Kind{KindUI, KindAG} {
		for range 50 {
			n, err := NewName(k)
			if err != nil {
				t.Fatal(err)
			}
			if !re.MatchString(n) || !strings.Contains(n, "."+string(k)+".") {
				t.Fatalf("bad name %q", n)
			}
			if seen[n] {
				t.Fatalf("name repeated: %q", n)
			}
			seen[n] = true
		}
	}
	if _, err := NewName("mcp"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestPeerPolicyCheck(t *testing.T) {
	const dir = `C:\Users\u\AppData\Local\Unravis`
	pol := PeerPolicy{InstallDir: dir, AllowedBase: defaultAllowedBase, RequireSameLogon: true}
	me := self{Session: 1, LogonSID: "S-1-5-5-0-111"}
	good := Peer{Session: 1, LogonSID: "S-1-5-5-0-111", Image: dir + `\unravis.exe`}
	tests := []struct {
		name string
		mod  func(p *Peer)
		ok   bool
	}{
		{"genuine", func(*Peer) {}, true},
		{"case and slash", func(p *Peer) { p.Image = strings.ToUpper(dir) + `/UNRAVIS-DESKTOP.EXE` }, true},
		{"extended prefix", func(p *Peer) { p.Image = `\\?\` + dir + `\unravis-core.exe` }, true},
		{"bin dir", func(p *Peer) { p.Image = dir + `\bin\unravis.exe` }, true},
		{"other session", func(p *Peer) { p.Session = 2 }, false},
		{"other logon", func(p *Peer) { p.LogonSID = "S-1-5-5-0-222" }, false},
		{"no logon", func(p *Peer) { p.LogonSID = "" }, false},
		{"outside dir", func(p *Peer) { p.Image = `C:\Temp\unravis.exe` }, false},
		{"lookalike dir", func(p *Peer) { p.Image = dir + `2\unravis.exe` }, false},
		{"sub dir not bin", func(p *Peer) { p.Image = dir + `\x\unravis.exe` }, false},
		{"deep bin", func(p *Peer) { p.Image = dir + `\bin\bin\unravis.exe` }, false},
		{"name not allowed", func(p *Peer) { p.Image = dir + `\cmd.exe` }, false},
		{"no image", func(p *Peer) { p.Image = "" }, false},
	}
	for _, tc := range tests {
		p := good
		tc.mod(&p)
		err := pol.check(p, me)
		if tc.ok != (err == nil) {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrPeerDenied) {
			t.Errorf("%s: error does not wrap ErrPeerDenied: %v", tc.name, err)
		}
	}
	if err := (PeerPolicy{}).check(good, me); err == nil {
		t.Error("empty policy must deny")
	}
	loose := pol
	loose.RequireSameLogon = false
	other := good
	other.LogonSID = ""
	if err := loose.check(other, me); err != nil {
		t.Errorf("logon not required but denied: %v", err)
	}
}

func TestServerPolicyCheck(t *testing.T) {
	const img = `C:\Apps\Unravis\unravis.exe`
	sp := ServerPolicy{ExpectedImage: img, InstallDir: `C:\Apps\Unravis`, ExpectedSession: 3}
	if err := sp.check(Peer{Session: 3, Image: strings.ToLower(img)}); err != nil {
		t.Fatalf("genuine server refused: %v", err)
	}
	for name, p := range map[string]Peer{
		"session": {Session: 0, Image: img},
		"image":   {Session: 3, Image: `C:\Apps\Unravis\unravis-desktop.exe`},
		"empty":   {Session: 3},
	} {
		if err := sp.check(p); !errors.Is(err, ErrServerMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Expected image outside the install dir is refused even though it matches itself.
	sp2 := ServerPolicy{ExpectedImage: `C:\Temp\unravis.exe`, InstallDir: `C:\Apps\Unravis`}
	if err := sp2.check(Peer{Image: `C:\Temp\unravis.exe`}); !errors.Is(err, ErrServerMismatch) {
		t.Errorf("outside install dir: %v", err)
	}
	if err := (ServerPolicy{}).check(Peer{}); err == nil {
		t.Error("empty server policy must refuse")
	}
}

func TestDefaultPeerPolicy(t *testing.T) {
	p := DefaultPeerPolicy()
	if p.InstallDir == "" || !p.RequireSameLogon || len(p.AllowedBase) != 3 {
		t.Fatalf("unexpected default policy: %+v", p)
	}
	p.AllowedBase[0] = "changed"
	if defaultAllowedBase[0] == "changed" {
		t.Fatal("default list is shared, not copied")
	}
}

func TestWinClean(t *testing.T) {
	for in, want := range map[string]string{
		`C:\a\b\`:           `C:\a\b`,
		`C:/a//b`:           `C:\a\b`,
		`\\?\C:\a`:          `C:\a`,
		`\\server\share\x\`: `\\server\share\x`,
	} {
		if got := winClean(in); got != want {
			t.Errorf("winClean(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIsShellImage(t *testing.T) {
	for img, want := range map[string]bool{
		`C:\Program Files\Unravis\unravis-desktop.exe`: true,
		`c:\x\UNRAVIS-DESKTOP.EXE`:                     true,
		`C:\Program Files\Unravis\unravis.exe`:         false,
		`C:\x\notunravis-desktop.exe`:                  false,
		``:                                             false,
	} {
		if got := IsShellImage(img); got != want {
			t.Errorf("%q: %v, want %v", img, got, want)
		}
	}
}

func TestIsCLIImage(t *testing.T) {
	for img, want := range map[string]bool{
		`C:\Program Files\Unravis\bin\unravis.exe`:     true,
		`C:\Program Files\Unravis\UNRAVIS.EXE`:         true,
		`C:\Program Files\Unravis\unravis-desktop.exe`: false,
		`C:\Program Files\Unravis\unravis-core.exe`:    false,
		`C:\Windows\System32\cmd.exe`:                  false,
		``:                                             false,
	} {
		if got := IsCLIImage(img); got != want {
			t.Errorf("%q: %v, want %v", img, got, want)
		}
	}
}

func TestIsCLIImageIsAnExactName(t *testing.T) {
	for _, img := range []string{`C:\A\xunravis.exe`, `C:\A\unravis.exe.bak`, `C:\A\unravis.exe `, `C:\A\unravis.exe\x.exe`, `C:\A\unravis-cli.exe`, `unravis.exe.exe`} {
		if IsCLIImage(img) {
			t.Errorf("%q", img)
		}
	}
}
