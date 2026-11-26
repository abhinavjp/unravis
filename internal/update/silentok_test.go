package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSilentNoUAC(t *testing.T) {
	// The answer must not depend on the services of the PC the test runs on (the owner's PC has the Blackbox service).
	old := servicesInstalled
	servicesInstalled = func(...string) bool { return false }
	defer func() { servicesInstalled = old }()
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

func TestSilentNoUACNeedsNoServiceLeftOver(t *testing.T) {
	old := servicesInstalled
	servicesInstalled = func(...string) bool { return true }
	defer func() { servicesInstalled = old }()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "setup-state.json"), []byte(`{"scope":"user"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if SilentNoUAC(dir) {
		t.Error("a service left from an earlier install for everyone still needs approval")
	}
}
