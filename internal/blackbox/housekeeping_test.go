package blackbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDisk models a volume of total bytes where the free space is the total
// minus everything in the recordings folder minus other.
type fakeDisk struct {
	dir          string
	total, other uint64
	err          error
}

func (f *fakeDisk) Free(string) (DiskInfo, error) {
	if f.err != nil {
		return DiskInfo{}, f.err
	}
	var used uint64
	ents, _ := os.ReadDir(f.dir)
	for _, e := range ents {
		if i, err := e.Info(); err == nil && ValidFileName(e.Name()) {
			used += uint64(i.Size())
		}
	}
	if used+f.other >= f.total {
		return DiskInfo{Free: 0, Total: f.total}, nil
	}
	return DiskInfo{Free: f.total - f.other - used, Total: f.total}, nil
}

// filled makes a store with one file per hour from `from`, each of size bytes,
// then leaves the store writing the last file.
func filled(t *testing.T, hours int, from time.Time, size int) (*Store, string, *clock) {
	t.Helper()
	dir := t.TempDir()
	c := &clock{t: from}
	s := openT(t, dir, "r1", c)
	for i := 0; i < hours; i++ {
		at := from.Add(time.Duration(i) * time.Hour)
		c.t = at
		_ = s.Append(padded("r1", uint64(i+1), at, size), "")
	}
	return s, dir, c
}

// padded makes a sample whose JSON line is size bytes long (when that is more
// than the bare sample).
func padded(run string, seq uint64, at time.Time, size int) Sample {
	s := smp(run, seq, at)
	s.V = FormatVersion
	b, _ := json.Marshal(s)
	if pad := size - (len(b) + 1) - len(`,"missing":[""]`); pad > 0 {
		s.Missing = []string{strings.Repeat("x", pad)}
	}
	return s
}

func TestHousekeepAgeRemovesOldFilesOnly(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 3, from, 100)
	// Another folder and a stray file must never be touched.
	_ = os.MkdirAll(filepath.Join(dir, "snapshots", "x"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep"), 0o644)

	now := from.Add(7*24*time.Hour + 90*time.Minute)
	res, err := s.Housekeep(Limits{KeepDays: 7}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	// Hours 00 and 01 ended before now-7d (= 01:30 on the 1st): only hour 00 is wholly older.
	if len(res.Removed) != 1 || res.Removed[0] != "bb-20261001-00.ndjson" {
		t.Fatalf("removed %v", res.Removed)
	}
	for _, p := range []string{"snapshots/x", "notes.txt", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("%s gone", p)
		}
	}
	if !hasEvent(s.Manifest(), EventHousekeeping) {
		t.Fatal("event")
	}
}

func TestHousekeepSizeRemovesOldestFirstAndKeepsCurrent(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, _, _ := filled(t, 5, from, 1000)
	now := from.Add(4 * time.Hour)
	res, err := s.Housekeep(Limits{MaxBytes: 2500}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bb-20261027-00.ndjson", "bb-20261027-01.ndjson", "bb-20261027-02.ndjson"}
	if len(res.Removed) != 3 {
		t.Fatalf("removed %v", res.Removed)
	}
	for i, w := range want {
		if res.Removed[i] != w {
			t.Fatalf("removed %v", res.Removed)
		}
	}
	if res.Bytes > 2500 || s.CurrentFile() != "bb-20261027-04.ndjson" {
		t.Fatalf("bytes %d", res.Bytes)
	}
}

func TestHousekeepNeverRemovesCurrentEvenOverSize(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 1, from, 5000)
	res, _ := s.Housekeep(Limits{MaxBytes: 10}, nil, from)
	if len(res.Removed) != 0 {
		t.Fatal("current file removed")
	}
	if _, err := os.Stat(filepath.Join(dir, FileNameFor(from))); err != nil {
		t.Fatal(err)
	}
}

func TestHousekeepFreeSpaceRemovesUntilEnough(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 6, from, 1000)
	// total 10000, other 3500, recordings 6000: free is 5%.
	disk := &fakeDisk{dir: dir, total: 10000, other: 3500}
	now := from.Add(5 * time.Hour)
	res, err := s.Housekeep(Limits{MinFreePercent: 20}, disk, now)
	if err != nil {
		t.Fatal(err)
	}
	// Needs free >= 2000: recordings must be <= 4500, so two files go.
	if res.DiskLow || !res.DiskKnown || res.FreePercent < 20 {
		t.Fatalf("%+v", res)
	}
	if len(res.Removed) != 2 || res.Removed[0] != "bb-20261027-00.ndjson" {
		t.Fatalf("removed %v", res.Removed)
	}
}

func TestHousekeepDiskLowWhenNothingMoreMayGo(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 3, from, 1000)
	disk := &fakeDisk{dir: dir, total: 10000, other: 9000}
	res, err := s.Housekeep(Limits{MinFreePercent: 10}, disk, from.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !res.DiskLow {
		t.Fatalf("%+v", res)
	}
	if len(res.Removed) != 2 || s.CurrentFile() == "" {
		t.Fatalf("removed %v", res.Removed)
	}
	if _, err := os.Stat(filepath.Join(dir, s.CurrentFile())); err != nil {
		t.Fatal("current file must stay")
	}
}

func TestHousekeepDiskReadErrorSkipsFreeRule(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 3, from, 1000)
	res, err := s.Housekeep(Limits{MinFreePercent: 50}, &fakeDisk{dir: dir, err: errors.New("x")}, from.Add(2*time.Hour))
	if err != nil || len(res.Removed) != 0 || res.DiskKnown || res.DiskLow {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestHousekeepProtectsFileOfTheCurrentHourBeforeFirstAppend(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 2, from, 1000)
	// Restart: no current file open yet, but the newest file is this hour's.
	_ = s.Close("stopped")
	c := &clock{t: from.Add(time.Hour + time.Minute)}
	s2 := openT(t, dir, "r2", c)
	res, _ := s2.Housekeep(Limits{MaxBytes: 1}, nil, c.t)
	for _, n := range res.Removed {
		if n == "bb-20261027-01.ndjson" {
			t.Fatal("this hour's file removed")
		}
	}
}
