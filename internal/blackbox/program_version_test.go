package blackbox

import "testing"

func TestUpdateAvailableRules(t *testing.T) {
	cases := []struct {
		recorder, program string
		want              bool
	}{
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", true},
		{"dev", "1.0.0", true},
		{"1.0.0", "dev", true},
		{"", "1.0.0", true}, // an older or stopped recorder reports nothing
		{"1.0.0", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := UpdateAvailable(c.recorder, c.program); got != c.want {
			t.Fatalf("UpdateAvailable(%q, %q) = %v, want %v", c.recorder, c.program, got, c.want)
		}
	}
}
