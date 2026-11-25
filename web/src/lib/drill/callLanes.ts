// Slow call screen: turns a tracing response into three drawable lanes.
// Each lane has one value per time step in its own measure — "running at once" from measured busy time,
// or "batches per second" from recorded host batch counts — and null for "not recorded",
// the peak, and slow items packed into at most three bar rows. Pure functions only.
// The input interfaces are structural: a tracing response satisfies them.

import { DRILL_TEXT } from './drillCopy';
import type { Readout } from './callReadout';

export type LaneKey = 'audit' | 'sql' | 'iis';

/**
 * What a lane's numbers measure. The two are never mixed:
 *  - 'at-once': average concurrency, worked out from measured busy time (busyMs) only.
 *  - 'batch-rate': host batch throughput in batches per second, worked out from recorded batch COUNTS.
 * A count is never busy time, and a rate never implies a duration. The 'blackbox' reason code is the
 * host PDH batch-count contract, so it always measures 'batch-rate' (W13-01).
 */
export type LaneMeasure = 'at-once' | 'batch-rate';

export interface LaneBucketIn {
  start: string;
  /** The bucket's own end as the server sent it. A clipped edge bucket ends before the step does. */
  end?: string;
  /** True when the server cut this bucket to the asked window: its count covers only start..end, not the whole step. */
  clipped?: boolean;
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
  /** What this lane's buckets measure. Absent = derived from the reason code ('blackbox' -> batch throughput). */
  measure?: LaneMeasure;
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
  /** Average concurrency from measured busy time. Null = unknown, never 0. Never derived from a batch rate. */
  atOnce: number | null;
  /** Host batch throughput in batches per second from recorded counts. Null = unknown. Only a 'batch-rate' lane fills it. */
  rate: number | null;
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
export type LaneGap = 'not-recorded' | 'no-file' | 'read-failed' | 'not-imported' | 'import-skipped';

export interface CallLane {
  key: LaneKey;
  /** Only used when the lane has no peak. Never a number: a gap is unknown, not zero. */
  gap: LaneGap;
  /** What the cells measure; every label, legend and readout must use this unit. */
  measure: LaneMeasure;
  cells: LaneCell[];
  /** The peak of the lane's own measure: concurrency, or batches/s when measure is 'batch-rate'. */
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

/** The measure of a lane: an explicit measure wins, else the 'blackbox' reason code means host batch throughput. */
export function measureOf(lane: { measure?: LaneMeasure; reasonCode?: string } | null | undefined): LaneMeasure {
  if (lane?.measure === 'batch-rate' || lane?.reasonCode === 'blackbox') return 'batch-rate';
  return 'at-once';
}

/** The one number a cell draws for its lane's measure. Unknown stays null, never 0. */
export function cellValue(cell: LaneCell, measure: LaneMeasure): number | null {
  return measure === 'batch-rate' ? cell.rate : cell.atOnce;
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
  const measure = measureOf(lane);
  const cells: LaneCell[] = [];
  for (let start = alignedStart(fromMs); start < toMs; start += stepMs) {
    const bucket = byStart.get(start);
    let value: number | null = null;
    let rate: number | null = null;
    let p95: number | null = null;
    let refused: number | null = null;
    if (bucket && read && bucket.complete !== false) {
      if (measure === 'batch-rate') {
        // Recorded batch counts are throughput: batches per second. A count is never busy time,
        // so concurrency stays unknown unless the source measured busy time of its own.
        // A clipped edge bucket was cut to the asked window, so its count covers only start..end:
        // divide by those covered seconds, not the full step (3,000 batches over 30 s of a 60 s
        // bucket is 100/s, not 50/s). A missing or unreadable end keeps the full step as the
        // divisor, so a bucket without edges reads exactly as before — never NaN or Infinity.
        if (finite(bucket.count) && bucket.count >= 0) {
          let seconds = win.bucketSeconds;
          if (bucket.clipped === true && typeof bucket.end === 'string') {
            const covered = (Date.parse(bucket.end) - Date.parse(bucket.start)) / 1000;
            if (finite(covered) && covered > 0) seconds = covered;
          }
          rate = bucket.count / seconds;
        }
        // Deliberate PERF-3 scope limit: the busy-time (at-once) path keeps the full step divisor.
        if (finite(bucket.busyMs)) value = atOnce(bucket.busyMs, win.bucketSeconds);
      } else {
        if (finite(bucket.busyMs)) value = atOnce(bucket.busyMs, win.bucketSeconds);
        else if (bucket.count === 0) value = 0; // the source was read and saw nothing
      }
      p95 = finite(bucket.p95Ms) ? bucket.p95Ms : null;
      if (finite(bucket.clientErrors) || finite(bucket.errors)) {
        refused = (finite(bucket.clientErrors) ? bucket.clientErrors : 0) + (finite(bucket.errors) ? bucket.errors : 0);
      }
    }
    cells.push({ start, end: start + stepMs, atOnce: value, rate, p95Ms: p95, refused });
  }
  return cells;
}

/**
 * Re-averages the cells inside each block (blocks start on whole multiples of the block length).
 * Coverage is independent of display averaging: a block's average is spread only over the cells that
 * hold a number, and only those cells receive it. A null (unknown or incomplete) cell stays null —
 * one known cell never turns its unknown neighbours into zeros (W13-02).
 */
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
  const values = new Map<number, { atOnce: number | null; rate: number | null }>();
  for (const [key, group] of groups) {
    let busy = 0;
    let covered = 0;
    let batches = 0;
    let rateCovered = 0;
    for (const cell of group) {
      const span = cell.end - cell.start;
      if (cell.atOnce !== null) {
        busy += cell.atOnce * span;
        covered += span;
      }
      if (cell.rate !== null && cell.rate !== undefined) {
        batches += cell.rate * span;
        rateCovered += span;
      }
    }
    values.set(key, {
      atOnce: covered > 0 ? busy / covered : null,
      rate: rateCovered > 0 ? batches / rateCovered : null
    });
  }
  return cells.map((cell) => {
    const value = values.get(Math.floor(cell.start / blockMs)) ?? { atOnce: null, rate: null };
    return {
      ...cell,
      atOnce: cell.atOnce === null ? null : value.atOnce,
      rate: cell.rate === null || cell.rate === undefined ? null : value.rate
    };
  });
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
    if (lane.reasonCode === 'not-imported') return 'not-imported';
    if (lane.reasonCode === 'import-skipped') return 'import-skipped';
  }
  if (lane.state === 'no-data') return 'no-file';
  if (lane.state === 'incomplete') return 'read-failed';
  return 'not-recorded';
}

