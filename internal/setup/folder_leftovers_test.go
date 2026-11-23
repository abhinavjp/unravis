package setup

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func userFolderCheck(e *testEnv) (Check, bool) {
	return checkByID(e.r.Preflight(PreflightInput{Op: OpInstall, Opts: e.userOpts()}), "install-folder")
}

// A folder that holds only what the installer makes (a cut-off setup's working folder, the old setup, a program) is an unfinished
// Unravis: setup goes on over it and does not block the person.
func TestUserFolderWithOnlyInstallerFilesPasses(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := e.userOpts().Location
	writeFile(t, filepath.Join(dir, ".setup-staging-20261122-101010", "unravis.exe"), "x")
	writeFile(t, filepath.Join(dir, ".setup-backup-20261122-101010", "unravis.exe"), "x")
	writeFile(t, filepath.Join(dir, ".previous", "marker.json"), "{}")
	writeFile(t, filepath.Join(dir, SetupExeName), "x")
	writeFile(t, filepath.Join(dir, "UNRAVIS-RELEASES.EXE"), "x") // name case does not matter
	writeFile(t, filepath.Join(dir, "Desktop.ini"), "x")          // dropped by Explorer
	if c, ok := userFolderCheck(e); ok {
		t.Fatalf("a folder of installer files was refused: %+v", c)
	}
}

// Somebody's own file blocks, and the line names it (and only it), and says what to do.
func TestUserFolderWithOtherFilesNamesThemAndSaysWhatToDo(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := e.userOpts().Location
	writeFile(t, filepath.Join(dir, SetupExeName), "x")
	writeFile(t, filepath.Join(dir, "notes.txt"), "theirs")
	c, ok := userFolderCheck(e)
	if !ok || c.Status != CheckFailed {
		t.Fatalf("want a failed line, got %+v (%v)", c, ok)
	}
	if !strings.Contains(c.Detail, "notes.txt") || strings.Contains(c.Detail, SetupExeName) {
		t.Errorf("detail should name only the other files: %q", c.Detail)
	}
	if !strings.Contains(c.Fix, "does not change or delete") {
		t.Errorf("fix should point at the action: %q", c.Fix)
	}
}

func TestNamesLineCutsLongLists(t *testing.T) {
	if got := namesLine([]string{"a", "b"}); got != "a, b" {
		t.Errorf("short: %q", got)
	}
	if got := namesLine([]string{"a", "b", "c", "d", "e", "f"}); got != "a, b, c, d and 2 more" {
		t.Errorf("long: %q", got)
	}
}

// A setup that goes on over the leftovers of a cut-off one clears the old staging folder, keeps a backup folder, a folder of the person's
// that only starts like ours, the current run's own staging folder, and every other file.
func TestClearStaleStagingKeepsBackupsAndOtherFiles(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	stale := filepath.Join(o.Location, ".setup-staging-20200101-000000", "unravis.exe")
	backup := filepath.Join(o.Location, ".setup-backup-20200101-000000", "unravis.exe")
	lookalike := filepath.Join(o.Location, ".setup-staging-mine", "keep.txt")
	other := filepath.Join(o.Location, "notes.txt")
	for _, f := range []string{stale, backup, lookalike, other} {
		writeFile(t, f, "x")
	}
	p, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(o.Location, stagingPrefix+p.stamp, "unravis.exe")
	writeFile(t, current, "x")
	p.clearStaleStaging()
	if exists(filepath.Dir(stale)) {
		t.Error("the stale staging folder was left")
	}
	for _, f := range []string{backup, lookalike, other, current} {
		if !exists(f) {
			t.Errorf("%s was removed", f)
		}
	}
}

// A link named like a staging folder is never followed or removed by name matching.
func TestClearStaleStagingSkipsALink(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	target := filepath.Join(e.root, "elsewhere")
	writeFile(t, filepath.Join(target, "precious.txt"), "x")
	if err := os.MkdirAll(o.Location, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(o.Location, ".setup-staging-20200101-000000")); err != nil {
		t.Skip("no symlinks here: " + err.Error())
	}
	p, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.clearStaleStaging()
	if !exists(filepath.Join(target, "precious.txt")) {
		t.Fatal("the link target was touched")
	}
}

// A folder that only starts like a working folder is somebody's own, so it blocks and is named.
func TestLookalikeStagingFolderCountsAsTheirs(t *testing.T) {
	e := newEnv(t, "1.2.0")
	writeFile(t, filepath.Join(e.userOpts().Location, ".setup-staging-mine", "a.txt"), "x")
	c, ok := userFolderCheck(e)
	if !ok || c.Status != CheckFailed || !strings.Contains(c.Detail, ".setup-staging-mine") {
		t.Fatalf("got %+v (%v)", c, ok)
	}
}

// The machine-scope message names the files too.
func TestMachineFolderMessageNamesTheFiles(t *testing.T) {
	e := newEnv(t, "1.2.0")
	writeFile(t, filepath.Join(e.dirs.ProgramFiles, "Unravis", "notes.txt"), "theirs")
	c, ok := installFolderCheck(t, e)
	if !ok || !strings.Contains(c.Detail, "notes.txt") {
		t.Fatalf("got %+v (%v)", c, ok)
	}
}

func TestOpenFolderRoute(t *testing.T) {
	c := newUIClient(t, &pathBackend{})
	var opened string
	c.s.OpenFolder = func(dir string) error { opened = dir; return nil }
	dir := t.TempDir()
	if code, out := c.do("POST", "/api/open-folder", `{"folder":`+jsonQuote(dir)+`}`, nil); code != http.StatusOK || out["ok"] != true || opened != dir {
		t.Fatalf("ok: %d %v opened %q", code, out, opened)
	}
	opened = ""
	if code, _ := c.do("POST", "/api/open-folder", `{"folder":`+jsonQuote(filepath.Join(dir, "nope"))+`}`, nil); code != http.StatusNotFound || opened != "" {
		t.Errorf("missing folder: %d opened %q", code, opened)
	}
	for _, bad := range []string{`\\\\host\\share`, `\\\\?\\C:\\x`, "//host/share"} {
		if code, _ := c.do("POST", "/api/open-folder", `{"folder":`+jsonQuote(bad)+`}`, nil); code != http.StatusBadRequest || opened != "" {
			t.Errorf("%q: %d opened %q", bad, code, opened)
		}
	}
	file := filepath.Join(dir, "a.txt")
	writeFile(t, file, "x")
	if code, _ := c.do("POST", "/api/open-folder", `{"folder":`+jsonQuote(file)+`}`, nil); code != http.StatusNotFound || opened != "" {
		t.Errorf("a file: %d opened %q", code, opened)
	}
	if code, _ := c.do("POST", "/api/open-folder", `{"folder":"relative\\path"}`, nil); code != http.StatusBadRequest || opened != "" {
		t.Errorf("relative: %d opened %q", code, opened)
	}
	if code, _ := c.do("POST", "/api/open-folder", `{"folder":"","x":1}`, nil); code != http.StatusBadRequest {
		t.Errorf("empty/unknown field: %d", code)
	}
	c.s.OpenFolder = func(string) error { return http.ErrNotSupported }
	if code, _ := c.do("POST", "/api/open-folder", `{"folder":`+jsonQuote(dir)+`}`, nil); code != http.StatusInternalServerError {
		t.Errorf("open failed: %d", code)
	}
}

func jsonQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
