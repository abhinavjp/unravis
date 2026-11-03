package main

import (
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/setup"
)

// WP-74j: the elevated worker writes no log and no link inside the user's profile; silent mode does both as the user. All tests use
// the fake PC of newRig (temporary folders), never the real profile.

// withDesktop gives the rig's fake person a Desktop folder (the rig has none by default).
func withDesktop(rg *testRig) string {
	rg.t.Helper()
	rg.dirs.Desktop = filepath.Join(rg.dirs.UserProfile, "Desktop")
	if err := os.MkdirAll(rg.dirs.Desktop, 0o755); err != nil {
		rg.t.Fatal(err)
	}
	d := rg.dirs
	rg.env.dirs = func() (setup.Dirs, error) { return d, nil }
	return d.Desktop
}

func machineLayout(rg *testRig) setup.Layout {
	return setup.NewLayout(setup.Options{Scope: setup.ScopeMachine, Location: setup.DefaultLocation(setup.ScopeMachine, rg.dirs)}, rg.dirs)
}

func TestWorkerLogIsNotWrittenAnywhere(t *testing.T) {
	rg := newRig(t)
	a, b := net.Pipe()
	rg.env.dialWorker = func(string, uint32) (*setup.WorkerPipe, error) {
		return &setup.WorkerPipe{Conn: b, SID: "S-1-5-21-1000", Profile: rg.dirs.UserProfile}, nil
	}
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"--worker", "--pipe", `\\.\pipe\unravis-setup-x`, "--parent", "4242"}, rg.env)
	}()
	u := rg.dirs.User()
	req := setup.WorkRequest{Op: setup.OpInstall, User: &u, Options: setup.Options{Scope: setup.ScopeMachine,
		Location: setup.DefaultLocation(setup.ScopeMachine, rg.dirs), StartAtSignIn: true}}
	var lines int
	res := setup.RunWorker(context.Background(), connElevator{a}, req, nil, func(string) { lines++ })
	if res.Outcome != setup.OutcomeDone {
		t.Fatalf("outcome %s: %s %s", res.Outcome, res.Message, res.Fix)
	}
	if got := <-done; got != 0 {
		t.Fatalf("worker exit %d", got)
	}
	if lines == 0 {
		t.Fatal("the worker's lines no longer reach the window")
	}
	_ = filepath.WalkDir(rg.dirs.UserProfile, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == "setup-logs" || strings.HasSuffix(d.Name(), "-admin.log") {
			t.Errorf("the worker wrote %s under the profile", p)
		}
		return nil
	})
	if logs, _ := filepath.Glob(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs", "*-admin.log")); len(logs) != 0 {
		t.Fatalf("worker logs %v", logs)
	}
	if exists(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs")) {
		t.Fatal("the worker made the setup-logs folder")
	}
}

func TestSilentWorkerInstallWritesProfileLinks(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	withDesktop(rg)
	l := machineLayout(rg)
	if got := run([]string{"--silent", "--config", rg.config(`{"scope":"machine"}`)}, rg.env); got != 0 {
		t.Fatalf("exit %d\n%s", got, rg.out)
	}
	if rg.elevated != 1 {
		t.Fatalf("elevated %d", rg.elevated)
	}
	if !exists(l.DesktopLink) || !exists(l.StartupLink) {
		t.Fatalf("desktop %v startup %v\n%s", exists(l.DesktopLink), exists(l.StartupLink), rg.out)
	}
}

func TestSilentWorkerUninstallRemovesOurProfileLinks(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	withDesktop(rg)
	l := machineLayout(rg)
	if got := run([]string{"--silent", "--config", rg.config(`{"scope":"machine"}`)}, rg.env); got != 0 {
		t.Fatalf("install: exit %d\n%s", got, rg.out)
	}
	if !exists(l.DesktopLink) || !exists(l.StartupLink) {
		t.Fatal("the install did not write the profile links")
	}
	// A foreign Desktop link in place of ours is kept.
	other := filepath.Join(rg.root, "Other", "other.exe")
	data, err := setup.Shortcut{Target: other, WorkDir: filepath.Dir(other), Icon: other, Description: "x"}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.DesktopLink, data, 0o644); err != nil {
		t.Fatal(err)
	}
	rg.out.b.Reset()
	run([]string{"--silent", "--uninstall"}, rg.env)
	if exists(l.StartupLink) {
		t.Fatalf("our Startup link is still there\n%s", rg.out)
	}
	got, err := os.ReadFile(l.DesktopLink)
	if err != nil || string(got) != string(data) {
		t.Fatalf("the foreign Desktop link was not kept (%v)\n%s", err, rg.out)
	}
	if !strings.Contains(rg.out.String(), "kept") {
		t.Fatalf("the kept link is not listed\n%s", rg.out)
	}
	if exists(filepath.Join(setup.DefaultLocation(setup.ScopeMachine, rg.dirs), setup.AppExeName)) {
		t.Fatal("the program is still there")
	}

	// With only our links, an uninstall removes them all and exits 0.
	rg2 := newRig(t)
	rg2.f.Mach.Admin = false
	withDesktop(rg2)
	l2 := machineLayout(rg2)
	if got := run([]string{"--silent", "--config", rg2.config(`{"scope":"machine"}`)}, rg2.env); got != 0 {
		t.Fatalf("install: exit %d\n%s", got, rg2.out)
	}
	if got := run([]string{"--silent", "--uninstall"}, rg2.env); got != 0 {
		t.Fatalf("uninstall: exit %d\n%s", got, rg2.out)
	}
	if exists(l2.DesktopLink) || exists(l2.StartupLink) {
		t.Fatal("one of our links was left behind")
	}
}

func TestSilentUpgradeKeepsTheTraySignInChoice(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	withDesktop(rg)
	l := machineLayout(rg)
	cfg := rg.config(`{"scope":"machine","startAtSignIn":true}`)
	if got := run([]string{"--silent", "--config", cfg}, rg.env); got != 0 {
		t.Fatalf("install: exit %d\n%s", got, rg.out)
	}
	if !exists(l.StartupLink) {
		t.Fatal("the install did not write the Startup link")
	}
	if err := os.Remove(l.StartupLink); err != nil { // the tray toggle turned it off
		t.Fatal(err)
	}
	rg.out.b.Reset()
	if got := run([]string{"--silent", "--upgrade", "--config", cfg}, rg.env); got != 0 {
		t.Fatalf("upgrade: exit %d\n%s", got, rg.out)
	}
	if exists(l.StartupLink) {
		t.Fatal("the upgrade wrote the Startup link back")
	}
	old, err := setup.Detect(rg.sys.Reg)
	if err != nil || old == nil {
		t.Fatalf("detect: %v %v", old, err)
	}
	if old.State.StartAtSignIn {
		t.Fatal("the saved option is still on")
	}
	if !exists(l.DesktopLink) {
		t.Fatal("the Desktop link is missing after the upgrade")
	}
}
