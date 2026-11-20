package update

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Names of the two files that carry a release to a PC with no internet.
const (
	OfflineManifestName  = "update-manifest.json"
	OfflineSignatureName = "update-manifest.json.sig"
)

// FolderFile is one file listed by a verified manifest, as found in the folder.
type FolderFile struct {
	Kind   string // setup, app, blackbox or lite
	Name   string
	Found  bool // the file is in the folder
	Path   string
	SHA256 string
	Size   int64
}

// FolderResult is a release checked in a folder.
type FolderResult struct {
	Manifest *Manifest
	Decision Decision
	Why      string // plain words when the release is not an upgrade
	Files    []FolderFile
}

// VerifyFolder runs the same checks as an online update on files carried on a stick: the signature over update-manifest.json (keys in
// t), the manifest fields, whether it is an upgrade for current, and every listed file that is in the folder against its signed size
// and SHA-256. It reads only the files the manifest names plus the two manifest files, never follows a link, and changes nothing.
// A listed file that is missing is reported as missing; a listed file that is there but wrong fails the whole check.
func VerifyFolder(dir string, t Trust, current string, now time.Time) (FolderResult, error) {
	var r FolderResult
	manifest, err := readSmallFile(filepath.Join(dir, OfflineManifestName))
	if err != nil {
		return r, fmt.Errorf("%s: %w", OfflineManifestName, err)
	}
	sig, err := readSmallFile(filepath.Join(dir, OfflineSignatureName))
	if err != nil {
		return r, fmt.Errorf("%s: %w", OfflineSignatureName, err)
	}
	m, err := Verify(manifest, sig, t, now)
	if err != nil {
		return r, err
	}
	r.Manifest = m
	r.Decision, r.Why, err = Judge(m, current, 0)
	if err != nil {
		return r, err
	}
	kinds := make([]string, 0, len(m.Assets))
	for k := range m.Assets {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		a := m.Assets[k]
		ff := FolderFile{Kind: k, Name: a.Name}
		if !safeFileName(a.Name) || (k == AssetSetup && !setupNameOK(a.Name)) {
			return r, fmt.Errorf("the file name for %s in the release is not safe", k)
		}
		p := filepath.Join(dir, a.Name)
		if _, serr := os.Lstat(p); serr != nil {
			r.Files = append(r.Files, ff)
			continue
		}
		got, ok := verifyFile(p, a)
		if !ok {
			return r, fmt.Errorf("%s does not match the signed release (size or SHA-256 differs); do not use it", a.Name)
		}
		ff.Found, ff.Path, ff.SHA256, ff.Size = true, got.Path, got.SHA256, got.Size
		r.Files = append(r.Files, ff)
	}
	return r, nil
}

// readSmallFile reads a regular file of at most MaxManifestSize bytes.
func readSmallFile(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("not found")
	}
	if !fi.Mode().IsRegular() || fi.Size() > MaxManifestSize {
		return nil, errors.New("is not a plain file of an allowed size")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot be read")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxManifestSize+1))
	if err != nil || len(b) > MaxManifestSize {
		return nil, errors.New("cannot be read")
	}
	return b, nil
}
