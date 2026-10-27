package blackbox

import (
	"fmt"
	"time"
)

// Limits are the housekeeping limits (decision A37). Housekeeping cannot be
// turned off: Validate refuses a config whose limits are not positive.
type Limits struct {
	KeepDays       int
	MaxBytes       int64
	MinFreePercent float64
}

// Housekeeping is what one housekeeping pass did.
type Housekeeping struct {
	Removed     []string // files removed, oldest first
	Skipped     []string // files that could not be removed (a reader holds them); tried again next pass
	FreePercent float64  // free space of the recordings volume after the pass
	DiskKnown   bool     // false when the volume could not be read
	DiskLow     bool     // still below the free-disk limit after removing what may go
	Bytes       int64    // size of the recordings after the pass
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
// touched (the snapshots folder, the manifest). If the free space is still too
// low when only protected files are left, DiskLow is set: the recorder pauses
// until space comes back. A file that cannot be removed (a reader downloading
// it holds it open on Windows) is skipped and listed in Skipped; the pass goes
// on with the next candidate and the next rule, and tries the file again next
// pass.
func (s *Store) Housekeep(l Limits, disk DiskSpace, now time.Time) (Housekeeping, error) {
	var res Housekeeping
	now = now.UTC()
	protected := FileNameFor(now)

	skipped := map[string]bool{}
	// remove takes one file out; a failure parks the file for this pass.
	remove := func(name string) {
		if err := s.Remove(name); err != nil {
			skipped[name] = true
			res.Skipped = append(res.Skipped, name)
			return
		}
		res.Removed = append(res.Removed, name)
	}
	// next is the oldest file that may go, or "".
	next := func() string {
		cur := s.CurrentFile()
		for _, f := range s.Manifest().Files {
			if f.Name != cur && f.Name != protected && !skipped[f.Name] {
				return f.Name
			}
		}
		return ""
	}

	// Age: a file goes once the whole hour it covers is older than the keep time.
	if l.KeepDays > 0 {
		cutoff := now.Add(-time.Duration(l.KeepDays) * 24 * time.Hour)
		for _, f := range s.Manifest().Files {
			if f.Name == s.CurrentFile() || f.Name == protected {
				continue
			}
			if f.Hour.Add(time.Hour).Before(cutoff) {
				remove(f.Name)
			}
		}
	}
	// Size.
	if l.MaxBytes > 0 {
		for s.Manifest().TotalBytes() > l.MaxBytes {
			n := next()
			if n == "" {
				break
			}
			remove(n)
		}
	}
	// Free space.
	if disk != nil && l.MinFreePercent > 0 {
		for {
			info, err := disk.Free(s.dir)
			if err != nil {
				break
			}
			res.DiskKnown, res.FreePercent = true, info.FreePercent()
			if res.FreePercent >= l.MinFreePercent {
				break
			}
			n := next()
			if n == "" {
				res.DiskLow = true
				break
			}
			remove(n)
		}
	}
	res.Bytes = s.Manifest().TotalBytes()
	if len(res.Removed) > 0 {
		s.Event(EventHousekeeping, fmt.Sprintf("removed %d file(s), oldest first: %s to %s", len(res.Removed), res.Removed[0], res.Removed[len(res.Removed)-1]))
	}
	return res, nil
}
