// Slow call screen: turns a tracing response into three drawable lanes.
// Each lane has one value per time step ("running at once", or null for "not recorded"),
// the peak, and slow items packed into at most three bar rows. Pure functions only.
// The input interfaces are structural: a tracing response satisfies them.

import { DRILL_TEXT } from './drillCopy';
import type { Readout } from './callReadout';

export type LaneKey = 'audit' | 'sql' | 'iis';

export interface LaneBucketIn {
  start: string;
  count: number;
  complete?: boolean;
  busyMs?: number | null;
  p95Ms?: number | null;
  errors?: number | null;
  clientErrors?: number | null;
}
export interface LaneEventIn {
  id: string;
  label: string;
  start: string;
  end: string;
  durationMs: number | null;
  kind?: string;
}
export interface LaneIn {
  key: LaneKey;
  state: string;
  reasonCode?: string;
  blockSeconds?: number;
  buckets: LaneBucketIn[];
  events: LaneEventIn[];
  hidden?: number;
}
export interface WindowIn {
  axisStart: string;
  bucketSeconds: number;
}

export interface LaneCell {
  start: number;
  end: number;
  atOnce: number | null;
  p95Ms: number | null;
  refused: number | null;
}
export interface LaneBar {
  id: string;
  label: string;
  start: number;
  end: number;
  row: number;
  kind: 'bar' | 'marker';
}
/** Why a lane has no numbers: the source was not selected, its read failed, or it simply did not record them. */
export type LaneGap = 'not-recorded' | 'no-file' | 'read-failed' | 'not-imported';

export interface CallLane {
  key: LaneKey;
  /** Only used when the lane has no peak. Never a number: a gap is unknown, not zero. */
  gap: LaneGap;
  cells: LaneCell[];
  peak: { atOnce: number; start: number } | null;
  bars: LaneBar[];
  moreBars: number;
  blockSeconds: number | null;
  hasShape: boolean;
}

export const MAX_BAR_ROWS = 3;

/** Most rows a list on the Slow call screen draws at once; the rest are counted in a plain note. */
export const LIST_ROW_CAP = 200;

function finite(n: number | null | undefined): n is number {
  return typeof n === 'number' && Number.isFinite(n);
}

/** Average number of calls running at once: busy time divided by step length. Unknown stays null, never 0. */
export function atOnce(busyMs: number | null | undefined, stepSeconds: number): number | null {
  if (!finite(busyMs) || !finite(stepSeconds) || stepSeconds <= 0) return null;
  return busyMs / (stepSeconds * 1000);
}

/** One cell per step between fromMs and toMs, aligned to the axis start. A step with no usable bucket is a null cell. */
export function laneCells(lane: LaneIn, win: WindowIn, fromMs: number, toMs: number): LaneCell[] {
  const stepMs = win.bucketSeconds * 1000;
  const axisStart = Date.parse(win.axisStart);
  if (!finite(stepMs) || stepMs <= 0 || !Number.isFinite(axisStart) || !Number.isFinite(fromMs) || !Number.isFinite(toMs)) return [];
  const alignedStart = (t: number) => axisStart + Math.floor((t - axisStart) / stepMs) * stepMs;

  const byStart = new Map<number, LaneBucketIn>();
  for (const bucket of lane.buckets ?? []) {
    const t = Date.parse(bucket.start);
    if (Number.isFinite(t)) byStart.set(alignedStart(t), bucket);
  }

  const read = lane.state === 'events';
  const cells: LaneCell[] = [];
  for (let start = alignedStart(fromMs); start < toMs; start += stepMs) {
    const bucket = byStart.get(start);
    let value: number | null = null;
    let p95: number | null = null;
    let refused: number | null = null;
    if (bucket && read && bucket.complete !== false) {
      if (finite(bucket.busyMs)) value = atOnce(bucket.busyMs, win.bucketSeconds);
      else if (bucket.count === 0) value = 0; // the source was read and saw nothing
      p95 = finite(bucket.p95Ms) ? bucket.p95Ms : null;
      if (finite(bucket.clientErrors) || finite(bucket.errors)) {
        refused = (finite(bucket.clientErrors) ? bucket.clientErrors : 0) + (finite(bucket.errors) ? bucket.errors : 0);
      }
    }
    cells.push({ start, end: start + stepMs, atOnce: value, p95Ms: p95, refused });
  }
  return cells;
}

/** Re-averages the cells inside each block (blocks start on whole multiples of the block length). */
export function regroupBlocks(cells: LaneCell[], blockSeconds: number): LaneCell[] {
  const blockMs = blockSeconds * 1000;
  if (!finite(blockMs) || blockMs <= 0 || cells.length === 0) return cells.map((c) => ({ ...c }));
  const groups = new Map<number, LaneCell[]>();
  for (const cell of cells) {
    const key = Math.floor(cell.start / blockMs);
    const group = groups.get(key);
    if (group) group.push(cell);
    else groups.set(key, [cell]);
  }
  const values = new Map<number, number | null>();
  for (const [key, group] of groups) {
    let busy = 0;
    let covered = 0;
    let any = false;
    for (const cell of group) {
      covered += cell.end - cell.start;
      if (cell.atOnce !== null) {
        busy += cell.atOnce * (cell.end - cell.start);
        any = true;
      }
    }
    values.set(key, any && covered > 0 ? busy / covered : null);
  }
  return cells.map((cell) => ({ ...cell, atOnce: values.get(Math.floor(cell.start / blockMs)) ?? null }));
}

type PackItem = { id: string; label: string; start: number; end: number; kind?: 'bar' | 'marker' };

