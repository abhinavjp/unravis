import { notObservedGap } from './copy/wordings';
import type { ActivityInput, Page, RequestRecord, SourceResult } from './types';

/**
 * The per-minute view of one episode (B3a, UI only). It reads the Activity endpoint for the episode plus 15 minutes either side,
 * then counts, for each minute, the requests that started, finished, were running, and were running as slow requests.
 * Pure logic and plain text only; the panel does the drawing. Nothing here names a cause.
 */

export const MINUTE_MS = 60_000;
/** How far either side of the episode the view reads. */
export const PAD_MS = 15 * MINUTE_MS;
/** The Activity endpoint refuses a window longer than this. */
export const MAX_WINDOW_MS = 24 * 60 * MINUTE_MS;
/** The largest page the Activity endpoint returns (it refuses a larger limit). */
export const PAGE_LIMIT = 100;
/** The most pages the view reads. A busy window beyond this is shown as partial. */
export const MAX_PAGES = 10;

export interface MinuteWindow { startMs: number; endMs: number; minutes: number; clamped: boolean }

/**
 * The window to read: the episode with 15 minutes either side, widened to whole minutes (so every bar covers a clock minute in any zone).
 * A window longer than 24 hours is cut at 24 hours and flagged. Null when the episode has no readable start and end.
 */
export function minuteWindow(episode: { start: string; end: string }, padMs = PAD_MS): MinuteWindow | null {
  const from = Date.parse(episode.start), to = Date.parse(episode.end);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to < from) return null;
  const startMs = Math.floor((from - padMs) / MINUTE_MS) * MINUTE_MS;
  let endMs = Math.ceil((to + padMs) / MINUTE_MS) * MINUTE_MS;
  let clamped = false;
  if (endMs - startMs > MAX_WINDOW_MS) { endMs = startMs + MAX_WINDOW_MS; clamped = true; }
  return { startMs, endMs, minutes: (endMs - startMs) / MINUTE_MS, clamped };
}

export type ActivityRow = Pick<RequestRecord, 'id' | 'sourceId' | 'initiatedAt' | 'completedAt'>;

export interface MinuteBucket {
  startMs: number;
  /** Requests whose start falls in this minute. */
  started: number;
  /** Requests whose finish falls in this minute. */
  completed: number;
  /** Requests running at any moment of this minute. */
  inFlight: number;
  /** Of those, the ones that had run for at least the slow request limit by the end of this minute. */
  stallsInFlight: number;
}

export interface MinuteCounts {
  buckets: MinuteBucket[];
  /** Rows counted in the buckets. */
  rows: number;
  /** Rows with no usable finish time. They are counted as still running to the end of the view. */
  noFinish: number;
  /** Rows with no usable start time, which cannot be placed. */
  noStart: number;
}

const finish = (row: ActivityRow, start: number): number | null => {
  const end = row.completedAt ? Date.parse(row.completedAt) : NaN;
  return Number.isFinite(end) && end >= start ? end : null;
};

/**
 * Buckets rows into minutes. A request is running in a minute when it started before the minute ended and finished after it began.
 * A row with no usable finish time is counted as running from its start to the end of the view, and reported in `noFinish`,
 * because the log cannot say whether it ended. Each request (source and id) is counted once.
 */
export function bucketMinutes(rows: ReadonlyArray<ActivityRow>, window: Pick<MinuteWindow, 'startMs' | 'endMs' | 'minutes'>, stallMs: number): MinuteCounts {
  const buckets: MinuteBucket[] = Array.from({ length: window.minutes }, (_, i) => ({ startMs: window.startMs + i * MINUTE_MS, started: 0, completed: 0, inFlight: 0, stallsInFlight: 0 }));
  const seen = new Set<string>();
  let counted = 0, noFinish = 0, noStart = 0;
  const indexOf = (ms: number) => Math.floor((ms - window.startMs) / MINUTE_MS);
  for (const row of rows) {
    const key = row.sourceId + '\u0000' + row.id;
    if (seen.has(key)) continue;
    seen.add(key);
    const start = row.initiatedAt ? Date.parse(row.initiatedAt) : NaN;
    if (!Number.isFinite(start)) { noStart++; continue; }
    const end = finish(row, start);
    if (end === null) noFinish++;
    if (start >= window.endMs || (end !== null && end < window.startMs)) continue;
    counted++;
    if (start >= window.startMs) buckets[indexOf(start)].started++;
    if (end !== null && end >= window.startMs && end < window.endMs) buckets[indexOf(end)].completed++;
    const first = Math.max(0, indexOf(start));
    const last = Math.min(window.minutes - 1, end === null ? window.minutes - 1 : indexOf(end));
    for (let i = first; i <= last; i++) {
      const bucket = buckets[i];
      // A request that finishes exactly on a minute boundary is no longer running in the next minute.
      if (end !== null && end === bucket.startMs && end !== start) continue;
      bucket.inFlight++;
      const upTo = end === null ? bucket.startMs + MINUTE_MS : Math.min(end, bucket.startMs + MINUTE_MS);
      if (upTo - start >= stallMs) bucket.stallsInFlight++;
    }
  }
  return { buckets, rows: counted, noFinish, noStart };
}

