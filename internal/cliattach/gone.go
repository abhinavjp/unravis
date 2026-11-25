package cliattach

import (
	"time"

	"api-audit-explorer/internal/locator"
)

// ProcessGone tells whether the process pid that was created at started has ended. A pid that now belongs to a process created at
// another time is not the one asked about, so it counts as gone.
func ProcessGone(pid int, started time.Time) bool {
	got, err := locator.SystemProber().ProcessStart(pid)
	if err != nil {
		return true
	}
	if started.IsZero() {
		return false
	}
	d := got.Sub(started)
	return d > time.Second || d < -time.Second
}
