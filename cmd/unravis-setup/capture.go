//go:build capture

package main

import (
	"fmt"

	"api-audit-explorer/internal/setup"
)

// The screenshot build (go build -tags capture) adds --capture: the setup page with the fake scenarios of setup.CaptureScenarios,
// picked per request by the X-Setup-Scenario header. It changes nothing on the PC and is never shipped.

var captureScenarios = setup.CaptureScenarios

func runCapture(o options, e env) int {
	sc := captureScenarios()
	be, ok := sc["fresh"]
	if !ok {
		fmt.Fprintln(e.stderr, "unravis-setup: the capture scenarios have no \"fresh\" one")
		return exitInvalid
	}
	e.idleAfter = 0
	e.startApp = func(string) error { return nil }
	e.openFolder = func(string) error { return nil }
	return serve(o, e, be, sc, "", func(string) {})
}
