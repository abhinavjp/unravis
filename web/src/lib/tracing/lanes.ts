/**
 * Pure chart math and wording for the Investigation Timeline (WP-17b): the slot grid the chart draws on (always
 * the server's floor-aligned axis from `response.window`, never recomputed here), the bars built from exactly
 * the buckets the server returned (no summing, no estimates), and every lane sentence for a missing or partial
 * state. No Svelte here; `TracingChart.svelte` imports from this file and from `./model` for types only.
 */
import { WAIT_TEXT } from '../copy/waitCopy';
import type { LintEntry } from '../copy/copylint';
import type { TracingBucket, TracingLane, TracingLaneKey, TracingReason, TracingWindow } from './model';
import { formatSpan } from './pickerModel';

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
  sql: 'Slow database calls (over 1 s)',
  iis: 'Web-server requests and how slow they were'
};

/** The lane-state sentences, worded as on the older call screen, for the states this lane carries. */
export const LANE_STATE_TEXT: Record<string, string> = {
  'no-data': 'This case has no file for this source',
  'none-seen': 'Files cover this time and nothing was seen',
  'clock-unchecked': 'Time zone not checked, so nothing is drawn',
  incomplete: 'Read hit its limit; events may be missing',
  'out-of-range': 'Files do not cover this time'
};

export const INCOMPLETE_TEXT: Record<TracingReason, string> = {
  timeout: WAIT_TEXT.laneTimeout,
  rowcap: 'The audit read reached its row limit; nothing was shortened.',
  error: 'The audit read failed; nothing was shortened.',
  'broad-scan': WAIT_TEXT.laneBroadScan
};

/** The reasons whose dictionary sentence replaces the server's own text, because it says what to do next. */
const ACTIONABLE_REASONS: readonly TracingReason[] = ['timeout', 'broad-scan'];

/** True for a lane the server cut short (it ran out of time, or the live source would not read the window). A disabled lane is not. */
export function laneCutShort(lane: TracingLane): boolean {
  return !lane.disabled && !!lane.incompleteReason && ACTIONABLE_REASONS.includes(lane.incompleteReason);
}

/** Fixed words for what was read and what was not. */
export const READ_TEXT = {
  notRead: 'not read',
  noFile: 'This case has no file for this source. Add a file to see this lane.',
  addFiles: 'Add files',
  tipUnread: 'This period was not read. That is not the same as no traffic.'
} as const;

/**
 * True when the server did not read the lane in full: it was refused, cut short, capped or failed, or a chunked read stopped before
 * its last day. A lane like that must never be taken for "no events": what it did not read is unknown. A lane switched off for its
 * time zone is not counted (it has its own note).
 */
export function laneNotFullyRead(lane: Pick<TracingLane, 'state' | 'disabled' | 'incompleteReason' | 'chunksTotal' | 'chunksRead' | 'buckets'>): boolean {
  if (lane.disabled) return false;
  if (lane.state === 'incomplete' || !!lane.incompleteReason) return true;
  if (lane.chunksTotal !== undefined && (lane.chunksRead ?? 0) < lane.chunksTotal) return true;
  return lane.buckets.some((bucket) => bucket.complete === false);
}

/** True when a run finished with any lane not read in full: the status block under the toolbar is shown. */
export const anyLaneCutShort = (lanes: TracingLane[]): boolean => lanes.some(laneNotFullyRead);

/** "Read 4 of 11 days" (and " through 15 Oct 00:00" when the read stopped early). '' for a lane read in one piece. */
export function laneChunkLine(lane: TracingLane, through: (iso: string) => string): string {
  if (lane.chunksTotal === undefined || lane.chunksTotal < 2) return '';
  const read = Math.min(lane.chunksRead ?? 0, lane.chunksTotal);
  const base = `Read ${read} of ${lane.chunksTotal} days`;
  return lane.readThrough && read < lane.chunksTotal ? `${base}, through ${through(lane.readThrough)}` : base;
}

