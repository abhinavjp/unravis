package setup

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/update"
)

// The working folders inside a program folder: a setup's staging and backup folders, and the folder the updater keeps the old setup in.
// A NAME that looks like one of ours proves nothing, because a person can have a folder called ".previous" with their own files in it.
// So a working folder counts as ours only when the name fits AND everything in it is a plain file this installer or the updater writes.
// One look at the inside decides both questions: "does this make the program folder somebody's own" and "may setup delete it".

// backupCounter splits a backup file's name into the run's counter and the name of the file it replaced (txn.go backupPath).
var backupCounter = regexp.MustCompile(`^\d+-(.+)$`)

// backupEntry says name is a file a backup folder holds: the counter, a dash, and the name of a file setup replaced or removed, which is
// one of the programs, a shortcut, the setup state, or a Blackbox settings, key or marker file. A copy being made carries ".part" until
// it is complete.
func backupEntry(name string) bool {
	m := backupCounter.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	orig := strings.TrimSuffix(m[1], ".part")
	if stagedName(orig) {
		return true
	}
	for _, own := range []string{ShortcutName, QuitShortcutName, ReleasesShortcutName, LegacyReleasesShortcutName, StateFileName, blackbox.KeyFileName, blackbox.ConfigFileName, blackbox.ConfigFileName + ".bak", blackbox.InstalledExeName, blackbox.ManagedMarkerName, blackbox.StoppedByUserMarkerName} {
		if strings.EqualFold(orig, own) {
			return true
		}
	}
	return false
}

// stagedName says name is a file a staging folder holds: the programs setup places, and the second Blackbox copy it stages for the beside-app place.
func stagedName(name string) bool {
	for _, own := range []string{AppExeName, KeeperExeName, BlackboxExeName, ReleasesExeName, LegacyReleasesExeName, CLIExeName, CLIInstalledName, HomestretchExeName, HSExeName, SetupExeName, "beside-app-" + BlackboxExeName} {
		if strings.EqualFold(name, own) {
			return true
		}
	}
	return false
}

// previousName is a file in the kept-version folder: the old setup, its marker, and the marker's temporary file (internal/update/health.go).
func previousName(name string) bool {
	for _, own := range []string{update.MarkerName, update.MarkerName + ".tmp", update.PreviousSetupName} {
		if strings.EqualFold(name, own) {
			return true
		}
	}
	return false
}

// shellFile says name is a file Windows Explorer may add to any folder it has shown; it does not make a folder somebody's own.
func shellFile(name string) bool {
	return strings.EqualFold(name, "desktop.ini") || strings.EqualFold(name, "Thumbs.db")
}

// workFolderOwned reports whether name, a folder inside parent, is a working folder this installer or the updater made (or the bin folder): the name has the
// form of one, and it holds nothing but plain files with the names those programs write (no folder, no link, no other file). A folder
// that cannot be read is not shown to be ours.
func workFolderOwned(parent, name string) bool {
	var allowed func(string) bool
	switch {
	case stampName.MatchString(name) && strings.HasPrefix(name, stagingPrefix):
		allowed = stagedName
	case stampName.MatchString(name) && strings.HasPrefix(name, backupPrefix):
		allowed = backupEntry
	case strings.EqualFold(name, update.PreviousDir):
		allowed = previousName
	case strings.EqualFold(name, CLIDirName):
		allowed = func(n string) bool {
			return strings.EqualFold(n, CLIInstalledName) || strings.EqualFold(n, HomestretchExeName) || strings.EqualFold(n, HSExeName)
		}
	default:
		return false
	}
	ents, err := os.ReadDir(filepath.Join(parent, name))
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.Type().IsRegular() || (!allowed(e.Name()) && !shellFile(e.Name())) {
			return false
		}
	}
	return true
}

// errNotOurWorkFolder is why removeWorkFolder left a folder alone.
var errNotOurWorkFolder = errors.New("it holds files the installer did not make, so it was left as it is")

// removeWorkFolder deletes the working folder name inside parent, and only when workFolderOwned says it is ours and it is a real
// folder (not a link or a junction). A folder that is not there is not an error. Anything else is left untouched and reported.
func removeWorkFolder(parent, name string) error {
	full := filepath.Join(parent, name)
	fi, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsDir() || !workFolderOwned(parent, name) {
		return errNotOurWorkFolder
	}
	return os.RemoveAll(full)
}
