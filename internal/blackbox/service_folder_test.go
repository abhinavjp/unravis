package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewFolderProblemByText(t *testing.T) {
	bad := map[string]string{
		`\\files01\share\bb`:          "network share",
		`D:\PROGRA~1\bb`:              "8.3",
		`C:\Users\me\bb`:              "profile",
		`C:\Documents and Settings\x`: "profile",
		`C:\inetpub\wwwroot\bb`:       "web server",
		`C:\Windows\Temp\bb`:          "Windows or program",
		`C:\Program Files\bb`:         "Windows or program",
		`c:\PROGRAM FILES (x86)\bb`:   "Windows or program",
		// SEC-FIX-11: Windows drops trailing dots and spaces from every name.
		`C:\Users.\me\bb`:         "profile",
		`C:\Windows .\Temp\bb`:    "Windows or program",
		`C:\Program Files...\bb`:  "Windows or program",
		`D:\data\PROGRA~1.\bb`:    "8.3",
		`D:\data\PROGRA~1.exe\bb`: "8.3",
	}
	for folder, want := range bad {
		if got := newFolderProblem(folder); !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want it to mention %q", folder, got, want)
		}
	}
	for _, ok := range []string{`D:\UnravisBlackbox`, `C:\UnravisBlackbox`, `E:\data\bb`, `C:\ProgramData\Unravis\bb`, `D:\logs~2024\bb`, `D:\data.\bb`} {
		if got := newFolderProblem(ok); got != "" {
			t.Errorf("%s refused: %q", ok, got)
		}
	}
}

func TestFolderVerdict(t *testing.T) {
	const f = `D:\UnravisBlackbox`
	cases := []struct {
		name string
		fi   FolderInfo
		bad  string // "" means allowed
	}{
		{"new folder", FolderInfo{Fixed: true, OwnerTrusted: true}, ""},
		{"empty folder", FolderInfo{Exists: true, Fixed: true, OwnerTrusted: true}, ""},
		{"empty folder of another owner", FolderInfo{Exists: true, Fixed: true}, "belongs to another account"},
		{"folder that does not exist yet", FolderInfo{Fixed: true}, ""},
		{"link above it", FolderInfo{Exists: true, Fixed: true, Reparse: true, OwnerTrusted: true}, "link to another place"},
		{"removable or network drive", FolderInfo{Exists: true, OwnerTrusted: true}, "not on a local hard drive"},
		{"somebody's files", FolderInfo{Exists: true, Fixed: true, OwnerTrusted: true, Entries: []string{"notes.docx"}}, "not Blackbox recordings"},
		{"our files, trusted owner", FolderInfo{Exists: true, Fixed: true, OwnerTrusted: true, Entries: []string{ManifestName, FileNameFor(t0)}}, ""},
		{"our files, other owner", FolderInfo{Exists: true, Fixed: true, Entries: []string{ManifestName}}, "belongs to another account"},
	}
	for _, c := range cases {
		got := folderVerdict(f, c.fi)
		if c.bad == "" && got != "" || c.bad != "" && !strings.Contains(got, c.bad) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.bad)
		}
		if got != "" && !strings.Contains(got, "Choose") {
			t.Errorf("%s: no next step in %q", c.name, got)
		}
	}
}

// guardEnv is a fake machine that also judges folders.
type guardEnv struct {
	*fakeEnv
	problem string
	asked   []string
}

func (e *guardEnv) FolderProblem(folder string) string {
	e.asked = append(e.asked, folder)
	return e.problem
}

func TestInstallRefusesAnUnsafeFolderBeforeChangingAnything(t *testing.T) {
	g := newSvcRig(t)
	ge := &guardEnv{fakeEnv: g.env, problem: g.cfg.Folder + " is a network share, so nothing was changed. Choose a new, empty folder on a local drive."}
	g.m.Env = ge
	_, err := g.m.Install(g.cfg)
	if err == nil {
		t.Fatal("install should fail")
	}
	if r := installReason(t, err); r != ReasonFolderUnsafe {
		t.Fatalf("reason %s: %v", r, err)
	}
	if !strings.Contains(err.Error(), "Choose") || len(ge.asked) != 1 {
		t.Fatalf("message %q asked %v", err.Error(), ge.asked)
	}
	g.nothingLeft(t)
	if len(g.env.secured) != 0 {
		t.Fatalf("a folder was secured: %v", g.env.secured)
	}
}

func TestApplyRefusesAnUnsafeNewFolderAndKeepsTheOldOne(t *testing.T) {
	g := installedRig(t)
	ge := &guardEnv{fakeEnv: g.env, problem: "that folder is a link. Choose a new, empty folder on a local drive."}
	g.m.Env = ge
	cfgB := g.cfg
	cfgB.Folder = filepath.Join(g.root, "other", "bb")
	stops := g.svc.did("stop")
	_, err := g.m.Apply(applySource(t, cfgB))
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Reason != ReasonFolderUnsafe {
		t.Fatalf("%v", err)
	}
	if g.svc.did("stop") != stops || g.svc.state != SvcRunning {
		t.Fatal("the running recorder was touched")
	}
	got, err := LoadConfig(g.p.ConfigPath)
	if err != nil || got.Folder != g.cfg.Folder {
		t.Fatalf("installed folder changed: %q %v", got.Folder, err)
	}
	if _, serr := os.Stat(cfgB.Folder); serr == nil {
		t.Fatal("the unsafe folder was created")
	}
	// An unchanged folder is not judged again: it is already in use.
	ge.asked = nil
	cfgC := g.cfg
	cfgC.KeepDays = g.cfg.KeepDays + 1
	if _, err := g.m.Apply(applySource(t, cfgC)); err != nil {
		t.Fatal(err)
	}
	if len(ge.asked) != 0 {
		t.Fatalf("the unchanged folder was judged: %v", ge.asked)
	}
}

func TestInspectFolderOnARealFolderAndAJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("owner and junction checks are Windows rules")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "bb")
	var env OSEnv
	fi, err := env.InspectFolder(dir)
	if err != nil || fi.Exists || fi.Reparse {
		t.Fatalf("missing folder: %+v %v", fi, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err = env.InspectFolder(dir)
	if err != nil || !fi.Exists || fi.Reparse || len(fi.Entries) != 1 || fi.Entries[0] != "a.txt" {
		t.Fatalf("real folder: %+v %v", fi, err)
	}
	link := filepath.Join(root, "link")
	if err := makeJunction(link, dir); err != nil {
		t.Skipf("cannot make a junction here: %v", err)
	}
	for _, p := range []string{link, filepath.Join(link, "inner")} {
		fi, err = env.InspectFolder(p)
		if err != nil || !fi.Reparse {
			t.Fatalf("%s: junction not seen: %+v %v", p, fi, err)
		}
	}
	if why := folderVerdict(link, fi); !strings.Contains(why, "link") {
		t.Fatalf("verdict %q", why)
	}
}
