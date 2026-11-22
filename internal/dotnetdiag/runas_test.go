package dotnetdiag

import "testing"

func TestRunAsIsAFixedWord(t *testing.T) {
	switch got := RunAs(); got {
	case "system", "elevated", "user", "unknown":
	default:
		t.Fatalf("RunAs() = %q", got)
	}
}
