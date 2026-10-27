package blackbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrCurrentFile is returned when something asks to remove the file being
// written.
var ErrCurrentFile = errors.New("blackbox: that is the file being written")

// StoreOptions say who is opening the folder.
type StoreOptions struct {
	Name            string
	Version         string
	Host            string
	IntervalSeconds int
	RunID           string
	Now             func() time.Time
}

// recFile is the open hour file: what Append needs of it.
type recFile interface {
	Write([]byte) (int, error)
	Sync() error
	Seek(int64, int) (int64, error)
	Truncate(int64) error
	Close() error
}

// Store keeps the recordings: hourly NDJSON files plus the manifest. Files are
// append-only and each sample is flushed to disk before Append returns. All
// methods are safe to call from several goroutines (the sampling loop writes,
// the status server reads).
type Store struct {
	dir  string
	opt  StoreOptions
	now  func() time.Time
	mu   sync.Mutex
	m    Manifest
	cur  recFile
	curN string

	// wrapFile lets tests put a failing writer around the opened file.
	wrapFile func(*os.File) recFile

	firstOfRun bool
	priorClean bool
	lastMax    time.Time // newest sample time written (by time, not by order)
	manifestOK bool      // false when the last manifest write failed
}

// OpenStore opens (and when needed creates) the recordings folder. It repairs
// what an unclean stop can leave: a torn last line, a file missing from the
// manifest, a manifest that is not there or not readable. A folder written by a
// newer recorder is refused.
func OpenStore(dir string, opt StoreOptions) (*Store, error) {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, opt: opt, now: opt.Now, firstOfRun: true, manifestOK: true}
	now := s.now().UTC()

	m, err := readManifest(dir)
	rebuilt := false
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		m = Manifest{Format: FormatVersion, Created: now}
		rebuilt = len(listRecordingFiles(dir)) > 0
	case errors.Is(err, ErrNewerFormat):
		return nil, err
	default:
		// Unreadable: keep the bad file for a person to look at and rebuild.
		bad := filepath.Join(dir, fmt.Sprintf("%s.bad-%s", ManifestName, now.Format("20060102T150405Z")))
		_ = os.Rename(filepath.Join(dir, ManifestName), bad)
		m = Manifest{Format: FormatVersion, Created: now}
		rebuilt = true
	}
	s.m = m
	s.m.Format = FormatVersion
	s.m.Name, s.m.Version, s.m.Host, s.m.IntervalSeconds = opt.Name, opt.Version, opt.Host, opt.IntervalSeconds
	if s.m.Created.IsZero() {
		s.m.Created = now
	}
	if rebuilt {
		s.event(now, EventManifestRebuilt, "from the recording files")
	}

	removeStrayTemps(dir)
	s.reconcile(now)

	// A manifest that still says running means the last run did not stop cleanly.
	s.priorClean = s.m.State != ManifestRunning
	if !s.priorClean && len(s.m.Runs) > 0 {
		last := &s.m.Runs[len(s.m.Runs)-1]
		if last.Stop == nil {
			stop := s.m.Updated
			if s.m.LastSample != nil {
				stop = *s.m.LastSample
			}
			last.Stop, last.Reason = &stop, "crash"
		}
	}
	if s.m.LastSample != nil {
		s.lastMax = *s.m.LastSample
	}
	s.m.Runs = append(s.m.Runs, RunEntry{ID: opt.RunID, Start: now, Version: opt.Version})
	if len(s.m.Runs) > maxRuns {
		s.m.Runs = s.m.Runs[len(s.m.Runs)-maxRuns:]
	}
	s.m.State = ManifestRunning
	s.m.Probe = Probe{Enabled: false}
	if err := s.writeManifestLocked(true); err != nil {
		return nil, err
	}
	return s, nil
}

