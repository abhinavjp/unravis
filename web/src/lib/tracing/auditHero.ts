/**
 * The audit lane's hero layer (the Run Analysis "Requests over time" look on the Investigation Timeline):
 * a per-slot percentile line, a dashed bridge across slots with no timed requests, a dot per slot and a
 * mark for each slot's slowest request, with the far-above-usual ones flagged. Pure TypeScript, no Svelte
 * and no DOM: the component only positions what this module returns.
 */
import { gapBridge, heroOutliers, type HeroSlot } from '../chartHero';
import { axisDuration } from '../chartScale';
import { percentileValue, type Percentile, type ZoomRange } from './canvas';
import type { TracingBucket } from './model';
import type { SlotGrid } from './lanes';

/** Every fixed sentence or label the hero layer can put on screen, in one place for the copy lint. */
export const HERO_TEXT = {
  legendRequests: 'Requests per interval',
  legendBridge: 'No timed requests between',
  legendSlowest: 'Slowest request in the interval',
  legendWebRequests: 'Web-server requests per interval',
  legendRefused: 'Refused or not found (4xx)',
  legendFailed: 'Failed (5xx)',
  tipNoTiming: 'No timed requests',
  tipSlowest: 'Slowest request:',
  tipOutlier: 'far above the usual P95',
  // The SQL lane (saved Query Store figures): its line is the average time, never a percentile.
  legendSqlStatements: 'Statements run per interval',
  legendSqlLine: 'Slowest statement',
  legendSqlSlowest: 'Far slower than usual',
  tipSqlAverage: 'Average time:',
  tipSqlSlowest: 'Slowest statement:',
  tipSqlOutlier: 'far above the usual',
  sqlOutlierOne: (top: string): string =>
    `One red diamond: a statement far slower than usual, up to ${top}. It shows when, not which statement or why.`,
  sqlOutlierMany: (count: number, top: string): string =>
    `${count} red diamonds: statements far slower than usual, up to ${top}. They show when, not which statement or why.`,
  empty: 'No timed requests in this view.',
  webRefused: (refused: number, total: number): string =>
    `${refused.toLocaleString('en-GB')} of ${total.toLocaleString('en-GB')} web-server requests in this view were refused or not found (4xx), the amber part of each bar.`,
  outlierOne: (top: string): string =>
    `One red diamond: a request far slower than usual, up to ${top}. It shows when, not why.`,
  outlierMany: (count: number, top: string): string =>
    `${count} red diamonds: requests far slower than usual, up to ${top}. They show when, not why.`
} as const;

/** One point of the hero layer, placed by percentage of the plot box. `index` is the absolute slot index. */
export interface HeroDot { index: number; leftPct: number; bottomPct: number; ms: number }

/** A slot's slowest request: hollow dot, or a flagged diamond; `text` is the printed value for a labelled mark, '' otherwise. */
export interface HeroMark extends HeroDot { flagged: boolean; labelled: boolean; text: string }

export interface HeroLayout {
  /** The right-axis top: a nice ceiling over every latency and slowest-request value; 0 when there is nothing to draw. */
  maxMs: number;
  /** The three right-axis ticks: bottom, middle, top. */
  ticks: Array<{ ms: number; bottomPct: number }>;
  /** Solid percentile line: runs of "x,y" points in a 0..100 box, one run per stretch of slots with a value. */
  line: string[];
  /** One dot per slot with a percentile value. */
  dots: HeroDot[];
  /** The dashed bridge over slots with no timed requests, in the same point format as `line`. */
  bridge: string[];
  /** One mark per slot with a slowest-request value. */
  marks: HeroMark[];
  flaggedCount: number;
  /** The largest flagged slowest request; null when nothing is flagged. */
  topFlaggedMs: number | null;
  /** The median slot latency: what a usual slot looks like in this view. */
  typical: number | null;
}

const emptyLayout = (): HeroLayout => ({ maxMs: 0, ticks: [], line: [], dots: [], bridge: [], marks: [], flaggedCount: 0, topFlaggedMs: null, typical: null });

/** The smallest 1, 2, 2.5 or 5 times a power of ten that is at least `value`; 0 for no positive value. */
function niceCeil(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  const base = Math.pow(10, Math.floor(Math.log10(value)));
  for (const multiplier of [1, 2, 2.5, 5, 10]) {
    const candidate = multiplier * base;
    if (candidate >= value) return candidate;
  }
  return 10 * base;
}

