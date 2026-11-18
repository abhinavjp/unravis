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
		{"1.2.0", "1.1.9", false},    // Unravis rolled back, newer recorder kept: no downgrade offer
		{"v1.10.0", "v1.9.0", false}, // numeric, not text, order
		{"1.9.0", "v1.10.0", true},
		{"1.0", "1.0.0", false},       // missing parts count as zero
		{"1.0.0-rc1", "1.0.0", false}, // a suffix does not make it older
		{"abc", "1.0.0", true},        // not comparable: any difference offers it
	}
	for _, c := range cases {
		if got := UpdateAvailable(c.recorder, c.program); got != c.want {
			t.Fatalf("UpdateAvailable(%q, %q) = %v, want %v", c.recorder, c.program, got, c.want)
		}
	}
}
