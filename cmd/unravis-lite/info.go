package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"time"

	"api-audit-explorer/internal/version"
)

const infoName = "lite-info.json"

// infoJSON is what the page reads to show Lite's own version. It holds no path, user name or host name.
func infoJSON() []byte {
	b, _ := json.Marshal(struct {
		App     string `json:"app"`
		Version string `json:"version"`
		Full    string `json:"full"`
	}{
		App:     "Unravis Lite",
		Version: strings.TrimPrefix(version.Short(), "Unravis "),
		Full:    strings.Replace(version.String(), "Unravis", "Unravis Lite", 1),
	})
	return b
}

// withInfo serves lite-info.json from memory and passes every other name to base. It is an overlay, not a route,
// so the allowlist in internal/lite stays as it is.
func withInfo(base fs.FS) fs.FS { return infoFS{base: base} }

type infoFS struct{ base fs.FS }

func (f infoFS) Open(name string) (fs.File, error) {
	if name == infoName {
		data := infoJSON()
		return &infoFile{Reader: bytes.NewReader(data), size: int64(len(data))}, nil
	}
	return f.base.Open(name)
}

// infoFile is read-only. It has Seek because http.FileServer needs it to find the size and serve ranges.
type infoFile struct {
	*bytes.Reader
	size int64
}

func (f *infoFile) Stat() (fs.FileInfo, error) { return infoStat{size: f.size}, nil }
func (f *infoFile) Close() error               { return nil }

type infoStat struct{ size int64 }

func (infoStat) Name() string       { return infoName }
func (s infoStat) Size() int64      { return s.size }
func (infoStat) Mode() fs.FileMode  { return 0o444 }
func (infoStat) ModTime() time.Time { return time.Time{} }
func (infoStat) IsDir() bool        { return false }
func (infoStat) Sys() any           { return nil }
