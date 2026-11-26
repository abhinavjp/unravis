//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The dashboard opens the browser through rundll32. The program must be named by its full path under the Windows system folder, never
// by a bare name that PATH decides.
func TestBrowserCommandUsesRundll32FromTheSystemFolder(t *testing.T) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatalf("GetSystemDirectory: %v", err)
	}
	const link = "http://127.0.0.1:1/?k=v"
	c := browserCommand("windows", link)
	if !filepath.IsAbs(c.Path) || !filepath.IsAbs(c.Args[0]) {
		t.Fatalf("the program must be an absolute path, got Path %q Args[0] %q", c.Path, c.Args[0])
	}
	if !strings.EqualFold(filepath.Dir(c.Path), filepath.Clean(sys)) {
		t.Fatalf("the program must sit directly in the system folder %q, got %q", sys, c.Path)
	}
	if !strings.EqualFold(filepath.Base(c.Path), "rundll32.exe") {
		t.Fatalf("the program must be rundll32.exe, got %q", c.Path)
	}
	if len(c.Args) != 3 || c.Args[1] != "url.dll,FileProtocolHandler" || c.Args[2] != link {
		t.Fatalf("unexpected arguments %q", c.Args)
	}
}
