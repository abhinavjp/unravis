package evidence

import "strconv"

// The only permitted wordings for absence and linkage (roadmap §6.6). These
// are exact twins of web/src/lib/copy/wordings.ts; wording_test.go reads that
// file and fails on any template difference. Importer output and the case
// store use these and never paraphrase them.

// num renders a number the way a JavaScript template literal does for the
// ranges used here: shortest form, no exponent, no trailing zeros.
func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// NotObservedThreshold: a source with a recording threshold shows no event.
func NotObservedThreshold(source, threshold string) string {
	return "Not observed in " + source + ". This source only records " + threshold + "; shorter waits would not appear."
}

// NotObservedGap: a source with a gap, sampling or retention limit shows no event.
func NotObservedGap(detail string) string {
	return "Not observed (gap or sampling: " + detail + ")."
}

// OverlapUnique: a qualifying event overlaps a stalled audit interval and the
// join class is unique.
func OverlapUnique(source, event string, seconds float64) string {
	return "A " + source + " session had a " + event + " of " + num(seconds) + " s overlapping this interval (Correlation)."
}

// OverlapAmbiguous: a qualifying event overlaps and the join class is ambiguous.
func OverlapAmbiguous(candidates int) string {
	return "Possible overlap (ambiguous: " + strconv.Itoa(candidates) + " candidates)."
}

// AuditCreate: an audit duration is large. It states what was measured,
// never a cause.
func AuditCreate(seconds float64) string {
	return "Audit create took " + num(seconds) + " s as measured from the client side."
}
