package sealedfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// MoveNoReplace moves a file to a name that is free, and refuses (os.ErrExist) when something is already there: neither file changes.
func TestMoveNoReplaceMovesToAFreeName(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(from, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MoveNoReplace(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("the source is still there: %v", err)
	}
	if got, _ := os.ReadFile(to); string(got) != "one" { //nolint:gosec // test reads its own temp file
		t.Fatalf("moved content = %q", got)
	}
}

func TestMoveNoReplaceRefusesAnExistingTarget(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(from, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MoveNoReplace(from, to); !errors.Is(err, os.ErrExist) {
		t.Fatalf("move onto an existing file = %v, want os.ErrExist", err)
	}
	if got, _ := os.ReadFile(from); string(got) != "new" { //nolint:gosec // test reads its own temp file
		t.Fatalf("the source changed: %q", got)
	}
	if got, _ := os.ReadFile(to); string(got) != "old" { //nolint:gosec // test reads its own temp file
		t.Fatalf("the existing target was replaced: %q", got)
	}
}