/** One sentence for the status block: what this lane read and what it did not. */
export function laneReadLine(lane: TracingLane, through: (iso: string) => string): string {
  const name = LANE_NAME[lane.key];
  if (lane.chunksTotal !== undefined && lane.chunksTotal >= 2 && (lane.chunksRead ?? 0) < lane.chunksTotal) {
    if ((lane.chunksRead ?? 0) === 0) return `${name}: none of the ${lane.chunksTotal} days were read. That is not the same as no traffic.`;
    return `${name}: ${laneChunkLine(lane, through)}. The days after that are marked ${READ_TEXT.notRead}.`;
  }
  const why = laneNote(lane) || LANE_STATE_TEXT.incomplete;
  if (lane.state === 'incomplete' && lane.buckets.length === 0) return `${name}: ${READ_TEXT.notRead}. ${why}`;
  return `${name}: read in part. ${why}`;
}

/** The lines of the status block, one per lane that was not read in full (lane order). */
export function readSummary(lanes: TracingLane[], through: (iso: string) => string): string[] {
  return LANE_ORDER.map((key) => lanes.find((lane) => lane.key === key)).filter((lane): lane is TracingLane => !!lane && laneNotFullyRead(lane)).map((lane) => laneReadLine(lane, through));
}

/**
 * The slots the lane did not read. A cell the server marked incomplete, any cell ending after `readThrough`, and every cell when a
 * chunked read finished no day at all. Their count of 0 means "not read", never "no traffic".
 */
export function unreadSlots(g: SlotGrid, lane: TracingLane): Set<number> {
  const out = new Set<number>();
  if (lane.disabled) return out;
  for (const bucket of lane.buckets) {
    if (bucket.complete !== false) continue;
    const index = Math.floor((Date.parse(bucket.start) - g.firstMs) / g.widthMs);
    if (index >= 0 && index < g.n) out.add(index);
  }
  const through = lane.readThrough ? Date.parse(lane.readThrough) : NaN;
  const none = lane.chunksTotal !== undefined && (lane.chunksRead ?? 0) === 0 && lane.chunksTotal >= 2;
  for (let i = 0; i < g.n && (Number.isFinite(through) || none); i++) {
    if (none || (Number.isFinite(through) && slotStartMs(g, i) + g.widthMs > through)) out.add(i);
  }
  return out;
}

/** The unread slots as runs of neighbours, clipped to the drawn range [from, to): one hatched block and one label per run. */
export function unreadRuns(slots: ReadonlySet<number>, from: number, to: number): Array<{ from: number; to: number }> {
  const runs: Array<{ from: number; to: number }> = [];
  for (let i = from; i < to; i++) {
    if (!slots.has(i)) continue;
    const last = runs[runs.length - 1];
    if (last && last.to === i) last.to = i + 1;
    else runs.push({ from: i, to: i + 1 });
  }
  return runs;
}

/** The sentence for a lane whose sources are all switched off in the picker. */
export function laneStatusLineFor(lane: TracingLane, unselected: readonly TracingLaneKey[], unselectedText: string): string {
  return unselected.includes(lane.key) ? unselectedText : laneStatusLine(lane);
}

export const laneCopyEntries = (): LintEntry[] => {
  const lane: TracingLane = {
    key: 'audit', label: 'API', state: 'incomplete', reason: '', incompleteReason: 'timeout', origin: 'live', asOf: '2026-10-01T00:00:00Z', consistency: 'live-non-snapshot',
    complete: false, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], chunksTotal: 11, chunksRead: 4, readThrough: '2026-10-05T00:00:00Z'
  };
  const at = () => '5 Oct 00:00';
  return [
    ...Object.entries(READ_TEXT).map(([id, text]) => ({ id: 'tracingRead.' + id, text })),
    { id: 'tracingRead.chunkLine', text: laneChunkLine(lane, at) },
    { id: 'tracingRead.partial', text: laneReadLine(lane, at) },
    { id: 'tracingRead.none', text: laneReadLine({ ...lane, chunksRead: 0, readThrough: undefined }, at) },
    { id: 'tracingRead.refused', text: laneReadLine({ ...lane, chunksTotal: undefined, chunksRead: undefined, readThrough: undefined, incompleteReason: 'broad-scan' }, at) }
  ];
};

