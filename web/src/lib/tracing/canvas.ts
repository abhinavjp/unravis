/**
 * Chart canvas maths (roadmap WP-23a): the zoom-range arithmetic, the latency percentile helpers and the
 * fixed control copy for the tracing chart. Pure TypeScript, no Svelte and no chart library: every function
 * takes its inputs and returns a value, so the chart component stays a thin shell over this module.
 */
import type { TracingBucket } from './model';
import type { SlotGrid } from './lanes';
import type { LintEntry } from '../copy/copylint';
import { slotAtFraction } from './tooltip';
import { zoneOffsetMs } from '../chartTime';

// ---------------------------------------------------------------------------------------------------------------------------------
// Zoom ranges. A ZoomRange is a half-open span of absolute bucket slot indices [from, to) into the lane's
// bucket array, so every consumer keeps addressing buckets by their real index no matter how far it is zoomed.

/** The narrowest window the chart will show, in bars. */
export const MIN_ZOOM_SLOTS = 4;

/** A half-open span of bucket slot indices: `from` is shown, `to` is the first slot not shown. */
export interface ZoomRange { from: number; to: number }

/** The whole window: every one of the `n` buckets. */
export function fullRange(n: number): ZoomRange {
  return { from: 0, to: n };
}

/** True when the range shows anything less than exactly the whole window of `n` buckets. */
export function isZoomed(range: ZoomRange, n: number): boolean {
  return range.from !== 0 || range.to !== n;
}

/**
 * Repairs a range so that `0 <= from < to <= n` holds and the width is at least `min(MIN_ZOOM_SLOTS, n)`.
 * A range wider than `n` is cut back to the full window; the span slides, never stretches past either end.
 */
export function clampRange(range: ZoomRange, n: number): ZoomRange {
  const size = Math.max(0, Math.floor(n));
  if (size === 0) return { from: 0, to: 0 };
  const minWidth = Math.min(MIN_ZOOM_SLOTS, size);
  let width = Math.round(range.to - range.from);
  width = Math.min(Math.max(width, minWidth), size);
  let from = Math.min(Math.max(0, Math.round(range.from)), size - 1);
  if (from + width > size) from = size - width;
  return { from, to: from + width };
}

/**
 * Halves the window (never narrower than MIN_ZOOM_SLOTS, the current width or `n`), centred on `focus`
 * (an absolute slot index; the middle of `range` when omitted), then clamped inside the grid.
 */
export function zoomIn(range: ZoomRange, n: number, focus?: number): ZoomRange {
  const width = range.to - range.from;
  const newWidth = Math.min(Math.max(MIN_ZOOM_SLOTS, Math.floor(width / 2)), width, Math.max(0, Math.floor(n)));
  const centre = focus === undefined ? (range.from + range.to) / 2 : focus;
  const from = Math.round(centre - newWidth / 2);
  return clampRange({ from, to: from + newWidth }, n);
}

/** Doubles the window (capped at `n`), centred on the middle of `range`, then clamped inside the grid. */
export function zoomOut(range: ZoomRange, n: number): ZoomRange {
  const width = Math.min((range.to - range.from) * 2, Math.max(0, Math.floor(n)));
  const centre = (range.from + range.to) / 2;
  const from = Math.round(centre - width / 2);
  return clampRange({ from, to: from + width }, n);
}

/**
 * The range a pointer drag picked: `a` and `b` are absolute slot indices in either order, both included,
 * so the result is [min, max + 1). Each index is clamped into [0, n) first, and a drag shorter than the
 * minimum width is widened around where it was drawn.
 */
export function rangeFromDrag(n: number, a: number, b: number): ZoomRange {
  const size = Math.max(0, Math.floor(n));
  if (size === 0) return { from: 0, to: 0 };
  const clampIndex = (value: number): number => Math.min(size - 1, Math.max(0, Math.round(value)));
  const low = Math.min(clampIndex(a), clampIndex(b));
  const high = Math.max(clampIndex(a), clampIndex(b));
  return clampRange({ from: low, to: high + 1 }, size);
}

