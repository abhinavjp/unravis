// Package sourcelinks remembers which audit sources a person had open, so Unravis can open them again after a restart.
//
// A link is "this audit source = saved connection id + database + collection + label". It holds names and ids only: never a password,
// a user name or an address. The password stays where the saved connection keeps it (the sealed saved-connections file, or the key
// keeper). The links file is sealed for the person who owns it and is never sent to the browser.
package sourcelinks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"api-audit-explorer/internal/sealedfile"
)

const (
	// MaxLinks is the most links kept. An investigation holds at most audit.MaxSources sources; the rest is room for sources opened
	// and removed over time. When it is full the oldest link is dropped.
	MaxLinks    = 32
	maxFileSize = 256 << 10
	maxName     = 200
)

var (
	ErrUnavailable = errors.New("saved audit source links are unavailable")
	ErrCorrupt     = errors.New("saved audit source links could not be read")
	ErrInvalid     = errors.New("invalid audit source link")
)

// Link is one audit source to open again.
type Link struct {
	SavedConnectionID string `json:"savedConnectionId"`
	Database          string `json:"database"`
	Collection        string `json:"collection"`
	Label             string `json:"label"`
	ConnectionLabel   string `json:"connectionLabel,omitempty"`
	// SelectionKey is the stable key of the source (audit.Source.SelectionKey); empty when the repository gave none.
	SelectionKey string `json:"selectionKey,omitempty"`
}

type document struct {
	Version int    `json:"version"`
	Links   []Link `json:"links"`
}

// Store is the sealed links file.
type Store struct {
	path      string
	protector sealedfile.Protector
	mu        sync.Mutex
}

// New opens the store at path. The file is created on the first Add.
func New(path string, protector sealedfile.Protector) (*Store, error) {
	if path == "" || protector == nil {
		return nil, ErrUnavailable
	}
	return &Store{path: path, protector: protector}, nil
}

func (l Link) valid() bool {
	if l.SavedConnectionID == "" || l.Database == "" || l.Collection == "" {
		return false
	}
	for _, v := range []string{l.SavedConnectionID, l.Database, l.Collection, l.Label, l.ConnectionLabel, l.SelectionKey} {
		if len(v) > maxName {
			return false
		}
	}
	return true
}

func (l Link) sameSource(o Link) bool {
	return l.SavedConnectionID == o.SavedConnectionID && l.Database == o.Database && l.Collection == o.Collection
}

// List returns the links, oldest first.
func (s *Store) List() ([]Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer unlock()
	d, err := s.read()
	if err != nil {
		return nil, err
	}
	return d.Links, nil
}

// Add records a link. The same saved connection, database and collection replaces its earlier link.
func (s *Store) Add(link Link) error {
	link.Database, link.Collection = strings.TrimSpace(link.Database), strings.TrimSpace(link.Collection)
	if !link.valid() {
		return ErrInvalid
	}
	return s.mutate(func(d *document) bool {
		kept := d.Links[:0:0]
		for _, prior := range d.Links {
			if !prior.sameSource(link) {
				kept = append(kept, prior)
			}
		}
		kept = append(kept, link)
		if len(kept) > MaxLinks {
			kept = kept[len(kept)-MaxLinks:]
		}
		d.Links = kept
		return true
	})
}

// Remove drops the link of a source the person removed. The selection key names it when the repository gave one; otherwise the
// database and collection do.
func (s *Store) Remove(selectionKey, database, collection string) error {
	return s.mutate(func(d *document) bool {
		kept := d.Links[:0:0]
		for _, l := range d.Links {
			hit := (selectionKey != "" && l.SelectionKey == selectionKey) ||
				(selectionKey == "" && l.Database == database && l.Collection == collection)
			if !hit {
				kept = append(kept, l)
			}
		}
		changed := len(kept) != len(d.Links)
		d.Links = kept
		return changed
	})
}

// Forget drops every link of a saved connection the person deleted.
func (s *Store) Forget(savedConnectionID string) error {
	return s.mutate(func(d *document) bool {
		kept := d.Links[:0:0]
		for _, l := range d.Links {
			if l.SavedConnectionID != savedConnectionID {
				kept = append(kept, l)
			}
		}
		changed := len(kept) != len(d.Links)
		d.Links = kept
		return changed
	})
}

func (s *Store) mutate(change func(*document) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer unlock()
	d, err := s.read()
	if err != nil {
		// A file that cannot be read is replaced by the next add: links are a convenience and hold no secret, so starting again
		// loses nothing that cannot be opened by hand.
		if !errors.Is(err, ErrCorrupt) {
			return err
		}
		d = document{Version: 1, Links: []Link{}}
	}
	if !change(&d) {
		return nil
	}
	plain, err := json.Marshal(d)
	if err != nil || len(plain) > maxFileSize {
		return ErrCorrupt
	}
	sealed, err := s.protector.Protect(plain)
	if err != nil || len(sealed) == 0 || len(sealed) > maxFileSize {
		return ErrUnavailable
	}
	if err := sealedfile.WriteAtomic(s.path, ".sourcelinks-*.tmp", sealed); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) read() (document, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return document{Version: 1, Links: []Link{}}, nil
	}
	if err != nil {
		return document{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return document{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(data) == 0 || len(data) > maxFileSize {
		return document{}, ErrCorrupt
	}
	plain, err := s.protector.Unprotect(data)
	if err != nil || len(plain) == 0 || len(plain) > maxFileSize {
		return document{}, ErrCorrupt
	}
	var d document
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || d.Version != 1 || d.Links == nil || len(d.Links) > MaxLinks {
		return document{}, ErrCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return document{}, ErrCorrupt
	}
	for _, l := range d.Links {
		if !l.valid() {
			return document{}, ErrCorrupt
		}
	}
	return d, nil
}
