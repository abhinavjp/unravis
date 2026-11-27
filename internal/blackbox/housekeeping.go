package blackbox

import (
	"fmt"
	"sort"
	"time"
)

// Limits are the housekeeping limits (decision A37). Housekeeping cannot be
// turned off: Validate refuses a config whose limits are not positive.
type Limits struct {
	KeepDays       int
	MaxBytes       int64
	MinFreePercent float64
	// SkipAge leaves the age rule out of this pass. The recorder sets it right
	// after it saw the wall clock jump forward: by the clock, every file may
	// look old, and the clock may be about to be corrected.
	SkipAge bool
}

// MaxAgeRemovalsPerPass caps how many files the age rule removes in one pass.
// Normally it removes one an hour, so the cap is never reached; after a clock
// that stays wrong, or a long stop, the history goes in steps the owner can
// notice, not all at once.
const MaxAgeRemovalsPerPass = 6

// KeepFloor is the newest stretch of recordings the free-space rule may never
// remove (RV-BB-2, PLAN O-1). When the drive is still too full after
// everything older has gone, the Blackbox pauses and says so; it does not eat
// into the last day, which is what an investigator needs most.
const KeepFloor = 24 * time.Hour

// keepNewestFiles is the count rule of the keep floor: the newest 25 files by
// hour order (24 whole hours plus the partly written current one) stay whatever
// the clock says.
const keepNewestFiles = 25

// FloorHeldMessage is the plain-English event text for a pass in which the
// keep floor stopped a removal the free-space rule wanted.
const FloorHeldMessage = "The disk is low, but the newest 24 hours of recordings are kept. Free some space on the recordings drive."

// Housekeeping is what one housekeeping pass did.
type Housekeeping struct {
	Removed     []string // files removed, oldest first
	Skipped     []string // files that could not be removed (a reader holds them); tried again next pass
	FreePercent float64  // free space of the recordings volume after the pass
	DiskKnown   bool     // false when the volume could not be read
	DiskLow     bool     // still below the free-disk limit after removing what may go
	Bytes       int64    // size of the recordings after the pass
	// FloorHeld: the free-space rule wanted to remove a file but it is inside the
	// newest 24 hours (KeepFloor), so it stayed. DiskLow is set with it.
	FloorHeld bool
}

// Incomplete reports that a file could not be removed, so the pass may have
// left the recordings over a limit. The caller must not read "disk not low"
// out of such a pass without a fresh free-space reading.
func (h Housekeeping) Incomplete() bool { return len(h.Skipped) > 0 }

