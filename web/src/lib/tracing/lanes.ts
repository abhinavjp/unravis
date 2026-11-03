/**
 * Pure chart math and wording for the Investigation Timeline (WP-17b): the slot grid the chart draws on (always
 * the server's floor-aligned axis from `response.window`, never recomputed here), the bars built from exactly
 * the buckets the server returned (no summing, no estimates), and every lane sentence for a missing or partial
 * state. No Svelte here; `TracingChart.svelte` imports from this file and from `./model` for types only.
 */
import type { TracingBucket, TracingLane, TracingLaneKey, TracingWindow } from './model';

export const LANE_ORDER: TracingLaneKey[] = ['audit', 'sql', 'iis'];

export const LANE_NAME: Record<TracingLaneKey, string> = {
  audit: 'API requests (audit log)',
  sql: 'Database (SQL Server)',
  iis: 'Web server (IIS)'
};

export const LANE_ASK: Record<TracingLaneKey, string> = {
  audit: 'Was the application slow or failing?',
  sql: 'Were database calls slow, blocked or failing?',
  iis: 'Were web requests slow or failing before reaching the app?'
};

export const PANEL_TITLE: Record<TracingLaneKey, string> = {
  audit: 'How many requests, and how slow they were',
  sql: 'Slow database calls (over 1 s) and failed calls',
  iis: 'Web-server requests (errors shown darker)'
};

/** The lane-state sentences, worded as on the older call screen, for the states this lane carries. */
export const LANE_STATE_TEXT: Record<string, string> = {
  'no-data': 'This case has no file for this source',
  'none-seen': 'Files cover this time and nothing was seen',
  'clock-unchecked': 'Time zone not checked, so nothing is drawn',
  incomplete: 'Read hit its limit; events may be missing',
  'out-of-range': 'Files do not cover this time'
};

export const INCOMPLETE_TEXT: Record<'timeout' | 'rowcap' | 'error', string> = {
  timeout: 'The audit read ran out of time; nothing was shortened.',
  rowcap: 'The audit read reached its row limit; nothing was shortened.',
  error: 'The audit read failed; nothing was shortened.'
};

export const MIXED_NOTE = {
  title: 'Some data was collected at different times.',
  body: 'API data was read just now, while SQL and web-server data came from saved logs. Compare the timing and trends, but do not expect totals from the different sources to match exactly.',
  fixed: 'This note cannot be dismissed.'
} as const;

export const TRUNCATED_NOTE = 'The list of events was cut at its limit. The counts in the bars are not affected.';

// ---------------------------------------------------------------------------------------------------------------------------------
// The slot grid: built only from the server's axis (`window.axisStart`/`axisEnd`/`bucketSeconds`). The server has
// already floored the axis to UTC multiples of the bucket width (INV-1); this module never floors or ceils the
// request window itself.

export interface SlotGrid {
  firstMs: number;
  widthMs: number;
  n: number;
  windowStartMs: number;
  windowEndMs: number;
}

const SERIES_CAP = 2000;

export function slotGrid(window: TracingWindow): SlotGrid | null {
  const firstMs = Date.parse(window.axisStart);
  const axisEndMs = Date.parse(window.axisEnd);
  const windowStartMs = Date.parse(window.start);
  const windowEndMs = Date.parse(window.end);
  const widthMs = window.bucketSeconds * 1000;
  if (!Number.isFinite(firstMs) || !Number.isFinite(axisEndMs) || !Number.isFinite(windowStartMs) || !Number.isFinite(windowEndMs)) return null;
  if (window.bucketSeconds <= 0 || axisEndMs <= firstMs) return null;
  const n = Math.ceil((axisEndMs - firstMs) / widthMs);
  if (n < 1 || n > SERIES_CAP) return null;
  return { firstMs, widthMs, n, windowStartMs, windowEndMs };
}

export const slotLeftPct = (g: SlotGrid, i: number): number => (100 * i) / g.n;
export const slotWidthPct = (g: SlotGrid): number => 100 / g.n;
export const slotStartMs = (g: SlotGrid, i: number): number => g.firstMs + i * g.widthMs;

/** True when the slot starts before the request window or ends after it: it exists only to fill out the server's axis. */
export function slotOutside(g: SlotGrid, i: number): boolean {
  const start = slotStartMs(g, i);
  const end = start + g.widthMs;
  return start < g.windowStartMs || end > g.windowEndMs;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Bars: exactly the buckets the server returned, one per cell. A bucket's start is floored into its cell (not
// matched exactly) because the server clips a bucket's start to the request window (WP-14/WP-15 `Bucket(k)`),
// so the first bucket can start inside its cell rather than on the cell boundary.

export interface Bar {
  index: number;
  count: number;
  errors: number;
}

export function barsFor(g: SlotGrid, buckets: TracingBucket[]): Bar[] {
  const byIndex = new Map<number, Bar>();
  for (const bucket of buckets) {
    const startMs = Date.parse(bucket.start);
    if (!Number.isFinite(startMs)) continue;
    const index = Math.floor((startMs - g.firstMs) / g.widthMs);
    if (index < 0 || index >= g.n) continue;
    const existing = byIndex.get(index);
    if (existing) {
      existing.count += bucket.count;
      existing.errors += bucket.errors;
    } else {
      byIndex.set(index, { index, count: bucket.count, errors: bucket.errors });
    }
  }
  return [...byIndex.values()].sort((a, b) => a.index - b.index);
}

export const barMax = (bars: Bar[]): number => bars.reduce((max, bar) => Math.max(max, bar.count), 0);

// ---------------------------------------------------------------------------------------------------------------------------------
// Wording: the bar width in plain words, the axis caption, and the one-line note under a lane that is not drawing a full set of bars.

export function widthLabel(seconds: number): string {
  if (seconds < 3600) {
    const minutes = Math.round(seconds / 60);
    return minutes + ' minute' + (minutes === 1 ? '' : 's');
  }
  const hours = Math.round(seconds / 3600);
  return hours + ' hour' + (hours === 1 ? '' : 's');
}

export function axisCaption(zoneName: string, bucketSeconds: number, hasOutside: boolean): string {
  let caption = 'Time (' + zoneName + '). Each bar covers ' + widthLabel(bucketSeconds) + ', starting at the time on its left edge; every panel uses the same time axis. ';
  if (bucketSeconds > 60) caption += 'Longer periods group more time into each bar so the chart stays readable. ';
  if (hasOutside) caption += 'Grey dashes are slots outside your window.';
  return caption;
}

export function laneNote(lane: TracingLane): string {
  if (lane.state === 'events') return '';
  if (lane.disabled) return lane.reason || LANE_STATE_TEXT['clock-unchecked'];
  if (lane.state === 'incomplete') {
    if (lane.reason) return lane.reason;
    if (lane.incompleteReason) return INCOMPLETE_TEXT[lane.incompleteReason];
    return LANE_STATE_TEXT.incomplete;
  }
  return lane.reason || LANE_STATE_TEXT[lane.state] || '';
}
