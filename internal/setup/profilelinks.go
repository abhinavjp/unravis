package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Shortcuts inside the user's profile (WP-74i). When setup runs elevated, the worker never writes, replaces or removes a link whose
// path is inside the user's profile (Dirs.UserProfile): a folder the user controls under an elevated process is the problem, so the
// privilege is removed instead of patched. The Desktop and Startup links are always window work; the Start-menu and Quit links are
// window work exactly when they are inside the profile (a just-for-me install). The non-elevated setup window, as the user, writes
// or removes them after the worker has committed; silent mode does the same through ApplyProfileLinks.

// The arguments of the two links that start the app in a special way (literals, so the installer does not import the app package).
const (
	startupLinkArg = "--background"
	quitLinkArg    = "--quit"
)

// maxLinkSize is the largest shortcut file setup reads; a real one is a few hundred bytes.
const maxLinkSize = 65536

// readLinkFile is the seam tests swap.
var readLinkFile = os.ReadFile

// isProfileLink reports whether the link is inside the user's profile.
func (r *Runner) isProfileLink(path string) bool { return insideFolder(path, r.Dirs.UserProfile) }

// linkTarget reads the program a shortcut file points to. A missing file is exists=false with no error. A file that is not a regular
// file, or is too large, is an error (and is not read).
func linkTarget(path string) (target string, exists bool, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	if !fi.Mode().IsRegular() {
		return "", true, errors.New(path + " is not a regular file")
	}
	if fi.Size() > maxLinkSize {
		return "", true, errors.New(path + " is too large to be a shortcut")
	}
	data, err := readLinkFile(path)
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

// refuseLinkedFolder fails when the folder that holds a link is itself a link (a junction or symbolic link). Only that folder
// matters: a redirected parent (OneDrive) is fine, and a user process only redirects its own writes. A folder that does not exist
// yet is not an error. Callers skip that one link and log; it never fails a step or a run.
func (r *Runner) refuseLinkedFolder(dir string) error { return r.refuseLink(dir) }

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
// the Desktop link (never over a foreign one) and, when startAtSignIn, the Startup link. A link in a linked folder is skipped and
// logged; a write error is remembered, the other links are still tried, and the first error is returned. onLog may be nil.
func (r *Runner) WriteProfileLinks(l Layout, startAtSignIn bool, onLog func(string)) error {
	log := func(s string) {
		if onLog != nil {
			onLog(s)
		}
	}
	var first error
	write := func(link string, quit, startup bool) {
		if err := r.refuseLinkedFolder(filepath.Dir(link)); err != nil {
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
	if l.DesktopLink != "" {
		if exists, ours := linkIsOurs(l.DesktopLink, l.AppExe); !exists || ours {
			write(l.DesktopLink, false, false)
		}
	}
	if startAtSignIn && l.StartupLink != "" {
		write(l.StartupLink, false, true)
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
