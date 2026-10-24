package evidence

import (
	"strings"
	"testing"
)

// OverlapShared (batch-4 M3): an event that is the only candidate of several intervals.
func TestOverlapSharedWording(t *testing.T) {
	for _, n := range []int{2, 3, 12} {
		s := OverlapShared(n)
		if err := LintCausal(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
		if strings.Contains(s, "1 candidates") || strings.Contains(s, "(Correlation)") || !strings.HasPrefix(s, "Possible overlap") {
			t.Errorf("%q", s)
		}
		if !strings.Contains(s, " "+itoa(n)+" audit intervals") {
			t.Errorf("%q does not state %d intervals", s, n)
		}
	}
}

func itoa(n int) string { return num(float64(n)) }
