// Bucket click hand-off (WP-61): a clicked bar becomes the analysis window; the checklist, After the fix and Nearby activity stay in guided and are not rebuilt here.
import type { RollupWindow } from '../rollup';
import type { TracingResponse, TracingLane, TracingLaneKey } from './model';
import type { LintEntry } from '../copy/copylint';
import { slotGrid, slotStartMs, slotOutside, barsFor, type Bar } from './lanes';

/** The slot's own start and end, ISO-8601 UTC. */
export interface HandoffBucket { start: string; end: string }

export const HANDOFF_TEXT = {
  empty: 'Nothing was recorded in that bar, so there is nothing to analyse. Pick a bar with events.'
};

export const handoffCopyEntries = (): LintEntry[] =>
  Object.entries(HANDOFF_TEXT).map(([id, text]) => ({ id: 'tracing.handoff.' + id, text }));

/** A row is shown unless the toolbar switched it off (an unset key means shown). */
export const isLaneVisible = (visible: Partial<Record<TracingLaneKey, boolean>>, key: TracingLaneKey): boolean => visible[key] !== false;

/** The chart's rule for "this lane draws bars": a disabled lane, or one that is not events or incomplete-with-bars, draws none, so a click there hands off nothing. */
export function laneDrawsBars(lane: Pick<TracingLane, 'state' | 'disabled'>, bars: Bar[]): boolean {
  if (lane.disabled) return false;
  if (lane.state === 'events') return true;
  return lane.state === 'incomplete' && bars.length > 0;
}

/**
 * The whole grid cell of a clicked bar, or null when there is nothing to analyse (no grid, a bad index, a slot outside
 * the request window, or no shown lane with events in it). `slotIndex` is absolute, so no zoom offset is applied.
 */
export function slotToBucket(response: TracingResponse, visible: Partial<Record<TracingLaneKey, boolean>>, slotIndex: number): HandoffBucket | null {
  const g = slotGrid(response.window);
  if (!g) return null;
  if (!Number.isInteger(slotIndex) || slotIndex < 0 || slotIndex > g.n - 1) return null;
  if (slotOutside(g, slotIndex)) return null;
  const hasEvents = response.lanes.some((lane) => {
    if (!isLaneVisible(visible, lane.key)) return false;
    const bars = barsFor(g, lane.buckets);
    return laneDrawsBars(lane, bars) && bars.some((bar) => bar.index === slotIndex && bar.count > 0);
  });
  if (!hasEvents) return null;
  const startMs = slotStartMs(g, slotIndex);
  return { start: new Date(startMs).toISOString(), end: new Date(startMs + g.widthMs).toISOString() };
}

/** Same rule as `cellWindow` in rollup.ts: both ends parse and start is before end. */
export function bucketToWindow(bucket: HandoffBucket | null | undefined): RollupWindow | null {
  if (!bucket) return null;
  const start = Date.parse(bucket.start);
  const end = Date.parse(bucket.end);
  if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end) return null;
  return { start: bucket.start, end: bucket.end };
}

export interface HandoffDeps { setWindow: (window: RollupWindow) => void; open: () => void }

/** Sets the window first, then opens the analysis once. Returns false and does nothing for a refused bucket. */
export function handOffBucket(bucket: HandoffBucket | null | undefined, deps: HandoffDeps): boolean {
  const window = bucketToWindow(bucket);
  if (!window) return false;
  deps.setWindow(window);
  deps.open();
  return true;
}
