// Package settingsfile keeps the code-mode settings in code-mode.json (BL-574, spec 04 D8).
//
// The file is the owner's switch for code mode: it is OFF when the file is missing, and also when it is damaged, too large, not a plain
// file or holds a field this build does not know. A damaged file can never turn code mode ON; the owner has to save the settings again.
// The file is read again at the start of every run (Settings), so switching off takes effect on the next run without a restart. It is a
// few bytes, so there is no cache to go stale.
package settingsfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/sealedfile"
)

// FileName is the name of the settings file inside the app's config folder.
const FileName = "code-mode.json"

// MaxBytes caps what is read. The real file is about 80 bytes.
const MaxBytes = 4 << 10

// ErrCorrupt means the file exists but cannot be trusted (bad JSON, an unknown field, too large, not a plain file). The settings read
// with it are the zero value: code mode off.
var ErrCorrupt = errors.New("code-mode settings file is damaged")

// file is the JSON shape. Field names are the wire names of the settings screen too.
type file struct {
	Enabled       bool `json:"enabled"`
	AllowDegraded bool `json:"allowDegraded"`
	KeepResults   bool `json:"keepResults"`
}

// Store is one settings file. The zero value is not usable; use New.
type Store struct {
	path string
	mu   sync.Mutex // one writer at a time; readers need no lock because a write is an atomic replace
	// replace moves the finished temp file over the settings file. A test replaces it to make a write fail halfway.
	replace func(from, to string) error
}

// New returns the store for the file in dir (the app's config folder). Nothing is read or created yet.
func New(dir string) *Store {
	return &Store{path: filepath.Join(dir, FileName), replace: sealedfile.Replace}
}

// Path is where the file lives, for tests. Callers must not put it in a log line or a reply.
func (s *Store) Path() string { return s.path }

// Load reads the settings. A missing file is not an error: it is the default (all off). Any other problem returns the zero Settings
// (off) and an error; ErrCorrupt for content that cannot be trusted, the OS error for a read that failed.
func (s *Store) Load() (codemode.Settings, error) {
	fi, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return codemode.Settings{}, nil
	}
	if err != nil {
		return codemode.Settings{}, err
	}
	// A link, a folder or a device is never read: only a plain file the app wrote itself.
	if !fi.Mode().IsRegular() || fi.Size() > MaxBytes {
		return codemode.Settings{}, ErrCorrupt
	}
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return codemode.Settings{}, nil
		}
		return codemode.Settings{}, err
	}
	defer func() { _ = f.Close() }()
	// The checks are made again on the open handle, so a swap between the Lstat and the Open (a link or another file put in the place)
	// is caught: what is read is the file that was checked.
	hfi, err := f.Stat()
	if err != nil {
		return codemode.Settings{}, err
	}
	if !hfi.Mode().IsRegular() || hfi.Size() > MaxBytes || !os.SameFile(fi, hfi) || !ownerOnly(hfi) {
		return codemode.Settings{}, ErrCorrupt
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return codemode.Settings{}, err
	}
	if len(data) > MaxBytes {
		return codemode.Settings{}, ErrCorrupt
	}
	var in file
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return codemode.Settings{}, ErrCorrupt
	}
	// Exactly one JSON value: trailing data (a second object, stray text) means the file is not what Save wrote.
	if _, err := dec.Token(); err != io.EOF {
		return codemode.Settings{}, ErrCorrupt
	}
	return codemode.Settings{Enabled: in.Enabled, AllowDegraded: in.AllowDegraded, KeepResults: in.KeepResults}, nil
}

// Settings is codemode.Config.Settings: the settings as the file says now, or all off when it cannot be read. It never returns an error
// and never turns anything on that the file did not clearly say.
func (s *Store) Settings() codemode.Settings {
	set, err := s.Load()
	if err != nil {
		return codemode.Settings{}
	}
	return set
}

// Save writes the settings: a temp file in the same folder (owner-only before any byte is written), flushed, then moved over the file.
// A failure at any step leaves the old file as it was and no temp file behind.
func (s *Store) Save(set codemode.Settings) error {
	data, err := json.Marshal(file{Enabled: set.Enabled, AllowDegraded: set.AllowDegraded, KeepResults: set.KeepResults})
	if err != nil {
		return err
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A folder, a link or anything else in the file's place is not replaced silently.
	if fi, err := os.Lstat(s.path); err == nil && !fi.Mode().IsRegular() {
		return ErrCorrupt
	}
	tmp, err := os.CreateTemp(dir, ".code-mode-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op once the replace has moved it
	// The owner-only rights are set while the file is still empty, so no reader ever sees the content under wider rights.
	if err := casestore.SecureFile(name); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return s.replace(name, s.path)
}
