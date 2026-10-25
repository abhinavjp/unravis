package casestore

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"api-audit-explorer/internal/sealedfile"
)

// File names of the two sealed files in a case folder that Rewrap moves.
const (
	KeyFileName      = "case.key"
	ManifestFileName = "manifest.dat"
)

// rewrapManifestMax bounds the manifest Rewrap will load: the store's own cap
// (4 MiB) plus room for the v2 header.
const rewrapManifestMax = 4<<20 + 1<<10

// ErrRewrapDirection means Rewrap was asked for something other than DPAPI to
// the master key. Moving a case back is not offered.
var ErrRewrapDirection = errors.New("re-wrap only moves a case from Windows sealing to the master key")

// Sealing is a protector kind with its base protector. For the master key P is the
// Keyring; Rewrap binds the per-file label itself.
type Sealing struct {
	Kind string
	P    sealedfile.Protector
}

// RewrapOptions are the hooks around Rewrap. Both may be nil.
type RewrapOptions struct {
	// Enter runs with the folder lock held, before any file is read. The caller
	// uses it to keep its own writers of the two files out; the returned func runs
	// when Rewrap is done.
	Enter func() (leave func(), err error)
	// Step is a fault-injection seam for tests: it is called with "staged",
	// "manifest-replaced" and "key-replaced". An error from it ends Rewrap at that
	// point without any rollback, as a power cut would.
	Step func(stage string) error
}

// Rewrap moves the sealed case.key and manifest.dat in dir from DPAPI to the master
// key. Only those two files are written; case.db is never touched.
//
// Order: both new files are written to temporary files in dir and each is read
// back with the new protector and compared with the original; then, under the
// folder lock, manifest.dat is replaced, then case.key. The key file goes last
// and is the file that matters (the manifest can be rebuilt from the database),
// so every failure before that final replace leaves the old case.key intact. If
// replacing case.key itself fails, the manifest is put back. A crash between the
// two replaces leaves a manifest and key on different protectors, which Open
// refuses as damaged (ErrKeyFile); running Rewrap again finishes it. Re-wrapping
// a case already on the master key is a no-op success.
//
// A locked or missing master key is ErrLocked and nothing is written. No error
// carries key bytes.
func Rewrap(dir string, from, to Sealing, opt RewrapOptions) error {
	if from.Kind != KindDPAPI || to.Kind != KindMasterKey || from.P == nil {
		return ErrRewrapDirection
	}
	release, err := sealedfile.Lock(dir)
	if err != nil {
		return err
	}
	defer release()
	if opt.Enter != nil {
		leave, err := opt.Enter()
		if err != nil {
			return err
		}
		defer leave()
	}
	lookup := func(kind string) (sealedfile.Protector, error) {
		switch kind {
		case from.Kind:
			return from.P, nil
		case to.Kind:
			if to.P == nil {
				return nil, ErrLocked
			}
			return to.P, nil
		}
		return nil, ErrKeyFile
	}

	keyPath := filepath.Join(dir, KeyFileName)
	manPath := filepath.Join(dir, ManifestFileName)
	keyRaw, err := readCapped(keyPath, keyFileMax)
	if err != nil {
		return err
	}
	manRaw, err := readCapped(manPath, rewrapManifestMax)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrKeyFile
		}
		return err
	}
	keys, keyKind, err := openKeys(keyRaw, lookup)
	if err != nil {
		return err
	}
	defer keys.Zero()
	manPlain, manKind, err := openSealedFile(manRaw, lookup, LabelManifest)
	if err != nil {
		return err
	}
	defer clear(manPlain)

	needMan, needKey := manKind != to.Kind, keyKind != to.Kind
	if !needMan && !needKey {
		return nil
	}

	var manTmp, keyTmp string
	defer func() {
		if manTmp != "" {
			_ = os.Remove(manTmp)
		}
		if keyTmp != "" {
			_ = os.Remove(keyTmp)
		}
	}()
	if needMan {
		p, err := Resolve(lookup, to.Kind, LabelManifest)
		if err != nil {
			return err
		}
		sealed, err := p.Protect(manPlain)
		if err != nil {
			return sealErr(err)
		}
		data := envelope(to.Kind, sealed)
		if manTmp, err = writeTemp(dir, "manifest.rw.*.tmp", data); err != nil {
			return err
		}
		back, err := os.ReadFile(manTmp)
		if err != nil {
			return err
		}
		got, kind, err := openSealedFile(back, lookup, LabelManifest)
		if err != nil {
			return err
		}
		same := kind == to.Kind && subtle.ConstantTimeCompare(got, manPlain) == 1
		clear(got)
		if !same {
			return ErrKeyFile
		}
	}
	if needKey {
		p, err := Resolve(lookup, to.Kind, LabelKey)
		if err != nil {
			return err
		}
		data, err := sealKeys(to.Kind, p, keys)
		if err != nil {
			return err
		}
		if keyTmp, err = writeTemp(dir, "case.rw.*.tmp", data); err != nil {
			return err
		}
		back, err := os.ReadFile(keyTmp)
		if err != nil {
			return err
		}
		got, kind, err := openKeys(back, lookup)
		if err != nil {
			return err
		}
		same := kind == to.Kind && subtle.ConstantTimeCompare(got.DB, keys.DB) == 1 &&
			subtle.ConstantTimeCompare(got.Pseudo, keys.Pseudo) == 1 && got.CaseID == keys.CaseID
		got.Zero()
		if !same {
			return ErrKeyFile
		}
	}
	if err := step(opt, "staged"); err != nil {
		return err
	}
	if needMan {
		if err := sealedfile.Replace(manTmp, manPath); err != nil {
			return fmt.Errorf("replacing the case manifest: %w", sealedfile.ErrUnavailable)
		}
		manTmp = ""
		if err := step(opt, "manifest-replaced"); err != nil {
			return err
		}
	}
	if needKey {
		if err := sealedfile.Replace(keyTmp, keyPath); err != nil {
			if needMan {
				putBack(dir, manPath, manRaw)
			}
			return fmt.Errorf("replacing the case key: %w", sealedfile.ErrUnavailable)
		}
		keyTmp = ""
		if err := step(opt, "key-replaced"); err != nil {
			return err
		}
	}
	return nil
}

