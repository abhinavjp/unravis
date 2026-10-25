package ledger

import (
	"regexp"
	"strings"
)

// idRunClass is the characters an evidence ID may hold after its prefix; a
// reference inside free text ends at the first character outside it.
const idRunClass = `[a-z0-9:_.\-]*`

// Stripper is implemented by the Store that Open and OpenSealed return. It is a
// separate interface so adding it breaks no other Store implementation.
type Stripper interface {
	StripEvidence(prefixes []string, replacement string) (int, error)
}

// StripEvidence replaces every reference to an erased case's evidence with
// replacement and returns how many references it replaced. A reference is an
// evidence ID that starts with one of prefixes (evidence.CasePrefixes(id)):
// either a whole entry of EvidenceIDs, or a token inside the free-text fields
// (hypothesis text, notes, counterexamples, alternatives), where an analyst may
// have pasted one. An entry that held several references to the case ends with
// one replacement, not several. Entries keep their state, facts and history; the
// ledger never changes a state on its own. An empty prefix is ignored, since it
// would match everything. On a save error the ledger is unchanged.
func (s *store) StripEvidence(prefixes []string, replacement string) (int, error) {
	var kept []string
	for _, p := range prefixes {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return 0, nil
	}
	quoted := make([]string, len(kept))
	for i, p := range kept {
		quoted[i] = regexp.QuoteMeta(p)
	}
	token := regexp.MustCompile(`(?:` + strings.Join(quoted, "|") + `)` + idRunClass)

	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	next := s.snapshot()
	for i := range next {
		h := clone(next[i])
		n := 0
		ids := make([]string, 0, len(h.EvidenceIDs))
		replaced := false
		for _, id := range h.EvidenceIDs {
			if hasAnyPrefix(id, kept) {
				n++
				if !replaced {
					ids = append(ids, replacement)
					replaced = true
				}
				continue
			}
			ids = append(ids, id)
		}
		h.EvidenceIDs = ids
		text := func(v string) string {
			return token.ReplaceAllStringFunc(v, func(string) string { n++; return replacement })
		}
		h.Text = text(h.Text)
		h.Notes = text(h.Notes)
		h.SettlingObservation = text(h.SettlingObservation)
		for j := range h.Facts.Counterexamples {
			h.Facts.Counterexamples[j] = text(h.Facts.Counterexamples[j])
		}
		for j := range h.Facts.Alternatives {
			h.Facts.Alternatives[j].Text = text(h.Facts.Alternatives[j].Text)
		}
		if n > 0 {
			next[i] = h
			count += n
		}
	}
	if count == 0 {
		return 0, nil
	}
	if err := s.commit(next); err != nil {
		return 0, err
	}
	return count, nil
}

func hasAnyPrefix(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}