// Housekeep removes the oldest recording files until the limits are met: first
// the files older than the keep days, then the oldest files until the total is
// under the size limit, then the oldest files until the free space of the
// volume is at least the minimum. The file being written (and the one for the
// current hour) is never removed, and nothing outside the recording files is
// touched (the snapshots folder, the manifest). The free-space rule also keeps
// the newest KeepFloor (24 hours) of recordings. If the free space is still too
// low when only protected files and the floor are left, DiskLow is set (and
// FloorHeld when the floor was what stopped a removal): the recorder pauses
// until space comes back. A file that cannot be removed (a reader downloading
// it holds it open on Windows) is skipped and listed in Skipped; the pass goes
// on with the next candidate and the next rule, and tries the file again next
// pass.
func (s *Store) Housekeep(l Limits, disk DiskSpace, now time.Time) (Housekeeping, error) {
	var res Housekeeping
	now = now.UTC()
	protected := FileNameFor(now)

	// The file list is copied once for the whole pass (oldest first) and the
	// decisions are made on that copy; the manifest file is written once at the
	// end, not after every removal.
	files := s.fileEntries()
	cur := s.CurrentFile()
	var total int64
	for _, f := range files {
		total += f.Bytes
	}
	skipped := map[string]bool{}
	gone := map[string]bool{}
	// remove takes one file out; a failure parks the file for this pass.
	remove := func(f FileEntry) {
		if err := s.remove(f.Name, false); err != nil {
			skipped[f.Name] = true
			res.Skipped = append(res.Skipped, f.Name)
			return
		}
		gone[f.Name] = true
		total -= f.Bytes
		res.Removed = append(res.Removed, f.Name)
	}
	// next is the oldest file that may go.
	next := func() (FileEntry, bool) {
		for _, f := range files {
			if f.Name != cur && f.Name != protected && !skipped[f.Name] && !gone[f.Name] {
				return f, true
			}
		}
		return FileEntry{}, false
	}

	// Age: a file goes once the whole hour it covers is older than the keep time.
	if l.KeepDays > 0 && !l.SkipAge {
		cutoff := now.Add(-time.Duration(l.KeepDays) * 24 * time.Hour)
		byAge := 0
		for _, f := range files {
			if f.Name == cur || f.Name == protected {
				continue
			}
			if f.Hour.Add(time.Hour).Before(cutoff) {
				if byAge >= MaxAgeRemovalsPerPass {
					break
				}
				byAge++
				remove(f)
			}
		}
	}
	// Size.
	if l.MaxBytes > 0 {
		for total > l.MaxBytes {
			f, ok := next()
			if !ok {
				break
			}
			remove(f)
		}
	}
	// Free space. Invariant: this rule never removes a file that is in the keep
	// floor. A file is in the floor when EITHER
	//  1. its hour is not before (anchor - 24h), where the anchor is the EARLIER
	//     of the newest recording file's hour and the current hour. The current
	//     hour's file is only partly written, so the whole hours back to
	//     anchor-24h are kept to cover a full 24 hours. Taking the earlier anchor
	//     errs toward keeping more: after a long stop "now" is far past the newest
	//     file, so the newest file anchors the floor (measuring from the clock
	//     would empty the folder); after a clock that ran ahead and was corrected,
	//     a file named in the future is the newest, so the current hour anchors
	//     the floor; or
	//  2. it is one of the newest keepNewestFiles files by name (hour) order,
	//     whatever the clock says. This covers a clock that ran behind and was
	//     corrected: the old files carry old names, the one new file carries the
	//     real hour, and the time rule alone would hold only that one file.
	// The age and size rules above are not limited by the floor.
	if disk != nil && l.MinFreePercent > 0 {
		byHour := append([]FileEntry(nil), files...)
		sort.SliceStable(byHour, func(i, j int) bool { return byHour[i].Hour.Before(byHour[j].Hour) })
		newest := map[string]bool{}
		for i := len(byHour) - 1; i >= 0 && len(newest) < keepNewestFiles; i-- {
			newest[byHour[i].Name] = true
		}
		anchor := time.Time{}
		for _, f := range files {
			if f.Hour.After(anchor) {
				anchor = f.Hour
			}
		}
		if nowHour := now.Truncate(time.Hour); nowHour.Before(anchor) {
			anchor = nowHour
		}
		floorStart := anchor.Add(-KeepFloor)
		inFloor := func(f FileEntry) bool { return !f.Hour.Before(floorStart) || newest[f.Name] }
		// nextOutsideFloor is the oldest file that may go and is not in the floor.
		// held reports that next() still had a candidate, so the floor is what
		// stopped the pass, not an empty list.
		nextOutsideFloor := func() (f FileEntry, ok, held bool) {
			for _, c := range files {
				if c.Name == cur || c.Name == protected || skipped[c.Name] || gone[c.Name] {
					continue
				}
				if inFloor(c) {
					held = true
					continue
				}
				return c, true, held
			}
			return FileEntry{}, false, held
		}
		for {
			info, err := disk.Free(s.dir)
			if err != nil {
				break
			}
			res.DiskKnown, res.FreePercent = true, info.FreePercent()
			if res.FreePercent >= l.MinFreePercent {
				break
			}
			f, ok, held := nextOutsideFloor()
			if !ok {
				res.DiskLow = true
				res.FloorHeld = held
				break
			}
			remove(f)
		}
	}
	res.Bytes = total
	if len(res.Removed) > 0 {
		// One manifest write for the whole pass: the event below writes it.
		s.Event(EventHousekeeping, fmt.Sprintf("removed %d file(s), oldest first: %s to %s", len(res.Removed), res.Removed[0], res.Removed[len(res.Removed)-1]))
	}
	return res, nil
}