/** A bucket's slowest request when it is a finite number >= 0; null when it is missing or bad. */
function maxValue(bucket: TracingBucket): number | null {
  const value = bucket.maxMs;
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

/**
 * The hero layer for the visible `range`: one HeroSlot per visible slot (buckets that share a slot keep the
 * larger latency and slowest value and add their counts), the line and bridge as "x,y" runs in a 0..100 box
 * (x centred on the slot, y measured down from the axis top), and one mark per slot with a slowest request.
 */
export function heroLayout(
  buckets: TracingBucket[],
  grid: Pick<SlotGrid, 'firstMs' | 'widthMs'>,
  range: ZoomRange,
  percentile: Percentile
): HeroLayout {
  const shown = range.to - range.from;
  if (shown < 1) return emptyLayout();
  const slots: HeroSlot[] = Array.from({ length: shown }, (_, k) => ({ at: grid.firstMs + (range.from + k) * grid.widthMs, count: 0, latency: null, max: null }));
  for (const bucket of buckets) {
    const startMs = Date.parse(bucket.start);
    if (!Number.isFinite(startMs)) continue;
    const index = Math.floor((startMs - grid.firstMs) / grid.widthMs);
    if (index < range.from || index >= range.to) continue;
    const slot = slots[index - range.from];
    slot.count += bucket.count;
    const latency = percentileValue(bucket, percentile);
    if (latency !== null) slot.latency = Math.max(slot.latency ?? 0, latency);
    const max = maxValue(bucket);
    if (max !== null) slot.max = Math.max(slot.max ?? 0, max);
  }
  const maxMs = niceCeil(Math.max(0, ...slots.flatMap((slot) => [slot.latency ?? 0, slot.max ?? 0])));
  if (maxMs === 0) return emptyLayout();
  const widthPct = 100 / shown;
  const xOf = (k: number): number => k * widthPct + widthPct / 2;
  const yOf = (value: number): number => 100 - (value / maxMs) * 100;
  const point = (k: number, value: number): string => xOf(k).toFixed(2) + ',' + yOf(value).toFixed(2);

  const runsOf = (values: Array<number | null>): string[] => {
    const runs: string[] = [];
    let run: string[] = [];
    for (let k = 0; k < shown; k++) {
      const value = values[k];
      if (value === null) {
        if (run.length > 0) runs.push(run.join(' '));
        run = [];
        continue;
      }
      run.push(point(k, value));
    }
    if (run.length > 0) runs.push(run.join(' '));
    return runs;
  };

  const latencyValues = slots.map((slot) => slot.latency);
  const line = runsOf(latencyValues);
  const bridge = runsOf(gapBridge(latencyValues));
  const dots: HeroDot[] = slots.flatMap((slot, k) =>
    slot.latency === null ? [] : [{ index: range.from + k, leftPct: xOf(k), bottomPct: (slot.latency / maxMs) * 100, ms: slot.latency }]);

  // Only the single worst diamond carries a number; the rest are read from the axis, so labels never pile up.
  const outliers = heroOutliers(slots, 1);
  const flagged = new Set(outliers.flagged);
  const labelled = new Set(outliers.labelled);
  const marks: HeroMark[] = slots.flatMap((slot, k) => {
    if (slot.max == null) return [];
    const isLabelled = labelled.has(k);
    return [{ index: range.from + k, leftPct: xOf(k), bottomPct: (slot.max / maxMs) * 100, ms: slot.max, flagged: flagged.has(k), labelled: isLabelled, text: isLabelled ? axisDuration(slot.max) : '' }];
  });
  const topFlaggedMs = outliers.flagged.length > 0 ? Math.max(...outliers.flagged.map((k) => slots[k].max as number)) : null;
  return {
    maxMs,
    ticks: [{ ms: 0, bottomPct: 0 }, { ms: maxMs / 2, bottomPct: 50 }, { ms: maxMs, bottomPct: 100 }],
    line,
    dots,
    bridge,
    marks,
    flaggedCount: outliers.flagged.length,
    topFlaggedMs,
    typical: outliers.typical
  };
}

/** The same sentence for the SQL lane, which speaks of statements. */
export function heroSqlSentence(layout: HeroLayout): string {
  if (layout.flaggedCount === 0 || layout.topFlaggedMs === null) return '';
  const top = axisDuration(layout.topFlaggedMs);
  return layout.flaggedCount === 1 ? HERO_TEXT.sqlOutlierOne(top) : HERO_TEXT.sqlOutlierMany(layout.flaggedCount, top);
}

/** The sentence under the chart about the flagged diamonds; '' when nothing is flagged. */
export function heroSentence(layout: HeroLayout): string {
  if (layout.flaggedCount === 0 || layout.topFlaggedMs === null) return '';
  const top = axisDuration(layout.topFlaggedMs);
  return layout.flaggedCount === 1 ? HERO_TEXT.outlierOne(top) : HERO_TEXT.outlierMany(layout.flaggedCount, top);
}
