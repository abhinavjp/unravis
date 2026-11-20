package update

import (
	"strings"
	"testing"
)

func TestParseKeys(t *testing.T) {
	good := "# c\n\nrc " + strings.Repeat("ab", 32) + "\nstable " + strings.Repeat("cd", 32) + "\n"
	ks, err := ParseKeys(good)
	if err != nil || len(ks) != 2 || ks[0].Role != "rc" {
		t.Fatalf("%v %v", ks, err)
	}
	for _, bad := range []string{"beta " + strings.Repeat("ab", 32), "rc zz", "rc " + strings.Repeat("ab", 31), "rc"} {
		if _, err := ParseKeys(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestEmbeddedKeysParse(t *testing.T) {
	if _, err := TrustedKeys(); err != nil {
		t.Fatal(err)
	}
}
