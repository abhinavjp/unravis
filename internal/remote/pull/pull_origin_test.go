package pull

import "testing"

func TestSafeNameKeepsTmpAway(t *testing.T) {
	if got := safeName("pull-record.json.tmp", map[string]bool{}); got == "pull-record.json.tmp" {
		t.Fatalf("a .tmp name was kept: %s", got)
	}
}
