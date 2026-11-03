package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/shelllink"
)

// All tests here use the fake PC of newEnv: temporary folders only, never the real Desktop, Start menu or Startup folder.

func userLayout(e *testEnv) Layout {
	return NewLayout(Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, e.dirs)}, e.dirs)
}

func machineLayout(e *testEnv) Layout {
	return NewLayout(Options{Scope: ScopeMachine, Location: DefaultLocation(ScopeMachine, e.dirs)}, e.dirs)
}

// readLink returns the target and arguments of a shortcut file.
func readLink(t *testing.T, path string) (target, args string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if target, err = ReadShortcutTarget(data); err != nil {
		t.Fatal(err)
	}
	if args, err = ReadShortcutArgs(data); err != nil {
		t.Fatal(err)
	}
	return target, args
}

// putLink writes a shortcut to exe (with args) at path.
func putLink(t *testing.T, path, exe, args string) {
	t.Helper()
	data, err := Shortcut{Target: exe, Args: args, WorkDir: filepath.Dir(exe), Icon: exe, Description: "x"}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
}

func TestWriteProfileLinksWritesDesktopAndStartup(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	if target, args := readLink(t, l.DesktopLink); !samePath(target, l.AppExe) || args != "" {
		t.Fatalf("desktop link = %q %q", target, args)
	}
	if target, args := readLink(t, l.StartupLink); !samePath(target, l.AppExe) || args != startupLinkArg {
		t.Fatalf("startup link = %q %q", target, args)
	}
}

func TestWriteProfileLinksSkipsStartupWhenOff(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	if err := e.r.WriteProfileLinks(l, false, nil); err != nil {
		t.Fatal(err)
	}
	if !exists(l.DesktopLink) || exists(l.StartupLink) {
		t.Fatalf("desktop %v startup %v", exists(l.DesktopLink), exists(l.StartupLink))
	}
}

func TestWriteProfileLinksKeepsAForeignDesktopLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	putLink(t, l.DesktopLink, filepath.Join(e.root, "Other", "other.exe"), "")
	before, _ := os.ReadFile(l.DesktopLink)
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(l.DesktopLink)
	if string(before) != string(after) {
		t.Fatal("the foreign Desktop link changed")
	}
	if !exists(l.StartupLink) {
		t.Fatal("the Startup link was not written")
	}
}

func TestWriteProfileLinksKeepsAForeignStartupLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	putLink(t, l.StartupLink, filepath.Join(e.root, "Other", "other.exe"), "")
	before, _ := os.ReadFile(l.StartupLink)
	var logged []string
	if err := e.r.WriteProfileLinks(l, true, func(s string) { logged = append(logged, s) }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(l.StartupLink)
	if string(before) != string(after) {
		t.Fatal("the foreign Startup link was overwritten")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "skipped") {
		t.Fatalf("log = %q", logged)
	}
	if !exists(l.DesktopLink) {
		t.Fatal("the Desktop link was not written")
	}
}

func TestWriteProfileLinksKeepsAnUnreadableStartupFile(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	writeFile(t, l.StartupLink, "garbage")
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(l.StartupLink); string(got) != "garbage" {
		t.Fatal("a Startup file that is not a shortcut was overwritten")
	}
}

func TestWriteProfileLinksReplacesAnOlderStartupLinkOfOurs(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	putLink(t, l.StartupLink, l.AppExe, "")
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	if target, args := readLink(t, l.StartupLink); !samePath(target, l.AppExe) || args != startupLinkArg {
		t.Fatalf("startup link = %q %q", target, args)
	}
}

func TestWriteProfileLinksReplacesOurOlderLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	putLink(t, l.DesktopLink, l.AppExe, "--old")
	if err := e.r.WriteProfileLinks(l, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, args := readLink(t, l.DesktopLink); args != "" {
		t.Fatalf("the older Desktop link was kept: args %q", args)
	}
}

func TestWriteProfileLinksUserScopeAlsoWritesStartMenuAndQuit(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := userLayout(e)
	if err := e.r.WriteProfileLinks(l, false, nil); err != nil {
		t.Fatal(err)
	}
	if target, args := readLink(t, l.StartMenuLink); !samePath(target, l.AppExe) || args != "" {
		t.Fatalf("start menu link = %q %q", target, args)
	}
	if target, args := readLink(t, l.QuitLink); !samePath(target, l.AppExe) || args != quitLinkArg {
		t.Fatalf("quit link = %q %q", target, args)
	}
}