/** "warn" (the partial style, as for a row limit) for a cut or capped lane; "bad" for a read that failed or has no known reason. */
export function laneNoteTone(lane: TracingLane): 'warn' | 'bad' {
  return lane.incompleteReason === 'timeout' || lane.incompleteReason === 'rowcap' || lane.incompleteReason === 'broad-scan' ? 'warn' : 'bad';
}

export const MIXED_NOTE = {
  title: 'Some data was collected at different times.',
  body: 'API data was read just now, while SQL and web-server data came from saved logs. Compare the timing and trends, but do not expect totals from the different sources to match exactly.',
  fixed: 'This note cannot be dismissed.'
} as const;

export const TRUNCATED_NOTE = 'The list of events was cut at its limit. The counts in the bars are not affected.';

/** The truncation note naming the lane(s) that were cut; the plain note when the response flags no lane. */
export function truncatedNote(lanes: TracingLane[]): string {
  const names = LANE_ORDER.filter((key) => lanes.some((lane) => lane.key === key && lane.truncated)).map((key) => LANE_NAME[key]);
  if (names.length === 0) return TRUNCATED_NOTE;
  return `The list of events for ${names.join(' and ')} was cut at its limit. The counts in the bars are not affected.`;
}

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
  /** 4xx answers (the IIS lane); 0 when the lane has none. */
  clientErrors: number;
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
      existing.clientErrors += bucket.clientErrors ?? 0;
    } else {
      byIndex.set(index, { index, count: bucket.count, errors: bucket.errors, clientErrors: bucket.clientErrors ?? 0 });
    }
  }
  return [...byIndex.values()].sort((a, b) => a.index - b.index);
}

export const barMax = (bars: Bar[]): number => bars.reduce((max, bar) => Math.max(max, bar.count), 0);

/** A bar's height as a share of the tallest bar, 0 to 100; 0 for an empty bucket or a scale with no height. */
export function barHeightPct(count: number, max: number): number {
  if (!(count > 0) || !(max > 0)) return 0;
  return Math.min(100, (count / max) * 100);
}

/**
 * Ascending whole-number ticks for a y axis that runs from 0 to `max` (the tallest bar's value): 0, the quarters and the
 * top. Below 40 a quarter is kept only when it is a whole multiple of the quarter step (`floor(max / 4)`, at least 1), so a
 * small axis never draws a tick between two whole numbers; from 40 up each quarter is rounded. The first tick is always 0 and
 * the last is always `Math.round(max)`. `[0]` for a scale with no height.
 */
export function yTicks(max: number): number[] {
  if (!Number.isFinite(max) || max <= 0) return [0];
  const fractions = [0, 0.25, 0.5, 0.75, 1];
  let ticks: number[];
  if (max < 40) {
    const step = Math.max(1, Math.floor(max / 4));
    ticks = fractions.map((f) => max * f).filter((v) => Number.isInteger(v) && v % step === 0);
  } else {
    ticks = fractions.map((f) => Math.round(max * f));
  }
  return [...new Set([0, ...ticks, Math.round(max)])].sort((a, b) => a - b);
}

/**
 * True when the lane draws bars. Events lanes always do; an incomplete lane only when `barCount` bars (default: its
 * buckets) exist; every other state, and any disabled lane, draws nothing. The one rule shared by chart and hand-off.
 */
export function laneDrawsBars(lane: TracingLane, barCount: number = lane.buckets.length): boolean {
  if (lane.disabled) return false;
  if (lane.state === 'events') return true;
  if (lane.state === 'incomplete') return barCount > 0;
  return false;
}

/** A row is hidden only when its flag is exactly false. */
export function isLaneVisible(visible: Partial<Record<TracingLaneKey, boolean>>, key: TracingLaneKey): boolean {
  return visible[key] !== false;
}

/** False when the lane has no file for its source: it read nothing, so a "from saved logs" line would claim a read that did not happen. */
export function showsProvenance(lane: TracingLane): boolean {
  return lane.state !== 'no-data';
}

export const NO_EVENTS_PERIOD = 'No events in this period';