/** Packs items into rows. Longest items get a place first; items that need a row beyond maxRows are dropped and counted. */
export function packBars(items: PackItem[], maxRows: number = MAX_BAR_ROWS): { bars: LaneBar[]; more: number } {
  const span = (i: PackItem) => Math.max(i.end, i.start + 1); // a marker still holds one millisecond
  const order = items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => (span(b.item) - b.item.start) - (span(a.item) - a.item.start) || a.item.start - b.item.start || a.index - b.index);
  const rows: PackItem[][] = [];
  const placed: LaneBar[] = [];
  let more = 0;
  for (const { item } of order) {
    let row = -1;
    for (let r = 0; r < Math.min(rows.length + 1, maxRows); r += 1) {
      const taken = rows[r] ?? [];
      if (taken.every((o) => span(o) <= item.start || span(item) <= o.start)) {
        row = r;
        break;
      }
    }
    if (row < 0) {
      more += 1;
      continue;
    }
    (rows[row] ??= []).push(item);
    placed.push({
      id: item.id,
      label: item.label,
      start: item.start,
      end: item.end,
      row,
      kind: item.kind ?? (item.end > item.start ? 'bar' : 'marker')
    });
  }
  placed.sort((a, b) => a.start - b.start || a.row - b.row);
  return { bars: placed, more };
}

function emptyLane(key: LaneKey): LaneIn {
  return { key, state: 'no-data', buckets: [], events: [] };
}

/** Why a lane without numbers is empty, from the lane's own state. A lane the answer does not hold is simply not recorded. */
export function laneGap(lane: Pick<LaneIn, 'state' | 'reasonCode'> | undefined): LaneGap {
  if (!lane) return 'not-recorded';
  if (lane.state === 'no-data' || lane.state === 'out-of-range') {
    if (lane.reasonCode === 'not-imported' || lane.reasonCode === 'import-skipped') return 'not-imported';
  }
  if (lane.state === 'no-data') return 'no-file';
  if (lane.state === 'incomplete') return 'read-failed';
  return 'not-recorded';
}

/** The short words for a lane gap (copy lives in drillCopy). */
export function laneGapText(gap: LaneGap | undefined): string {
  if (gap === 'not-imported') return DRILL_TEXT.laneNotImported;
  if (gap === 'no-file') return DRILL_TEXT.laneNoFile;
  if (gap === 'read-failed') return DRILL_TEXT.laneReadFailed;
  return DRILL_TEXT.notRecorded;
}

/** Keeps the first `cap` rows of a list. `total` is always the full count, so a note can say how many are left out. */
export function capRows<T>(rows: readonly T[], cap: number = LIST_ROW_CAP): { rows: T[]; total: number; capped: boolean } {
  const limit = Number.isFinite(cap) && cap > 0 ? Math.floor(cap) : LIST_ROW_CAP;
  return { rows: rows.length > limit ? rows.slice(0, limit) : rows.slice(), total: rows.length, capped: rows.length > limit };
}

/** True when two readouts would draw the same text, so the parent need not be told again. */
export function sameReadout(a: Readout | null, b: Readout | null): boolean {
  if (a === b) return true;
  if (a === null || b === null) return false;
  return JSON.stringify(a) === JSON.stringify(b);
}

/** Builds the drawable lane for one source. A missing lane is drawn as not recorded. */
export function buildLane(lane: LaneIn | undefined, key: LaneKey, win: WindowIn, fromMs: number, toMs: number): CallLane {
  const source = lane ?? emptyLane(key);
  let cells = laneCells(source, win, fromMs, toMs);
  const blockSeconds = finite(source.blockSeconds) && source.blockSeconds > 0 ? source.blockSeconds : null;
  if (key === 'sql' && blockSeconds !== null && blockSeconds > win.bucketSeconds) cells = regroupBlocks(cells, blockSeconds);

  let peak: CallLane['peak'] = null;
  for (const cell of cells) {
    if (cell.atOnce !== null && (peak === null || cell.atOnce > peak.atOnce)) peak = { atOnce: cell.atOnce, start: cell.start };
  }

  const items: PackItem[] = [];
  for (const event of source.events ?? []) {
    const start = Date.parse(event.start);
    const end = Date.parse(event.end);
    if (!Number.isFinite(start) && !Number.isFinite(end)) continue;
    const duration = finite(event.durationMs) && event.durationMs > 0 ? event.durationMs : 0;
    if (duration === 0) {
      const at = Number.isFinite(start) ? start : end;
      items.push({ id: event.id, label: event.label, start: at, end: at, kind: 'marker' });
    } else if (key === 'iis') {
      const stop = Number.isFinite(end) ? end : start + duration;
      items.push({ id: event.id, label: event.label, start: stop - duration, end: stop, kind: 'bar' });
    } else {
      const from = Number.isFinite(start) ? start : end - duration;
      const to = Number.isFinite(end) ? end : start + duration;
      items.push({ id: event.id, label: event.label, start: from, end: to, kind: 'bar' });
    }
  }
  const { bars, more } = packBars(items);

  return {
    key,
    gap: lane ? laneGap(lane) : 'not-recorded', // a lane missing from the answer (still loading) is not yet known
    cells,
    peak,
    bars,
    moreBars: more,
    blockSeconds,
    hasShape: cells.some((c) => c.atOnce !== null)
  };
}

/** One decimal under 10 ('4.2'), a whole number from 10 up, '0' for 0. */
export function formatAtOnce(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '0';
  const tenth = Math.round(n * 10) / 10;
  if (tenth < 10) return tenth.toFixed(1);
  return String(Math.round(n));
}