func TestWriteProfileLinksMachineScopeLeavesStartMenuAndQuitToTheWorker(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	if exists(l.StartMenuLink) || exists(l.QuitLink) {
		t.Fatal("a link outside the profile was written")
	}
}

func TestWriteProfileLinksRemovesItsTmpFileOnFailure(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	// The temporary file path is an empty folder, so writing the temporary file fails (and its cleanup removes the folder).
	if err := os.MkdirAll(l.StartupLink+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	err := e.r.WriteProfileLinks(l, true, nil)
	if err == nil {
		t.Fatal("want the write error")
	}
	if exists(l.StartupLink + ".tmp") {
		t.Fatal("a .tmp file was left")
	}
	if !exists(l.DesktopLink) {
		t.Fatal("the other links must still be tried")
	}
}

func TestRemoveProfileLinksRemovesOnlyOurs(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	other := filepath.Join(e.root, "Other", "other.exe")
	putLink(t, l.DesktopLink, l.AppExe, "")
	putLink(t, l.StartupLink, other, "")
	lines := e.r.RemoveProfileLinks(l)
	if exists(l.DesktopLink) {
		t.Fatal("our Desktop link was kept")
	}
	if !exists(l.StartupLink) {
		t.Fatal("a foreign link was removed")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "kept: it points to another program") || !strings.Contains(lines[0], l.StartupLink) {
		t.Fatalf("lines = %q", lines)
	}
}

func TestRemoveProfileLinksKeepsAnUnreadableLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	writeFile(t, l.DesktopLink, "this is not a shortcut")
	lines := e.r.RemoveProfileLinks(l)
	if !exists(l.DesktopLink) || len(lines) != 1 {
		t.Fatalf("kept %v lines %q", exists(l.DesktopLink), lines)
	}
}

func TestRemoveProfileLinksToleratesMissingFiles(t *testing.T) {
	e := newEnv(t, "1.0.0")
	if lines := e.r.RemoveProfileLinks(userLayout(e)); len(lines) != 0 {
		t.Fatalf("lines = %q", lines)
	}
}

func TestRemoveProfileLinksUserScopeRemovesStartMenuAndQuit(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := userLayout(e)
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	if lines := e.r.RemoveProfileLinks(l); len(lines) != 0 {
		t.Fatalf("lines = %q", lines)
	}
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if exists(p) {
			t.Fatalf("%s was kept", p)
		}
	}
}

func TestLinkIsOursIsFalseForAnOversizedFile(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	writeFile(t, l.DesktopLink, strings.Repeat("x", shelllink.MaxFileSize+1))
	if ex, ours := linkIsOurs(l.DesktopLink, l.AppExe); !ex || ours {
		t.Fatalf("exists %v ours %v", ex, ours)
	}
}

func TestLinkTargetDoesNotReadAnOversizedFile(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	writeFile(t, l.DesktopLink, strings.Repeat("x", shelllink.MaxFileSize+1))
	old := readLinkFile
	readLinkFile = func(string) ([]byte, error) {
		t.Fatal("an oversized link was read")
		return nil, nil
	}
	t.Cleanup(func() { readLinkFile = old })
	if _, ex, err := linkTarget(l.DesktopLink); !ex || err == nil {
		t.Fatalf("exists %v err %v", ex, err)
	}
	// A folder is not read either.
	if _, ex, err := linkTarget(e.dirs.Desktop); !ex || err == nil {
		t.Fatalf("folder: exists %v err %v", ex, err)
	}
	if _, ex, err := linkTarget(filepath.Join(e.dirs.Desktop, "missing.lnk")); ex || err != nil {
		t.Fatalf("missing: exists %v err %v", ex, err)
	}
}

func TestRunnerHasNoProfileLinksFlagOffByDefault(t *testing.T) {
	if (&Runner{}).NoProfileLinks {
		t.Fatal("NoProfileLinks must be off by default")
	}
}

func TestIsProfileLink(t *testing.T) {
	r := &Runner{Dirs: Dirs{UserProfile: `C:\Users\ana`}}
	for _, c := range []struct {
		path string
		want bool
	}{
		{`C:\Users\ana\Desktop\Unravis.lnk`, true},
		{`C:\Users\ana`, true},
		{`C:\Users\anabel\Desktop\Unravis.lnk`, false},
		{`C:\ProgramData\Unravis.lnk`, false},
		{``, false},
	} {
		if got := r.isProfileLink(c.path); got != c.want {
			t.Errorf("%q = %v", c.path, got)
		}
	}
	if (&Runner{}).isProfileLink(`C:\Users\ana\x.lnk`) {
		t.Error("an empty profile matches nothing")
	}
}
