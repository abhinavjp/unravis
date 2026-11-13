/**
 * Turns a clicked timeline slot into the slowest-calls query and the nearby window around one call. Pure TypeScript.
 */
import type { Query, Page } from '../types';
import type { TracingLane, TracingSourceRef, TracingWindow } from '../tracing/model';

/** The most source ids one drill query carries (the server's MaxSources). */
const MAX_SOURCE_IDS = 8;
/** The largest bucket the drill query asks for, in seconds (one day). */
const MAX_BUCKET_SECONDS = 86400;
/** The smallest bucket the drill query asks for, in seconds. */
const MIN_BUCKET_SECONDS = 10;

/** The ISO bounds of one clicked slot: its start and the start of the next slot. Null when the axis start or the slot is not usable. */
export function slotWindow(window: TracingWindow, slot: number): { start: string; end: string } | null {
  const bucketMs = window.bucketSeconds * 1000;
  const startMs = Date.parse(window.axisStart) + slot * bucketMs;
  if (!Number.isFinite(startMs) || !Number.isFinite(bucketMs) || bucketMs <= 0 || slot < 0) return null;
  return { start: new Date(startMs).toISOString(), end: new Date(startMs + bucketMs).toISOString() };
}

/** The audit lane's sources read; when none were read, the listed sources that have a file. Deduplicated, at most 8. May be empty: the caller must not send an empty list. */
export function drillSourceIds(lane: TracingLane, sources: readonly TracingSourceRef[]): string[] {
  const read = lane.sourcesRead && lane.sourcesRead.length > 0 ? lane.sourcesRead : sources.filter((source) => !source.noFile);
  const ids: string[] = [];
  for (const source of read) {
    if (!ids.includes(source.sourceId)) ids.push(source.sourceId);
    if (ids.length >= MAX_SOURCE_IDS) break;
  }
  return ids;
}

/** The slowest-calls query for one window: every kind of call, every outcome, no record filters. */
export function drillQuery(win: { start: string; end: string }, sourceIds: string[]): Query {
  const startMs = Date.parse(win.start);
  const endMs = Date.parse(win.end);
  const width = endMs - startMs;
  const bucketSeconds = Math.min(MAX_BUCKET_SECONDS, Math.max(MIN_BUCKET_SECONDS, Math.round(width / 1000)));
  return {
    sourceIds: [...sourceIds],
    start: win.start,
    end: win.end,
    baselineStart: new Date(startMs - width).toISOString(),
    baselineEnd: win.start,
    kind: 'all',
    outcome: 'all',
    metric: 'total',
    bucketSeconds,
    controller: '',
    action: ''
  };
}

/** The first row that names both its source and its id, or null when none does. */
export function pickCall(page: Page | null): { sourceId: string; id: string } | null {
  if (!page) return null;
  for (const row of page.rows) {
    if (row.sourceId && row.id) return { sourceId: row.sourceId, id: row.id };
  }
  return null;
}

/** The window of `minutes` either side of the call's start (or its completion when it has no start). Null when neither parses. */
export function nearbyBounds(
  call: { initiatedAt: string | null; completedAt: string | null },
  minutes = 15
): { start: string; end: string } | null {
  const anchorText = call.initiatedAt ?? call.completedAt;
  if (anchorText === null || anchorText === undefined) return null;
  const anchorMs = Date.parse(anchorText);
  if (Number.isNaN(anchorMs)) return null;
  const spanMs = minutes * 60_000;
  return { start: new Date(anchorMs - spanMs).toISOString(), end: new Date(anchorMs + spanMs).toISOString() };
}
