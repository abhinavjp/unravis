package sqlite

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/sealedfile"
)

const (
	manifestName    = "manifest.dat"
	manifestVersion = 1
	manifestMax     = 4 << 20
)

// manifest is the sealed mirror of the case: title, retention clock and one
// entry per committed import, including the acknowledgement text. The database
// stays the source of truth for events; the manifest lets List and Sweep work
// without opening every database, and lets Open notice a database that lost
// rows (storage decision, "On open").
type manifest struct {
	V       int              `json:"v"`
	CaseID  string           `json:"caseId"`
	Title   string           `json:"title"`
	Created time.Time        `json:"created"`
	Closed  *time.Time       `json:"closed,omitempty"`
	Imports []manifestImport `json:"imports"`
	// Mark, RequestID and History are the lifecycle fields (P2). They are omitted
	// when empty, so a manifest from before them reads and writes as before and the
	// manifest version stays 1. They live only here: a case file carries the
	// database, so a restored case starts with no mark, no request id and a fresh
	// history.
	Mark      string            `json:"mark,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
	History   []manifestHistory `json:"history,omitempty"`
}

type manifestHistory struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
}

type manifestImport struct {
	ID        int64     `json:"id"`
	Importer  string    `json:"importer"`
	FileName  string    `json:"fileName"`
	SHA256    string    `json:"sha256"`
	Bytes     int64     `json:"bytes"`
	Rows      int64     `json:"rows"`
	Rejected  int64     `json:"rejected"`
	Events    int64     `json:"events"`
	HostLabel string    `json:"hostLabel"`
	TZRule    string    `json:"tzRule"`
	Imported  time.Time `json:"imported"`
	Status    string    `json:"status"`
	AckGating bool      `json:"ackGating"`
	AckText   string    `json:"ackText"`
	// Supplied is the owner-supplied settings as canonical JSON (sorted keys), or
	// empty. A string keeps the struct comparable for sameImport. Older manifests
	// have none.
	Supplied string `json:"supplied,omitempty"`
}

func readManifest(dir string, p sealedfile.Protector) (manifest, error) {
	path := filepath.Join(dir, manifestName)
	info, err := os.Stat(path)
	if err != nil {
		return manifest{}, err
	}
	if info.Size() == 0 || info.Size() > manifestMax {
		return manifest{}, ErrDamaged
	}
	sealed, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	plain, err := p.Unprotect(sealed)
	if err != nil {
		return manifest{}, ErrDamaged
	}
	defer clear(plain)
	var m manifest
	if err := json.Unmarshal(plain, &m); err != nil || m.V != manifestVersion {
		return manifest{}, ErrDamaged
	}
	return m, nil
}

func writeManifest(dir string, p sealedfile.Protector, m manifest) error {
	m.V = manifestVersion
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	defer clear(plain)
	sealed, err := p.Protect(plain)
	if err != nil {
		return errors.New("sealing case manifest failed")
	}
	return sealedfile.WriteAtomic(filepath.Join(dir, manifestName), "manifest.*.tmp", sealed)
}
