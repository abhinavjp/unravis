package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArchiveSlugVectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Q3 Slow API!", "q3-slow-api"},
		{"  ", "case"},
		{"", "case"},
		{"CON", "case-con"},
		{"com1", "case-com1"},
		{"LPT9", "case-lpt9"},
		{"Ünïcode éé", "n-code"},
		{strings.Repeat("a", 100), strings.Repeat("a", 40)},
		{"---x---", "x"},
		{"a  b__c", "a-b-c"},
		{"!!!", "case"},
		// The cut at 40 never leaves a trailing dash.
		{strings.Repeat("a", 39) + " tail", strings.Repeat("a", 39)},
		{"console", "console"},
	}
	for _, c := range cases {
		if got := archiveSlug(c.in); got != c.want {
			t.Errorf("archiveSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestArchiveFileNameCollisions(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 5, 7, 0, time.UTC)
	got := archiveFileName("Q3 Slow API!", "ABCD2345", at, nil)
	if got != "q3-slow-api-abcd2345-20261028-090507.unravis-case" {
		t.Fatalf("name = %q", got)
	}
	used := map[string]bool{got: true}
	second := archiveFileName("Q3 Slow API!", "ABCD2345", at, func(n string) bool { return used[n] })
	if second != "q3-slow-api-abcd2345-20261028-090507-2.unravis-case" {
		t.Fatalf("second = %q", second)
	}
	used[second] = true
	third := archiveFileName("Q3 Slow API!", "ABCD2345", at, func(n string) bool { return used[n] })
	if third != "q3-slow-api-abcd2345-20261028-090507-3.unravis-case" {
		t.Fatalf("third = %q", third)
	}
}

func TestArchiveShapeRefusals(t *testing.T) {
	refused := []string{
		`\\srv\share\x`, `\\?\UNC\srv\s`, `x\y`, `C:\a\..\b`, `C:\a\.\b`, `C:`, `C:rel`, ``, `\x`,
		`C:\a\\b`, "C:\\a\x00b", `C:\a:stream`, `C:\a?`, `\\.\C:\x`, `C:\a.`, `C:\a `,
		`/etc`, `..\x`, `\\?\C:`, `\\?\UNC\C:\x`,
	}
	for _, p := range refused {
		if _, err := checkArchiveShape(p); !errors.Is(err, errArchiveFolder) {
			t.Errorf("checkArchiveShape(%q) = %v, want a refusal", p, err)
		}
	}
	accepted := map[string]string{
		`\\?\C:\Cases`: `C:\Cases`,
		`c:\Cases\`:    `C:\Cases`,
		`D:/x/y`:       `D:\x\y`,
		`C:\`:          `C:\`,
	}
	for in, want := range accepted {
		got, err := checkArchiveShape(in)
		if err != nil || got != want {
			t.Errorf("checkArchiveShape(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func testEnv(remote func(string) (bool, error)) archiveEnv {
	e := realArchiveEnv()
	if remote != nil {
		e.remote = remote
	}
	return e
}

func needWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("path rules are Windows rules")
	}
}

func TestArchiveFolderAccepts(t *testing.T) {
	needWindows(t)
	dir := t.TempDir()
	data := t.TempDir()
	got, err := testEnv(nil).resolveArchiveFolder(dir, data)
	if err != nil {
		t.Fatalf("a plain folder was refused: %v", err)
	}
	if !strings.EqualFold(got, dir) && !strings.EqualFold(got, mustEval(t, dir)) {
		t.Fatalf("resolved = %q, want %q", got, dir)
	}
	// The prefix form is accepted and stripped.
	got2, err := testEnv(nil).resolveArchiveFolder(`\\?\`+dir, data)
	if err != nil || strings.HasPrefix(got2, `\\`) {
		t.Fatalf("prefixed folder = %q, %v", got2, err)
	}
	// The probe file is gone.
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("the writable probe left %d files", len(ents))
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestArchiveFolderRefusals(t *testing.T) {
	needWindows(t)
	dir := t.TempDir()
	data := t.TempDir()
	env := testEnv(nil)

	missing := filepath.Join(dir, "nope")
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(data, "sub")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"missing":         missing,
		"a file":          file,
		"the data folder": data,
		"inside data":     inside,
		"dotdot":          dir + `\..\` + filepath.Base(dir),
		"relative":        "cases",
		"unc":             `\\srv\share\x`,
	} {
		if _, err := env.resolveArchiveFolder(p, data); !errors.Is(err, errArchiveFolder) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}

	// A network drive.
	remote := testEnv(func(string) (bool, error) { return true, nil })
	if _, err := remote.resolveArchiveFolder(dir, data); !errors.Is(err, errArchiveFolder) {
		t.Errorf("remote: err = %v", err)
	}
	// A drive type that cannot be read is refused too.
	unknown := testEnv(func(string) (bool, error) { return false, errDriveUnknown })
	if _, err := unknown.resolveArchiveFolder(dir, data); !errors.Is(err, errArchiveFolder) {
		t.Errorf("unknown drive: err = %v", err)
	}
	// A folder that cannot be written to.
	ro := env
	ro.writable = func(string) error { return os.ErrPermission }
	if _, err := ro.resolveArchiveFolder(dir, data); !errors.Is(err, errArchiveFolder) {
		t.Errorf("not writable: err = %v", err)
	}
}

func TestArchiveFolderJunctionIntoDataFolder(t *testing.T) {
	needWindows(t)
	data := t.TempDir()
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, data).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	defer os.Remove(link)
	if _, err := testEnv(nil).resolveArchiveFolder(link, data); !errors.Is(err, errArchiveFolder) {
		t.Fatalf("a junction into the data folder: err = %v, want a refusal", err)
	}
	// A junction to a folder outside the data folder is followed and accepted.
	other := t.TempDir()
	link2 := filepath.Join(dir, "link2")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link2, other).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	defer os.Remove(link2)
	got, err := testEnv(nil).resolveArchiveFolder(link2, data)
	if err != nil {
		t.Fatalf("a junction to another folder: %v", err)
	}
	if !strings.EqualFold(got, mustEval(t, other)) {
		t.Fatalf("resolved = %q, want %q", got, mustEval(t, other))
	}
}

func TestArchiveFolderShortNameOfDataFolder(t *testing.T) {
	needWindows(t)
	root := t.TempDir()
	data := filepath.Join(root, "Unravis Data Folder With A Long Name")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	short := shortPathOf(t, data)
	if short == "" || strings.EqualFold(short, data) {
		t.Skip("8.3 short names are off on this volume")
	}
	if _, err := testEnv(nil).resolveArchiveFolder(short, data); !errors.Is(err, errArchiveFolder) {
		t.Fatalf("the 8.3 name of the data folder: err = %v, want a refusal", err)
	}
}

func TestArchiveLengthBoundary(t *testing.T) {
	folder := `C:\` + strings.Repeat("a", 100)
	name := "n.unravis-case"
	// folder + `\` + name is 3+100+1+14 = 118; pad the folder so the sum is exact.
	pad := maxArchivePath - len(folder) - 1 - len(name)
	f240 := folder + strings.Repeat("b", pad)
	if err := checkArchiveLength(f240, name); err != nil {
		t.Fatalf("240 characters was refused: %v", err)
	}
	if got := pathUnits(filepath.Join(f240, name)); got != 240 {
		t.Fatalf("test setup: %d", got)
	}
	if err := checkArchiveLength(f240+"b", name); !errors.Is(err, errArchiveFolder) {
		t.Fatalf("241 characters: err = %v, want a refusal", err)
	}
}
