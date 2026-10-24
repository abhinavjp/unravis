package evidence

import (
	"strconv"
	"strings"
)

// The only permitted wordings for absence and linkage (roadmap §6.6). These
// are exact twins of web/src/lib/copy/wordings.ts; wording_test.go reads that
// file and fails on any template difference. Importer output and the case
// store use these and never paraphrase them.

// num renders a number the way a JavaScript template literal does for the
// ranges used here: shortest form, no exponent, no trailing zeros.
func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// consonantSoundPrefixes start with a vowel letter but a consonant sound
// ("a unique wait", "a use"). Twin of CONSONANT_SOUND_PREFIXES in wordings.ts.
var consonantSoundPrefixes = []string{"uni", "use", "usu", "uti", "eu", "one", "ubiq"}

// article returns "an" when the first word of phrase starts with a vowel
// sound, else "a". It reads the spelling only: two to four capitals start an
// acronym that is spoken letter by letter ("an ASP.NET", "an IIS", "a USB",
// "a SQL" as in "sequel"); a first character that is not a letter is read
// aloud as a consonant (".NET" is "dot net"). Twin of article in wordings.ts.
func article(phrase string) string {
	f := strings.Fields(phrase)
	if len(f) == 0 {
		return "a"
	}
	w := f[0]
	c := w[0]
	if c >= '0' && c <= '9' {
		if c == '8' {
			return "an"
		}
		return "a"
	}
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return "a"
	}
	n := 0
	for n < len(w) && (w[n] >= 'a' && w[n] <= 'z' || w[n] >= 'A' && w[n] <= 'Z') {
		n++
	}
	if s := w[:n]; n >= 2 && n <= 4 && s == strings.ToUpper(s) {
		if s == "SQL" {
			return "a"
		}
		if strings.IndexByte("AEIOFHLMNRSX", s[0]) >= 0 {
			return "an"
		}
		return "a"
	}
	lw := strings.ToLower(w)
	if strings.IndexByte("aeiou", lw[0]) < 0 {
		return "a"
	}
	for _, p := range consonantSoundPrefixes {
		if strings.HasPrefix(lw, p) {
			return "a"
		}
	}
	return "an"
}

// indefinite puts the right indefinite article before phrase: "a MongoDB
// serverStatus", "an ASP.NET Core log". A capital gives "A" or "An", for the
// start of a sentence. Twin of indefinite in wordings.ts.
func indefinite(phrase string, capital bool) string {
	a := article(phrase)
	if capital {
		a = strings.ToUpper(a[:1]) + a[1:]
	}
	return a + " " + phrase
}

// sampleSubject is the subject of the sample sentence: one sample takes an
// article ("A MongoDB serverStatus sample"), several take their count
// ("3 MongoDB serverStatus samples"). The source is a name without a noun, so
// the noun is never repeated. Twin of sampleSubject in wordings.ts.
func sampleSubject(source string, samples int) string {
	if samples == 1 {
		return indefinite(source+" sample", true)
	}
	return strconv.Itoa(samples) + " " + source + " samples"
}

// NotObservedThreshold: a source with a recording threshold shows no event.
func NotObservedThreshold(source, threshold string) string {
	return "Not observed in " + source + ". This source only records " + threshold + "; shorter waits would not appear."
}

// NotObservedGap: a source with a gap, sampling or retention limit shows no event.
func NotObservedGap(detail string) string {
	return "Not observed (gap or sampling: " + detail + ")."
}

// OverlapUnique: a qualifying event overlaps a stalled audit interval and the
// join class is unique. The source and the event are plain names without an
// article; the sentence picks "A"/"An" and "a"/"an" itself.
func OverlapUnique(source, event string, seconds float64) string {
	return indefinite(source, true) + " session had " + indefinite(event, false) + " of " + num(seconds) + " s overlapping this interval (Correlation)."
}

// OverlapAmbiguous: a qualifying event overlaps and the join class is ambiguous.
func OverlapAmbiguous(candidates int) string {
	return "Possible overlap (ambiguous: " + strconv.Itoa(candidates) + " candidates)."
}

// OverlapShared: one qualifying event overlaps several audit intervals as the
// only candidate of each, so it cannot be pinned to any one of them. It is listed
// once per interval and never counted. There is no TypeScript twin: the screen
// shows the sentence the server wrote.
func OverlapShared(intervals int) string {
	return "Possible overlap (one event overlaps " + strconv.Itoa(intervals) + " audit intervals; it is not matched to any one of them)."
}

// AuditCreate: an audit duration is large. It states what was measured,
// never a cause.
func AuditCreate(seconds float64) string {
	return "Audit create took " + num(seconds) + " s as measured from the client side."
}

// Wave-2 wordings (wave-2 plan §4.0). The caller formats every value,
// including its unit, so the sentence carries no number rule of its own.

// MetricRose (Symptom): a sampled metric went up over a span.
func MetricRose(subject, metric, delta, span, from, to string) string {
	return subject + ": " + metric + " rose by " + delta + " over " + span + " (from " + from + " to " + to + ")."
}

// MetricSampleOverlap (Correlation): metric samples fall inside a stalled
// audit interval. The source is a name without the word "sample"; samples is
// how many samples the sentence speaks of (see sampleSubject).
func MetricSampleOverlap(source string, samples int, metric, value string) string {
	return sampleSubject(source, samples) + " showed " + metric + " at " + value + " during this interval (Correlation)."
}

// MarkerBefore (Correlation): a marker event (a restart, a recycle, a crash)
// comes shortly before a stalled audit interval.
func MarkerBefore(offset, marker, host string) string {
	return "This interval starts " + offset + " after a " + marker + " on " + host + " (Correlation)."
}

// EpisodeShare (Symptom): how often a condition holds inside stall episodes
// and inside the comparison windows outside them.
func EpisodeShare(condition string, k, n, m, total int) string {
	return condition + " in " + strconv.Itoa(k) + " of " + strconv.Itoa(n) + " stall episodes, and in " +
		strconv.Itoa(m) + " of " + strconv.Itoa(total) + " comparison windows outside stalls."
}

// TopInWindow (Symptom): the highest values of a metric in one window.
func TopInWindow(metric, list string) string {
	return "Highest " + metric + " in this window: " + list + "."
}
