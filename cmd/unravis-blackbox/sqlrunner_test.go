package main

import (
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// C13 wiring: the SQL service must carry the owner's disk bounds, because host
// housekeeping never looks inside the \sql subfolder. These are the numbers a
// real install would enforce; the enforcement itself (a full disk, a folder
// over its cap) needs the owner's server.
func TestSQLDiskBounds(t *testing.T) {
	minFree, keepHours, maxBytes := sqlDiskBounds(blackbox.Config{
		KeepDays: 7, MaxGB: 2, MinFreePercent: 10,
	})
	if minFree != 10 {
		t.Errorf("free-space floor = %d, want the owner's 10", minFree)
	}
	if keepHours != 168 {
		t.Errorf("retention = %d hours, want 7 days in hours", keepHours)
	}
	if want := int64((2 << 30) / sqlBudgetShare); maxBytes != want {
		t.Errorf("byte cap = %d, want %d (maxGB / %d)", maxBytes, want, sqlBudgetShare)
	}

	// The smallest settings the validator accepts must still bound the folder.
	_, keepHours, maxBytes = sqlDiskBounds(blackbox.Config{
		KeepDays: 1, MaxGB: 0.1, MinFreePercent: 1,
	})
	if keepHours != 24 {
		t.Errorf("keepDays 1 -> %d hours", keepHours)
	}
	if maxBytes <= 0 {
		t.Errorf("byte cap = %d, want a positive bound at the smallest config", maxBytes)
	}
}

// sqlFreePercent answers for a real folder: a reading, or an error the runner
// treats as "no room". A negative or above-100 answer would silently disable
// the floor, so it is bounded here.
func TestSQLFreePercentRange(t *testing.T) {
	free, err := sqlFreePercent(t.TempDir())
	if err != nil {
		t.Skipf("this platform cannot read volume free space: %v", err)
	}
	if free < 0 || free > 100 {
		t.Errorf("free percent = %v, want 0..100", free)
	}
}
