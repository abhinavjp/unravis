package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// maxInput is an entry with every field at its limit, using "<", which JSON
// escapes to six bytes, so it is the worst case for serialised size.
func maxInput() Input {
	long := strings.Repeat("<", MaxTextLen)
	in := Input{Text: long, State: StateOpen, Notes: long}
	for i := 0; i < MaxListLen; i++ {
		in.EvidenceIDs = append(in.EvidenceIDs, strings.Repeat("z", 120))
		in.Facts.Counterexamples = append(in.Facts.Counterexamples, long)
		in.Facts.Alternatives = append(in.Facts.Alternatives, Alternative{Text: long, Status: long})
	}
	return in
}

func listSize(t *testing.T, s Store) int {
	t.Helper()
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	return len(b) + 1 // the server's encoder adds a newline
}

func TestTotalSizeBudget(t *testing.T) {
	if MaxTotalBytes >= 16<<20 {
		t.Fatalf("MaxTotalBytes %d is not below the 16 MiB response cap", MaxTotalBytes)
	}
	s := newMem(t)
	in := maxInput()
	created, refused := 0, false
	for i := 0; i < MaxHypotheses; i++ {
		h, err := s.Create(fmt.Sprintf("entry %d", i))
		if err != nil {
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "The ledger is full. Delete or shorten entries before adding more.") {
				t.Fatalf("create %d: %v", i, err)
			}
			refused = true
			break
		}
		created++
		if _, err := s.Update(h.ID, in); err != nil {
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "The ledger is full.") {
				t.Fatalf("update %d: %v", i, err)
			}
			refused = true
			break
		}
		if n := listSize(t, s); n > MaxTotalBytes {
			t.Fatalf("list is %d bytes after %d entries, over the budget %d", n, created, MaxTotalBytes)
		}
	}
	if !refused {
		t.Fatal("max-size entries were never refused")
	}
	if created < 2 {
		t.Fatalf("only %d entries fit; the budget is too tight for a maximum-size entry", created)
	}
	if n := listSize(t, s); n > MaxTotalBytes || n > 16<<20 {
		t.Fatalf("list is %d bytes", n)
	}
}

func TestSingleMaxEntryFits(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, "x")
	if _, err := s.Update(h.ID, maxInput()); err != nil {
		t.Fatalf("one maximum-size entry must fit: %v", err)
	}
}

func TestBudgetAppliesToReuseAndRecovers(t *testing.T) {
	s := newMem(t)
	in := maxInput()
	var ids []string
	for {
		h := mustCreate(t, s, "x")
		if _, err := s.Update(h.ID, in); err != nil {
			// Update refused: the entry stays small. Fill the remaining room with reuse
			// of a big entry instead.
			if err := s.Delete(h.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
		ids = append(ids, h.ID)
	}
	// Room is left for less than one more maximum-size entry; reuse copies text only,
	// so it is small and fits, but a long text edit that grows the ledger does not.
	r, err := s.Reuse(ids[0])
	if err != nil {
		t.Fatalf("reuse of a short text: %v", err)
	}
	if _, err := s.Update(r.ID, in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("growing update over the budget: %v", err)
	}
	// A shrinking edit is still allowed, and so is delete.
	small := in
	small.Notes = "short"
	if _, err := s.Update(ids[0], small); err != nil {
		t.Fatalf("shrinking update: %v", err)
	}
	if err := s.Delete(ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(r.ID, in); err != nil {
		t.Fatalf("update after room was freed: %v", err)
	}
}

func TestCheckTextIdentifiers(t *testing.T) {
	accepted := []string{
		"Audit-create stalls coincide with a SQL lock wait on the audit table",
		"Mongo 7.0.16 stall at 09:14:05 on 2026-10-21",
		"Window 2026-10-24 14:45 to 2026-10-24 15:10 UTC",
		"Started 2026-10-24T14:45:00Z and ended at 14:45",
		"Seen in 12 14 15 16 episodes of 20",
		"Rate was 3.14159265 per second",
		"About 1,234,567 rows, 99.5 percent matched",
		"p95 was 1234 ms across 123456 rows",
		"Phone numbers are not stored here",
		"Evidence ep:2026-10-21t09 and ep:2026-10-22t14 agree",
		"ref p2:b1:0a1b2c3d4e5f6071 and build abc123def",
		"supported_by_strong_correlation-with-a-very-long-hyphenated-word-without-digits",
		"Purely alphabetic AbCdEfGhIjKlMnOpQrStUvWxYzAbCdEfGhIjKlMnOpQrStUvWxYz run",
		"eyJ is the prefix of a JWT but this is a sentence. Not one.",
		"version 1.2.3 and 10.20.30",
	}
	rejected := []struct{ name, text string }{
		{"phone intl spaces", "call +44 7700 900123 for the owner"},
		{"phone intl compact", "+447700900123"},
		{"phone us dashes", "555-123-4567"},
		{"phone us parens", "(555) 123-4567"},
		{"phone dots", "555.123.4567"},
		{"phone spaces", "555 123 4567"},
		{"phone contiguous", "07700900123"},
		{"seven digit id", "user 1234567 stalled"},
		{"date then phone", "on 2026-10-24 call 555-123-4567"},
		{"jwt", "token eyJhbGciOiJIUzI1NiJ9.x.y"},
		{"jwt full", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"hex 32", "key 0123456789abcdef0123456789abcdef end"},
		{"hex 40", "sha da39a3ee5e6b4b0d3255bfef95601890afd80709"},
		{"base64 40", "secret Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MEFCQ0RFRkdISUo= end"},
		{"base64url 44", "secret aB3_dE6-gH9jK2mN5pQ8sT1vW4yZ7bC0eF3hJ6kL9n"},
		{"existing ipv6", "host 2001:db8::7"},
	}
	for _, text := range accepted {
		if err := checkText("the notes", text); err != nil {
			t.Errorf("accepted %q: %v", text, err)
		}
	}
	for _, c := range rejected {
		err := checkText("the notes", c.text)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %q was not rejected (%v)", c.name, c.text, err)
		}
	}
}
