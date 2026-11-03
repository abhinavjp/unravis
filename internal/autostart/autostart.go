// Package autostart turns "start at sign-in" on and off by creating or removing one shortcut, Unravis.lnk, in the user's Startup
// folder. The shortcut is the single source of truth: it counts as "on" only when it points at this installed unravis.exe with the
// argument --background. Only a shortcut that is provably ours (its target is this exe) is ever replaced or removed.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/shelllink"
)

// LinkName is the file name of the Startup shortcut.
const LinkName = "Unravis.lnk"

// Arg is the argument the shortcut passes so the app starts in the background (tray only, no browser).
const Arg = "--background"

// maxLinkSize is the largest file read as a shortcut; a real one is a few hundred bytes.
const maxLinkSize = 64 << 10

const (
	exeName   = "unravis.exe"
	stateName = "setup-state.json"
)

var (
	// ErrNotInstalled means the exe is not an installed copy of Unravis, so no shortcut is made for it.
	ErrNotInstalled = errors.New("autostart: this copy of Unravis is not installed")
	// ErrNotOurs means a Unravis.lnk exists in the Startup folder that is not provably ours; it is left alone.
	ErrNotOurs = errors.New("autostart: the Startup shortcut belongs to something else")
)

// Seams for tests.
var (
	renameFile = os.Rename
	tempDir    = func() string {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Unravis")
		}
		return os.TempDir()
	}
)

// LinkPath is the full path of the Startup shortcut.
func LinkPath(startupDir string) string { return filepath.Join(startupDir, LinkName) }

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// readLink reads the shortcut at path when it is a regular file (not a link or directory) of at most 64 KiB.
func readLink(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxLinkSize {
		return nil, ErrNotOurs
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxLinkSize {
		return nil, ErrNotOurs
	}
	return data, nil
}

// isOurs reports whether data is a shortcut whose target is exe.
func isOurs(data []byte, exe string) bool {
	target, err := shelllink.ReadShortcutTarget(data)
	return err == nil && samePath(target, exe)
}

// Enabled reports whether the Startup shortcut points at exe with the --background argument. Any error gives false.
func Enabled(startupDir, exe string) bool {
	data, err := readLink(LinkPath(startupDir))
	if err != nil || !isOurs(data, exe) {
		return false
	}
	args, err := shelllink.ReadShortcutArgs(data)
	return err == nil && args == Arg
}

// Allowed reports whether exe is an installed copy of unravis.exe: an absolute path, named unravis.exe, an existing regular file, with
// setup-state.json (a regular file) in the same folder.
func Allowed(exe string) bool {
	if exe == "" || !filepath.IsAbs(exe) || !strings.EqualFold(filepath.Base(exe), exeName) {
		return false
	}
	if fi, err := os.Stat(exe); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	fi, err := os.Stat(filepath.Join(filepath.Dir(exe), stateName))
	return err == nil && fi.Mode().IsRegular()
}

// Enable creates the Startup shortcut for exe, or replaces an older one of ours. It refuses an exe that is not installed
// (ErrNotInstalled) and an existing Unravis.lnk that is not ours (ErrNotOurs).
func Enable(startupDir, exe string) error {
	if !Allowed(exe) {
		return ErrNotInstalled
	}
	if !filepath.IsAbs(startupDir) {
		return errors.New("autostart: the Startup folder must be an absolute path")
	}
	if fi, err := os.Stat(startupDir); err != nil || !fi.IsDir() {
		return errors.New("autostart: the Startup folder does not exist")
	}
	link := LinkPath(startupDir)
	if _, err := os.Lstat(link); err == nil {
		data, rerr := readLink(link)
		if rerr != nil {
			if errors.Is(rerr, ErrNotOurs) {
				return ErrNotOurs
			}
			return fmt.Errorf("autostart: read the existing shortcut: %w", rerr)
		}
		if !isOurs(data, exe) {
			return ErrNotOurs
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("autostart: check the existing shortcut: %w", err)
	}

	data, err := shelllink.Shortcut{
		Target:      exe,
		Args:        Arg,
		WorkDir:     filepath.Dir(exe),
		Description: "Unravis",
		Icon:        exe,
	}.Bytes()
	if err != nil {
		return err
	}

	dir := tempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("autostart: make the temp folder: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "unravis-lnk-*.partial")
	if err != nil {
		return fmt.Errorf("autostart: make the temp file: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmpName)
		if werr != nil {
			return fmt.Errorf("autostart: write the temp file: %w", werr)
		}
		return fmt.Errorf("autostart: write the temp file: %w", cerr)
	}
	if err := renameFile(tmpName, link); err != nil {
		// For example the temp folder is on another volume: remove the temp file and write straight to the shortcut.
		_ = os.Remove(tmpName)
		if err := os.WriteFile(link, data, 0o600); err != nil {
			_ = os.Remove(link) // our own path (new, or an older shortcut of ours): do not leave a half file
			return fmt.Errorf("autostart: write the shortcut: %w", err)
		}
	}
	return nil
}

// Disable removes the Startup shortcut when it is ours. No shortcut is not an error; one that points elsewhere (or cannot be read as
// a shortcut) is left in place and gives ErrNotOurs. It removes only that one path.
func Disable(startupDir, exe string) error {
	link := LinkPath(startupDir)
	if _, err := os.Lstat(link); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	data, err := readLink(link)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, ErrNotOurs) {
			return ErrNotOurs
		}
		return fmt.Errorf("autostart: read the shortcut: %w", err)
	}
	if !isOurs(data, exe) {
		return ErrNotOurs
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("autostart: remove the shortcut: %w", err)
	}
	return nil
}
