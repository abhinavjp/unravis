package main

import (
	"errors"
	"log"
	"os"

	"api-audit-explorer/internal/applog"
)

// Seams for tests: the real process exit and the real message box are never
// reached from a test.
var (
	exitProcess    = os.Exit
	showMessage    = showMessageBox
	stderrUnusable = stderrIsUnusable
)

const (
	fatalTitle      = "Unravis could not start"
	fatalFooter     = "Details are in the Unravis log folder."
	fatalMaxRunes   = 800
	fatalCutSuffix  = "..."
	fatalTextJoiner = "\n\n"
)

// reportFatal is the one start-up failure path: it logs err, shows a short
// message box when there is no console to read it on (a windowless build
// started from Explorer, the tray or the Startup folder), and exits with 1,
// or with 11 when the start was refused because it is elevated.
func reportFatal(err error) {
	log.Print(err)
	var ee *exitCodeError
	if errors.As(err, &ee) {
		exitProcess(ee.code) // a planned exit (attached to an owner): no message box, the caller reads the code
		return
	}
	if stderrUnusable() {
		showMessage(fatalTitle, fatalText(err))
	}
	if errors.Is(err, ErrElevated) {
		exitProcess(exitElevated)
		return
	}
	exitProcess(1)
}

// fatalText is the message box body: the error text (run through the log
// sanitizer so a secret is never shown) cut to 800 runes, a blank line and a
// pointer to the log folder.
func fatalText(err error) string {
	msg := ""
	if err != nil {
		msg = applog.Sanitize(err.Error())
	}
	if r := []rune(msg); len(r) > fatalMaxRunes {
		msg = string(r[:fatalMaxRunes]) + fatalCutSuffix
	}
	return msg + fatalTextJoiner + fatalFooter
}

// exitCodeError ends the process with a specific code and no message box. Today: exitAttached.
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string { return e.msg }