// listRecordingFiles lists the recording file names in dir.
func listRecordingFiles(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && ValidFileName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// removeStrayTemps deletes temporary files an interrupted write left behind.
func removeStrayTemps(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() && strings.Contains(e.Name(), ".tmp-") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// reconcile makes the manifest agree with the files: entries without a file are
// dropped, files that differ from their entry (or have none) are scanned, and a
// torn last line is cut off.
func (s *Store) reconcile(now time.Time) {
	onDisk := map[string]os.FileInfo{}
	for _, n := range listRecordingFiles(s.dir) {
		if st, err := os.Stat(filepath.Join(s.dir, n)); err == nil {
			onDisk[n] = st
		}
	}
	kept := s.m.Files[:0]
	have := map[string]bool{}
	for _, f := range s.m.Files {
		st, ok := onDisk[f.Name]
		if !ok {
			s.event(now, EventFileMissing, f.Name)
			continue
		}
		have[f.Name] = true
		if st.Size() != f.Bytes {
			if e, repaired, err := scanFile(filepath.Join(s.dir, f.Name)); err == nil {
				if repaired {
					s.event(now, EventFileRepaired, f.Name)
				}
				f = e
			}
		}
		kept = append(kept, f)
	}
	s.m.Files = kept
	for n := range onDisk {
		if have[n] {
			continue
		}
		if e, repaired, err := scanFile(filepath.Join(s.dir, n)); err == nil {
			if repaired {
				s.event(now, EventFileRepaired, n)
			}
			s.m.Files = append(s.m.Files, e)
		}
	}
	// The newest file may end in a torn line even when its size matches.
	sortFiles(s.m.Files)
	if n := len(s.m.Files); n > 0 {
		if e, repaired, err := scanFile(filepath.Join(s.dir, s.m.Files[n-1].Name)); err == nil && repaired {
			s.event(now, EventFileRepaired, e.Name)
			s.m.Files[n-1] = e
		}
	}
	s.recomputeCoverage()
}

// scanFile reads one recording file: it cuts off a torn last line (no final
// newline, or not JSON), then counts the samples and finds the first and last
// time. repaired says whether bytes were cut.
func scanFile(path string) (FileEntry, bool, error) {
	name := filepath.Base(path)
	hour, ok := HourOfName(name)
	if !ok {
		return FileEntry{}, false, errors.New("not a recording file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FileEntry{}, false, err
	}
	repaired := false
	// Cut after the last newline when the file does not end with one.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		cut := bytes.LastIndexByte(data, '\n') + 1
		if err := os.Truncate(path, int64(cut)); err != nil {
			return FileEntry{}, false, err
		}
		data = data[:cut]
		repaired = true
	}
	// A last line that ends in a newline but is not a sample is cut too.
	for len(data) > 0 {
		end := len(data) - 1
		start := bytes.LastIndexByte(data[:end], '\n') + 1
		var probe struct {
			T time.Time `json:"t"`
		}
		if json.Unmarshal(data[start:end], &probe) == nil && !probe.T.IsZero() {
			break
		}
		if err := os.Truncate(path, int64(start)); err != nil {
			return FileEntry{}, false, err
		}
		data = data[:start]
		repaired = true
	}
	e := FileEntry{Name: name, Hour: hour, Bytes: int64(len(data))}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var probe struct {
			T time.Time `json:"t"`
		}
		if json.Unmarshal(sc.Bytes(), &probe) != nil || probe.T.IsZero() {
			continue
		}
		t := probe.T.UTC()
		if e.Samples == 0 || t.Before(e.First) {
			e.First = t
		}
		if e.Samples == 0 || t.After(e.Last) {
			e.Last = t
		}
		e.Samples++
	}
	return e, repaired, sc.Err()
}

func (s *Store) recomputeCoverage() {
	s.m.FirstSample, s.m.LastSample = nil, nil
	for _, f := range s.m.Files {
		if f.Samples == 0 {
			continue
		}
		if s.m.FirstSample == nil || f.First.Before(*s.m.FirstSample) {
			t := f.First
			s.m.FirstSample = &t
		}
		if s.m.LastSample == nil || f.Last.After(*s.m.LastSample) {
			t := f.Last
			s.m.LastSample = &t
		}
	}
}

func (s *Store) event(t time.Time, kind, detail string) {
	s.m.Events = append(s.m.Events, Event{T: t.UTC(), Kind: kind, Detail: detail})
	if len(s.m.Events) > maxEvents {
		s.m.Events = s.m.Events[len(s.m.Events)-maxEvents:]
	}
}

// Event records something about the recording in the manifest.
func (s *Store) Event(kind, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.event(s.now(), kind, detail)
	_ = s.writeManifestLocked(false)
}

// Append writes one sample as one line, flushes it to disk, and updates the
// manifest. causeHint names why a gap before this sample happened when the
// recorder knows (for example "disk-low"); otherwise a gap inside a run is
// "late". On a write error the file is cut back to its size before the write,
// so a half line is never left behind.
func (s *Store) Append(smp Sample, causeHint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	smp.T = smp.T.UTC().Truncate(time.Millisecond)
	smp.V = FormatVersion
	line, err := json.Marshal(smp)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	name := FileNameFor(smp.T)
	if err := s.openFileLocked(name); err != nil {
		return err
	}
	before, err := s.cur.Seek(0, 2)
	if err != nil {
		return err
	}
	if _, err := s.cur.Write(line); err != nil {
		s.rollbackLocked(before)
		return err
	}
	if err := s.cur.Sync(); err != nil {
		s.rollbackLocked(before)
		return err
	}

	// The sample is on disk; now the bookkeeping.
	if !s.lastMax.IsZero() {
		if smp.T.Before(s.lastMax) {
			s.event(smp.T, EventClockBack, fmt.Sprintf("sample is %.1f s before the newest one", s.lastMax.Sub(smp.T).Seconds()))
		} else {
			cause := causeHint
			if s.firstOfRun {
				cause = CauseCrash
				if s.priorClean {
					cause = CauseRestart
				}
			} else if cause == "" {
				cause = CauseLate
			}
			if g, ok := GapBetween(s.lastMax, smp.T, s.opt.IntervalSeconds, cause); ok {
				s.m.Gaps = append(s.m.Gaps, g)
				if len(s.m.Gaps) > maxGaps {
					s.m.Gaps = s.m.Gaps[len(s.m.Gaps)-maxGaps:]
				}
			}
		}
	}
	s.firstOfRun = false
	if smp.T.After(s.lastMax) {
		s.lastMax = smp.T
	}
	e := s.entryLocked(name, smp.T)
	e.Bytes = before + int64(len(line))
	e.Samples++
	if e.Samples == 1 || smp.T.Before(e.First) {
		e.First = smp.T
	}
	if e.Samples == 1 || smp.T.After(e.Last) {
		e.Last = smp.T
	}
	s.recomputeCoverage()
	_ = s.writeManifestLocked(false)
	return nil
}

// rollbackLocked cuts the hour file back to size before, so a half-written line
// never stays in it. The file is opened for plain writing at the end (never
// O_APPEND): on Windows a handle opened with O_APPEND has no right to change the
// file's length, so Truncate on it is refused and the half line would stay and
// get the next sample glued onto it. If the handle still cannot truncate, the
// file is closed and cut by name; the next Append reopens it.
func (s *Store) rollbackLocked(before int64) {
	if err := s.cur.Truncate(before); err == nil {
		return
	}
	name := s.curN
	_ = s.cur.Close()
	s.cur, s.curN = nil, ""
	if err := os.Truncate(filepath.Join(s.dir, name), before); err != nil {
		s.event(s.now(), EventWriteFailed, "could not cut a half-written line out of "+name+": "+err.Error())
	}
}

// entryLocked finds or adds the manifest entry of a file.
func (s *Store) entryLocked(name string, t time.Time) *FileEntry {
	for i := range s.m.Files {
		if s.m.Files[i].Name == name {
			return &s.m.Files[i]
		}
	}
	hour, _ := HourOfName(name)
	s.m.Files = append(s.m.Files, FileEntry{Name: name, Hour: hour})
	sortFiles(s.m.Files)
	for i := range s.m.Files {
		if s.m.Files[i].Name == name {
			return &s.m.Files[i]
		}
	}
	return nil
}

func (s *Store) openFileLocked(name string) error {
	if s.cur != nil && s.curN == name {
		return nil
	}
	if s.cur != nil {
		_ = s.cur.Sync()
		_ = s.cur.Close()
		s.cur, s.curN = nil, ""
	}
	// Not O_APPEND, see rollbackLocked. Append seeks to the end before each write
	// and the store is the only writer.
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	s.cur, s.curN = recFile(f), name
	if s.wrapFile != nil {
		s.cur = s.wrapFile(f)
	}
	return nil
}

// writeManifestLocked rewrites the manifest through a temporary file and a
// rename. A remote reader holding the file open can make the rename fail on
// Windows; that is retried briefly and then left for the next write, so a reader
// never stops the recording. sync forces the data to disk first.
func (s *Store) writeManifestLocked(sync bool) error {
	s.m.Updated = s.now().UTC()
	data, err := json.MarshalIndent(s.m, "", " ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, ManifestName)
	var werr error
	for i := 0; i < 3; i++ {
		if werr = writeFileAtomicSync(path, data, 0o644, sync); werr == nil {
			s.manifestOK = true
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.manifestOK = false
	if sync {
		return werr
	}
	return nil
}

func writeFileAtomicSync(path string, data []byte, perm os.FileMode, sync bool) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if sync {
		if err := tmp.Sync(); err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Remove deletes one recording file and its manifest entry. The file being
// written is never removed.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidFileName(name) {
		return errors.New("blackbox: not a recording file")
	}
	if name == s.curN {
		return ErrCurrentFile
	}
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	kept := s.m.Files[:0]
	for _, f := range s.m.Files {
		if f.Name != name {
			kept = append(kept, f)
		}
	}
	s.m.Files = kept
	s.pruneGapsLocked()
	s.recomputeCoverage()
	_ = s.writeManifestLocked(false)
	return nil
}

// pruneGapsLocked drops gaps that lie wholly before the oldest remaining file.
func (s *Store) pruneGapsLocked() {
	if len(s.m.Files) == 0 {
		return
	}
	oldest := s.m.Files[0].Hour
	kept := s.m.Gaps[:0]
	for _, g := range s.m.Gaps {
		if !g.To.Before(oldest) {
			kept = append(kept, g)
		}
	}
	s.m.Gaps = kept
}

// Close ends the run cleanly: the last file is flushed and the manifest says
// stopped, with the reason.
func (s *Store) Close(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil {
		_ = s.cur.Sync()
		_ = s.cur.Close()
		s.cur, s.curN = nil, ""
	}
	now := s.now().UTC()
	if n := len(s.m.Runs); n > 0 && s.m.Runs[n-1].ID == s.opt.RunID {
		s.m.Runs[n-1].Stop, s.m.Runs[n-1].Reason = &now, reason
	}
	s.m.State = ManifestStopped
	return s.writeManifestLocked(true)
}

// Manifest returns a copy of the manifest as it stands.
func (s *Store) Manifest() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.m
	m.Files = append([]FileEntry(nil), s.m.Files...)
	m.Gaps = append([]Gap(nil), s.m.Gaps...)
	m.Events = append([]Event(nil), s.m.Events...)
	m.Runs = append([]RunEntry(nil), s.m.Runs...)
	return m
}

// CurrentFile is the name of the file being written ("" before the first
// sample).
func (s *Store) CurrentFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.curN
}

// Dir is the recordings folder.
func (s *Store) Dir() string { return s.dir }

// ManifestWritable says whether the last manifest write worked.
func (s *Store) ManifestWritable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifestOK
}
