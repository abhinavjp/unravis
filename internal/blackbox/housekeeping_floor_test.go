package blackbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// RV-BB-2 / PLAN O-1: the free-space rule keeps the newest 24 hours of
// recordings. 30 hourly files of 1000 bytes: hours 00-05 are older than the
// floor, hours 06-29 are the floor (24 files), hour 29 is also the open file.
func TestHousekeepKeepFloor(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		other       uint64 // bytes on the volume that are not recordings (total is 100000)
		minFree     float64
		wantRemoved int
		wantLow     bool
		wantHeld    bool
		wantLeft    int // recording files left
	}{
		// Plenty of space: nothing changes.
		{"plenty of space", 10000, 10, 0, false, false, 30},
		// Short of space, and the oldest files are enough: nothing to hold back.
		{"older files are enough", 62500, 10, 3, false, false, 27},
		// Short of space and even all older files are not enough: the six older
		// ones go, the newest 24 hours stay, the disk stays low.
		{"older files not enough", 70000, 10, 6, true, true, 24},
		// Wants everything gone: still only the six older ones.
		{"wants everything", 70000, 90, 6, true, true, 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, dir, _ := filled(t, 30, from, 1000)
			disk := &fakeDisk{dir: dir, total: 100000, other: tc.other}
			res, err := s.Housekeep(Limits{MinFreePercent: tc.minFree}, disk, from.Add(29*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Removed) != tc.wantRemoved || res.DiskLow != tc.wantLow || res.FloorHeld != tc.wantHeld {
				t.Fatalf("removed %d low %v held %v, want %d %v %v", len(res.Removed), res.DiskLow, res.FloorHeld, tc.wantRemoved, tc.wantLow, tc.wantHeld)
			}
			for i, n := range res.Removed { // oldest first, and only hours 00-05
				if want := FileNameFor(from.Add(time.Duration(i) * time.Hour)); n != want {
					t.Fatalf("removed[%d] = %s, want %s", i, n, want)
				}
			}
			ents, _ := os.ReadDir(dir)
			left := 0
			for _, e := range ents {
				if ValidFileName(e.Name()) {
					left++
				}
			}
			if left != tc.wantLeft {
				t.Fatalf("%d recording files left, want %d", left, tc.wantLeft)
			}
			// The newest 24 hours are always there.
			if tc.wantRemoved <= 6 {
				for h := 6; h < 30; h++ {
					if _, err := os.Stat(filepath.Join(dir, FileNameFor(from.Add(time.Duration(h)*time.Hour)))); err != nil {
						t.Fatalf("hour %d inside the floor was removed", h)
					}
				}
			}
		})
	}
}

// The floor is measured from the newest recording, not from the clock: after a
// long stop "now" is days later, and the free-space rule must still not empty
// the folder.
func TestHousekeepKeepFloorIsFromNewestRecordingNotNow(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 30, from, 1000)
	_ = s.Close("stopped")
	c := &clock{t: from.Add(10 * 24 * time.Hour)}
	s2 := openT(t, dir, "r2", c)
	disk := &fakeDisk{dir: dir, total: 100000, other: 90000}
	res, err := s2.Housekeep(Limits{MinFreePercent: 90}, disk, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 6 || !res.FloorHeld || !res.DiskLow {
		t.Fatalf("%+v", res)
	}
}

// A clock that ran ahead for an hour and was corrected leaves a file named in the
// future. The floor must not anchor on it: the real newest 24 hours survive.
func TestHousekeepKeepFloorIgnoresAFileNamedInTheFuture(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, c := filled(t, 30, from, 1000)
	now := from.Add(29 * time.Hour)
	c.t = now.Add(48 * time.Hour)
	_ = s.Append(padded("r1", 99, c.t, 1000), "") // the clock ran two days ahead
	c.t = now                                     // and was corrected; the future file is no longer the open one
	_ = s.Close("stopped")
	s2 := openT(t, dir, "r2", c)
	disk := &fakeDisk{dir: dir, total: 100000, other: 90000}
	res, err := s2.Housekeep(Limits{MinFreePercent: 90}, disk, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FloorHeld || !res.DiskLow {
		t.Fatalf("%+v", res)
	}
	for h := 6; h < 30; h++ { // real hours 06-29 all survive
		if _, err := os.Stat(filepath.Join(dir, FileNameFor(from.Add(time.Duration(h)*time.Hour)))); err != nil {
			t.Fatalf("hour %d of the real newest 24 hours was removed", h)
		}
	}
	if len(res.Removed) != 6 {
		t.Fatalf("removed %v", res.Removed)
	}
}

// The age rule is not limited by the floor: files older than the keep days go
// even when they are the newest recordings there are.
func TestHousekeepKeepFloorDoesNotLimitTheAgeRule(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, _, _ := filled(t, 3, from, 100)
	now := from.Add(30 * 24 * time.Hour)
	res, err := s.Housekeep(Limits{KeepDays: 7}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 2 || res.FloorHeld {
		t.Fatalf("%+v", res)
	}
}

// The floor does not hide the open file or an empty folder: with nothing but
// protected files left, FloorHeld stays false (the Blackbox pauses as before).
func TestHousekeepKeepFloorNotReportedWhenOnlyProtectedFilesRemain(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s, dir, _ := filled(t, 1, from, 1000)
	disk := &fakeDisk{dir: dir, total: 10000, other: 9500}
	res, _ := s.Housekeep(Limits{MinFreePercent: 50}, disk, from)
	if !res.DiskLow || res.FloorHeld || len(res.Removed) != 0 {
		t.Fatalf("%+v", res)
	}
}

// The recorder says so once, not every minute, and says it again only after the
// floor stopped holding.
func TestRecorderKeepFloorEventOncePerStretch(t *testing.T) {
	g := newRig(t, nil)
	held := true
	orig := housekeepFn
	housekeepFn = func(s *Store, l Limits, d DiskSpace, now time.Time) (Housekeeping, error) {
		return Housekeeping{FloorHeld: held, DiskLow: held, DiskKnown: true, FreePercent: 5}, nil
	}
	t.Cleanup(func() { housekeepFn = orig })
	count := func() int {
		n := 0
		for _, e := range g.s.Manifest().Events {
			if e.Kind == EventHousekeeping && e.Detail == FloorHeldMessage {
				n++
			}
		}
		return n
	}
	g.r.Tick(g.clk.t)
	g.tick(61 * time.Second)
	g.tick(61 * time.Second)
	if n := count(); n != 1 {
		t.Fatalf("floor events = %d, want 1", n)
	}
	held = false
	g.tick(61 * time.Second)
	held = true
	g.tick(61 * time.Second)
	if n := count(); n != 2 {
		t.Fatalf("floor events after it let go and held again = %d, want 2", n)
	}
}
