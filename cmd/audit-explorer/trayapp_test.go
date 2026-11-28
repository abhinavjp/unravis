package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/tray"
	"api-audit-explorer/internal/version"
)

type trayCalls struct {
	reopen, quit, logs int
	setTo              []bool
	enabled            bool
	allowed            bool
	setErr             error
	warns              chan [2]string

	opened      []string
	checkingNow bool
	checks      atomic.Int32
	installs    atomic.Int32
	installErr  error
}

func newTrayCalls(allowed bool) *trayCalls {
	return &trayCalls{allowed: allowed, warns: make(chan [2]string, 4)}
}

func (c *trayCalls) deps() trayDeps {
	return trayDeps{
		reopen:           func() { c.reopen++ },
		quit:             func() { c.quit++ },
		openLogs:         func() { c.logs++ },
		autostartAllowed: func() bool { return c.allowed },
		autostartEnabled: func() bool { return c.enabled },
		setAutostart: func(on bool) error {
			c.setTo = append(c.setTo, on)
			if c.setErr == nil {
				c.enabled = on
			}
			return c.setErr
		},
		warn: func(title, text string) { c.warns <- [2]string{title, text} },
	}
}

func TestTrayMenuWithNoUpdaterIsTheShortMenu(t *testing.T) {
	items := trayMenu(newTrayCalls(true).deps())
	want := []tray.Item{
		{ID: idInfo, Text: "Unravis " + version.Version, Disabled: true},
		{Separator: true},
		{ID: idOpen, Text: "Open Unravis"},
		{ID: idAutostart, Text: "Start at sign-in"},
		{ID: idLogs, Text: "Open log folder"},
		{Separator: true},
		{ID: idQuit, Text: "Quit Unravis"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("menu = %+v", items)
	}
	if err := tray.ValidateItems(items); err != nil {
		t.Fatal(err)
	}
}

// withUpdater adds the update parts to the fake deps.
func (c *trayCalls) withUpdater(st server.UpdateStatus) trayDeps {
	d := c.deps()
	d.openTo = func(to string) { c.opened = append(c.opened, to) }
	d.update = func() server.UpdateStatus { return st }
	d.checking = func() bool { return c.checkingNow }
	d.checkUpdates = func() { c.checks.Add(1) }
	d.installUpdate = func() error { c.installs.Add(1); return c.installErr }
	return d
}

func TestTrayMenuWithAnUpdaterShowsTheVersionAndTheUpdateLine(t *testing.T) {
	st := server.UpdateStatus{Status: "latest", Current: "1.4.0-rc.62", Channel: "rc"}
	items := trayMenu(newTrayCalls(false).withUpdater(st))
	want := []tray.Item{
		{ID: idInfo, Text: "Unravis 1.4.0-rc.62 (rc channel)", Disabled: true},
		{ID: idUpdate, Text: "Up to date", Disabled: true},
		{Separator: true},
		{ID: idOpen, Text: "Open Unravis"},
		{ID: idCheck, Text: "Check for updates"},
		{ID: idWhatsNew, Text: "What's new"},
		{ID: idLogs, Text: "Open log folder"},
		{Separator: true},
		{ID: idQuit, Text: "Quit Unravis"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("menu = %+v", items)
	}
	if err := tray.ValidateItems(items); err != nil {
		t.Fatal(err)
	}
}

func TestTrayMenuUpdateLineIsAButtonOnlyWhenItHasAnAction(t *testing.T) {
	cases := []struct {
		name     string
		st       server.UpdateStatus
		disabled bool
	}{
		{"latest", server.UpdateStatus{Status: "latest", Current: "1"}, true},
		{"available", server.UpdateStatus{Status: "available", Current: "1", Latest: "2"}, false},
		{"ready", server.UpdateStatus{Status: "available", Current: "1", Latest: "2", CanInstall: true, Install: &server.InstallInfo{State: "ready", Version: "2"}}, false},
		{"downloading", server.UpdateStatus{Status: "available", Current: "1", Latest: "2", Install: &server.InstallInfo{State: "downloading", Version: "2"}}, true},
	}
	for _, tc := range cases {
		items := trayMenu(newTrayCalls(false).withUpdater(tc.st))
		for _, it := range items {
			if it.ID == idUpdate && it.Disabled != tc.disabled {
				t.Errorf("%s: disabled = %v, want %v", tc.name, it.Disabled, tc.disabled)
			}
			if it.ID == idCheck && tc.name == "downloading" && !it.Disabled {
				t.Errorf("%s: Check for updates should be greyed while downloading", tc.name)
			}
		}
	}
}

func TestTrayMenuGreysCheckForUpdatesWhileACheckRuns(t *testing.T) {
	c := newTrayCalls(false)
	c.checkingNow = true
	for _, it := range trayMenu(c.withUpdater(server.UpdateStatus{Status: "latest", Current: "1"})) {
		if it.ID == idCheck && !it.Disabled {
			t.Fatal("Check for updates is clickable while a check runs")
		}
		if it.ID == idUpdate && it.Text != "Checking for updates..." {
			t.Fatalf("update line = %q", it.Text)
		}
	}
}

func TestTrayStateCarriesTheTooltipAndTheDot(t *testing.T) {
	if got := trayState(newTrayCalls(false).deps()); got != (tray.State{}) {
		t.Fatalf("no updater: %+v", got)
	}
	ready := server.UpdateStatus{Status: "available", Current: "1.0.0", Latest: "1.1.0", CanInstall: true, Install: &server.InstallInfo{State: "ready", Version: "1.1.0"}}
	got := trayState(newTrayCalls(false).withUpdater(ready))
	if !got.Attention || !strings.Contains(got.Tooltip, "restart") {
		t.Fatalf("ready: %+v", got)
	}
	got = trayState(newTrayCalls(false).withUpdater(server.UpdateStatus{Status: "latest", Current: "1.0.0"}))
	if got.Attention || got.Tooltip != "Unravis 1.0.0 · up to date" {
		t.Fatalf("latest: %+v", got)
	}
}

func TestTrayUpdateLineActions(t *testing.T) {
	ready := server.UpdateStatus{Status: "available", Current: "1", Latest: "2", CanInstall: true, Install: &server.InstallInfo{State: "ready", Version: "2"}}
	c := newTrayCalls(false)
	trayOnSelect(c.withUpdater(ready))(idUpdate)
	waitUntil(t, func() bool { return c.installs.Load() == 1 })
	if len(c.opened) != 0 {
		t.Fatalf("install opened %v", c.opened)
	}

	avail := server.UpdateStatus{Status: "available", Current: "1", Latest: "2"}
	c = newTrayCalls(false)
	trayOnSelect(c.withUpdater(avail))(idUpdate)
	if !reflect.DeepEqual(c.opened, []string{"updates"}) || c.installs.Load() != 0 {
		t.Fatalf("available: opened=%v installs=%d", c.opened, c.installs.Load())
	}

	c = newTrayCalls(false)
	trayOnSelect(c.withUpdater(server.UpdateStatus{Status: "latest", Current: "1"}))(idUpdate)
	if len(c.opened) != 0 || c.installs.Load() != 0 {
		t.Fatalf("latest did something: opened=%v installs=%d", c.opened, c.installs.Load())
	}
}

func TestTrayInstallFailureWarnsInPlainWords(t *testing.T) {
	ready := server.UpdateStatus{Status: "available", Current: "1", Latest: "2", CanInstall: true, Install: &server.InstallInfo{State: "ready", Version: "2"}}
	c := newTrayCalls(false)
	c.installErr = errors.New("another change is in progress")
	trayOnSelect(c.withUpdater(ready))(idUpdate)
	select {
	case w := <-c.warns:
		if w[0] != "Unravis could not start the update" || !strings.Contains(w[1], "another change") {
			t.Fatalf("warning = %v", w)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no warning")
	}
}

func TestTrayCheckAndWhatsNewDispatch(t *testing.T) {
	c := newTrayCalls(false)
	sel := trayOnSelect(c.withUpdater(server.UpdateStatus{Status: "latest", Current: "1"}))
	sel(idCheck)
	waitUntil(t, func() bool { return c.checks.Load() == 1 })
	sel(idWhatsNew)
	if !reflect.DeepEqual(c.opened, []string{"whatsnew"}) {
		t.Fatalf("opened = %v", c.opened)
	}
	// With no updater the same ids do nothing and do not panic.
	trayOnSelect(newTrayCalls(false).deps())(idCheck)
	trayOnSelect(newTrayCalls(false).deps())(idWhatsNew)
	trayOnSelect(newTrayCalls(false).deps())(idUpdate)
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTrayMenuHidesStartAtSignInForAnUninstalledCopy(t *testing.T) {
	items := trayMenu(newTrayCalls(false).deps())
	for _, it := range items {
		if it.ID == idAutostart {
			t.Fatal("start at sign-in shown for an uninstalled copy")
		}
	}
	if len(items) != 6 {
		t.Fatalf("got %d items, want 6", len(items))
	}
	if err := tray.ValidateItems(items); err != nil {
		t.Fatal(err)
	}
}

func TestTrayMenuTicksStartAtSignInWhenEnabled(t *testing.T) {
	c := newTrayCalls(true)
	c.enabled = true
	for _, it := range trayMenu(c.deps()) {
		if it.ID == idAutostart && !it.Checked {
			t.Fatal("start at sign-in not ticked")
		}
	}
}

func TestTrayOnSelectDispatches(t *testing.T) {
	cases := []struct {
		id                      uint32
		reopen, quit, logs, set int
	}{
		{idOpen, 1, 0, 0, 0},
		{idLogs, 0, 0, 1, 0},
		{idAutostart, 0, 0, 0, 1},
		{idQuit, 0, 1, 0, 0},
		{99, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		c := newTrayCalls(true)
		trayOnSelect(c.deps())(tc.id)
		if c.reopen != tc.reopen || c.quit != tc.quit || c.logs != tc.logs || len(c.setTo) != tc.set {
			t.Fatalf("id %d: reopen=%d quit=%d logs=%d set=%d", tc.id, c.reopen, c.quit, c.logs, len(c.setTo))
		}
	}
}

func TestTrayToggleTurnsAutostartOnThenOff(t *testing.T) {
	c := newTrayCalls(true)
	sel := trayOnSelect(c.deps())
	sel(idAutostart)
	sel(idAutostart)
	if !reflect.DeepEqual(c.setTo, []bool{true, false}) {
		t.Fatalf("setAutostart got %v", c.setTo)
	}
}

func TestTrayToggleErrorWarnsWithoutBlockingTheTray(t *testing.T) {
	c := newTrayCalls(true)
	c.setErr = errors.New("boom-text")
	d := c.deps()
	release := make(chan struct{})
	got := make(chan string, 1)
	d.warn = func(_, text string) {
		got <- text
		<-release
	}
	done := make(chan struct{})
	go func() { trayOnSelect(d)(idAutostart); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("trayOnSelect blocked on the warning")
	}
	select {
	case text := <-got:
		if !strings.Contains(text, "boom-text") {
			t.Fatalf("warning = %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no warning")
	}
	close(release)
}

func TestOpenLogsRunsExplorerWithTheLogFolder(t *testing.T) {
	old := runProgram
	t.Cleanup(func() { runProgram = old })
	var calls atomic.Int32
	var gotName, gotArg string
	runProgram = func(name, arg string) error {
		calls.Add(1)
		gotName, gotArg = name, arg
		return nil
	}
	d := productionTrayDeps(func() {}, func() {}, func(string, string) {})
	d.openLogs()
	if calls.Load() != 1 {
		t.Fatalf("runner called %d times", calls.Load())
	}
	if gotName != windowsFile("explorer.exe") {
		t.Fatalf("program = %q", gotName)
	}
	if gotArg != applog.DefaultDir(os.Getenv) {
		t.Fatalf("folder = %q", gotArg)
	}
}

func TestWindowsFileUsesTheWindowsFolder(t *testing.T) {
	t.Setenv("SystemRoot", `C:\X`)
	if got, want := windowsFile("explorer.exe"), filepath.Join(`C:\X`, "explorer.exe"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	t.Setenv("SystemRoot", "")
	if got, want := windowsFile("explorer.exe"), filepath.Join(`C:\Windows`, "explorer.exe"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProductionDepsHideStartAtSignInForATestBinary(t *testing.T) {
	d := productionTrayDeps(func() {}, func() {}, func(string, string) {})
	if d.autostartAllowed() {
		t.Fatal("a test binary must not be an installed copy")
	}
}

// A line that said "see Updates" must never restart the app because the status moved to ready before the press.
func TestTrayPressDoesNothingWhenTheStatusMovedOnSinceTheMenuWasBuilt(t *testing.T) {
	avail := server.UpdateStatus{Status: "available", Current: "1", Latest: "2"}
	ready := server.UpdateStatus{Status: "available", Current: "1", Latest: "2", CanInstall: true, Install: &server.InstallInfo{State: "ready", Version: "2"}}
	c := newTrayCalls(false)
	d := c.withUpdater(avail)
	d.shownAct = new(atomic.Int32)
	trayMenu(d) // the menu opens showing "see Updates"
	d.update = func() server.UpdateStatus { return ready }
	trayOnSelect(d)(idUpdate)
	time.Sleep(50 * time.Millisecond)
	if c.installs.Load() != 0 || len(c.opened) != 0 {
		t.Fatalf("a stale press acted: installs=%d opened=%v", c.installs.Load(), c.opened)
	}
	trayMenu(d) // a fresh menu shows the restart line, so the press now installs
	trayOnSelect(d)(idUpdate)
	waitUntil(t, func() bool { return c.installs.Load() == 1 })
}
