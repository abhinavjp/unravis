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
	return "This interval starts " + offset + " after " + indefinite(marker, false) + " on " + host + " (Correlation)."
}

// MarkerAfter (Correlation): the twin of MarkerBefore for a marker event that
// comes shortly after a stalled audit interval ends. It states the order in
// time and nothing more.
func MarkerAfter(offset, marker, host string) string {
	return "This interval ends " + offset + " before " + indefinite(marker, false) + " on " + host + " (Correlation)."
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

// A2 rule wordings (wave-2 plan §4.3). Each states what was measured or what
// is missing, never a cause. The caller formats every value with its unit. A
// gap function returns the whole NotObservedGap sentence, so a rule never
// builds a sentence of its own.

// LowestInWindow (Symptom): the lowest value of a metric in one window.
func LowestInWindow(metric, value string) string {
	return "Lowest " + metric + " in this window: " + value + "."
}

// CountInWindow (Symptom): how many things of one kind were counted in the window.
func CountInWindow(what string, n int) string {
	return what + " in this window: " + strconv.Itoa(n) + "."
}

// OverlapBuffered (Correlation): the twin of OverlapUnique for a source that
// is a fixed-length buffer of entries (Redis SLOWLOG, the Redis latency
// monitor), which has no sessions. The source is a plain name without an
// article.
func OverlapBuffered(source, event string, seconds float64) string {
	return "The " + source + " buffer held " + indefinite(event, false) + " of " + num(seconds) + " s overlapping this interval (Correlation)."
}

// HostName is the host as a sentence shows it: an unnamed host has no label.
// Twin of hostName in wordings.ts.
func HostName(host string) string {
	if host == "" {
		return "an unnamed host"
	}
	return host
}

// OnHost puts a host after a phrase ("host CPU at or above 90 % on web-a").
func OnHost(phrase, host string) string {
	return phrase + " on " + host
}

// ListEntry is one item of a TopInWindow list: a name and its value.
func ListEntry(name, value string) string {
	return name + " (" + value + ")"
}

// OtherProcesses is the last item of a process list when host CPU is known:
// the part of the host not listed.
func OtherProcesses(value string) string {
	return "all other processes (" + value + ")"
}

// ListedProcessesOnly is the last item of a process list when host CPU is not
// known.
func ListedProcessesOnly() string {
	return "(sum of listed processes only)"
}

// CoverageGap: a series has too few samples in the window to test.
func CoverageGap(percent int, metric, host string) string {
	return NotObservedGap(strconv.Itoa(percent) + " % of samples present for " + metric + " on " + host)
}

// NoProcessCountersGap: a host has host counters but no per-process counters.
func NoProcessCountersGap(host string) string {
	return NotObservedGap("no per-process counters for " + host)
}

// NoRuntimeCountersGap: a .NET Core host has app logs but no runtime counters.
func NoRuntimeCountersGap(host string) string {
	return NotObservedGap("no runtime counters for this process on " + host)
}

// NoThreadPoolCounterGap: a .NET Framework host has no thread-pool counter.
func NoThreadPoolCounterGap(host string) string {
	return NotObservedGap("no thread-pool counter exists on " + host + "; ASP.NET Requests Queued shown instead")
}

// NoAppLogGap: a host has counters but no application log.
func NoAppLogGap(host string) string {
	return NotObservedGap("no app log for " + host)
}

// EventCapGap: more events matched than the rules keep.
func EventCapGap(kept int) string {
	return NotObservedGap("the rules read only the first " + strconv.Itoa(kept) + " matching events, so overlap and log results are partial")
}

// ShortSegmentGap: a memory series, cut at start markers, has a segment that is
// too short to test for a rise. Every value is formatted by the caller.
func ShortSegmentGap(subject, metric, from, to string, samples int, span string, minSamples int, minSpan, minCoverage string) string {
	return NotObservedGap(subject + " " + metric + ": the segment from " + from + " to " + to + " has " + strconv.Itoa(samples) +
		" samples over " + span + "; a rise is tested on " + strconv.Itoa(minSamples) + " or more samples over " + minSpan +
		" or more, with " + minCoverage + " % of the samples expected")
}

// NoSlowlogImportGap: a host has other Redis imports but no SLOWLOG import.
func NoSlowlogImportGap(host string) string {
	return NotObservedGap("no Redis SLOWLOG import for " + host)
}

// SlowlogThresholdGap: the SLOWLOG threshold is not stated, so the threshold
// wording is not available.
func SlowlogThresholdGap(host string) string {
	return NotObservedGap("slowlog-log-slower-than not stated for " + host)
}

// SlowlogWindowUnknownGap: the time the SLOWLOG buffer covers is not known.
func SlowlogWindowUnknownGap(host string) string {
	return NotObservedGap("the Redis slow-log window on " + host + " is unknown")
}

// SlowlogWindowStartsGap: the SLOWLOG window starts after the interval starts.
func SlowlogWindowStartsGap(host, from string) string {
	return NotObservedGap("the Redis slow-log window on " + host + " starts at " + from + ", after this interval starts")
}

// SlowlogWindowEndsGap: the SLOWLOG window ends before the interval ends.
func SlowlogWindowEndsGap(host, to string) string {
	return NotObservedGap("the Redis slow-log window on " + host + " ends at " + to + ", before this interval ends")
}

// SlowlogIDGap: the SLOWLOG entry ids skip a number, so entries may be missing.
func SlowlogIDGap(host string) string {
	return NotObservedGap("the Redis slow-log buffer on " + host + " has a gap in its entry ids, so entries may be missing")
}

// RecycleGapEnabledTriggers: IIS 8.5 and later log a recycle only for the
// triggers the pool enables. Twin of recycleGapEnabledTriggers in wordings.ts.
func RecycleGapEnabledTriggers() string {
	return "Application pool recycle events are logged only for the triggers enabled in logEventOnRecycle; other recycle triggers may have left no event."
}

// RecycleGapLegacyDefaults: before IIS 8.5, and for an unknown runtime, only
// three triggers are logged by default.
func RecycleGapLegacyDefaults() string {
	return "Application pool recycle events are logged by default only for the Time, Memory and PrivateMemory triggers before IIS 8.5, and logEventOnRecycle may have been changed; other recycle triggers may have left no event."
}
