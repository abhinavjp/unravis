package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"api-audit-explorer/internal/shelllink"
)

// Shortcuts inside the user's profile (WP-74i). When setup runs elevated, the worker never writes, replaces or removes a link whose
// path is inside the user's profile (Dirs.UserProfile): a folder the user controls under an elevated process is the problem, so the
// privilege is removed instead of patched. The Desktop and Startup links are always window work; the Start-menu and Quit links are
// window work exactly when they are inside the profile (a just-for-me install). The non-elevated setup window, as the user, writes
// or removes them after the worker has committed; silent mode does the same through ApplyProfileLinks.

// The arguments of the two links that start the app in a special way. The Startup one is the same constant the app writes
// (internal/shelllink, shared with internal/autostart), so the two cannot drift.
const (
	startupLinkArg = shelllink.BackgroundArg
	quitLinkArg    = "--quit"
)

// readLinkFile is the seam tests swap.
var readLinkFile = os.ReadFile

// isProfileLink reports whether the link is inside the user's profile.
func (r *Runner) isProfileLink(path string) bool { return insideFolder(path, r.Dirs.UserProfile) }

// linkTarget reads the program a shortcut file points to. A missing file is exists=false with no error. A file that is not a regular
// file, or is too large, is an error (and is not read).
func linkTarget(path string) (target string, exists bool, err error) {
	data, err := shelllink.ReadFile(path, readLinkFile)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	target, err = ReadShortcutTarget(data)
	if err != nil {
		return "", true, err
	}
	return target, true, nil
}

// linkIsOurs reports whether a shortcut exists and points to exe. A file that cannot be read or parsed "exists, not ours".
func linkIsOurs(path, exe string) (exists, ours bool) {
	target, exists, err := linkTarget(path)
	if !exists {
		return false, false
	}
	if err != nil {
		return true, false
	}
	return true, samePath(target, exe)
}

// mayWriteLink reports whether the link at path may be written: it is not there yet, or it is ours (points to exe). A shortcut of the
// same name that belongs to the user, or cannot be read, is never overwritten.
func mayWriteLink(path, exe string) bool {
	exists, ours := linkIsOurs(path, exe)
	return !exists || ours
}

// profileLinkData builds a shortcut file as the plan does.
func profileLinkData(l Layout, quit, startup bool) ([]byte, error) {
	s := Shortcut{Target: l.AppExe, WorkDir: l.InstallDir, Icon: l.AppExe, Description: "Unravis"}
	switch {
	case quit:
		s.Description, s.Args = "Quit Unravis", quitLinkArg
	case startup:
		s.Args = startupLinkArg
	}
	return s.Bytes()
}

// writeLinkFile replaces the link with data: a temporary file next to it, then a rename. Any failure removes the temporary file.
func writeLinkFile(link string, data []byte) (err error) {
	if err = os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	tmp := link + ".tmp"
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err = os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// WriteProfileLinks writes the links the window owns, as the user: the Start-menu and Quit links when they are inside the profile,
// the Desktop link and, when startAtSignIn, the Startup link (neither over a foreign one). A link in a linked folder is skipped and
// logged; a write error is remembered, the other links are still tried, and the first error is returned. onLog may be nil.
func (r *Runner) WriteProfileLinks(l Layout, startAtSignIn bool, onLog func(string)) error {
	log := func(s string) {
		if onLog != nil {
			onLog(s)
		}
	}
	var first error
	write := func(link string, quit, startup bool) {
		if err := r.refuseLink(filepath.Dir(link)); err != nil {
			log("shortcut skipped: " + err.Error())
			return
		}
		data, err := profileLinkData(l, quit, startup)
		if err == nil {
			err = writeLinkFile(link, data)
		}
		if err != nil {
			if first == nil {
				first = fmt.Errorf("%s: %w", link, err)
			}
			return
		}
	}
	if l.StartMenuLink != "" && r.isProfileLink(l.StartMenuLink) {
		write(l.StartMenuLink, false, false)
	}
	if l.QuitLink != "" && r.isProfileLink(l.QuitLink) {
		write(l.QuitLink, true, false)
	}
	if l.DesktopLink != "" && mayWriteLink(l.DesktopLink, l.AppExe) {
		write(l.DesktopLink, false, false)
	}
	if startAtSignIn && l.StartupLink != "" {
		if mayWriteLink(l.StartupLink, l.AppExe) {
			write(l.StartupLink, false, true)
		} else {
			log("shortcut skipped: " + l.StartupLink + " belongs to something else")
		}
	}
	return first
}

// RemoveProfileLinks removes the links the window owns that are ours (they point to this install's program). A link that points
// elsewhere, or cannot be read, is kept. It returns one line per problem; empty when all is well.
func (r *Runner) RemoveProfileLinks(l Layout) []string {
	var lines []string
	remove := func(link string) {
		if link == "" {
			return
		}
		exists, ours := linkIsOurs(link, l.AppExe)
		if !exists {
			return
		}
		if !ours {
			lines = append(lines, "Shortcut "+link+" (kept: it points to another program)")
			return
		}
		if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
			lines = append(lines, "Shortcut "+link+" ("+err.Error()+")")
		}
	}
	if r.isProfileLink(l.StartMenuLink) {
		remove(l.StartMenuLink)
	}
	if r.isProfileLink(l.QuitLink) {
		remove(l.QuitLink)
	}
	remove(l.DesktopLink)
	remove(l.StartupLink)
	return lines
}
