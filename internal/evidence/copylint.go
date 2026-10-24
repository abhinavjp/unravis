package evidence

import (
	"errors"
	"regexp"
)

// ErrCausal is returned by LintCausal.
var ErrCausal = errors.New("causal wording")

// causalRe lists the words and phrases importer, join and export text may
// never contain: cause (any form), because, waiting on, blocked by, due to,
// owing to, resulted in, led to, attributable, triggered by, root cause,
// responsible for, proven and proof (any form), and the like. It is matched
// against claimNorm text, where words are single-spaced and lower case. It is
// deliberately stricter than the web lint: there is no negation exemption,
// because importer text is fixed sentences and never needs one.
var causalRe = regexp.MustCompile(`\b(?:caus(?:e|es|ed|ing|al|ally|ality|ation|ative)|because|waiting on|blocked by|due to|owing to|resulted in|results from|resulting from|led to|leads to|attributable|triggered by|root cause|stems from|responsible for|as a result|proven|proves|proved|provably|proof)\b`)

// LintCausal rejects text that names a cause. The text is folded by claimNorm
// first (NFKC, invisible characters and accents removed, look-alike letters
// folded, punctuation read as a space), so "PRОVEN" with a Cyrillic letter, a
// zero-width space inside a word, "blocked_by" and "due-to" are still caught.
// The error names the matched word, never the text.
func LintCausal(text string) error {
	if m := causalRe.FindString(claimNorm(text)); m != "" {
		return errors.Join(ErrCausal, errors.New("found \""+m+"\""))
	}
	return nil
}
