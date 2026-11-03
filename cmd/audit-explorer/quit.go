package main

import (
	"errors"
	"fmt"
	"io"
	"runtime"

	"api-audit-explorer/internal/instance"
)

// hostOS is the operating system the flag rules check. Tests swap it.
var hostOS = runtime.GOOS

const quitTitle = "Unravis"

// quitSignal asks the running copy of this user to quit. It sends one of two
// fixed signal kinds through a per-user, per-session kernel event: there is no
// network route, so no remote or unauthenticated caller can reach it, and the
// instance package refuses an event owned by another account.
func quitSignal(k instance.Kind) error {
	n, err := instance.CurrentNames(instance.DefaultApp)
	if err != nil {
		return err
	}
	return instance.Signal(n, k)
}

// runQuit asks the running copy to quit and returns the process exit code.
// Success is silent. Anything to say goes to w, or into a message box when
// there is no console to read it on.
func runQuit(w io.Writer, signal func(instance.Kind) error) int {
	err := signal(instance.Quit)
	if err == nil {
		return 0
	}
	var text string
	code := 1
	switch {
	case errors.Is(err, instance.ErrNoInstance):
		text, code = "Unravis is not running.", 0
	case errors.Is(err, instance.ErrAccessDenied):
		text = "Unravis is running under another Windows account or as administrator. Quit it from there."
	default:
		text = fmt.Sprintf("Unravis could not be asked to quit: %v", err)
	}
	if stderrUnusable() {
		showMessage(quitTitle, text)
	} else {
		fmt.Fprintln(w, text)
	}
	return code
}
