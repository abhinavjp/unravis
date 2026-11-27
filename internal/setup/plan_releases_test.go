package setup

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The releases window is an optional companion in the payload: with it, setup places it and a Start-menu link; without it, nothing
// about the install changes; uninstall removes both.

func TestNewLayoutHasReleasesPathsBesideTheApp(t *testing.T) {
	d := desktopTestDirs()
	for _, scope := range []Scope{ScopeUser, ScopeMachine} {
		l := NewLayout(Options{Scope: scope, Location: DefaultLocation(scope, d)}, d)
		if l.ReleasesExe != filepath.Join(l.InstallDir, "homestretch-app.exe") ||
			l.ReleasesLink != filepath.Join(filepath.Dir(l.StartMenuLink), "Homestretch.lnk") ||
			l.HomestretchExe != filepath.Join(l.InstallDir, "bin", "homestretch.exe") ||
			l.HSExe != filepath.Join(l.InstallDir, "bin", "hs.exe") {
			t.Fatalf("scope %v: %+v", scope, l)
		}
	}
}

func TestLegacyDashboardPayloadInstallsUnderTheNewName(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.Payload = testPayload(t, "1.2.0", true, LegacyReleasesExeName)
	o := e.userOpts()
	o.Releases = true
	l := NewLayout(o, e.dirs)
	e.mustRun(OpInstall, o, UninstallOptions{})
	assertPayloadFile(t, e.r.Payload, LegacyReleasesExeName, l.ReleasesExe)
	if _, ours := linkIsOurs(l.ReleasesLink, l.ReleasesExe); !ours || exists(l.legacyReleasesExe()) || exists(l.legacyReleasesLink()) {
		t.Fatal("the legacy payload was not installed under the Homestretch names")
	}
}

func TestInstallAndUninstallHomestretchPrograms(t *testing.T) {
	for _, scope := range []Scope{ScopeMachine, ScopeUser} {
		t.Run(string(scope), func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			e.r.Payload = testPayload(t, "1.2.0", true, CLIExeName, HomestretchExeName, HSExeName, ReleasesExeName)
			o := e.userOpts()
			if scope == ScopeMachine {
				o = e.machineOpts()
			}
			o.Releases = true
			l := NewLayout(o, e.dirs)
			e.mustRun(OpInstall, o, UninstallOptions{})
			assertPayloadFile(t, e.r.Payload, CLIExeName, l.CLIExe)
			assertPayloadFile(t, e.r.Payload, HomestretchExeName, l.HomestretchExe)
			assertPayloadFile(t, e.r.Payload, HSExeName, l.HSExe)
			assertPayloadFile(t, e.r.Payload, ReleasesExeName, l.ReleasesExe)
			target, args := readLink(t, l.ReleasesLink)
			if !samePath(target, l.ReleasesExe) || args != "" {
				t.Errorf("dashboard shortcut = %q %q", target, args)
			}
			writeFile(t, l.legacyReleasesExe(), "old dashboard")
			putLink(t, l.legacyReleasesLink(), l.legacyReleasesExe(), "")
			e.r.NoProfileLinks = scope == ScopeUser
			res := e.mustRun(OpUninstall, o, UninstallOptions{})
			if e.r.NoProfileLinks {
				if !exists(l.ReleasesLink) || !exists(l.legacyReleasesLink()) {
					t.Fatal("the worker removed a profile shortcut")
				}
				e.r.ApplyProfileLinks(o, &res, nil)
				if res.Outcome != OutcomeDone {
					t.Fatalf("uninstall profile links: %+v", res)
				}
			}
			for _, path := range []string{l.CLIExe, l.HomestretchExe, l.HSExe, l.CLIDir, l.ReleasesExe, l.ReleasesLink, l.legacyReleasesExe(), l.legacyReleasesLink()} {
				if exists(path) {
					t.Errorf("uninstall left %s", path)
				}
			}
		})
	}
}

