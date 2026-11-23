package ownerlease

import (
	"path/filepath"
	"testing"
)

func TestPathNames(t *testing.T) {
	dir := t.TempDir()
	if got, want := Path(dir, false), filepath.Join(dir, "owner.lock"); got != want {
		t.Errorf("Path normal = %q, want %q", got, want)
	}
	if got, want := Path(dir, true), filepath.Join(dir, "owner-demo.lock"); got != want {
		t.Errorf("Path demo = %q, want %q", got, want)
	}
}

func TestAcquireRelease(t *testing.T) {
	release, err := Acquire(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // idempotent
}