/** The one sentence a lane that draws no bars shows in its panel and in its hover text ('' when it draws bars). */
export function laneStatusLine(lane: TracingLane): string {
  if (lane.state === 'none-seen' && !lane.disabled && !laneCutShort(lane)) return NO_EVENTS_PERIOD;
  return laneNote(lane);
}

/** True when any lane that draws bars has a bucket with events in it: the chart controls only make sense then. */
export function hasAnyData(g: SlotGrid | null, lanes: TracingLane[]): boolean {
  if (!g) return false;
  return lanes.some((lane) => {
    if (lane.disabled) return false;
    const bars = barsFor(g, lane.buckets);
    return laneDrawsBars(lane, bars.length) && bars.some((bar) => bar.count > 0);
  });
}

/**
 * The slots in [from, to) that only fill out the server's axis. Only the first and last slot of the axis can be
 * outside the request window (the server floors the start and ceils the end), so only those two are tested.
 */
export function outsideSlots(g: SlotGrid, from: number, to: number): number[] {
  const edges = g.n > 1 ? [0, g.n - 1] : [0];
  return edges.filter((i) => i >= from && i < to && slotOutside(g, i));
}

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

/**
 * The line above the chart (the wireframe's "Each bar = 1 minute · 60 bars on the axis · window 1 h"). `lead` is the part drawn bold:
 * how much time one bar holds. `rest` says how many bars the axis has, how long the window is, and (only when it is true) that a
 * long window groups more time into each bar and that the axis has end slots outside the window. Every figure comes from the grid
 * the chart draws on, so the line can never disagree with the bars.
 */
export function barSizeParts(g: SlotGrid, bucketSeconds: number): { lead: string; rest: string } {
  const spanMs = g.windowEndMs - g.windowStartMs;
  let outside = 0;
  for (let i = 0; i < g.n; i++) if (slotOutside(g, i)) outside++;
  let rest = ` · ${g.n} ${g.n === 1 ? 'bar' : 'bars'} on the axis · window ${formatSpan(spanMs)}`;
  if (bucketSeconds > 60) rest += ' · longer periods group more time into each bar so the chart stays readable';
  if (outside > 0) rest += ` · ${outside} end ${outside === 1 ? 'slot' : 'slots'} outside the window`;
  return { lead: 'Each bar = ' + widthLabel(bucketSeconds), rest };
}

export function axisCaption(zoneName: string, bucketSeconds: number, hasOutside: boolean): string {
  let caption = 'Time (' + zoneName + '). Each bar covers ' + widthLabel(bucketSeconds) + ', starting at the time on its left edge; every panel uses the same time axis. ';
  if (bucketSeconds > 60) caption += 'Longer periods group more time into each bar so the chart stays readable. ';
  if (hasOutside) caption += 'Grey dashes are slots outside your window.';
  return caption;
}

export function laneNote(lane: TracingLane): string {
  if (lane.disabled) return lane.reason || LANE_STATE_TEXT['clock-unchecked'];
  // Files are listed for the lane but the case holds none of their events (never imported, or set aside): the server says which.
  if (lane.state === 'no-data' && lane.emptyBecause && lane.reason) return lane.reason;
  // A lane with no file at all: say so plainly rather than the server's short phrase.
  if (lane.state === 'no-data' && lane.key !== 'audit') return READ_TEXT.noFile;
  // A lane cut short by a time limit can still carry the state "events" with the bars it did read; it says so all the same.
  if (laneCutShort(lane) && (lane.state === 'incomplete' || lane.state === 'events' || lane.state === 'none-seen')) return INCOMPLETE_TEXT[lane.incompleteReason as TracingReason];
  if (lane.state === 'events') return '';
  if (lane.state === 'incomplete') {
    if (lane.reason) return lane.reason;
    if (lane.incompleteReason) return INCOMPLETE_TEXT[lane.incompleteReason];
    return LANE_STATE_TEXT.incomplete;
  }
  return lane.reason || LANE_STATE_TEXT[lane.state] || '';
}

/** A y-axis tick's position as a share of the scale height, 0 to 100 measured from the bottom; 0 for a scale with no height. */
export function tickBottomPct(value: number, max: number): number {
  if (!(max > 0)) return 0;
  return Math.min(100, Math.max(0, (value / max) * 100));
}
