/**
 * Chart canvas maths (roadmap WP-23a): the zoom-range arithmetic, the latency percentile helpers and the
 * fixed control copy for the tracing chart. Pure TypeScript, no Svelte and no chart library: every function
 * takes its inputs and returns a value, so the chart component stays a thin shell over this module.
 */
import type { TracingBucket } from './model';
import type { LintEntry } from '../copy/copylint';
import { slotAtFraction } from './tooltip';

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
  dragHint: 'Drag across the chart to zoom into those bars.'
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
    { id: 'trace-canvas.zoomedText', text: zoomedText(60, 120) }
  ];
}
