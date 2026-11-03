package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/autostart"
	"api-audit-explorer/internal/tray"
)

// Menu item IDs. Zero is reserved: a separator has no ID.
const (
	idOpen      uint32 = 1
	idLogs      uint32 = 2
	idAutostart uint32 = 3
	idQuit      uint32 = 4
)

// trayDeps is everything the tray menu does, so tests can use fakes.
type trayDeps struct {
	reopen           func()
	quit             func()
	openLogs         func()
	autostartAllowed func() bool
	autostartEnabled func() bool
	setAutostart     func(on bool) error
	warn             func(title, text string)
}

// trayMenu builds the menu. "Start at sign-in" is shown only for an installed copy.
func trayMenu(d trayDeps) []tray.Item {
	items := []tray.Item{
		{ID: idOpen, Text: "Open Unravis"},
		{ID: idLogs, Text: "Open log folder"},
	}
	if d.autostartAllowed() {
		items = append(items, tray.Item{ID: idAutostart, Text: "Start at sign-in", Checked: d.autostartEnabled()})
	}
	return append(items, tray.Item{Separator: true}, tray.Item{ID: idQuit, Text: "Quit Unravis"})
}

// trayOnSelect returns the menu handler. It runs on the tray thread, so it
// never blocks: a failed sign-in change is reported on its own goroutine.
func trayOnSelect(d trayDeps) func(uint32) {
	return func(id uint32) {
		switch id {
		case idOpen:
			d.reopen()
		case idLogs:
			d.openLogs()
		case idAutostart:
			if err := d.setAutostart(!d.autostartEnabled()); err != nil {
				go d.warn("Unravis", "Could not change start at sign-in: "+err.Error())
			}
		case idQuit:
			d.quit()
		}
	}
}

// windowsFile returns the full path of a file in the Windows folder, so the
// program is never looked up through PATH or the current folder.
func windowsFile(name string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, name)
}

// productionTrayDeps wires the menu to the real log folder and Startup folder.
// runProgram is injectable for tests; production starts the program directly,
// with no shell.
func productionTrayDeps(reopen, quit func(), warn func(title, text string)) trayDeps {
	return trayDepsWith(reopen, quit, warn, runProgram)
}

var runProgram = func(name string, arg string) error {
	return exec.Command(name, arg).Start()
}

func trayDepsWith(reopen, quit func(), warn func(title, text string), run func(name, arg string) error) trayDeps {
	startup := func() (exe, dir string, ok bool) {
		exe, err := os.Executable()
		if err != nil {
			return "", "", false
		}
		dir, err = autostart.StartupDir()
		if err != nil {
			return "", "", false
		}
		return exe, dir, true
	}
	return trayDeps{
		reopen: reopen,
		quit:   quit,
		warn:   warn,
		openLogs: func() {
			_ = run(windowsFile("explorer.exe"), applog.DefaultDir(os.Getenv))
		},
		autostartAllowed: func() bool {
			exe, _, ok := startup()
			return ok && autostart.Allowed(exe)
		},
		autostartEnabled: func() bool {
			exe, dir, ok := startup()
			return ok && autostart.Enabled(dir, exe)
		},
		setAutostart: func(on bool) error {
			exe, dir, ok := startup()
			if !ok {
				return autostart.ErrNotInstalled
			}
			if on {
				return autostart.Enable(dir, exe)
			}
			return autostart.Disable(dir, exe)
		},
	}
}
