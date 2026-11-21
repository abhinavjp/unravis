package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// A setup run that was cut off (power loss, a killed process) leaves its journal on disk (internal/setup/journal.go). The next setup
// run puts things back first, but nobody may run one. So the app looks for a journal at start and tells the person (BL-170). It starts
// nothing from a journal: anyone can write one in a folder they can create, and a hand-over to an old setup could downgrade a good
// install. The names below equal setup's; a test in internal/setup keeps them equal.
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

// FindPendingJournal looks in each folder for a journal that is not committed, has been still for journalSettle and was not tried yet
// (tried says so for a run's start time). now is the clock.
func FindPendingJournal(folders []string, now time.Time, tried func(time.Time) bool) (PendingJournal, bool) {
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
		if readJSONBig(p, &j) != nil || j.Committed || j.Started.IsZero() || (tried != nil && tried(j.Started)) {
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

// MaxJournalTries is how many starts of the app act on one cut-off run's journal. A recovery can fail without being the journal's fault
// (another setup holds the lock, the person refuses the Windows prompt), so it is tried again at the next start, a few times.
const MaxJournalTries = 3

type journalTry struct {
	Started time.Time `json:"started"`
	Tries   int       `json:"tries"`
}

type journalTries struct {
	Runs []journalTry `json:"runs"`
}

// maxJournalRuns is how many runs' counts are kept. Two journals (machine and person) can be stuck at once and each keeps its own count.
const maxJournalRuns = 8

func (t *Tracker) loadTries() journalTries {
	var s journalTries
	if readJSON(t.triedPath(), &s) != nil {
		return journalTries{}
	}
	return s
}

// JournalTries says how many times the app already acted on the run that started at started.
func (t *Tracker) JournalTries(started time.Time) int {
	for _, r := range t.loadTries().Runs {
		if r.Started.Equal(started) {
			return r.Tries
		}
	}
	return 0
}

// JournalTried says the app used up its tries for that run.
func (t *Tracker) JournalTried(started time.Time) bool {
	return t.JournalTries(started) >= MaxJournalTries
}

// MarkJournalTried counts one more try for the run that started at started.
func (t *Tracker) MarkJournalTried(started time.Time) {
	s := t.loadTries()
	found := false
	for i := range s.Runs {
		if s.Runs[i].Started.Equal(started) {
			s.Runs[i].Tries++
			found = true
		}
	}
	if !found {
		s.Runs = append(s.Runs, journalTry{Started: started, Tries: 1})
	}
	if len(s.Runs) > maxJournalRuns {
		s.Runs = s.Runs[len(s.Runs)-maxJournalRuns:]
	}
	_ = os.MkdirAll(t.StateDir, 0o700)
	_ = writeJSON(t.triedPath(), s)
}
