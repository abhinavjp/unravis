package casestore

import (
	"testing"
	"time"
)

func TestSweepDueIsOneRuleForEveryStore(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { c := now.Add(-d); return &c }
	cases := []struct {
		name string
		info CaseInfo
		want bool
	}{
		{"never closed", CaseInfo{}, false},
		{"closed exactly 30 days ago", CaseInfo{Closed: at(Retention)}, false},
		{"closed just over 30 days ago", CaseInfo{Closed: at(Retention + time.Second)}, true},
		{"closed in the future (a clock that ran ahead)", CaseInfo{Closed: at(-2 * Retention)}, false},
		{"locked own-key case has no close date", CaseInfo{OwnKey: true, KeyLocked: true}, false},
		{"missing folder", CaseInfo{Closed: at(2 * Retention), Missing: true}, false},
		{"archiving", CaseInfo{Closed: at(2 * Retention), Mark: MarkArchiving}, false},
		{"freeing", CaseInfo{Closed: at(2 * Retention), Mark: MarkFreeing}, false},
		{"archive active", CaseInfo{Closed: at(2 * Retention), Archive: &ArchiveInfo{Active: true}}, false},
		{"archive pending", CaseInfo{Closed: at(2 * Retention), Archive: &ArchiveInfo{Pending: &ArchivePending{}}}, false},
		{"archive record, not active, nothing pending", CaseInfo{Closed: at(2 * Retention), Archive: &ArchiveInfo{}}, true},
	}
	for _, c := range cases {
		if got := SweepDue(c.info, now); got != c.want {
			t.Errorf("%s: SweepDue = %v, want %v", c.name, got, c.want)
		}
	}
}
