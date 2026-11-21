package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// A setup run that was cut off (power loss, a killed process) leaves its journal on disk (internal/setup/journal.go). The next setup
// run puts things back first, but nobody may run one. So the app looks for a journal at start and, when the old setup kept by this very
// update is there, hands over to it (BL-170). The names below equal setup's; a test in internal/setup keeps them equal.
const (
	// JournalFolder is the folder name under ProgramData (an elevated run) or the local app data (a run without administrator rights).
	JournalFolder = "Unravis Setup"
	// JournalFile is the journal's file name.
	JournalFile = "journal.json"
	// journalSettle is how old a journal must be before it counts as cut off: a setup that is still running writes it all the time.
	journalSettle = 3 * time.Minute
)

// PendingJournal is a journal of a run that did not finish.
type PendingJournal struct {
	Path string
	// Version is the version that run was installing.
	Version string
	// Started identifies the run; the app tries a run's journal once.
	Started time.Time
}

// FindPendingJournal looks in each folder for a journal that is not committed and has been still for journalSettle. now is the clock.
func FindPendingJournal(folders []string, now time.Time) (PendingJournal, bool) {
	for _, dir := range folders {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, JournalFile)
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < journalSettle {
			continue
		}
		var j struct {
			Version   string    `json:"version"`
			Started   time.Time `json:"started"`
			Committed bool      `json:"committed"`
		}
		if readJSONBig(p, &j) != nil || j.Committed {
			continue
		}
		return PendingJournal{Path: p, Version: j.Version, Started: j.Started}, true
	}
	return PendingJournal{}, false
}

// JournalFolders lists where a journal can be: the machine folder first, then the person's.
func JournalFolders(programData, localAppData string) []string {
	var out []string
	for _, base := range []string{programData, localAppData} {
		if base != "" {
			out = append(out, filepath.Join(base, JournalFolder))
		}
	}
	return out
}

// readJSONBig reads a journal: it lists every change a run made, so it may be much larger than the small state files.
func readJSONBig(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 8<<20 {
		return os.ErrInvalid
	}
	return json.NewDecoder(f).Decode(v)
}

func (t *Tracker) triedPath() string { return filepath.Join(t.StateDir, "journal-tried.json") }

// JournalTried says the app already tried to recover from the run that started at started. It tries a run once, so a setup that cannot
// finish (or a person who refuses its administrator prompt) never makes the app loop.
func (t *Tracker) JournalTried(started time.Time) bool {
	var s struct {
		Started time.Time `json:"started"`
	}
	return readJSON(t.triedPath(), &s) == nil && s.Started.Equal(started)
}

// MarkJournalTried records that the app tried the run that started at started.
func (t *Tracker) MarkJournalTried(started time.Time) {
	_ = os.MkdirAll(t.StateDir, 0o700)
	_ = writeJSON(t.triedPath(), struct {
		Started time.Time `json:"started"`
	}{started})
}
