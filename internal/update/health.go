package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Rollback after a bad update (docs/auto-updater/SPEC.md section 9).
//
// After an upgrade the installer keeps the OLD setup program in a ".previous" folder beside the program files, with a marker that
// says which version it holds and its hash. That old setup carries the old program files, so going back is the same proven
// install engine run with --allow-downgrade; there is no second restore code.
//
// The new app counts how many times it started without staying up. Three starts in a row that did not last a minute (and did not
// end by the person closing it) mean the update is bad: the app opens the old setup, puts the version on a skip list so it is
// not offered again, and stops. A rollback is only ever started on this PC and always goes to the version the marker names.

const (
	// PreviousDir is the folder, inside the program folder, that holds the old setup.
	PreviousDir = ".previous"
	// MarkerName is the marker inside PreviousDir.
	MarkerName = "pending.json"
	// PreviousSetupName is the old setup program inside PreviousDir.
	PreviousSetupName = "unravis-setup.exe"

	// HealthyAfter is how long the new version must stay up to count as working.
	HealthyAfter = 60 * time.Second
	// MaxFailedStarts is how many unconfirmed starts are allowed before the update is given up.
	MaxFailedStarts = 3

	maxStateFile = 8 << 10
	maxSkips     = 20
)

// Marker is written by the installer after an upgrade.
type Marker struct {
	From        string    `json:"from"`
	To          string    `json:"to"`
	SetupSHA256 string    `json:"setupSha256"`
	At          time.Time `json:"at"`
}

// Valid says the marker names two versions and the hash of the old setup.
func (m Marker) Valid() bool {
	f, errF := ParseVersion(m.From)
	t, errT := ParseVersion(m.To)
	return errF == nil && errT == nil && f.String() == m.From && t.String() == m.To && sha256RE.MatchString(m.SetupSHA256)
}

// WriteMarker writes the marker into dir (the PreviousDir folder) through a temporary file, so a reader never sees half of it.
func WriteMarker(dir string, m Marker) error {
	if !m.Valid() {
		return errors.New("the update marker is not valid")
	}
	return writeJSON(filepath.Join(dir, MarkerName), m)
}

// ReadMarker reads and checks the marker in dir.
func ReadMarker(dir string) (Marker, error) {
	var m Marker
	if err := readJSON(filepath.Join(dir, MarkerName), &m); err != nil {
		return Marker{}, err
	}
	if !m.Valid() {
		return Marker{}, errors.New("the update marker is not valid")
	}
	return m, nil
}

func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// A temporary file with its own name in the same folder: two writers never share one, a stale file from a killed run cannot block
	// this one, and the rename stays on one volume. Sync before the rename so a power cut cannot leave an empty file under the real name.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	// CreateTemp makes the file private to the writer; these files are read by the person's own app after the installer (a different
	// account, possibly elevated) wrote them, so keep the permissions the plain write gave.
	_ = f.Chmod(0o644)
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxStateFile {
		return errors.New("the file is too large")
	}
	return json.NewDecoder(f).Decode(v)
}

// Action is what the app does at start.
type Action int

const (
	// ActNone: no update is being watched. Start as usual.
	ActNone Action = iota
	// ActWatch: this is the first days of a new version. Start, and call Confirm after HealthyAfter.
	ActWatch
	// ActRecover: the new version failed to stay up three times. Go back.
	ActRecover
)

// Tracker watches the first starts of a freshly installed version. StateDir is the per-user folder it writes its counter and the
// skip list to (the program folder may not be writable by this person); PrevDir is the PreviousDir folder the installer wrote.
type Tracker struct {
	StateDir string
	PrevDir  string
	// Version is the running version, as the installer wrote it into the marker (without a leading v).
	Version string

	mu sync.Mutex
}

type healthState struct {
	To        string    `json:"to"`
	At        time.Time `json:"at"`
	Starts    int       `json:"starts"`
	Confirmed bool      `json:"confirmed"`
	// Recovered says the hand-over to the old setup was tried for this marker. It is tried once: a person who closes the setup window
	// (or cannot pass its administrator prompt) must still get the app to start.
	Recovered bool `json:"recovered,omitempty"`
}

func (t *Tracker) statePath() string { return filepath.Join(t.StateDir, "health.json") }

// SetupPath is the old setup program the rollback starts.
func (t *Tracker) SetupPath() string { return filepath.Join(t.PrevDir, PreviousSetupName) }

// marker returns the marker when it names the version that is running.
func (t *Tracker) marker() (Marker, bool) {
	m, err := ReadMarker(t.PrevDir)
	if err != nil || m.To != t.Version {
		return Marker{}, false
	}
	return m, true
}

func (t *Tracker) load(m Marker) healthState {
	var s healthState
	if err := readJSON(t.statePath(), &s); err != nil || s.To != m.To || !s.At.Equal(m.At) {
		return healthState{To: m.To, At: m.At}
	}
	return s
}

// Begin is called once at start, before the app does anything else. It counts this start.
func (t *Tracker) Begin() (Action, Marker) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.marker()
	if !ok {
		return ActNone, Marker{}
	}
	s := t.load(m)
	if s.Confirmed || s.Recovered {
		return ActNone, m
	}
	s.Starts++
	_ = os.MkdirAll(t.StateDir, 0o755)
	_ = writeJSON(t.statePath(), s)
	if s.Starts > MaxFailedStarts {
		return ActRecover, m
	}
	return ActWatch, m
}

// MarkRecovered records that the rollback is being handed over, so no later start tries it again.
func (t *Tracker) MarkRecovered() {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.marker()
	if !ok {
		return
	}
	s := t.load(m)
	s.Recovered = true
	_ = os.MkdirAll(t.StateDir, 0o755)
	_ = writeJSON(t.statePath(), s)
}

// Confirm says the new version stayed up long enough. The counter stops mattering.
func (t *Tracker) Confirm() {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.marker()
	if !ok {
		return
	}
	s := t.load(m)
	s.Confirmed = true
	_ = writeJSON(t.statePath(), s)
}

// CleanExit is called when the person closes the app on purpose before it was confirmed: a short visit is not a crash.
func (t *Tracker) CleanExit() {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.marker()
	if !ok {
		return
	}
	s := t.load(m)
	if s.Confirmed || s.Starts == 0 {
		return
	}
	s.Starts--
	_ = writeJSON(t.statePath(), s)
}

type skipFile struct {
	Versions []string `json:"versions"`
}

func (t *Tracker) skipPath() string { return filepath.Join(t.StateDir, "skip.json") }

// Skip puts a version on the list of versions that are not offered again. Only that exact version is skipped, so the next one is offered.
func (t *Tracker) Skip(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var f skipFile
	_ = readJSON(t.skipPath(), &f)
	for _, v := range f.Versions {
		if v == version {
			return
		}
	}
	f.Versions = append(f.Versions, version) // oldest first; the oldest go when the list is full
	if len(f.Versions) > maxSkips {
		f.Versions = f.Versions[len(f.Versions)-maxSkips:]
	}
	_ = os.MkdirAll(t.StateDir, 0o755)
	_ = writeJSON(t.skipPath(), f)
}

// Skipped says whether version is on the skip list.
func (t *Tracker) Skipped(version string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	var f skipFile
	if readJSON(t.skipPath(), &f) != nil {
		return false
	}
	for _, v := range f.Versions {
		if v == version {
			return true
		}
	}
	return false
}
