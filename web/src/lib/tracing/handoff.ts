// Bucket click hand-off (WP-61): a clicked bar becomes the analysis window; the checklist, After the fix and Nearby activity stay in guided and are not rebuilt here.
import type { RollupWindow } from '../rollup';
import type { TracingResponse, TracingLaneKey } from './model';
import type { LintEntry } from '../copy/copylint';
import { slotGrid, slotStartMs, slotOutside, barsFor } from './lanes';

/** The slot's own start and end, ISO-8601 UTC. */
export interface HandoffBucket { start: string; end: string }

export const HANDOFF_TEXT = {
  empty: 'Nothing was recorded in that bar, so there is nothing to analyse. Pick a bar with events.'
};

export const handoffCopyEntries = (): LintEntry[] =>
  Object.entries(HANDOFF_TEXT).map(([id, text]) => ({ id: 'tracing.handoff.' + id, text }));

/**
 * The whole grid cell of a clicked bar, or null when there is nothing to analyse (no grid, a bad index, a slot outside
 * the request window, or no shown lane with events in it). `slotIndex` is absolute, so no zoom offset is applied.
 */
export function slotToBucket(response: TracingResponse, visible: Partial<Record<TracingLaneKey, boolean>>, slotIndex: number): HandoffBucket | null {
  const g = slotGrid(response.window);
  if (!g) return null;
  if (!Number.isInteger(slotIndex) || slotIndex < 0 || slotIndex > g.n - 1) return null;
  if (slotOutside(g, slotIndex)) return null;
  const hasEvents = response.lanes.some((lane) =>
    visible[lane.key] !== false && barsFor(g, lane.buckets).some((bar) => bar.index === slotIndex && bar.count > 0));
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
