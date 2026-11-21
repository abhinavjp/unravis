package setup

import (
	"testing"

	"api-audit-explorer/internal/update"
)

// The app (internal/update) finds a cut-off run's journal by name; its constants must equal setup's.
func TestAppAndSetupAgreeOnTheJournalPlace(t *testing.T) {
	if update.JournalFolder != MachineJournalFolder || update.JournalFile != JournalName {
		t.Fatalf("app looks for %q/%q, setup writes %q/%q", update.JournalFolder, update.JournalFile, MachineJournalFolder, JournalName)
	}
	d := Dirs{ProgramData: `C:\PD`, LocalAppData: `C:\LA`}
	f := update.JournalFolders(d.ProgramData, d.LocalAppData)
	if len(f) != 2 || f[0] != MachineJournalDir(d) || f[1] != UserJournalDir(d) {
		t.Fatalf("%v", f)
	}
}
