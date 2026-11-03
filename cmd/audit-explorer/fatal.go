package main

import (
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
// started from Explorer, the tray or the Startup folder), and exits with 1.
func reportFatal(err error) {
	log.Print(err)
	if stderrUnusable() {
		showMessage(fatalTitle, fatalText(err))
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
