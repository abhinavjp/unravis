package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCanRecoverFindsTheHelpLineAnywhereInALargeFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 3<<20)
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"at the start":  {RecoverMarker + big, true},
		"in the middle": {big + RecoverMarker + big, true},
		// the text straddles the 1 MiB read size
		"across a read": {big[:1<<20-10] + RecoverMarker + big, true},
		"at the end":    {big + RecoverMarker, true},
		"absent":        {big + "a setup run that was cut off", false},
		"empty":         {"", false},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := SetupCanRecover(p); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
	if SetupCanRecover(filepath.Join(dir, "missing")) || SetupCanRecover(dir) {
		t.Fatal("a missing file or a folder cannot recover")
	}
}
