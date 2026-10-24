package ledger

import (
	"path/filepath"
	"strings"
	"testing"
)

// prefixesOf mirrors evidence.CasePrefixes, which this package cannot import
// (evidence imports ledger).
func prefixesOf(caseID string) []string {
	return []string{"ev:" + caseID[:8] + ":", "jn:" + caseID[:8] + ":"}
}

const (
	stripCaseA = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	stripCaseB = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	stripRepl  = "case erased"
)

func stripID(kind, caseID, rest string) string {
	return kind + ":" + caseID[:8] + ":" + rest
}

func stripper(t *testing.T, s Store) Stripper {
	t.Helper()
	st, ok := s.(Stripper)
	if !ok {
		t.Fatal("store does not implement Stripper")
	}
	return st
}

func seedEntry(t *testing.T, s Store, text string, ids []string, notes string) Hypothesis {
	t.Helper()
	h := mustCreate(t, s, text)
	out, err := s.Update(h.ID, Input{
		Text:        text,
		State:       StateOpen,
		EvidenceIDs: ids,
		Notes:       notes,
		Facts: Facts{
			Counterexamples: []string{},
			Alternatives:    []Alternative{{Text: "Scheduled index rebuild", Status: AltOpen}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStripEvidenceReplacesIDsOfOneCaseOnly(t *testing.T) {
	s := newMem(t)
	a1 := stripID("ev", stripCaseA, "toy:abcdefabcdef0001")
	a2 := stripID("jn", stripCaseA, "b1-overlap:abcdefabcdef0002")
	b1 := stripID("ev", stripCaseB, "toy:abcdefabcdef0003")
	h := seedEntry(t, s, "Audit-create stalls coincide with a lock wait", []string{a1, b1, a2, "ep:2001-01-02t03"}, "")

	n, err := stripper(t, s).StripEvidence(prefixesOf(stripCaseA), stripRepl)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("replaced %d, want 2", n)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("entries = %d", len(list))
	}
	got := list[0]
	want := []string{stripRepl, b1, "ep:2001-01-02t03"}
	if strings.Join(got.EvidenceIDs, "|") != strings.Join(want, "|") {
		t.Fatalf("ids = %v, want %v", got.EvidenceIDs, want)
	}
	if got.State != h.State || !got.UpdatedAt.Equal(h.UpdatedAt) || len(got.History) != len(h.History) {
		t.Fatal("state, update time or history changed")
	}
}

func TestStripEvidenceFreeText(t *testing.T) {
	s := newMem(t)
	a1 := stripID("ev", stripCaseA, "toy:abcdefabcdef0001")
	b1 := stripID("ev", stripCaseB, "toy:abcdefabcdef0003")
	h := seedEntry(t, s, "Lock wait seen", nil, "see "+a1+" and "+b1+" for the first stall")
	n, err := stripper(t, s).StripEvidence(prefixesOf(stripCaseA), stripRepl)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("replaced %d, want 1", n)
	}
	list, _ := s.List()
	var got Hypothesis
	for _, e := range list {
		if e.ID == h.ID {
			got = e
		}
	}
	want := "see " + stripRepl + " and " + b1 + " for the first stall"
	if got.Notes != want {
		t.Fatalf("notes = %q, want %q", got.Notes, want)
	}
}

func TestStripEvidenceNoMatchChangesNothing(t *testing.T) {
	s := newMem(t)
	seedEntry(t, s, "Lock wait seen", []string{stripID("ev", stripCaseB, "toy:abcdefabcdef0003")}, "")
	before, _ := s.List()
	n, err := stripper(t, s).StripEvidence(prefixesOf(stripCaseA), stripRepl)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	after, _ := s.List()
	if len(before) != len(after) || !before[0].UpdatedAt.Equal(after[0].UpdatedAt) || before[0].EvidenceIDs[0] != after[0].EvidenceIDs[0] {
		t.Fatal("ledger changed")
	}
}

func TestStripEvidenceEmptyPrefixIgnored(t *testing.T) {
	s := newMem(t)
	seedEntry(t, s, "Lock wait seen", []string{"ep:2001-01-02t03"}, "")
	n, err := stripper(t, s).StripEvidence([]string{"", ""}, stripRepl)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	list, _ := s.List()
	if list[0].EvidenceIDs[0] != "ep:2001-01-02t03" {
		t.Fatal("an empty prefix matched an entry")
	}
}

func TestStripEvidencePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedEntry(t, s, "Lock wait seen", []string{stripID("ev", stripCaseA, "toy:abcdefabcdef0001")}, "")
	if _, err := stripper(t, s).StripEvidence(prefixesOf(stripCaseA), stripRepl); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := again.List()
	if len(list) != 1 || len(list[0].EvidenceIDs) != 1 || list[0].EvidenceIDs[0] != stripRepl {
		t.Fatalf("reopened ids = %v", list[0].EvidenceIDs)
	}
}
