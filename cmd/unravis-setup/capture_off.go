//go:build !capture

package main

import "api-audit-explorer/internal/setup"

// The shipped build has no --capture flag.
var captureScenarios func() map[string]setup.UIBackend

func runCapture(options, env) int { return exitInvalid }