func assertPayloadFile(t *testing.T, p *Payload, name, path string) {
	t.Helper()
	want, err := fsSHA256(p.FS, name)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fileSHA256(path); err != nil || got != want {
		t.Errorf("%s does not match %s in the payload: %v", path, name, err)
	}
}

func TestUpgradeMigratesLegacyDashboard(t *testing.T) {
	for _, tc := range []struct {
		name        string
		worker      bool
		foreignLink bool
	}{
		{name: "in plan"},
		{name: "foreign shortcut", foreignLink: true},
		{name: "after elevated worker", worker: true},
		{name: "foreign shortcut after elevated worker", worker: true, foreignLink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			o := e.userOpts()
			l := NewLayout(o, e.dirs)
			e.mustRun(OpInstall, o, UninstallOptions{})
			writeFile(t, l.legacyReleasesExe(), "old dashboard")
			target := l.legacyReleasesExe()
			if tc.foreignLink {
				target = filepath.Join(e.root, "other", LegacyReleasesExeName)
			}
			putLink(t, l.legacyReleasesLink(), target, "")
			e.f.Proc.Run(l.legacyReleasesExe())
			e.f.Proc.Run(l.ReleasesExe)
			e.f.Proc.Run(filepath.Join(e.root, "other", ReleasesExeName))
			e.upgradeTo("1.3.0")
			e.r.Payload = testPayload(t, "1.3.0", true, HomestretchExeName, HSExeName, ReleasesExeName)
			e.r.NoProfileLinks = tc.worker
			res := e.mustRun(OpUpgrade, o, UninstallOptions{})
			if tc.worker {
				if !exists(l.legacyReleasesLink()) || exists(l.ReleasesLink) {
					t.Fatal("the worker changed a profile shortcut")
				}
				e.r.ApplyProfileLinks(o, &res, nil)
			}
			assertPayloadFile(t, e.r.Payload, ReleasesExeName, l.ReleasesExe)
			assertPayloadFile(t, e.r.Payload, HomestretchExeName, l.HomestretchExe)
			assertPayloadFile(t, e.r.Payload, HSExeName, l.HSExe)
			if _, ours := linkIsOurs(l.ReleasesLink, l.ReleasesExe); !ours || exists(l.legacyReleasesExe()) {
				t.Fatal("dashboard migration did not finish")
			}
			if tc.foreignLink {
				if _, kept := linkIsOurs(l.legacyReleasesLink(), target); !kept {
					t.Fatal("migration changed the foreign shortcut")
				}
			} else if exists(l.legacyReleasesLink()) {
				t.Fatal("migration left the old shortcut")
			}
			running, err := e.f.Proc.List()
			if err != nil || len(running) != 1 || !samePath(running[0].Exe, filepath.Join(e.root, "other", ReleasesExeName)) {
				t.Fatalf("processes after upgrade = %+v, %v", running, err)
			}
		})
	}
}

func TestHomestretchPayloadChecksums(t *testing.T) {
	for _, name := range []string{HomestretchExeName, HSExeName, ReleasesExeName} {
		t.Run(name, func(t *testing.T) {
			p := testPayload(t, "1.2.0", true, HomestretchExeName, HSExeName, ReleasesExeName)
			data, err := fs.ReadFile(p.FS, name)
			if err != nil {
				t.Fatal(err)
			}
			data[0] ^= 1
			p.FS.(fstest.MapFS)[name] = &fstest.MapFile{Data: data}
			if err := p.Verify(); err == nil || !strings.Contains(err.Error(), name+" does not match its checksum") {
				t.Fatalf("Verify() = %v", err)
			}
		})
	}
}

