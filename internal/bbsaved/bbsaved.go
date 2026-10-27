// Package bbsaved keeps the list of Blackboxes Unravis knows about, sealed for the
// current user in blackboxes.dat. A Blackbox reached by URL has a read key; the
// key lives only in this file. Entry, the part the page and the cases see, never
// carries a key, and the only way to get a key out is Key, which only the server
// side that reads the Blackbox calls.
package bbsaved

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/sealedfile"
)

// Errors of the store.
var (
	ErrUnavailable = errors.New("The saved Blackbox list is unavailable on this PC.")
	ErrCorrupt     = errors.New("The saved Blackbox list could not be read (damaged, or sealed for another user). It was left as it is.")
	ErrNotFound    = errors.New("That Blackbox is not in the list.")
	ErrDuplicate   = errors.New("That Blackbox is already in the list.")
	ErrInvalid     = errors.New("The Blackbox details are not valid.")
	ErrTooMany     = errors.New("The list is full. Remove a Blackbox before adding another.")
)

// MaxEntries is the most Blackboxes the list holds.
const MaxEntries = 50

const (
	maxFileSize = 1 << 20
	maxText     = 200
)

// Entry is what the page sees. It has no key.
type Entry struct {
	ID string `json:"id"`
	// Name is the Blackbox name (from its manifest, or typed).
	Name string `json:"name"`
	// Kind is folder, share or url.
	Kind blackbox.LocationKind `json:"kind"`
	// Location is how it is reached, as shown (a folder, \\server\share or http://host:port).
	Location string `json:"location"`
	// Canon is the key two spellings of one place share.
	Canon string `json:"canon"`
	// ThisPC is set for a Blackbox this PC runs, which Unravis can start, stop and remove.
	ThisPC bool `json:"thisPc"`
	// RecorderHost and RecorderName come from the manifest read at add time, so the
	// same Blackbox reached by a folder and by a URL is one entry.
	RecorderHost string `json:"recorderHost,omitempty"`
	RecorderName string `json:"recorderName,omitempty"`
	// HasKey says a read key is sealed here. The key itself is never in Entry.
	HasKey  bool      `json:"hasKey"`
	AddedAt time.Time `json:"addedAt"`
}

// AddInput is a Blackbox to add. Key is only for a URL.
type AddInput struct {
	Name         string
	Location     blackbox.Location
	Key          string
	ThisPC       bool
	RecorderHost string
	RecorderName string
}

type stored struct {
	Entry
	Key string `json:"key,omitempty"`
}

type vault struct {
	Version int      `json:"version"`
	Records []stored `json:"records"`
}

// Store is the sealed list. Safe for concurrent use.
type Store struct {
	path      string
	protector sealedfile.Protector
	mu        sync.Mutex
	now       func() time.Time
}

// New opens the list at path. A missing file is an empty list and writes nothing.
func New(path string, protector sealedfile.Protector) (*Store, error) {
	if path == "" || protector == nil {
		return nil, ErrUnavailable
	}
	return &Store{path: path, protector: protector, now: time.Now}, nil
}

// SetClock replaces the clock (tests, and the demo's fixed time).
func (s *Store) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// NewDefault sits beside the other saved files and is sealed the same way.
func NewDefault(getenv func(string) string) (*Store, error) {
	base := ""
	if getenv != nil {
		base = getenv("AUDIT_CONFIG_DIR")
	}
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return nil, ErrUnavailable
		}
		base = filepath.Join(base, "APIAuditExplorer")
	}
	protector, err := sealedfile.CurrentUser()
	if err != nil {
		return nil, ErrUnavailable
	}
	return New(filepath.Join(base, "blackboxes.dat"), protector)
}

func clean(s string) string { return strings.TrimSpace(s) }

// same says two records are one Blackbox: the same place, or the same recorder.
func same(a Entry, canon, recHost, recName string) bool {
	if a.Canon == canon {
		return true
	}
	return recHost != "" && recName != "" &&
		strings.EqualFold(a.RecorderHost, recHost) && strings.EqualFold(a.RecorderName, recName)
}

// List returns the entries, oldest first. No entry carries a key.
func (s *Store) List() ([]Entry, error) {
	v, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(v.Records))
	for i := range v.Records {
		out[i] = v.Records[i].Entry
	}
	return out, nil
}

// Get returns one entry.
func (s *Store) Get(id string) (Entry, error) {
	v, err := s.read()
	if err != nil {
		return Entry{}, err
	}
	for _, r := range v.Records {
		if r.ID == id {
			return r.Entry, nil
		}
	}
	return Entry{}, ErrNotFound
}

// Find returns the entry that is the same Blackbox as the place and recorder
// given, if there is one.
func (s *Store) Find(canon, recHost, recName string) (Entry, bool) {
	v, err := s.read()
	if err != nil {
		return Entry{}, false
	}
	for _, r := range v.Records {
		if same(r.Entry, canon, clean(recHost), clean(recName)) {
			return r.Entry, true
		}
	}
	return Entry{}, false
}

