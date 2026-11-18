package server

import (
	"strings"
	"testing"
)

// E2E-01: a recorder the budget slowed to 60 s ticks every 60 s. The host lane
// must judge gaps against that cadence, not the configured 15 s base, or every
// sample pair reads as a gap.
func TestHostReadingUsesTheEffectiveInterval(t *testing.T) {
	b := newBBEnv(t, nil)
	rec := bbRec("web-b", func(r *simRec) { r.Interval = 60 })
	rec.man.IntervalSeconds = 15
	rec.man.EffectiveIntervalSeconds = 60
	b.world.put(share, rec)
	it := b.mustAdd(share, "")
	a := b.hostAnswer(it.ID, "2026-10-20T10:00:00Z", "2026-10-20T11:30:00Z")
	if len(a.Gaps) != 0 {
		t.Fatalf("a 60 s cadence made %d fake gaps: %+v", len(a.Gaps), a.Gaps)
	}
	// Without an effective interval the base still judges.
	rec2 := bbRec("web-c", func(r *simRec) { r.Interval = 60 })
	rec2.man.IntervalSeconds = 15
	const share2 = `\\web-c\UnravisBlackbox`
	b.world.put(share2, rec2)
	it2 := b.mustAdd(share2, "")
	if a2 := b.hostAnswer(it2.ID, "2026-10-20T10:00:00Z", "2026-10-20T11:30:00Z"); len(a2.Gaps) == 0 {
		t.Fatal("control: a 15 s base with 60 s samples should read as gaps")
	}
}

// E2E-06: a recording that covers too little of the incident says so in Step 9,
// not "no host data is imported".
func TestHostReadingStep9NamesTheCause(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil)) // records 08:30 to 12:00
	it := b.mustAdd(share, "")
	// The incident is entirely after the recording ended.
	a := b.hostAnswer(it.ID, "2026-10-20T13:00:00Z", "2026-10-20T14:00:00Z")
	reasons := strings.Join(a.Step.Reasons, " ")
	if strings.Contains(reasons, "No host data is imported") {
		t.Fatalf("step 9 says nothing is imported: %q", reasons)
	}
	if !strings.Contains(a.Note, "covers 0 of 60 minutes") {
		t.Fatalf("note = %q", a.Note)
	}
}
