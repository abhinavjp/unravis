package localci

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetPausedMakesAndRemovesThePauseFileTheAgentHonours(t *testing.T) {
	dir := t.TempDir()
	if IsPaused(dir) {
		t.Fatal("a fresh state folder is not paused")
	}
	if err := SetPaused(dir, true); err != nil {
		t.Fatal(err)
	}
	if !IsPaused(dir) {
		t.Fatal("not paused after SetPaused(true)")
	}
	if _, err := os.Stat(filepath.Join(dir, "PAUSE")); err != nil {
		t.Fatalf("the file must be named PAUSE, the one serve looks at: %v", err)
	}
	if err := SetPaused(dir, false); err != nil {
		t.Fatal(err)
	}
	if IsPaused(dir) {
		t.Fatal("still paused after SetPaused(false)")
	}
}

func TestSetPausedIsRepeatableAndKeepsAHandMadePauseFile(t *testing.T) {
	dir := t.TempDir()
	if err := SetPaused(dir, false); err != nil {
		t.Fatalf("resuming a folder that is not paused: %v", err)
	}
	if err := os.WriteFile(PausePath(dir), []byte("by hand"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetPaused(dir, true); err != nil {
		t.Fatalf("pausing a paused folder: %v", err)
	}
	b, _ := os.ReadFile(PausePath(dir))
	if string(b) != "by hand" {
		t.Fatalf("a hand-made pause file was rewritten: %q", b)
	}
	if err := SetPaused(dir, false); err != nil {
		t.Fatal(err)
	}
}

func TestSetPausedCreatesAMissingStateFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "state")
	if err := SetPaused(dir, true); err != nil || !IsPaused(dir) {
		t.Fatalf("paused=%v err=%v", IsPaused(dir), err)
	}
}
