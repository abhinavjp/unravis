import { parseLocalUtc, toLocalUtc } from './frame';

/**
 * Nearby activity: what each source recorded around one moment. This file turns the moment and the two spans into the window to read, and
 * sorts a call into one of three bands by its time. The bands are about how long a call took. They are not the suspect colours.
 */

export const MAX_SPAN_MINUTES = 120;
export const NEARBY_ROWS = 200;
const MINUTE = 60 * 1000;

export type DurationBand = 'fast' | 'slow' | 'very-slow' | 'unknown';

/** Under 1 second is fast, 1 to 2 seconds is slow, over 2 seconds is very slow. A call with no time is unknown, never fast. */
export function durationBand(ms: number | null | undefined): DurationBand {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms < 0) return 'unknown';
  if (ms > 2000) return 'very-slow';
  return ms >= 1000 ? 'slow' : 'fast';
}

export interface NearbyWindow { start: string; end: string }

/** The window around a moment typed in UTC, or null when the moment or a span cannot be used. Spans are whole minutes from 0 to 120. */
export function nearbyWindow(around: string, before: number, after: number): NearbyWindow | null {
  const at = parseLocalUtc(around);
  if (!Number.isFinite(at)) return null;
  const ok = (n: number) => Number.isInteger(n) && n >= 0 && n <= MAX_SPAN_MINUTES;
  if (!ok(before) || !ok(after) || before + after === 0) return null;
  return { start: new Date(at - before * MINUTE).toISOString(), end: new Date(at + after * MINUTE).toISOString() };
}

/** A quick window: the same number of minutes either side of the moment. */
export const QUICK_SPANS: ReadonlyArray<{ label: string; before: number; after: number }> = [
  { label: '5 minutes either side', before: 5, after: 5 },
  { label: '15 minutes either side', before: 15, after: 15 },
  { label: '30 minutes before', before: 30, after: 0 }
];

/** The moment to start from: the onset the engine found, else the start of the incident window, else empty. */
export function defaultAround(onset: string | undefined, incidentStart: string): string {
  if (onset) {
    const ms = Date.parse(onset);
    if (Number.isFinite(ms)) return toLocalUtc(ms);
  }
  return incidentStart;
}
