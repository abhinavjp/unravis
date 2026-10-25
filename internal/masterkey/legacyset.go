package masterkey

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"api-audit-explorer/internal/sealedfile"
)

// LegacySetFileName is the legacy set, kept next to masterkey.dat.
//
// Once a master key exists, a case folder sealed only by Windows (DPAPI) is
// accepted only if its id is in this set: the ids of the DPAPI cases that existed
// when the master key was set up (or, for a master key made before this file
// existed, when it was first unlocked). A same-user process can write any file in
// the cases folder, so without the set it could plant a complete DPAPI case that
// opens without the passphrase and lists beside real cases. The set is sealed
// under the unlocked Keyring (AES-256-GCM, its own AAD label), so a process
// without the passphrase cannot add an id, and the ids are not readable on disk.
const LegacySetFileName = "legacyset.dat"

const (
	legacySetLabel   = "unravis/masterkey/legacyset/v1"
	legacySetVersion = 1
	// legacySetMax bounds what ReadLegacySet will load.
	legacySetMax = 4 << 20
	// legacySetIDMax bounds one id; case ids are 26 characters.
	legacySetIDMax = 64
)

// LegacyState is what is in the legacy set slot of a folder.
type LegacyState int

const (
	// LegacyAbsent: no legacy set file.
	LegacyAbsent LegacyState = iota
	// LegacyOK: a well-formed set that opens under the Keyring.
	LegacyOK
	// LegacyDamaged: something is there that does not open under the Keyring
	// (garbage, truncated, another master key's file, not a regular file).
	LegacyDamaged
)

type legacySetDoc struct {
	V   int      `json:"v"`
	IDs []string `json:"ids"`
}

// LegacySetPath is the location of the legacy set file inside dir.
func LegacySetPath(dir string) string { return filepath.Join(dir, LegacySetFileName) }

// WriteLegacySet seals ids under ring and writes legacyset.dat atomically. The
// file holds only the sealed blob. A locked Keyring is ErrLocked.
func WriteLegacySet(dir string, ring *Keyring, ids []string) error {
	if ring == nil {
		return ErrLocked
	}
	out := append([]string{}, ids...)
	sort.Strings(out)
	plain, err := json.Marshal(legacySetDoc{V: legacySetVersion, IDs: out})
	if err != nil {
		return err
	}
	defer clear(plain)
	blob, err := ring.Seal(legacySetLabel, plain)
	if err != nil {
		return err
	}
	return sealedfile.WriteAtomic(LegacySetPath(dir), "legacyset.*.tmp", blob)
}

// ReadLegacySet opens legacyset.dat with ring. A missing file is LegacyAbsent. A
// file that is there but is not a good set is LegacyDamaged with no error. A
// non-nil error is an I/O failure or a locked Keyring that proves nothing about
// the file: the caller must not treat it as "absent".
func ReadLegacySet(dir string, ring *Keyring) ([]string, LegacyState, error) {
	if ring == nil {
		return nil, LegacyAbsent, ErrLocked
	}
	path := LegacySetPath(dir)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, LegacyAbsent, nil
	}
	if err != nil {
		return nil, LegacyAbsent, err
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > legacySetMax {
		return nil, LegacyDamaged, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, LegacyAbsent, nil
	}
	if err != nil {
		return nil, LegacyAbsent, err
	}
	defer f.Close()
	hi, err := f.Stat()
	if err != nil {
		return nil, LegacyAbsent, err
	}
	if !hi.Mode().IsRegular() || !os.SameFile(fi, hi) {
		return nil, LegacyDamaged, nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, legacySetMax+1))
	if err != nil {
		return nil, LegacyAbsent, err
	}
	if len(raw) == 0 || len(raw) > legacySetMax {
		return nil, LegacyDamaged, nil
	}
	plain, err := ring.Open(legacySetLabel, raw)
	if errors.Is(err, ErrLocked) {
		return nil, LegacyAbsent, err
	}
	if err != nil {
		return nil, LegacyDamaged, nil
	}
	defer clear(plain)
	var doc legacySetDoc
	if json.Unmarshal(plain, &doc) != nil || doc.V != legacySetVersion {
		return nil, LegacyDamaged, nil
	}
	for _, id := range doc.IDs {
		if id == "" || len(id) > legacySetIDMax {
			return nil, LegacyDamaged, nil
		}
	}
	return doc.IDs, LegacyOK, nil
}
