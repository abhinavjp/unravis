package update

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSilentReopenFallsBackToThePlainSilentArgsWhenTheMarkerIsAbsent(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old-setup.exe")
	if err := os.WriteFile(old, []byte("MZ an older setup program that does not know the reopen flag"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := silentReopenArgs(old), []string{"--silent", "--close-app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("older setup file: %v, want %v (the plain silent launch, no --reopen)", got, want)
	}
	if got, want := silentReopenArgs(filepath.Join(dir, "missing.exe")), []string{"--silent", "--close-app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing setup file: %v, want %v", got, want)
	}
	cur := filepath.Join(dir, "new-setup.exe")
	if err := os.WriteFile(cur, []byte("MZ ... "+reopenMarker+" ..."), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := silentReopenArgs(cur), []string{"--silent", "--close-app", "--reopen"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("setup file with the marker: %v, want %v", got, want)
	}
}
