package blackbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func sourcesBytes(doc SourcesDoc) ([]byte, error) {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteSourcesFile writes dir/sources.json through a temp file and a rename, so a reader never sees half a file.
// It does nothing when the file already holds the same document apart from Generated.
func WriteSourcesFile(dir string, doc SourcesDoc) error {
	target := filepath.Join(dir, SourcesName)

	if old, err := ReadSourcesFile(dir); err == nil {
		a, b := old, doc
		a.Generated, b.Generated = time.Time{}, time.Time{}
		ab, e1 := sourcesBytes(a)
		bb, e2 := sourcesBytes(b)
		if e1 == nil && e2 == nil && bytes.Equal(ab, bb) {
			return nil
		}
	}

	data, err := sourcesBytes(doc)
	if err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // the manifest is read by the Unravis reader account
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadSourcesFile reads dir/sources.json back (tests, and the check above).
func ReadSourcesFile(dir string) (SourcesDoc, error) {
	var doc SourcesDoc
	data, err := os.ReadFile(filepath.Join(dir, SourcesName)) //nolint:gosec // the recordings folder
	if err != nil {
		return doc, err
	}
	err = json.Unmarshal(data, &doc)
	return doc, err
}
