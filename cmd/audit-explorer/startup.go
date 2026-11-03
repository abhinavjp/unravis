package main

import (
	"io"

	"api-audit-explorer/internal/applog"
)

// printStartup sends the start-up lines where they belong. lines is what
// startupLines returns: line 0 is the "listening at" text, line 1 is either the
// "opened in your browser" note or the one-time launch link.
//
// Line 0 always goes to logLine. When the browser was opened, line 1 goes to
// logLine too. Otherwise line 1 carries the launch link (and its one-time code),
// so it goes to console only and never to logLine: the log file must not hold a
// working code. A console write error is ignored. printed is true only when the
// link line was written to console without error.
func printStartup(lines []string, opened bool, logLine func(string), console io.Writer) (printed bool) {
	if len(lines) > 0 {
		logLine(lines[0])
	}
	if len(lines) < 2 {
		return false
	}
	if opened {
		logLine(lines[1])
		return false
	}
	if console == nil {
		return false
	}
	n, err := io.WriteString(console, lines[1]+"\n")
	return err == nil && n == len(lines[1])+1
}

// launchCodeSource is where mintLaunchCode gets a fresh code. Tests swap it for
// codes the sanitizer's long-token guess would not hide, so they can tell the
// registry from that guess.
var launchCodeSource = newLaunchCode

// mintLaunchCode makes a launch code and registers it with the log sanitizer,
// so a code that leaks into any log line is replaced by [redacted]. Only the
// last 8 codes are remembered. Every place that mints a code calls this, never
// newLaunchCode directly.
func mintLaunchCode() (string, error) {
	code, err := launchCodeSource()
	if err != nil {
		return "", err
	}
	applog.RegisterEphemeralSecret(code)
	return code, nil
}
