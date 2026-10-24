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
  return `This interval starts ${offset} after a ${marker} on ${host} (Correlation).`;
}

/** Symptom: how often a condition holds inside stall episodes and inside the comparison windows outside them. */
export function episodeShare(condition: string, k: number, n: number, m: number, total: number): string {
  return `${condition} in ${k} of ${n} stall episodes, and in ${m} of ${total} comparison windows outside stalls.`;
}

/** Symptom: the highest values of a metric in one window. */
export function topInWindow(metric: string, list: string): string {
  return `Highest ${metric} in this window: ${list}.`;
}

/** Plain-language form of an audit-create measurement (roadmap §8.14 part 5). Never names a cause. */
export function plainAuditCreate(approxSeconds: number): string {
  return `Saving the record took about ${approxSeconds} seconds before the action ran, as measured from the application side.`;
}
