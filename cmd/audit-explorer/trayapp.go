package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/autostart"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/tray"
	"api-audit-explorer/internal/version"
)

// Menu item IDs. Zero is reserved: a separator has no ID.
const (
	idOpen      uint32 = 1
	idLogs      uint32 = 2
	idAutostart uint32 = 3
	idQuit      uint32 = 4
	idInfo      uint32 = 5 // the greyed version line
	idUpdate    uint32 = 6 // the update line; some states make it a button
	idCheck     uint32 = 7
	idWhatsNew  uint32 = 8
)

// trayDeps is everything the tray menu does, so tests can use fakes. The update fields are nil in a run with no update checker (a demo
// run); the menu then leaves those lines out.
type trayDeps struct {
	reopen           func()
	quit             func()
	openLogs         func()
	autostartAllowed func() bool
	autostartEnabled func() bool
	setAutostart     func(on bool) error
	warn             func(title, text string)

	openTo        func(to string)            // open a screen: "updates" or "whatsnew"
	update        func() server.UpdateStatus // the cached update status
	checking      func() bool                // a check the person started is running
	checkUpdates  func()                     // run one check (blocks until it ends)
	installUpdate func() error               // restart into the downloaded update (blocks)
}

// trayMenu builds the menu. "Start at sign-in" is shown only for an installed copy.
func trayMenu(d trayDeps) []tray.Item {
	var items []tray.Item
	busy := false
	if d.update != nil {
		st := d.update()
		checking := d.checking != nil && d.checking()
		v := viewUpdate(st, checking, time.Now())
		busy = checking || (st.Install != nil && (st.Install.State == "launched" || st.Install.State == "downloading"))
		items = append(items,
			tray.Item{ID: idInfo, Text: versionLine(st), Disabled: true},
			tray.Item{ID: idUpdate, Text: v.Line, Disabled: v.Act == actNone})
	} else {
		items = append(items, tray.Item{ID: idInfo, Text: clipRunes("Unravis "+version.Version, tray.MaxTextRunes), Disabled: true})
	}
	items = append(items, tray.Item{Separator: true}, tray.Item{ID: idOpen, Text: "Open Unravis"})
	if d.update != nil {
		items = append(items, tray.Item{ID: idCheck, Text: "Check for updates", Disabled: busy})
	}
	if d.openTo != nil {
		items = append(items, tray.Item{ID: idWhatsNew, Text: "What's new"})
	}
	if d.autostartAllowed() {
		items = append(items, tray.Item{ID: idAutostart, Text: "Start at sign-in", Checked: d.autostartEnabled()})
	}
	return append(items, tray.Item{ID: idLogs, Text: "Open log folder"}, tray.Item{Separator: true}, tray.Item{ID: idQuit, Text: "Quit Unravis"})
}

// trayState is the tooltip and the attention dot, polled by the tray every few seconds from the cached status.
func trayState(d trayDeps) tray.State {
	if d.update == nil {
		return tray.State{}
	}
	v := viewUpdate(d.update(), d.checking != nil && d.checking(), time.Now())
	return tray.State{Tooltip: v.Tip, Attention: v.Attention}
}

// trayOnSelect returns the menu handler. It runs on the tray thread, so it
// never blocks: a failed sign-in change, a check and an install each run on their own goroutine.
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
		case idWhatsNew:
			if d.openTo != nil {
				d.openTo("whatsnew")
			}
		case idCheck:
			if d.checkUpdates != nil {
				go d.checkUpdates()
			}
		case idUpdate:
			trayUpdatePressed(d)
		}
	}
}

// trayUpdatePressed does what the update line says, from the status as it is now.
func trayUpdatePressed(d trayDeps) {
	if d.update == nil {
		return
	}
	switch viewUpdate(d.update(), false, time.Now()).Act {
	case actInstall:
		if d.installUpdate != nil {
			go func() {
				if err := d.installUpdate(); err != nil {
					d.warn("Unravis could not start the update", err.Error())
				}
			}()
		}
	case actUpdates:
		if d.openTo != nil {
			d.openTo("updates")
		}
	}
}

// windowsFile returns the full path of a file under the Windows folder (parts are joined below it), so the
// program is never looked up through PATH or the current folder.
func windowsFile(parts ...string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(append([]string{root}, parts...)...)
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
	// The exe path and the Startup folder do not change while the app runs, so each menu action reads them once, not on every call from
	// the tray thread.
	var (
		once          sync.Once
		startExe, dir string
		startOK       bool
	)
	startup := func() (string, string, bool) {
		once.Do(func() {
			exe, err := os.Executable()
			if err != nil {
				return
			}
			d, err := autostart.StartupDir()
			if err != nil {
				return
			}
			startExe, dir, startOK = exe, d, true
		})
		return startExe, dir, startOK
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
