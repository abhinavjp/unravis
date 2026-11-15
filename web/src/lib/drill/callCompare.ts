// Call detail: where one call sits against the normal range of the window on screen. Pure functions.
// The only per-step numbers the tracing answer carries are the slowest-5% (P95) marks, so "normal" here is the spread of those marks:
// the typical one (the middle) and the highest. A window with no usable mark gives null, never a guess.

export type NormalVerdict = 'above' | 'near' | 'below';
export interface NormalRange { verdict: NormalVerdict; typicalMs: number; highestMs: number; steps: number }

export function compareToNormal(tookMs: number | null | undefined, steps: readonly { p95Ms: number | null }[]): NormalRange | null {
  if (tookMs == null || !Number.isFinite(tookMs)) return null;
  const marks = steps.map((s) => s.p95Ms).filter((v): v is number => v !== null && Number.isFinite(v)).sort((a, b) => a - b);
  if (marks.length === 0) return null;
  const mid = Math.floor(marks.length / 2);
  const typicalMs = marks.length % 2 ? marks[mid] : (marks[mid - 1] + marks[mid]) / 2;
  const highestMs = marks[marks.length - 1];
  const verdict: NormalVerdict = tookMs > highestMs ? 'above' : tookMs >= typicalMs ? 'near' : 'below';
  return { verdict, typicalMs, highestMs, steps: marks.length };
}
