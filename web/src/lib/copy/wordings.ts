/**
 * The only permitted wordings for absence and linkage (roadmap §6.6). Components and the plain-summary
 * builder must call these; paraphrasing them fails the copy lint.
 */

/** A source with a recording threshold shows no event. */
export function notObservedThreshold(source: string, threshold: string): string {
  return `Not observed in ${source}. This source only records ${threshold}; shorter waits would not appear.`;
}

/** A source with a gap, sampling or retention limit shows no event. */
export function notObservedGap(detail: string): string {
  return `Not observed (gap or sampling: ${detail}).`;
}

/** A qualifying event overlaps a stalled audit interval and the join class is unique. */
export function overlapUnique(source: string, event: string, seconds: number): string {
  return `A ${source} session had a ${event} of ${seconds} s overlapping this interval (Correlation).`;
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

/** Correlation: a metric sample falls inside a stalled audit interval. */
export function metricSampleOverlap(source: string, metric: string, value: string): string {
  return `A ${source} sample showed ${metric} at ${value} during this interval (Correlation).`;
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
