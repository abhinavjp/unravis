//go:build windows

package dotnetdiag

import "testing"

// On the CI runner the job is an administrator or a plain user, never SYSTEM.
func TestRunAsClasses(t *testing.T) {
	switch got := RunAs(); got {
	case "elevated", "user":
	default:
		t.Fatalf("RunAs() = %q on the runner, want elevated or user", got)
	}
}