/** The short words for a lane gap (copy lives in drillCopy). */
export function laneGapText(gap: LaneGap | undefined): string {
  if (gap === 'not-imported') return DRILL_TEXT.laneNotImported;
  if (gap === 'import-skipped') return DRILL_TEXT.laneImportSkipped;
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
  const measure = measureOf(source);
  let cells = laneCells(source, win, fromMs, toMs);
  const blockSeconds = finite(source.blockSeconds) && source.blockSeconds > 0 ? source.blockSeconds : null;
  // An imported Query Store file lane arrives already spread over the minutes each interval ran (BL-037), so it is not
  // averaged back into UTC-aligned blocks here; blockSeconds stays only for the block-length wording.
  if (key === 'sql' && blockSeconds !== null && blockSeconds > win.bucketSeconds && source.reasonCode !== 'qs-file') cells = regroupBlocks(cells, blockSeconds);

  let peak: CallLane['peak'] = null;
  for (const cell of cells) {
    const value = cellValue(cell, measure);
    if (value !== null && (peak === null || value > peak.atOnce)) peak = { atOnce: value, start: cell.start };
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
    measure,
    cells,
    peak,
    bars,
    moreBars: more,
    blockSeconds,
    hasShape: cells.some((c) => cellValue(c, measure) !== null)
  };
}

/** One decimal under 10 ('4.2'), a whole number from 10 up, '0' for 0. */
export function formatAtOnce(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '0';
  const tenth = Math.round(n * 10) / 10;
  if (tenth < 10) return tenth.toFixed(1);
  return String(Math.round(n));
}

/** A run of one axis, in drawing units. */
export interface PressSpan { a: number; b: number }
/** The box of a bar, or the invisible area around it that answers a press. */
export interface PressArea { x: PressSpan; y: PressSpan }

