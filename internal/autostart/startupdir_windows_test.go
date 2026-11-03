//go:build windows

package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupDirIsAnExistingAbsoluteStartupFolder(t *testing.T) {
	dir, err := StartupDir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("not absolute: %q", dir)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("not an existing folder: %q (%v)", dir, err)
	}
	if !strings.EqualFold(filepath.Base(dir), "Startup") {
		t.Fatalf("base name %q", filepath.Base(dir))
	}
}

func TestStartupDirDoesNotCreateAnything(t *testing.T) {
	dir, err := StartupDir()
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		es, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		return len(es)
	}
	before := count()
	if _, err := StartupDir(); err != nil {
		t.Fatal(err)
	}
	_ = Enabled(dir, os.Args[0])
	if after := count(); after != before {
		t.Fatalf("entry count changed from %d to %d", before, after)
	}
}
