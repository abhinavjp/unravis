package setup

import (
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/update"
)

// A name alone does not make a folder the installer's: ".previous" or a timestamped working folder that holds the person's own files
// is somebody's folder. It blocks the program folder (and is named), and no step ever deletes it.

func TestWorkFolderOwnedNeedsOurNamesAndOurFiles(t *testing.T) {
	root := t.TempDir()
	good := map[string][]string{
		".setup-staging-20261122-101010": {"unravis.exe", "unravis-setup.exe", "beside-app-unravis-blackbox.exe"},
		".setup-backup-20261122-101010":  {"1-unravis.exe", "12-Unravis.lnk"},
		update.PreviousDir:               {update.MarkerName, update.MarkerName + ".tmp", update.PreviousSetupName},
	}
	for dir, files := range good {
		for _, f := range files {
			writeFile(t, filepath.Join(root, dir, f), "x")
		}
		if !workFolderOwned(root, dir) {
			t.Errorf("%s with %v was not recognised as ours", dir, files)
		}
	}
	bad := map[string]string{
		".setup-staging-20261122-111111": "thesis.docx",
		".setup-backup-20261122-111111":  "thesis.docx", // no counter in front
		".setup-backup-20261122-151515":  "1-tax-return.pdf", // a counter, but not a file setup ever backs up
		"x" + update.PreviousDir:         "unravis-setup.exe",
	}
	for dir, f := range bad {
		writeFile(t, filepath.Join(root, dir, f), "x")
		if workFolderOwned(root, dir) {
			t.Errorf("%s holding %s was taken for ours", dir, f)
		}
	}
	// the files Explorer adds on its own do not make a folder theirs
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-141414", "unravis.exe"), "x")
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-141414", "Desktop.ini"), "x")
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-141414", "thumbs.db"), "x")
	if !workFolderOwned(root, ".setup-staging-20261122-141414") {
		t.Error("desktop.ini and Thumbs.db made a staging folder look foreign")
	}
	// a nested folder, or one foreign file among ours, is enough to make it theirs
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-121212", "unravis.exe"), "x")
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-121212", "sub", "a.txt"), "x")
	writeFile(t, filepath.Join(root, update.PreviousDir+"2", "x"), "x")
	if workFolderOwned(root, ".setup-staging-20261122-121212") {
		t.Error("a staging folder with a sub folder was taken for ours")
	}
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-131313", "unravis.exe"), "x")
	writeFile(t, filepath.Join(root, ".setup-staging-20261122-131313", "photos.zip"), "x")
	if workFolderOwned(root, ".setup-staging-20261122-131313") {
		t.Error("a staging folder with one foreign file was taken for ours")
	}
}

// The program-folder check names a .previous or timestamped folder that holds somebody's files.
func TestForeignFilesInsideAWorkFolderNameBlockTheProgramFolder(t *testing.T) {
	for _, name := range []string{update.PreviousDir, ".setup-staging-20261122-101010", ".setup-backup-20261122-101010"} {
		e := newEnv(t, "1.2.0")
		writeFile(t, filepath.Join(e.userOpts().Location, name, "tax-return.pdf"), "theirs")
		c, ok := userFolderCheck(e)
		if !ok || c.Status != CheckFailed || !strings.Contains(c.Detail, name) {
			t.Errorf("%s: got %+v (%v)", name, c, ok)
		}
	}
}

// Setup that goes on over leftovers never deletes a folder with a staging name that holds the person's file.
func TestClearStaleStagingKeepsAFolderWithForeignContent(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	mine := filepath.Join(o.Location, ".setup-staging-20200101-000000", "thesis.docx")
	ours := filepath.Join(o.Location, ".setup-staging-20200202-000000", "unravis.exe")
	writeFile(t, mine, "theirs")
	writeFile(t, ours, "x")
	p, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.clearStaleStaging()
	if !exists(mine) {
		t.Error("a folder with the person's file in it was deleted")
	}
	if exists(filepath.Dir(ours)) {
		t.Error("a real stale staging folder was left")
	}
}

// Upgrading never deletes a ".previous" that holds the person's own files, and never records it for the rollback to delete.
func TestUpgradeLeavesAForeignPreviousFolderAlone(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	writeFile(t, e.r.SetupExe, "setup program 1.2.0 "+rollbackFlags)
	e.mustRun(OpInstall, o, UninstallOptions{})
	prev := filepath.Join(NewLayout(o, e.dirs).InstallDir, update.PreviousDir)
	theirs := filepath.Join(prev, "notes.txt")
	writeFile(t, theirs, "theirs")
	e.upgradeTo("1.3.0")
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	if !exists(theirs) {
		t.Fatal("the person's file in .previous was deleted by an upgrade")
	}
	if exists(filepath.Join(prev, update.MarkerName)) {
		t.Error("a marker was written into a folder that is not ours")
	}
}

// Uninstall removes our working folders but leaves a folder with the person's files in it, and says it kept files.
func TestUninstallLeavesAForeignPreviousFolderAlone(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	theirs := filepath.Join(l.InstallDir, update.PreviousDir, "notes.txt")
	writeFile(t, theirs, "theirs")
	ours := filepath.Join(l.InstallDir, ".setup-backup-20200101-000000", "1-unravis.exe")
	writeFile(t, ours, "x")
	e.mustRun(OpUninstall, Options{}, UninstallOptions{})
	if !exists(theirs) {
		t.Fatal("the person's file in .previous was deleted by the uninstall")
	}
	if exists(filepath.Dir(ours)) {
		t.Error("our own backup folder was left behind")
	}
}