/**
 * The absolute bucket slot at `fraction` (0 to 1) across the visible range: the slot inside the range,
 * shifted by its start. -1 when the fraction picks no slot (see `slotAtFraction`).
 */
export function absoluteSlot(range: ZoomRange, fraction: number): number {
  const slot = slotAtFraction(fraction, range.to - range.from);
  return slot < 0 ? -1 : range.from + slot;
}

/** `index + delta`, kept inside the visible range so the keyboard focus never leaves the window. */
export function clampArrow(range: ZoomRange, index: number, delta: number): number {
  return Math.min(range.to - 1, Math.max(range.from, index + delta));
}

/**
 * Where "Zoom in" should centre. A selected bucket inside the range wins. Otherwise the data decides, so repeated
 * zooming never walks away to an empty stretch: the middle of the span of non-empty buckets when that span fits
 * the narrower window, else the tallest bucket. Undefined (zoom on the range middle) when the range holds no data.
 */
export function zoomFocus(
  range: ZoomRange,
  selected: number | null | undefined,
  bars: ReadonlyArray<{ index: number; count: number }>
): number | undefined {
  if (selected != null && selected >= range.from && selected < range.to) return selected;
  const totals = new Map<number, number>();
  for (const bar of bars) {
    if (bar.index < range.from || bar.index >= range.to || !(bar.count > 0)) continue;
    totals.set(bar.index, (totals.get(bar.index) ?? 0) + bar.count);
  }
  if (totals.size === 0) return undefined;
  const indexes = [...totals.keys()];
  const low = Math.min(...indexes);
  const high = Math.max(...indexes);
  const width = range.to - range.from;
  const nextWidth = Math.min(width, Math.max(MIN_ZOOM_SLOTS, Math.floor(width / 2)));
  if (high - low + 1 <= nextWidth) return (low + high) / 2;
  let peak = low;
  for (const [index, total] of totals) {
    const best = totals.get(peak) ?? 0;
    if (total > best || (total === best && index < peak)) peak = index;
  }
  return peak;
}

/**
 * The next slot from `index` in the direction of `delta` that is not `outside` (a slot that only fills out the axis),
 * kept inside the range. Stays on `index` (kept in range) when nothing further that way is inside the window.
 */
export function stepSlot(range: ZoomRange, index: number, delta: number, outside: (slot: number) => boolean): number {
  const dir = delta < 0 ? -1 : 1;
  for (let slot = index + dir; slot >= range.from && slot < range.to; slot += dir) {
    if (!outside(slot)) return slot;
  }
  return Math.min(range.to - 1, Math.max(range.from, index));
}

