//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// showMessageBox shows a modal error box. The result is ignored.
func showMessageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(title)
	windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND)
}

// stderrIsUnusable reports that nothing can read what is written to stderr.
func stderrIsUnusable() bool {
	if os.Stderr == nil {
		return true
	}
	return !stdHandleUsable(windows.Handle(os.Stderr.Fd()))
}