/**
 * BL-492: the invisible press area of every bar. A bar is only a few pixels tall (the lane height is fixed, Slow call lane rule BL-005), so each bar
 * gets an area of up to `needX` x `needY` drawing units (24 px) around it, inside `minX`..`maxX` and `minY`..`maxY`. The Slow call lanes allow the
 * area to reach into their non-interactive name row above the drawing, never into the lane above or below. Two areas never overlap: where two bars
 * sit close together (stacked bar rows, or bars side by side in one row) the free space between them is split at the middle of the gap between the
 * two bars, and each bar then takes what is left on its other side. A bar with nothing close reaches the full size, a bar in a stack gets what its
 * row allows. No area covers another bar. Two bars drawn over each other split their shared part in the middle, so each keeps a pressable part of
 * its own bar. A bar with no room left (two short bars at the very edge) gets no area and is absent from the result.
 */
export function barPressAreas(
  bars: readonly { key: string; x: PressSpan; y: PressSpan }[],
  needX: number,
  needY: number,
  maxX: number,
  maxY: number,
  minX = 0,
  minY = 0
): Map<string, PressArea> {
  // Grows a span to `need`, evenly on both sides, inside the limits; what one side cannot give is taken from the other.
  const grow = (s: PressSpan, need: number, limit: PressSpan): PressSpan => {
    const extra = Math.max(0, need - (s.b - s.a));
    let a = s.a - extra / 2;
    let b = s.b + extra / 2;
    if (a < limit.a) { b = Math.min(limit.b, b + (limit.a - a)); a = limit.a; }
    if (b > limit.b) { a = Math.max(limit.a, a - (b - limit.b)); b = limit.b; }
    return { a, b };
  };
  const overlap = (p: PressSpan, q: PressSpan): number => Math.min(p.b, q.b) - Math.max(p.a, q.a);
  const room: PressArea[] = bars.map(() => ({ x: { a: minX, b: maxX }, y: { a: minY, b: maxY } }));
  let hit: PressArea[] = [];
  // Each pass grows every area inside its room, then cuts the room of two areas that still touch at the middle of the gap between their bars.
  // Rooms only shrink, so the passes end; the last pass has no touching pair left.
  for (let pass = 0; pass < 8; pass += 1) {
    hit = bars.map((b, i) => ({ x: grow(b.x, needX, room[i].x), y: grow(b.y, needY, room[i].y) }));
    let cut = false;
    for (let i = 0; i < bars.length; i += 1) {
      for (let j = i + 1; j < bars.length; j += 1) {
        const ox = overlap(hit[i].x, hit[j].x);
        const oy = overlap(hit[i].y, hit[j].y);
        if (ox <= 0 || oy <= 0) continue;
        // The bars are apart on x, on y or both: cut along an axis where there is a gap between the bars (the one that loses less when both).
        const gapX = Math.max(bars[j].x.a - bars[i].x.b, bars[i].x.a - bars[j].x.b);
        const gapY = Math.max(bars[j].y.a - bars[i].y.b, bars[i].y.a - bars[j].y.b);
        const byY = gapY >= 0 && (gapX < 0 || oy / needY <= ox / needX);
        const axis = byY ? 'y' : 'x';
        const lo = bars[i][axis].a <= bars[j][axis].a ? i : j;
        const hi = lo === i ? j : i;
        // Two bars drawn over each other (a bar is drawn at least 3 px wide, so short items close in time can overlap on screen): the cut is
        // the middle of the part they share, so each keeps a pressable part of its own bar and neither area covers what only the other bar shows.
        const mid = !byY && gapX < 0
          ? (bars[hi].x.a + Math.min(bars[lo].x.b, bars[hi].x.b)) / 2
          : (bars[lo][axis].b + bars[hi][axis].a) / 2;
        const cutAt = Math.min(axis === 'x' ? maxX : maxY, Math.max(axis === 'x' ? minX : minY, mid)); // two short bars at the very edge: the cut may not lie outside the drawing
        room[lo][axis] = { a: room[lo][axis].a, b: Math.min(room[lo][axis].b, cutAt) };
        room[hi][axis] = { a: Math.max(room[hi][axis].a, cutAt), b: room[hi][axis].b };
        cut = true;
      }
    }
    if (!cut) break;
  }
  // A bar whose area came out empty (the other bar took all of the room up to the edge of the drawing) gets none: its drawn bar is still pressable.
  return new Map(bars.flatMap((b, i): [string, PressArea][] => (hit[i].x.b > hit[i].x.a && hit[i].y.b > hit[i].y.a ? [[b.key, hit[i]]] : [])));
}
