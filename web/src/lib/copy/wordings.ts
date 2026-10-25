/**
 * The only permitted wordings for absence and linkage (roadmap §6.6). Components and the plain-summary
 * builder must call these; paraphrasing them fails the copy lint.
 */

/** Vowel letters that start a consonant sound ("a unique wait"). Twin of consonantSoundPrefixes in wording.go. */
const CONSONANT_SOUND_PREFIXES = ['uni', 'use', 'usu', 'uti', 'eu', 'one', 'ubiq'];

/**
 * "an" when the first word of the phrase starts with a vowel sound, else "a". It reads the spelling only: two to four
 * capitals start an acronym spoken letter by letter ("an ASP.NET", "an IIS", "a USB", "a SQL" as in "sequel"); a first
 * character that is not a letter is read aloud as a consonant (".NET" is "dot net"). Twin of article in wording.go.
 */
export function article(phrase: string): 'a' | 'an' {
  const word = phrase.trim().split(/\s+/)[0];
  if (!word) return 'a';
  const c = word[0];
  if (/[0-9]/.test(c)) return c === '8' ? 'an' : 'a';
  if (!/[A-Za-z]/.test(c)) return 'a';
  const letters = word.match(/^[A-Za-z]+/)![0];
  if (letters.length >= 2 && letters.length <= 4 && letters === letters.toUpperCase()) {
    if (letters === 'SQL') return 'a';
    return 'AEIOFHLMNRSX'.includes(letters[0]) ? 'an' : 'a';
  }
  const lower = word.toLowerCase();
  if (!'aeiou'.includes(lower[0])) return 'a';
  return CONSONANT_SOUND_PREFIXES.some((p) => lower.startsWith(p)) ? 'a' : 'an';
}

/** The phrase with the right indefinite article; a capital gives "A"/"An" for a sentence start. Twin of indefinite in wording.go. */
export function indefinite(phrase: string, capital = false): string {
  const a = article(phrase);
  return `${capital ? a[0].toUpperCase() + a.slice(1) : a} ${phrase}`;
}

/**
 * The subject of the sample sentence: one sample takes an article ("A MongoDB serverStatus sample"), several take
 * their count ("3 MongoDB serverStatus samples"). The source is a name without a noun. Twin of sampleSubject in wording.go.
 */
export function sampleSubject(source: string, samples: number): string {
  return samples === 1 ? indefinite(`${source} sample`, true) : `${samples} ${source} samples`;
}

/** A source with a recording threshold shows no event. */
export function notObservedThreshold(source: string, threshold: string): string {
  return `Not observed in ${source}. This source only records ${threshold}; shorter waits would not appear.`;
}

/** A source with a gap, sampling or retention limit shows no event. */
export function notObservedGap(detail: string): string {
  return `Not observed (gap or sampling: ${detail}).`;
}

/** A qualifying event overlaps a stalled audit interval and the join class is unique. The source and the event are names without an article. */
export function overlapUnique(source: string, event: string, seconds: number): string {
  return `${indefinite(source, true)} session had ${indefinite(event)} of ${seconds} s overlapping this interval (Correlation).`;
}

/** A qualifying event overlaps and the join class is ambiguous. */
export function overlapAmbiguous(candidates: number): string {
  return `Possible overlap (ambiguous: ${candidates} candidates).`;
}

/** An audit duration is large. It states what was measured, never a cause. */
export function auditCreate(seconds: number): string {
  return `Audit create took ${seconds} s as measured from the client side.`;
}

/** Symptom: a sampled metric went up over a span. The caller formats every value with its unit. */
export function metricRose(subject: string, metric: string, delta: string, span: string, from: string, to: string): string {
  return `${subject}: ${metric} rose by ${delta} over ${span} (from ${from} to ${to}).`;
}

/** Correlation: metric samples fall inside a stalled audit interval. The source is a name without the word "sample". */
export function metricSampleOverlap(source: string, samples: number, metric: string, value: string): string {
  return `${sampleSubject(source, samples)} showed ${metric} at ${value} during this interval (Correlation).`;
}

/** Correlation: a marker event (a restart, a recycle, a crash) comes shortly before a stalled audit interval. */
export function markerBefore(offset: string, marker: string, host: string): string {
  return `This interval starts ${offset} after ${indefinite(marker)} on ${host} (Correlation).`;
}

/** Correlation: the twin of markerBefore for a marker event that comes shortly after a stalled audit interval ends. */
export function markerAfter(offset: string, marker: string, host: string): string {
  return `This interval ends ${offset} before ${indefinite(marker)} on ${host} (Correlation).`;
}

/** Symptom: how often a condition holds inside stall episodes and inside the comparison windows outside them. */
export function episodeShare(condition: string, k: number, n: number, m: number, total: number): string {
  return `${condition} in ${k} of ${n} stall episodes, and in ${m} of ${total} comparison windows outside stalls.`;
}

/** Symptom: the highest values of a metric in one window. */
export function topInWindow(metric: string, list: string): string {
  return `Highest ${metric} in this window: ${list}.`;
}

/*
 * A2 rule wordings (wave-2 plan 4.3). Each states what was measured or what is missing, never a cause. The caller
 * formats every value with its unit. A gap function returns the whole notObservedGap sentence.
 */

