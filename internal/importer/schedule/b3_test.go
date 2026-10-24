package schedule

import (
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

func bev(kind, ts string, attr map[string]string) evidence.Event {
	t := utc(ts)
	return evidence.Event{Kind: kind, Start: t, End: t, Attrs: attr}
}

func TestB3CountsNearBoundaries(t *testing.T) {
	b := []evidence.Event{
		bev(KindBoundary, "2001-03-24T03:00:00Z", nil),
		bev(KindBoundary, "2001-03-24T04:00:00Z", nil),
		bev(KindJobRun, "2001-03-24T05:00:00Z", nil), // not a boundary
	}
	stalls := []time.Time{
		utc("2001-03-24T03:00:20Z"), // 20 s after a boundary: near
		utc("2001-03-24T03:59:30Z"), // 30 s before: near
		utc("2001-03-24T05:00:00Z"), // only a job run: a real miss
		utc("2001-03-24T03:30:00Z"), // between boundaries: a real miss
	}
	k, n, unk := B3(stalls, b, time.Minute)
	if k != 2 || n != 4 || unk != 0 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
	// The tolerance is inclusive.
	k, _, _ = B3([]time.Time{utc("2001-03-24T03:01:00Z")}, b, time.Minute)
	if k != 1 {
		t.Errorf("edge of tolerance: k=%d", k)
	}
	k, _, _ = B3([]time.Time{utc("2001-03-24T03:01:01Z")}, b, time.Minute)
	if k != 0 {
		t.Errorf("past tolerance: k=%d", k)
	}
}

func TestB3UnknownNeverMiss(t *testing.T) {
	b := []evidence.Event{
		bev(KindBoundary, "2001-03-24T03:00:00Z", nil),
		bev(KindUnknown, "2001-03-24T00:00:00Z", map[string]string{"job": "job-gamma", "reason": reasonCron}),
	}
	stalls := []time.Time{utc("2001-03-24T03:00:00Z"), utc("2001-03-24T09:00:00Z"), {}}
	k, n, unk := B3(stalls, b, time.Minute)
	// One known hit; the miss is unknown because a schedule was unexpandable; a
	// zero time is not a real time and is unknown too.
	if k != 1 || n != 3 || unk != 2 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
	if misses := n - k - unk; misses != 0 {
		t.Errorf("misses = %d, want 0", misses)
	}
}

func TestB3DSTFlaggedBoundaryIsUnknown(t *testing.T) {
	b := []evidence.Event{
		bev(KindBoundary, "2001-03-25T01:30:00Z", map[string]string{"dst": "nonexistent"}),
	}
	k, n, unk := B3([]time.Time{utc("2001-03-25T01:30:10Z"), utc("2001-03-25T06:00:00Z")}, b, time.Minute)
	if k != 0 || n != 2 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
}

func TestB3EmptyAndNegativeTolerance(t *testing.T) {
	if k, n, unk := B3(nil, nil, time.Minute); k != 0 || n != 0 || unk != 0 {
		t.Errorf("empty = %d %d %d", k, n, unk)
	}
	b := []evidence.Event{bev(KindBoundary, "2001-03-24T03:00:00Z", nil)}
	if k, _, _ := B3([]time.Time{utc("2001-03-24T03:00:00Z")}, b, -time.Hour); k != 1 {
		t.Errorf("negative tolerance should behave as zero, k=%d", k)
	}
}

func TestB3WithImportedFixture(t *testing.T) {
	ev, _ := mustParse(t, NewHangfire(), fixture(t, "hangfire_good.csv"), hangfireOpts())
	stalls := []time.Time{
		utc("2001-03-24T05:15:05Z"), // job-alpha every 15 minutes: near
		utc("2001-03-24T05:07:30Z"), // between alpha boundaries, but gamma and delta are unknown
	}
	k, n, unk := B3(stalls, ev, 30*time.Second)
	if k != 1 || n != 2 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
}

func TestB3WordingPassesLint(t *testing.T) {
	for _, c := range []struct{ k, n, u int }{{0, 0, 0}, {3, 10, 0}, {3, 10, 2}, {0, 4, 4}} {
		s := B3Wording(c.k, c.n, c.u, 90*time.Second)
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
		for _, bad := range []string{"strong", "proven", "proves", "caused", "because"} {
			if containsFold(s, bad) {
				t.Errorf("%q contains %q", s, bad)
			}
		}
	}
	if s := B3Wording(3, 10, 2, time.Minute); !containsFold(s, "3 of 10") || !containsFold(s, "not counted as misses") {
		t.Errorf("wording = %q", s)
	}
}

func containsFold(s, sub string) bool {
	return len(sub) <= len(s) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := []rune(s), []rune(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		match := true
		for j := range lsub {
			a, b := ls[i+j], lsub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