func step(opt RewrapOptions, stage string) error {
	if opt.Step == nil {
		return nil
	}
	return opt.Step(stage)
}

// sealErr never passes a protector's error text on.
func sealErr(err error) error {
	if errors.Is(err, ErrLocked) {
		return ErrLocked
	}
	return fmt.Errorf("sealing a case file: %w", sealedfile.ErrUnavailable)
}

// openSealedFile opens a manifest-shaped file (any kind) and returns its plain
// bytes and kind.
func openSealedFile(raw []byte, lookup ProtectorLookup, label string) ([]byte, string, error) {
	kind, blob, _, err := parseEnvelope(raw)
	if err != nil {
		return nil, "", err
	}
	p, err := Resolve(lookup, kind, label)
	if err != nil {
		return nil, "", err
	}
	plain, err := p.Unprotect(blob)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return nil, "", ErrLocked
		}
		return nil, "", ErrKeyFile
	}
	return plain, kind, nil
}

func readCapped(path string, max int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > max {
		return nil, ErrKeyFile
	}
	return os.ReadFile(path)
}

// writeTemp writes data to a new 0600 file in dir and flushes it.
func writeTemp(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// putBack restores the original manifest bytes after the key replace failed. Best
// effort: if it also fails the case is left in the state a crash would leave,
// which a second Rewrap repairs.
func putBack(dir, path string, original []byte) {
	tmp, err := writeTemp(dir, "manifest.rb.*.tmp", bytes.Clone(original))
	if err != nil {
		return
	}
	if sealedfile.Replace(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}
