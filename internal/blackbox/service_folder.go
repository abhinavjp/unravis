package blackbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ReasonFolderUnsafe: the recordings folder chosen is not a safe place for the
// SYSTEM recorder to write (SEC-03, SEC-04). Nothing was changed.
const ReasonFolderUnsafe Reason = "folder-unsafe"

// FolderInfo is what OSEnv learned about a recordings folder before a Blackbox
// is placed or moved into it.
type FolderInfo struct {
	Exists bool
	// Reparse: the folder, or a folder above it, is a link, a junction or a
	// mounted volume. The service would write somewhere else than the person sees.
	Reparse bool
	// OwnerTrusted: the folder is owned by Administrators, SYSTEM or
	// TrustedInstaller, and nobody else. A folder another account made stays that
	// account's, even when it is empty: an owner can always change the
	// permissions back, and can keep a handle that survives the change.
	OwnerTrusted bool
	// Fixed: the folder is on a local hard drive.
	Fixed bool
	// Entries are the names inside the folder.
	Entries []string
}

// ErrFolderChanged: the recordings folder was swapped, is a link, or belongs to
// another account at the moment it was locked down. Nothing was changed.
var ErrFolderChanged = errors.New("the folder changed while it was being locked down, or belongs to another account")

// SecureCreator is implemented by an Env that can make a folder with its owner
// and permissions set in the create call (SEC-FIX-06). An Env without it (a test
// fake) gets a plain folder and SecureDir afterwards.
type SecureCreator interface {
	// CreateSecureDir makes dir, one level, and fails if the name exists.
	CreateSecureDir(dir string, usersRead bool) error
}

// FolderGuard is implemented by an Env that judges a recordings folder before a
// Blackbox is placed or moved into it: FolderProblem is the sentence to show
// (cause and next step), or "" when the folder may be used. An Env without it (a
// test fake) accepts any folder.
type FolderGuard interface {
	FolderProblem(folder string) string
}

// shortNameRe matches the tail of an 8.3 short name such as PROGRA~1.
var shortNameRe = regexp.MustCompile(`~[0-9]+$`)

// isShortName says whether one path segment looks like an 8.3 short name: a base
// name (before the first dot) of 8 characters or fewer that ends in ~ and
// digits. A longer name such as logs~2024 cannot be one, so it is allowed.
func isShortName(part string) bool {
	base := part
	if i := strings.IndexByte(part, '.'); i >= 0 {
		base = part[:i]
	}
	return len(base) <= 8 && shortNameRe.MatchString(base)
}

// newFolderProblem says why folder must not become a new recordings folder, by
// its text alone: a network share, a short (8.3) name that can hide the real
// place, a Windows or program folder, or somebody's profile or web root. The
// rules are stricter than Validate (which also runs on a folder that is already
// in use, and must not stop an installed service from starting). Empty when the
// text is fine.
func newFolderProblem(folder string) string {
	p := strings.ReplaceAll(strings.TrimSpace(folder), "/", `\`)
	if strings.HasPrefix(p, `\\`) {
		return "a network share"
	}
	p = filepath.Clean(p)
	vol := filepath.VolumeName(p)
	rest := strings.Trim(strings.TrimPrefix(p, vol), `\`)
	if vol == "" || rest == "" {
		return "" // not a drive path or a root: Validate says so
	}
	// SEC-FIX-11: Windows drops trailing dots and spaces from every name, so
	// "Users." is the Users folder. Compare the names as Windows will use them.
	var parts []string
	for _, part := range strings.Split(rest, `\`) {
		if part = strings.TrimRight(part, ". "); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	for _, part := range parts {
		if isShortName(part) {
			return "a short (8.3) name, which can hide the real folder"
		}
	}
	switch strings.ToLower(parts[0]) {
	case "users", "documents and settings":
		return "a user profile folder"
	case "inetpub":
		return "a web server folder"
	case "windows", "program files", "program files (x86)", "perflogs", "recovery", "boot", "msocache", "$windows.~bt", "$windows.~ws", "config.msi":
		return "a Windows or program folder"
	}
	return ""
}

// isBlackboxFile says whether name is something the recorder writes.
func isBlackboxFile(folder, name string) bool {
	return ValidFileName(name) || name == ManifestName || strings.HasPrefix(name, ManifestName+".") ||
		name == SnapshotsDir || name == filepath.Base(SQLDataDir(folder)) || isStoreTemp(name)
}

// folderVerdict is the rule for a folder that was inspected. It returns the
// sentence to show (cause and next step) or "" when the folder may be used.
func folderVerdict(folder string, fi FolderInfo) string {
	next := " Choose a new, empty folder on a local drive, such as " + DefaultFolder + "."
	if fi.Reparse {
		return folder + " is, or sits inside, a link to another place, so the recordings would not be where you expect." + next
	}
	if !fi.Fixed {
		return folder + " is not on a local hard drive, so the recorder service cannot rely on it." + next
	}
	if len(fi.Entries) > 0 {
		ours := false
		for _, n := range fi.Entries {
			if isBlackboxFile(folder, n) {
				ours = true
				break
			}
		}
		if !ours {
			return folder + " already holds files that are not Blackbox recordings, and the Blackbox would change who can open them." + next
		}
	}
	// SEC-FIX-05: an existing folder, empty or not, must belong to Administrators
	// or SYSTEM. An empty folder another account made keeps that owner, who can
	// hold a handle that outlives the permission change. A folder that does not
	// exist yet is made by the Blackbox itself, with the right owner from the start.
	if fi.Exists && !fi.OwnerTrusted {
		return folder + " belongs to another account, so it may have been changed by someone else. If it is yours, take ownership of it as an administrator first (Properties, Security, Advanced, Owner)." + next
	}
	return ""
}

// FolderProblem is the real FolderGuard: the text rules, then what is on disk.
func (e OSEnv) FolderProblem(folder string) string {
	if why := newFolderProblem(folder); why != "" {
		return fmt.Sprintf("%s is %s, so the Blackbox will not record there. Choose a new, empty folder on a local drive, such as %s.", folder, why, DefaultFolder)
	}
	fi, err := e.InspectFolder(folder)
	if err != nil {
		return fmt.Sprintf("Windows would not let the Blackbox look at %s, so nothing was changed. Choose another folder, or check that the drive is connected.", folder)
	}
	return folderVerdict(folder, fi)
}

// folderProblem asks the Env, when it can judge folders.
func (m *Manager) folderProblem(folder string) string {
	if g, ok := m.Env.(FolderGuard); ok {
		return g.FolderProblem(folder)
	}
	return ""
}