// Add saves a Blackbox. A Blackbox that is already in the list (the same place
// typed another way, or the same recorder reached another way) is not added again:
// Add returns the earlier entry and ErrDuplicate, and the sealed key already
// there is kept. A key typed for a URL is sealed; for a folder or share none is
// kept.
func (s *Store) Add(in AddInput) (Entry, error) {
	name := clean(in.Name)
	if name == "" || len(name) > maxText || in.Location.Canonical() == "" || in.Location.Kind == "" {
		return Entry{}, ErrInvalid
	}
	key := clean(in.Key)
	if len(key) > maxText || strings.ContainsAny(key, "\r\n") {
		return Entry{}, ErrInvalid
	}
	if in.Location.NeedsKey() && key == "" {
		return Entry{}, ErrInvalid
	}
	if !in.Location.NeedsKey() {
		key = ""
	}
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID: id, Name: name, Kind: in.Location.Kind, Location: in.Location.Display(), Canon: in.Location.Canonical(),
		ThisPC: in.ThisPC, RecorderHost: clean(in.RecorderHost), RecorderName: clean(in.RecorderName),
		HasKey: key != "", AddedAt: s.now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return Entry{}, ErrUnavailable
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return Entry{}, err
	}
	for _, r := range v.Records {
		if same(r.Entry, e.Canon, e.RecorderHost, e.RecorderName) {
			return r.Entry, ErrDuplicate
		}
	}
	if len(v.Records) >= MaxEntries {
		return Entry{}, ErrTooMany
	}
	v.Records = append(v.Records, stored{Entry: e, Key: key})
	if err := s.write(v); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// SetThisPC marks or unmarks an entry as run by this PC.
func (s *Store) SetThisPC(id string, thisPC bool) error {
	return s.update(id, func(r *stored) { r.ThisPC = thisPC })
}

// SetKey replaces the sealed read key of a URL entry (the owner typed a new one).
func (s *Store) SetKey(id, key string) error {
	key = clean(key)
	if key == "" || len(key) > maxText || strings.ContainsAny(key, "\r\n") {
		return ErrInvalid
	}
	return s.update(id, func(r *stored) {
		if r.Kind == blackbox.LocURL {
			r.Key, r.HasKey = key, true
		}
	})
}

func (s *Store) update(id string, f func(*stored)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return ErrUnavailable
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return err
	}
	for i := range v.Records {
		if v.Records[i].ID == id {
			f(&v.Records[i])
			return s.write(v)
		}
	}
	return ErrNotFound
}

// Remove deletes an entry and its sealed key. The Blackbox itself and its
// recordings are not touched.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return ErrUnavailable
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return err
	}
	for i, r := range v.Records {
		if r.ID == id {
			v.Records = append(v.Records[:i], v.Records[i+1:]...)
			return s.write(v)
		}
	}
	return ErrNotFound
}

// Key returns the sealed read key of an entry, for the code that reads the
// Blackbox. It is never sent to the page, a log, a case file or an export. An
// entry with no key (a folder or share) returns "".
func (s *Store) Key(id string) (string, error) {
	v, err := s.read()
	if err != nil {
		return "", err
	}
	for _, r := range v.Records {
		if r.ID == id {
			return r.Key, nil
		}
	}
	return "", ErrNotFound
}

func (s *Store) read() (vault, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return vault{}, ErrUnavailable
	}
	defer unlock()
	return s.readLocked()
}

func (s *Store) readLocked() (vault, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return vault{Version: 1, Records: []stored{}}, nil
	}
	if err != nil {
		return vault{}, ErrUnavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return vault{}, ErrUnavailable
	}
	if len(data) == 0 || len(data) > maxFileSize {
		return vault{}, ErrCorrupt
	}
	plain, err := s.protector.Unprotect(data)
	if err != nil || len(plain) == 0 {
		return vault{}, ErrCorrupt
	}
	var v vault
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	if dec.Decode(&v) != nil || v.Version != 1 || v.Records == nil || len(v.Records) > MaxEntries {
		return vault{}, ErrCorrupt
	}
	seen := map[string]bool{}
	for _, r := range v.Records {
		if r.ID == "" || len(r.ID) > 64 || r.Canon == "" || r.HasKey != (r.Key != "") || seen[r.ID] {
			return vault{}, ErrCorrupt
		}
		seen[r.ID] = true
	}
	return v, nil
}

func (s *Store) write(v vault) error {
	plain, err := json.Marshal(v)
	if err != nil || len(plain) > maxFileSize {
		return ErrCorrupt
	}
	sealed, err := s.protector.Protect(plain)
	if err != nil || len(sealed) == 0 || len(sealed) > maxFileSize {
		return ErrUnavailable
	}
	if err := sealedfile.WriteAtomic(s.path, ".blackboxes-*.tmp", sealed); err != nil {
		return ErrUnavailable
	}
	return nil
}

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "bb-" + hex.EncodeToString(raw[:]), nil
}
