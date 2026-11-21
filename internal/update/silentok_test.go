package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSilentNoUAC(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"just for me, no Blackbox", `{"scope":"user","location":"x"}`, true},
		{"explicit null Blackbox", `{"scope":"user","blackbox":null}`, true},
		{"just for me, with Blackbox", `{"scope":"user","blackbox":{"name":"b"}}`, false},
		{"everyone on the PC", `{"scope":"machine"}`, false},
		{"not JSON", `nope`, false},
		{"empty", ``, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "setup-state.json"), []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := SilentNoUAC(dir); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	if SilentNoUAC(t.TempDir()) {
		t.Error("no state file said yes")
	}
}