/** The first (Home) or last (End) slot of the range that is not `outside`; the range edge itself when every slot is. */
export function edgeSlot(range: ZoomRange, edge: 'first' | 'last', outside: (slot: number) => boolean): number {
  if (edge === 'first') {
    for (let slot = range.from; slot < range.to; slot += 1) if (!outside(slot)) return slot;
    return range.from;
  }
  for (let slot = range.to - 1; slot >= range.from; slot -= 1) if (!outside(slot)) return slot;
  return range.to - 1;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Time-axis ticks. At most about six labels, thinned further whenever the measured plot is too narrow for them.

const MAX_TICKS = 6;
const LABEL_PX_PER_CHAR = 7;
const LABEL_GAP_PX = 12;
const LABEL_OVERHANG_PX = 8;

/** The slot spacing between two tick labels: about MAX_TICKS labels, widened until labels of `labelChars` characters fit in `widthPx`. */
export function tickStep(shown: number, widthPx: number, labelChars: number): number {
  const base = Math.max(1, Math.ceil(shown / MAX_TICKS));
  if (!(widthPx > 0) || shown < 1) return base;
  const slotPx = widthPx / shown;
  const needPx = Math.max(0, labelChars) * LABEL_PX_PER_CHAR + LABEL_GAP_PX;
  return Math.max(base, Math.ceil(needPx / slotPx));
}

/** Where the visible slots sit in time, so labels can land on round clock times in the display zone. */
export interface TickAlign { startMs: number; widthMs: number; zone: string }

const MINUTE_MS = 60_000, HOUR_MS = 60 * MINUTE_MS;
/** Round clock steps a label may land on, shortest first (a day is the longest: later steps have no natural boundary). */
const ROUND_STEPS_MS = [MINUTE_MS, 2 * MINUTE_MS, 5 * MINUTE_MS, 10 * MINUTE_MS, 15 * MINUTE_MS, 30 * MINUTE_MS, HOUR_MS, 2 * HOUR_MS, 3 * HOUR_MS, 6 * HOUR_MS, 12 * HOUR_MS, 24 * HOUR_MS];

/**
 * The slots (0-based within the visible range) whose start falls on a round clock time, with at least `step` slots between labels.
 * The round step is the smallest one that is wide enough and is a whole number of slots. Empty when there is none.
 */
function roundOffsets(shown: number, step: number, align: TickAlign): number[] {
  const { startMs, widthMs, zone } = align;
  if (!(widthMs > 0)) return [];
  const unit = ROUND_STEPS_MS.find((ms) => ms >= step * widthMs && ms % widthMs === 0);
  if (!unit) return [];
  const lastMs = startMs + (shown - 1) * widthMs;
  const firstOffset = zoneOffsetMs(startMs, zone);
  const steady = firstOffset === zoneOffsetMs(lastMs, zone);
  // The window starts on a whole hour (or on the step itself) that the step's own grid misses (15:00 with a 2 hour step):
  // keep the label on the start and count the step from there, so the start is never left unlabeled.
  if (steady && ((firstOffset + startMs) % unit + unit) % unit !== 0 && ((firstOffset + startMs) % Math.min(unit, HOUR_MS) + Math.min(unit, HOUR_MS)) % Math.min(unit, HOUR_MS) === 0) {
    const gap = unit / widthMs;
    const anchored: number[] = [];
    for (let k = 0; k < shown; k += gap) anchored.push(k);
    return anchored;
  }
  const offsets: number[] = [];
  for (let k = 0; k < shown; k += 1) {
    const at = startMs + k * widthMs;
    // A clock change inside the range moves the local time, so each slot is read in the zone then.
    const local = at + (steady ? firstOffset : zoneOffsetMs(at, zone));
    if (((local % unit) + unit) % unit === 0) offsets.push(k);
  }
  return offsets;
}

/**
 * Offsets (0-based within the visible range) that get a time label. A later label that would hang past the right edge of the plot is dropped.
 * `widthPx` 0 means "not measured yet": only the count rule applies. With `align` the labels sit on round clock times (08:00, 09:00 ...) when the
 * slot width allows it; otherwise the first slot and every Nth slot after it carry one.
 */
export function tickOffsets(shown: number, widthPx: number, labelChars: number, align?: TickAlign): number[] {
  if (shown < 1) return [];
  const step = tickStep(shown, widthPx, labelChars);
  const half = (Math.max(0, labelChars) * LABEL_PX_PER_CHAR) / 2;
  const fits = (k: number) => !(widthPx > 0 && k > 0 && (k / shown) * widthPx + half > widthPx + LABEL_OVERHANG_PX);
  const round = align ? roundOffsets(shown, step, align) : [];
  if (round.length > 0) {
    const kept = round.filter(fits);
    if (kept.length > 0) return kept;
  }
  const offsets: number[] = [];
  for (let k = 0; k < shown; k += step) {
    if (!fits(k)) break;
    offsets.push(k);
  }
  return offsets;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Latency percentiles. The server may send p50Ms/p95Ms on a bucket or leave them out; the helpers below
// are the one place that decides whether a value is fit to draw.

export type Percentile = 'p50' | 'p95';

/** The percentiles the chart can draw, in legend order. */
export const PERCENTILES: Percentile[] = ['p50', 'p95'];

/** The on-screen name of each latency line. */
export const PERCENTILE_LABEL: Record<Percentile, string> = {
  p50: 'Typical (P50)',
  p95: 'Slow end (P95)'
};

/** The bucket's value for the percentile when it is a finite number >= 0; null when it is missing or bad. */
export function percentileValue(bucket: TracingBucket, percentile: Percentile): number | null {
  const value = percentile === 'p50' ? bucket.p50Ms : bucket.p95Ms;
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

/** True when at least one bucket carries a usable value for the percentile. */
export function hasLatency(buckets: TracingBucket[], percentile: Percentile): boolean {
  return buckets.some((bucket) => percentileValue(bucket, percentile) !== null);
}

/**
 * The latency line for the visible `range`: runs of "x,y" points in a 0..100 box (x across the range, y 0 at the top),
 * one run per stretch of consecutive buckets with a value, so a bucket without one leaves a gap. `max` is the tallest
 * visible value (what y = 0 stands for). Buckets that land in the same slot keep the larger value.
 */
export function latencySegments(
  buckets: TracingBucket[],
  grid: Pick<SlotGrid, 'firstMs' | 'widthMs'>,
  range: ZoomRange,
  percentile: Percentile
): { segments: string[]; max: number } {
  const shown = range.to - range.from;
  if (shown < 1) return { segments: [], max: 0 };
  const values = new Map<number, number>();
  for (const bucket of buckets) {
    const value = percentileValue(bucket, percentile);
    const startMs = Date.parse(bucket.start);
    if (value === null || !Number.isFinite(startMs)) continue;
    const index = Math.floor((startMs - grid.firstMs) / grid.widthMs);
    if (index >= range.from && index < range.to) values.set(index, Math.max(values.get(index) ?? 0, value));
  }
  const max = Math.max(0, ...values.values());
  const widthPct = 100 / shown;
  const segments: string[] = [];
  let run: string[] = [];
  for (let index = range.from; index < range.to; index += 1) {
    const value = values.get(index);
    if (value === undefined) {
      if (run.length > 0) segments.push(run.join(' '));
      run = [];
      continue;
    }
    const x = (index - range.from) * widthPct + widthPct / 2;
    const y = max > 0 ? 100 - (value / max) * 100 : 100;
    run.push(x.toFixed(2) + ',' + y.toFixed(2));
  }
  if (run.length > 0) segments.push(run.join(' '));
  return { segments, max };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Control copy. Every fixed sentence the chart controls can put on screen, in one place for the copy lint.

export const CANVAS_TEXT = {
  ariaLabel: 'Chart controls',
  zoomIn: 'Zoom in',
  zoomOut: 'Zoom out',
  resetZoom: 'Reset zoom',
  latencyLabel: 'Latency line',
  noLatency: 'No latency figures were returned for this window.',
  allHidden: 'All rows are hidden. Turn a row on to see it.',
  dragHint: 'Drag across the chart to zoom into those bars.',
  noAxis: 'The time axis could not be drawn for this window. Try a shorter period.'
} as const;

/** The status line under the chart while zoomed. */
export function zoomedText(shown: number, total: number): string {
  return 'Showing ' + shown + ' of ' + total + ' bars.';
}

/** Every fixed sentence this module can put on screen, as entries for the copy lint. */
export function canvasCopyEntries(): LintEntry[] {
  return [
    { id: 'trace-canvas.ariaLabel', text: CANVAS_TEXT.ariaLabel },
    { id: 'trace-canvas.zoomIn', text: CANVAS_TEXT.zoomIn },
    { id: 'trace-canvas.zoomOut', text: CANVAS_TEXT.zoomOut },
    { id: 'trace-canvas.resetZoom', text: CANVAS_TEXT.resetZoom },
    { id: 'trace-canvas.latencyLabel', text: CANVAS_TEXT.latencyLabel },
    { id: 'trace-canvas.noLatency', text: CANVAS_TEXT.noLatency },
    { id: 'trace-canvas.allHidden', text: CANVAS_TEXT.allHidden },
    { id: 'trace-canvas.dragHint', text: CANVAS_TEXT.dragHint },
    { id: 'trace-canvas.noAxis', text: CANVAS_TEXT.noAxis },
    { id: 'trace-canvas.zoomedText', text: zoomedText(60, 120) }
  ];
}
