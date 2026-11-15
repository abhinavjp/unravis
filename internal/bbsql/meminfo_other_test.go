//go:build !windows

package bbsql

import "testing"

// budgetWorkingSetBytes: C13's memory bound is a Windows working-set claim, and
// psapi is not here. Reporting no number (rather than a Go heap stand-in) keeps
// the gate honest: the caller skips, so a non-Windows run never passes the
// memory bound on evidence about something else.
func budgetWorkingSetBytes(*testing.T) (int64, bool) { return 0, false }
