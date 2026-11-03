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
	"api-audit-explorer/internal/tray"
)

type trayCalls struct {
	reopen, quit, logs int
	setTo              []bool
	enabled            bool
	allowed            bool
	setErr             error
	warns              chan [2]string
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

func TestTrayMenuHasTheFourEntries(t *testing.T) {
	items := trayMenu(newTrayCalls(true).deps())
	want := []tray.Item{
		{ID: idOpen, Text: "Open Unravis"},
		{ID: idLogs, Text: "Open log folder"},
		{ID: idAutostart, Text: "Start at sign-in"},
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

func TestTrayMenuHidesStartAtSignInForAnUninstalledCopy(t *testing.T) {
	items := trayMenu(newTrayCalls(false).deps())
	for _, it := range items {
		if it.ID == idAutostart {
			t.Fatal("start at sign-in shown for an uninstalled copy")
		}
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4", len(items))
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
