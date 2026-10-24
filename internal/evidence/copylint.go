package evidence

import (
	"errors"
	"regexp"
)

// ErrCausal is returned by LintCausal.
var ErrCausal = errors.New("causal wording")

// causalRe lists the words importer and join text may never contain:
// cause (any form), because, waiting on, blocked by, due to, proven (any
// form). It is deliberately stricter than the web lint: there is no negation
// exemption, because importer text is fixed sentences and never needs one.
var causalRe = regexp.MustCompile(`(?i)\b(?:caus(?:e|es|ed|ing)|because|waiting\s+on|blocked\s+by|due\s+to|proven|proves|proved)\b`)

// LintCausal rejects text that names a cause. The text is normalised first
// (NFKC, invisible characters removed, look-alike letters folded), so
// "PRОVEN" with a Cyrillic letter or a zero-width space inside a word is
// still caught. The error names the matched word, never the text.
func LintCausal(text string) error {
	if m := causalRe.FindString(normalizeText(text)); m != "" {
		return errors.Join(ErrCausal, errors.New("found \""+m+"\""))
	}
	return nil
}
