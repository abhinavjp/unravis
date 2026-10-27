package blackbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// ManifestName is the manifest file in the recordings folder.
const ManifestName = "manifest.json"

// States the manifest can hold. Running is written at start; Stopped is written
// only by a clean stop, so a manifest that still says running with an old last
// sample means the recorder ended without a clean stop.
const (
	ManifestRunning = "running"
	ManifestStopped = "stopped"
)

// Caps that keep the manifest small.
const (
	maxGaps   = 1000
	maxEvents = 200
	maxRuns   = 200
)

// FileEntry is one hourly NDJSON file.
type FileEntry struct {
	Name    string    `json:"name"`
	Hour    time.Time `json:"hour"` // start of the UTC hour the file covers
	Bytes   int64     `json:"bytes"`
	Samples int       `json:"samples"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
}

// RunEntry is one start of the recorder.
type RunEntry struct {
	ID      string     `json:"id"`
	Start   time.Time  `json:"start"`
	Version string     `json:"version"`
	Stop    *time.Time `json:"stop,omitempty"`
	Reason  string     `json:"reason,omitempty"` // "stopped", "crash", ...
}

// Event is something worth knowing about the recording that is not a sample.
type Event struct {
	T      time.Time `json:"t"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

// Event kinds.
const (
	EventClockJump       = "clock-jump"
	EventClockBack       = "clock-back"
	EventManifestRebuilt = "manifest-rebuilt"
	EventFileMissing     = "file-missing"
	EventFileRepaired    = "file-repaired"
	EventPausedDiskLow   = "paused-disk-low"
	EventResumed         = "resumed"
	EventHousekeeping    = "housekeeping"
	EventCollectFailed   = "collect-failed"
	EventWriteFailed     = "write-failed"
	EventSettingsRestart = "settings-applied"
)

// Manifest is manifest.json: what the folder holds and how complete it is. The
// file is rewritten whole through a temporary file and a rename, so a reader
// sees either the old or the new one.
type Manifest struct {
	Format          int         `json:"format"`
	Name            string      `json:"name"`
	Version         string      `json:"version"`
	Host            string      `json:"host"`
	IntervalSeconds int         `json:"intervalSeconds"`
	Created         time.Time   `json:"created"`
	Updated         time.Time   `json:"updated"`
	State           string      `json:"state"`
	FirstSample     *time.Time  `json:"firstSample,omitempty"`
	LastSample      *time.Time  `json:"lastSample,omitempty"`
	Runs            []RunEntry  `json:"runs"`
	Files           []FileEntry `json:"files"`
	Gaps            []Gap       `json:"gaps"`
	Events          []Event     `json:"events"`
	Probe           Probe       `json:"probe"`
}

// ErrNewerFormat is returned when the manifest was written by a newer recorder.
// The store refuses to touch such a folder.
var ErrNewerFormat = errors.New("blackbox: the recordings folder was written by a newer version")

var fileNameRe = regexp.MustCompile(`^bb-(\d{4})(\d{2})(\d{2})-(\d{2})\.ndjson$`)

// FileNameFor is the hourly file that holds a sample taken at t.
func FileNameFor(t time.Time) string {
	return "bb-" + t.UTC().Format("20060102-15") + ".ndjson"
}

// HourOfName reads the hour a recording file name stands for.
func HourOfName(name string) (time.Time, bool) {
	if !fileNameRe.MatchString(name) {
		return time.Time{}, false
	}
	h, err := time.Parse("bb-20060102-15", name[:len(name)-len(".ndjson")])
	if err != nil {
		return time.Time{}, false
	}
	return h.UTC(), true
}

// ValidFileName says whether name is one of the recording files, for downloads.
func ValidFileName(name string) bool {
	_, ok := HourOfName(name)
	return ok
}

// Coverage is the span from the first to the last sample.
func (m Manifest) Coverage() (first, last time.Time, ok bool) {
	if m.FirstSample == nil || m.LastSample == nil {
		return time.Time{}, time.Time{}, false
	}
	return *m.FirstSample, *m.LastSample, true
}

// TotalBytes is the size of the recording files.
func (m Manifest) TotalBytes() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Bytes
	}
	return n
}

// SampleCount is the number of samples in the recording files.
func (m Manifest) SampleCount() int {
	n := 0
	for _, f := range m.Files {
		n += f.Samples
	}
	return n
}

// readManifest reads the manifest in dir. A missing file is os.ErrNotExist.
func readManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("blackbox: manifest unreadable: %w", err)
	}
	if m.Format > FormatVersion {
		return Manifest{}, ErrNewerFormat
	}
	if m.Format < 1 {
		return Manifest{}, errors.New("blackbox: manifest has no format version")
	}
	return m, nil
}

// ReadManifest reads the manifest of a recordings folder, for readers.
func ReadManifest(dir string) (Manifest, error) { return readManifest(dir) }

// sortFiles orders entries oldest first.
func sortFiles(files []FileEntry) {
	sort.Slice(files, func(i, j int) bool { return files[i].Hour.Before(files[j].Hour) })
}

// nearestExisting walks up from path to the first folder that exists, so the
// free space of a folder that is not made yet can still be asked.
func nearestExisting(path string) string {
	p := filepath.Clean(path)
	for {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}
