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

/** Plain-language form of an audit-create measurement (roadmap §8.14 part 5). Never names a cause. */
export function plainAuditCreate(approxSeconds: number): string {
  return `Saving the record took about ${approxSeconds} seconds before the action ran, as measured from the application side.`;
}