/** The minute with the most slow requests running, or null when none was running. The earliest minute wins a tie. */
export function peakStallMinute(buckets: ReadonlyArray<MinuteBucket>): MinuteBucket | null {
  let best: MinuteBucket | null = null;
  for (const bucket of buckets) if (bucket.stallsInFlight > 0 && (!best || bucket.stallsInFlight > best.stallsInFlight)) best = bucket;
  return best;
}

// ---- Reading the endpoint ----

export interface MinuteLoad {
  rows: RequestRecord[];
  /** The read stopped early (a page cap, a truncated page or the 24 hour limit), so the counts may be too low. */
  partial: boolean;
  /** Sources that failed on at least one page. */
  failed: SourceResult[];
}

export type FetchActivity = (input: ActivityInput, signal?: AbortSignal) => Promise<Page>;

/**
 * Reads the window page by page until the server has no more, a page is cut short, or MAX_PAGES is reached.
 * `fetchPage` is api.activity in the app and a stub in tests. An aborted read throws the signal's reason.
 */
export async function loadMinuteRows(fetchPage: FetchActivity, sourceIds: string[], window: Pick<MinuteWindow, 'startMs' | 'endMs' | 'clamped'>, signal?: AbortSignal): Promise<MinuteLoad> {
  const rows: RequestRecord[] = [];
  const failed: SourceResult[] = [];
  let partial = window.clamped;
  let cursor = '';
  for (let page = 0; page < MAX_PAGES; page++) {
    if (signal?.aborted) throw signal.reason ?? new DOMException('Aborted', 'AbortError');
    const result = await fetchPage({ sourceIds, start: new Date(window.startMs).toISOString(), end: new Date(window.endMs).toISOString(), cursor, limit: PAGE_LIMIT }, signal);
    rows.push(...result.rows);
    for (const source of result.sources ?? []) if (!source.ok && !failed.some((f) => f.sourceId === source.sourceId)) failed.push(source);
    if (result.truncated) partial = true;
    cursor = result.nextCursor ?? '';
    if (!cursor) return { rows, partial, failed };
  }
  return { rows, partial: true, failed };
}

// ---- Text ----

export const STALL_MINUTES_TEXT = {
  open: 'Show minute by minute',
  close: 'Hide minute by minute',
  title: 'Minute by minute',
  intro: 'This reads every request from 15 minutes before this episode to 15 minutes after it. It counts them for each minute.',
  running: 'Reading activity…',
  failed: 'The activity could not be read. Try again.',
  retry: 'Try again',
  started: 'Started',
  completed: 'Finished',
  inFlight: 'Running',
  stalls: 'Slow and running',
  minute: 'Minute',
  tableLabel: 'Requests for each minute around this episode',
  empty: 'No requests were recorded in this window.',
  partial: 'Partial: not every request was read, so the counts may be too low.',
  clamped: 'The view was cut at 24 hours, so it does not reach the end of the episode.',
  noStalls: 'No slow request was running in any minute of this view.'
} as const;

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** One sentence on rows the counts cannot place. Empty when there are none. */
export function unplacedLine(counts: Pick<MinuteCounts, 'noFinish' | 'noStart'>): string {
  const parts: string[] = [];
  if (counts.noFinish > 0) parts.push(`${plural(counts.noFinish, 'request has', 'requests have')} no finish time and ${counts.noFinish === 1 ? 'is' : 'are'} counted as still running`);
  if (counts.noStart > 0) parts.push(`${plural(counts.noStart, 'request has', 'requests have')} no start time and ${counts.noStart === 1 ? 'is' : 'are'} left out`);
  return parts.length ? parts.join('. ') + '.' : '';
}

/** The headline for the view. `minuteLabel` formats a minute's start in the zone on screen. */
export function peakLine(buckets: ReadonlyArray<MinuteBucket>, minuteLabel: (ms: number) => string): string {
  const peak = peakStallMinute(buckets);
  if (!peak) return STALL_MINUTES_TEXT.noStalls;
  return `The most slow requests running at once was ${peak.stallsInFlight}, in the minute starting at ${minuteLabel(peak.startMs)}.`;
}

/** The absence line for a source that answered with nothing at all, using the standard wording. */
export const emptyWindowLine = (): string => notObservedGap('no requests were returned for this window');