func TestFailedDashboardMigrationRestoresLegacyFiles(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	e.mustRun(OpInstall, o, UninstallOptions{})
	writeFile(t, l.legacyReleasesExe(), "old dashboard")
	putLink(t, l.legacyReleasesLink(), l.legacyReleasesExe(), "")
	e.upgradeTo("1.3.0")
	e.r.Payload = testPayload(t, "1.3.0", true, HomestretchExeName, HSExeName, ReleasesExeName)
	e.r.FailAfter = func(step string) error {
		if step == "shortcuts" {
			return os.ErrPermission
		}
		return nil
	}
	res := e.run(OpUpgrade, o, UninstallOptions{})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("upgrade result = %+v", res)
	}
	if data, err := os.ReadFile(l.legacyReleasesExe()); err != nil || string(data) != "old dashboard" {
		t.Fatalf("legacy dashboard was not restored: %q, %v", data, err)
	}
	if _, ours := linkIsOurs(l.legacyReleasesLink(), l.legacyReleasesExe()); !ours {
		t.Fatal("legacy shortcut was not restored")
	}
	if exists(l.ReleasesExe) || exists(l.ReleasesLink) || exists(l.CLIDir) {
		t.Fatal("rollback left newly placed Homestretch files")
	}
}

func TestSetupKeepsForeignFilesWithHomestretchNames(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	e.r.Payload = testPayload(t, "1.2.0", true, HomestretchExeName)
	e.mustRun(OpInstall, o, UninstallOptions{})
	if err := os.Rename(l.HomestretchExe, filepath.Join(e.root, HomestretchExeName)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, l.HomestretchExe, "replacement")
	writeFile(t, l.HSExe, "standalone alias")
	if res := e.run(OpRepair, o, UninstallOptions{}); res.Outcome != OutcomeRolledBack || !strings.Contains(res.Message, "already exists") {
		t.Fatalf("repair result = %+v", res)
	}
	e.mustRun(OpUninstall, o, UninstallOptions{})
	for path, want := range map[string]string{l.HomestretchExe: "replacement", l.HSExe: "standalone alias"} {
		data, err := os.ReadFile(path) //nolint:gosec // test reads its own temp-dir path
		if err != nil || string(data) != want {
			t.Errorf("foreign file %s changed: %q, %v", path, data, err)
		}
	}
}

func TestInstallWithoutTheReleasesFileWritesNoReleasesLink(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if exists(l.ReleasesExe) || exists(l.ReleasesLink) {
		t.Fatal("an older payload must install exactly as before")
	}
}

func TestHomestretchCLIInstallsWithoutTheDashboardOption(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.Payload = testPayload(t, "1.2.0", true, HomestretchExeName, HSExeName, ReleasesExeName)
	o := e.userOpts() // Releases is off
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	assertPayloadFile(t, e.r.Payload, HomestretchExeName, l.HomestretchExe)
	assertPayloadFile(t, e.r.Payload, HSExeName, l.HSExe)
	if has, err := e.sys.Env.HasPathEntry(HKCU, l.CLIDir); err != nil || !has {
		t.Fatalf("bin PATH entry = %t, %v", has, err)
	}
	if exists(l.ReleasesExe) || exists(l.ReleasesLink) {
		t.Fatal("a normal install must not carry the releases window")
	}
}

func TestAnInstalledReleasesWindowIsKeptOnRepairWithoutTheOption(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.Payload = testPayload(t, "1.2.0", true, ReleasesExeName)
	o := e.userOpts()
	o.Releases = true
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	writeFile(t, l.ReleasesExe, "damaged")
	o.Releases = false
	e.mustRun(OpRepair, o, UninstallOptions{})
	assertPayloadFile(t, e.r.Payload, ReleasesExeName, l.ReleasesExe)
	if !exists(l.ReleasesExe) || !exists(l.ReleasesLink) {
		t.Fatal("repair must keep (and restore) an installed releases window")
	}
}

func TestReleasesOptionComesFromTheSilentConfig(t *testing.T) {
	c, err := ParseSilentConfig([]byte(`{"scope":"user","releases":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.Request(OpInstall)
	if err != nil || !req.Options.Releases {
		t.Fatalf("%v %+v", err, req.Options)
	}
	c, _ = ParseSilentConfig([]byte(`{"scope":"user"}`))
	if req, _ = c.Request(OpInstall); req.Options.Releases {
		t.Fatal("off unless asked for")
	}
}