/** Symptom: the lowest value of a metric in one window. */
export function lowestInWindow(metric: string, value: string): string {
  return `Lowest ${metric} in this window: ${value}.`;
}

/** Symptom: how many things of one kind were counted in the window. */
export function countInWindow(what: string, n: number): string {
  return `${what} in this window: ${n}.`;
}

/** Correlation: the twin of overlapUnique for a source that is a fixed-length buffer of entries (Redis SLOWLOG). */
export function overlapBuffered(source: string, event: string, seconds: number): string {
  return `The ${source} buffer held ${indefinite(event)} of ${seconds} s overlapping this interval (Correlation).`;
}

/** The host as a sentence shows it: an unnamed host has no label. Twin of HostName in wording.go. */
export function hostName(host: string): string {
  return host === '' ? 'an unnamed host' : host;
}

/** A phrase followed by a host ("host CPU at or above 90 % on web-a"). */
export function onHost(phrase: string, host: string): string {
  return `${phrase} on ${host}`;
}

/** One item of a topInWindow list: a name and its value. */
export function listEntry(name: string, value: string): string {
  return `${name} (${value})`;
}

/** The last item of a process list when host CPU is known: the part of the host not listed. */
export function otherProcesses(value: string): string {
  return `all other processes (${value})`;
}

/** The last item of a process list when host CPU is not known. */
export function listedProcessesOnly(): string {
  return `(sum of listed processes only)`;
}

/** A series has too few samples in the window to test. */
export function coverageGap(percent: number, metric: string, host: string): string {
  return notObservedGap(`${percent} % of samples present for ${metric} on ${host}`);
}

/** A host has host counters but no per-process counters. */
export function noProcessCountersGap(host: string): string {
  return notObservedGap(`no per-process counters for ${host}`);
}

/** A .NET Core host has app logs but no runtime counters. */
export function noRuntimeCountersGap(host: string): string {
  return notObservedGap(`no runtime counters for this process on ${host}`);
}

/** A .NET Framework host has no thread-pool counter. */
export function noThreadPoolCounterGap(host: string): string {
  return notObservedGap(`no thread-pool counter exists on ${host}; ASP.NET Requests Queued shown instead`);
}

/** A host has counters but no application log. */
export function noAppLogGap(host: string): string {
  return notObservedGap(`no app log for ${host}`);
}

/** More events matched than the rules keep. */
export function eventCapGap(kept: number): string {
  return notObservedGap(`the rules read only the first ${kept} matching events, so overlap and log results are partial`);
}

/** A memory series, cut at start markers, has a segment too short to test for a rise. */
export function shortSegmentGap(subject: string, metric: string, from: string, to: string, samples: number, span: string, minSamples: number, minSpan: string, minCoverage: string): string {
  return notObservedGap(`${subject} ${metric}: the segment from ${from} to ${to} has ${samples} samples over ${span}; a rise is tested on ${minSamples} or more samples over ${minSpan} or more, with ${minCoverage} % of the samples expected`);
}

/** A host has other Redis imports but no SLOWLOG import. */
export function noSlowlogImportGap(host: string): string {
  return notObservedGap(`no Redis SLOWLOG import for ${host}`);
}

/** The SLOWLOG threshold is not stated, so the threshold wording is not available. */
export function slowlogThresholdGap(host: string): string {
  return notObservedGap(`slowlog-log-slower-than not stated for ${host}`);
}

/** The time the SLOWLOG buffer covers is not known. */
export function slowlogWindowUnknownGap(host: string): string {
  return notObservedGap(`the Redis slow-log window on ${host} is unknown`);
}

/** The SLOWLOG window starts after the interval starts. */
export function slowlogWindowStartsGap(host: string, from: string): string {
  return notObservedGap(`the Redis slow-log window on ${host} starts at ${from}, after this interval starts`);
}

/** The SLOWLOG window ends before the interval ends. */
export function slowlogWindowEndsGap(host: string, to: string): string {
  return notObservedGap(`the Redis slow-log window on ${host} ends at ${to}, before this interval ends`);
}

/** The SLOWLOG entry ids skip a number, so entries may be missing. */
export function slowlogIdGap(host: string): string {
  return notObservedGap(`the Redis slow-log buffer on ${host} has a gap in its entry ids, so entries may be missing`);
}

/** IIS 8.5 and later log a recycle only for the triggers the pool enables. Twin of RecycleGapEnabledTriggers in wording.go. */
export function recycleGapEnabledTriggers(): string {
  return `Application pool recycle events are logged only for the triggers enabled in logEventOnRecycle; other recycle triggers may have left no event.`;
}

/** Before IIS 8.5, and for an unknown runtime, only three triggers are logged by default. */
export function recycleGapLegacyDefaults(): string {
  return `Application pool recycle events are logged by default only for the Time, Memory and PrivateMemory triggers before IIS 8.5, and logEventOnRecycle may have been changed; other recycle triggers may have left no event.`;
}

/** Plain-language form of an audit-create measurement (roadmap §8.14 part 5). Never names a cause. */
export function plainAuditCreate(approxSeconds: number): string {
  return `Saving the record took about ${approxSeconds} seconds before the action ran, as measured from the application side.`;
}
