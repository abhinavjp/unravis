package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func userOpts(e *testEnv, signIn bool) Options {
	return Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, e.dirs), StartAtSignIn: signIn}
}

func TestDeriveStartAtSignInReadsTheStartupLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	saved := userOpts(e, true)
	l := NewLayout(saved, e.dirs)

	req := Options{StartAtSignIn: false}
	e.r.DeriveStartAtSignIn(OpUpgrade, saved, &req)
	if req.StartAtSignIn {
		t.Fatal("no link: want false")
	}

	putLink(t, l.StartupLink, filepath.Join(e.root, "Other", "other.exe"), "")
	req = Options{StartAtSignIn: true}
	e.r.DeriveStartAtSignIn(OpRepair, saved, &req)
	if req.StartAtSignIn {
		t.Fatal("foreign link: want false")
	}

	putLink(t, l.StartupLink, l.AppExe, startupLinkArg)
	req = Options{StartAtSignIn: false}
	e.r.DeriveStartAtSignIn(OpUpgrade, saved, &req)
	if !req.StartAtSignIn {
		t.Fatal("our link: want true")
	}
}

func TestDeriveStartAtSignInKeepsTheSavedValueWhenTheLinkCannotBeRead(t *testing.T) {
	e := newEnv(t, "1.0.0")
	saved := userOpts(e, true)
	l := NewLayout(saved, e.dirs)
	putLink(t, l.StartupLink, l.AppExe, startupLinkArg)
	old := readLinkFile
	readLinkFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
	t.Cleanup(func() { readLinkFile = old })
	req := Options{StartAtSignIn: false}
	e.r.DeriveStartAtSignIn(OpUpgrade, saved, &req)
	if !req.StartAtSignIn {
		t.Fatal("want the saved value")
	}
}

func TestDeriveStartAtSignInLeavesAFreshInstallAlone(t *testing.T) {
	e := newEnv(t, "1.0.0")
	saved := userOpts(e, false)
	for _, op := range []Op{OpInstall, OpUninstall} {
		req := Options{StartAtSignIn: true}
		e.r.DeriveStartAtSignIn(op, saved, &req)
		if !req.StartAtSignIn {
			t.Fatalf("%s changed the request", op)
		}
	}
}

func TestApplyProfileLinksWritesAfterAnInstall(t *testing.T) {
	e := newEnv(t, "1.0.0")
	opts := userOpts(e, true)
	l := NewLayout(opts, e.dirs)
	res := &Result{Outcome: OutcomeDone, Op: OpInstall, AppExe: l.AppExe}
	e.r.ApplyProfileLinks(opts, res, nil)
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if !exists(p) {
			t.Fatalf("%s was not written", p)
		}
	}
	if res.Outcome != OutcomeDone || len(res.Left) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestApplyProfileLinksUsesTheInstalledExeFromTheResult(t *testing.T) {
	e := newEnv(t, "1.0.0")
	opts := Options{Scope: ScopeUser, Location: filepath.Join(e.root, "Apps", "old")}
	canon := filepath.Join(e.root, "Apps", "canonical", AppExeName)
	res := &Result{Outcome: OutcomeDone, Op: OpInstall, AppExe: canon}
	e.r.ApplyProfileLinks(opts, res, nil)
	l := NewLayout(opts, e.dirs)
	target, _ := readLink(t, l.DesktopLink)
	if !samePath(target, canon) {
		t.Fatalf("target = %q", target)
	}
}

func TestApplyProfileLinksUninstallRemovesOnlyOursAndListsKept(t *testing.T) {
	e := newEnv(t, "1.0.0")
	opts := userOpts(e, true)
	l := NewLayout(opts, e.dirs)
	if err := e.r.WriteProfileLinks(l, true, nil); err != nil {
		t.Fatal(err)
	}
	putLink(t, l.DesktopLink, filepath.Join(e.root, "Other", "other.exe"), "")
	res := &Result{Outcome: OutcomeDone, Op: OpUninstall}
	e.r.ApplyProfileLinks(opts, res, nil)
	if exists(l.StartMenuLink) || exists(l.QuitLink) || exists(l.StartupLink) {
		t.Fatal("our links were kept")
	}
	if !exists(l.DesktopLink) {
		t.Fatal("the foreign link was removed")
	}
	if res.Outcome != OutcomeLeftovers || len(res.Left) != 1 || !strings.Contains(res.Left[0], "kept") {
		t.Fatalf("result = %+v", res)
	}

	// All well: the outcome stays done.
	e2 := newEnv(t, "1.0.0")
	if err := e2.r.WriteProfileLinks(NewLayout(opts, e2.dirs), true, nil); err != nil {
		t.Fatal(err)
	}
	res = &Result{Outcome: OutcomeDone, Op: OpUninstall}
	e2.r.ApplyProfileLinks(opts, res, nil)
	if res.Outcome != OutcomeDone || len(res.Left) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestApplyProfileLinksDoesNothingForARolledBackResult(t *testing.T) {
	e := newEnv(t, "1.0.0")
	opts := userOpts(e, true)
	l := NewLayout(opts, e.dirs)
	for _, o := range []Outcome{OutcomeRolledBack, OutcomeRollbackLeft, OutcomeCancelled, OutcomePreflightFailed, OutcomeCancelledUAC, OutcomeInvalid} {
		res := &Result{Outcome: o, Op: OpInstall, AppExe: l.AppExe}
		e.r.ApplyProfileLinks(opts, res, nil)
		if exists(l.DesktopLink) || exists(l.StartMenuLink) || exists(l.StartupLink) || len(res.Left) != 0 {
			t.Fatalf("%s wrote links", o)
		}
	}
}

func TestApplyProfileLinksListsAWriteFailureAndKeepsTheOutcome(t *testing.T) {
	e := newEnv(t, "1.0.0")
	opts := userOpts(e, true)
	l := NewLayout(opts, e.dirs)
	// The temporary file path is a folder, so the Startup link cannot be written.
	if err := os.MkdirAll(l.StartupLink+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	var logs []string
	res := &Result{Outcome: OutcomeDone, Op: OpInstall, AppExe: l.AppExe}
	e.r.ApplyProfileLinks(opts, res, func(s string) { logs = append(logs, s) })
	if res.Outcome != OutcomeDone || len(res.Left) != 1 || !strings.HasPrefix(res.Left[0], "Shortcut (") {
		t.Fatalf("result = %+v", res)
	}
	if len(logs) != 1 || !strings.HasPrefix(logs[0], "shortcut: ") {
		t.Fatalf("logs = %q", logs)
	}
}
