package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const settlingText = "If the lock wait is gone on the next nightly run, the theory fails."

func TestSettlingObservationRoundTripAndTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := mustCreate(t, s, demoText)
	if h.SettlingObservation != "" {
		t.Fatalf("new entry has an observation: %q", h.SettlingObservation)
	}
	got, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, SettlingObservation: "  " + settlingText + " \n"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SettlingObservation != settlingText {
		t.Fatalf("not trimmed: %q", got.SettlingObservation)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := again.List()
	if len(list) != 1 || list[0].SettlingObservation != settlingText {
		t.Fatalf("not persisted: %+v", list)
	}
	// Clearing it works.
	cleared, err := again.Update(h.ID, Input{Text: demoText, State: StateOpen})
	if err != nil || cleared.SettlingObservation != "" {
		t.Fatalf("clear: %v %q", err, cleared.SettlingObservation)
	}
}

func TestSettlingObservationValidation(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	try := func(v string) error {
		_, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, SettlingObservation: v})
		return err
	}
	for name, v := range map[string]string{
		"exactly 500":   strings.Repeat("x", MaxSettlingLen),
		"500 runes":     strings.Repeat("é", MaxSettlingLen),
		"line break":    "first look\nthen look again",
		"tab":           "a\tb",
		"empty":         "",
		"only spaces":   "   ",
		"times and ids": "Stall at 09:14:05 on 2026-10-21 in ref p2:b1:0a1b2c3d4e5f6071",
	} {
		if err := try(v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := try("keep me"); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"501":          strings.Repeat("x", MaxSettlingLen+1),
		"501 runes":    strings.Repeat("é", MaxSettlingLen+1),
		"nul":          "a\x00b",
		"escape":       "a\x1bb",
		"delete":       "a\x7fb",
		"c1 control":   "a\u0085b",
		"email":        "ask ops.team@example.com",
		"ip":           "host 10.20.30.40",
		"url":          "see http://intranet/req/1",
		"record id":    "row 507f1f77bcf86cd799439011",
		"secret":       "key aB3dE6gH9jK2mN5pQ8sT1vW4yZ7bC0eF end",
		"phone number": "call 555 123 4567",
	} {
		if err := try(v); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s was not rejected: %v", name, err)
		}
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].SettlingObservation != "keep me" {
		t.Fatalf("a rejected edit changed the entry: %+v", list)
	}
}

func TestSettlingObservationOverLengthMessageNamesLimit(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	_, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, SettlingObservation: strings.Repeat("x", 501)})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}

func TestReuseCopiesSettlingObservation(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateContradicted, SettlingObservation: settlingText, Notes: "n"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reuse(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SettlingObservation != settlingText {
		t.Fatalf("not copied: %q", r.SettlingObservation)
	}
	if r.Notes != "" || r.State != StateOpen {
		t.Fatalf("reuse changed other rules: %+v", r)
	}
}

func TestStripEvidenceCoversSettlingObservation(t *testing.T) {
	s := newMem(t)
	a1 := stripID("ev", stripCaseA, "toy:abcdefabcdef0001")
	b1 := stripID("ev", stripCaseB, "toy:abcdefabcdef0003")
	h := mustCreate(t, s, "Lock wait seen")
	if _, err := s.Update(h.ID, Input{Text: "Lock wait seen", State: StateOpen, SettlingObservation: "check " + a1 + " then " + b1}); err != nil {
		t.Fatal(err)
	}
	n, err := stripper(t, s).StripEvidence(prefixesOf(stripCaseA), stripRepl)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	list, _ := s.List()
	if want := "check " + stripRepl + " then " + b1; list[0].SettlingObservation != want {
		t.Fatalf("got %q, want %q", list[0].SettlingObservation, want)
	}
}

func TestOldLedgerFileWithoutTheFieldStillOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	old := `{"version":1,"hypotheses":[{"id":"abc","text":"t","state":"open","evidenceIds":[],"notes":"","facts":{"presentK":0,"totalN":0,"counterexamples":[],"alternatives":[],"timezoneRecorded":false,"confounderOverlap":false,"coverageGap":false,"overlapOnly":false},"guards":[],"createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z","history":[]}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].SettlingObservation != "" {
		t.Fatalf("list = %+v", list)
	}
}
