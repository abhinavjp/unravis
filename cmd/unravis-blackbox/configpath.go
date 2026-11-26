package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Path rules for `config validate <file>`. The command can run as an administrator or SYSTEM, so the path must name an
// ordinary file on a local fixed drive and nothing that makes Windows reach out (a network share, a device, a pipe, the
// \??\ NT form) or that opens a device by its reserved name.

var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// windowsPathOK is the pure string rule for a Windows path: exactly `X:\...`, no stream (second colon), no ".." and no
// reserved device name in any element. It is a function of the string only, so it is tested on every platform.
func windowsPathOK(path string) bool {
	if len(path) < 4 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return false
	}
	c := path[0]
	if !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') {
		return false
	}
	rest := path[3:]
	if strings.Contains(rest, ":") || strings.ContainsAny(path, "\x00?*<>|\"") {
		return false
	}
	for _, el := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if el == ".." {
			return false
		}
		name := strings.ToUpper(strings.TrimRight(el, ". "))
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if reservedDeviceNames[strings.TrimRight(name, " ")] {
			return false
		}
	}
	return true
}

// plainFolders checks that no folder on the way to path is a link or a junction (a link in a parent could point at a
// network share, and Lstat of the file alone would not see it).
func plainFolders(path string) bool {
	dir := filepath.Dir(path)
	for {
		fi, err := os.Lstat(dir)
		if err != nil || fi.Mode().Type() != os.ModeDir {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return true
		}
		dir = parent
	}
}

// validatePathAllowed applies the rules for this platform.
func validatePathAllowed(path string) bool {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\x00") {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	return windowsPathOK(path) && driveIsFixed(path[0]) && plainFolders(path)
}
