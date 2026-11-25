package localci

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Read helpers for dashboards. Everything here only reads: it never creates a folder or a file in the store.

// LedgerPath is the file the status reporter remembers its posts in (see DedupReporter).
func LedgerPath(stateDir string) string { return filepath.Join(stateDir, "status", "ledger.json") }

// LedgerRow is one remembered post: the last state and description sent for one commit and context.
type LedgerRow struct {
	Repo        string    `json:"repo"`
	SHA         string    `json:"sha"`
	Context     string    `json:"context"`
	State       string    `json:"state"`
	Description string    `json:"description"`
	At          time.Time `json:"at"`
}

// ReadLedger lists the remembered posts, newest first. A missing file is an empty list.
func ReadLedger(stateDir string) ([]LedgerRow, error) {
	b, err := os.ReadFile(LedgerPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]ledgerEntry
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	out := make([]LedgerRow, 0, len(m))
	for k, e := range m {
		row := LedgerRow{State: e.State, Description: e.Description, At: e.At}
		// The key is "<repo>@<sha> <context>".
		if i := strings.IndexByte(k, ' '); i > 0 {
			row.Context = k[i+1:]
			k = k[:i]
		}
		if i := strings.IndexByte(k, '@'); i >= 0 {
			row.Repo, row.SHA = k[:i], k[i+1:]
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Context < out[j].Context
	})
	return out, nil
}

// ListRequests reads the queue folders without creating them (OpenQueue does). Missing folders are empty.
func ListRequests(stateDir string) ([]Request, error) {
	st, err := os.Stat(QueueDir(stateDir))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !st.IsDir()) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	q := &Queue{Dir: QueueDir(stateDir)}
	var out []Request
	for _, folder := range []string{StateQueued, StateRunning, "done"} {
		rs, err := q.folderOf(folder)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		out = append(out, rs...)
	}
	return out, nil
}

// RunLabels are the run id endings a request's run folder can have: the id is "<yyyymmdd-hhmmss>-<label>".
func (r Request) RunLabels() []string {
	stem := strings.TrimSuffix(strings.TrimSuffix(r.Workflow, ".yml"), ".yaml")
	if r.Kind == KindPR {
		return []string{"pr" + strconv.Itoa(r.Number) + "-" + stem}
	}
	if r.Kind == "" || stem == "" {
		return nil
	}
	return []string{r.Kind + "-" + slug(r.Ref) + "-" + stem, "push-" + slug(r.Ref) + "-" + stem, "schedule-" + slug(r.Ref) + "-" + stem}
}

// IDTime reads the time a run id starts with ("20261125-093314-...", UTC).
func IDTime(id string) (time.Time, bool) {
	if len(id) < 15 {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102-150405", id[:15])
	return t, err == nil
}

// RunFolders lists the run folders of the store, newest first, with whether each has its run.json (a run that is
// still going has none: the record and the index line are written when it ends).
func (s *Store) RunFolders() []RunFolder {
	ents, err := os.ReadDir(s.RunsPath())
	if err != nil {
		return nil
	}
	var out []RunFolder
	for _, e := range ents {
		if !e.IsDir() || !ValidRunID(e.Name()) {
			continue
		}
		_, serr := os.Stat(filepath.Join(s.RunsPath(), e.Name(), "run.json"))
		out = append(out, RunFolder{ID: e.Name(), Recorded: serr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// RunFolder is one folder under runs/.
type RunFolder struct {
	ID       string
	Recorded bool // run.json exists: the run ended
}

// LiveRunID finds the folder of the run a running request is working on. The queue does not know the run id until the
// run ends, so it is found by its name and its start time: the newest folder with no run.json whose id ends with one
// of the request's labels and that was made at or after the claim. Empty when there is none (yet).
func (s *Store) LiveRunID(r Request) string {
	labels := r.RunLabels()
	if len(labels) == 0 {
		return ""
	}
	for _, f := range s.RunFolders() { // newest first
		if f.Recorded {
			continue
		}
		t, ok := IDTime(f.ID)
		if !ok {
			continue
		}
		if !r.ClaimedAt.IsZero() && t.Before(r.ClaimedAt.Add(-time.Minute)) {
			continue
		}
		for _, l := range labels {
			if idHasLabel(f.ID, l) {
				return f.ID
			}
		}
	}
	return ""
}

// idHasLabel is true for "<stamp>-<label>" and "<stamp>-<label>-<n>" (the id was taken twice in a second).
func idHasLabel(id, label string) bool {
	if len(id) < 16 || id[15] != '-' {
		return false
	}
	rest := id[16:]
	if rest == label {
		return true
	}
	n, ok := strings.CutPrefix(rest, label+"-")
	if !ok || n == "" {
		return false
	}
	_, err := strconv.Atoi(n)
	return err == nil
}

// ComparePath is the file the compare command saves its last report to (with --save), for dashboards to show.
func ComparePath(stateDir string) string { return filepath.Join(stateDir, "compare", "latest.json") }

// SaveCompare writes the report where ReadCompare finds it (a temp file renamed over the target).
func SaveCompare(stateDir string, rep *CompareReport) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	p := ComparePath(stateDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ReadCompare reads the saved report. A missing file is (nil, nil): nobody has run compare --save here yet.
func ReadCompare(stateDir string) (*CompareReport, error) {
	b, err := os.ReadFile(ComparePath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rep CompareReport
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}
