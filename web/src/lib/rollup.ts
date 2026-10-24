import type { Query, Quantile, SourceResult, Provenance } from './types';
import { bucketForWindows, formatTimeRange } from './time';

/*
 * The daily table and the hour grid (GET /api/daily and GET /api/hourgrid). The server cuts days and hours in one
 * time zone, so a day is 23 or 25 hours long when the clocks change, and it says which days the window cuts.
 * This file holds the types, the request path, and the pure helpers: no fetch, no Svelte, so tests can load it.
 */

export type RollupTrend = 'up' | 'down' | 'flat' | '';

export interface RollupDay {
  date: string; start: string; end: string; hours: number; partial: boolean;
  total: number; sync: number; background: number; other: number; latencyCount: number;
  p50: Quantile | null; p95: Quantile | null; count4xx: number; count5xx: number;
  lowTraffic: boolean; vsPrev: RollupTrend; vsPrevPct: number | null; vsPrevDate?: string;
}
export interface HourCell {
  date: string; hour: number; start: string; end: string; count: number; latencyCount: number;
  p95: Quantile | null; slowCount: number; noise: boolean;
}
export interface HourGridDay { date: string; hours: number; partial: boolean }
interface RollupView { from: string; to: string; zone: string; slowMs: number; sources: SourceResult[]; provenance: Provenance[] }
export interface DailyResponse extends RollupView { lowTrafficBelow: number; flatPercent: number; days: RollupDay[] }
export interface HourGridResponse extends RollupView { noiseBelow: number; days: HourGridDay[]; cells: HourCell[] }
/** A cell turned into a window: the UTC start and end of the requests it counts. */
export interface RollupWindow { start: string; end: string }
export interface RollupParams { from: string; to: string; tz: string; sourceIds: string[]; slowMs?: number }

export const MAX_ROLLUP_DAYS = 120;
export const HOURS_PER_ROW = 24;
const DAY_MS = 86_400_000;

export interface RollupRange { id: string; label: string; days: number }
/** The spans the view offers. Each ends now. Over 24 hours a live source may refuse (the server says so). */
export const rollupRanges: RollupRange[] = [
  { id: '1d', label: 'Last 24 hours', days: 1 },
  { id: '7d', label: 'Last 7 days', days: 7 },
  { id: '30d', label: 'Last 30 days', days: 30 },
  { id: '60d', label: 'Last 60 days', days: 60 },
  { id: '90d', label: 'Last 90 days', days: 90 },
  { id: '120d', label: 'Last 120 days', days: MAX_ROLLUP_DAYS }
];

/** The request parameters for a span that ends at `now`. A span is never longer than the server accepts. */
export function rollupParams(range: RollupRange, now: Date, zone: string, sourceIds: string[]): RollupParams {
  const days = Math.min(MAX_ROLLUP_DAYS, Math.max(1, range.days));
  return { from: new Date(now.getTime() - days * DAY_MS).toISOString(), to: now.toISOString(), tz: zone, sourceIds };
}

export function rollupPath(kind: 'daily' | 'hourgrid', params: RollupParams): string {
  const query = new URLSearchParams({ from: params.from, to: params.to, tz: params.tz });
  if (params.sourceIds.length) query.set('sources', params.sourceIds.join(','));
  if (params.slowMs !== undefined && Number.isFinite(params.slowMs)) query.set('slowMs', String(params.slowMs));
  return '/' + kind + '?' + query.toString();
}

// ---- Words ----

/** Every fixed sentence of the view, in one place so the copy lint can read them. */
export const rollupCopy = {
  openLabel: 'Daily and hourly view',
  intro: 'See each day on its own row, and each hour of each day as one square. This is a separate read of the sources, over the span you choose.',
  lowTraffic: 'Grey days have fewer than 100 requests. A grey day is never the earlier day in a comparison.',
  trendRule: 'Each arrow compares a day with the last earlier day that had at least 100 requests. A change under 10 percent counts as about the same.',
  partial: 'A day marked partial is cut by the span, so its counts are smaller than a full day.',
  timings: 'P50 and P95 use finished synchronous requests only. A day needs at least 20 of them to show a time.',
  gridHow: 'Each square is one hour. Darker means a higher value. Choose a square to set the window to that hour.',
  gridNoise: 'A dashed square has fewer than 5 requests, so its shade is a rough guide only.',
  gridEmpty: 'A blank square had no requests.',
  gridKeys: 'Use the arrow keys to move between squares, and Enter to choose one.',
  noDays: 'No days to show. No source returned data for this span.',
  refusedHelp: 'You can choose a shorter span.',
  csvButton: 'Download CSV',
  csvName: 'daily-view.csv',
  error: 'The daily and hourly view could not be read.'
};

export const zoneNote = (zone: string): string => 'Days and hours follow ' + (zone === 'UTC' ? 'UTC' : zone + ' time') + '.';

export const slowNote = (slowMs: number): string => 'Slow means ' + Number(slowMs).toLocaleString('en-US') + ' ms or more.';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
/** "2026-10-25" as "25 Oct". The server date is already a local date, so no zone maths is done here. */
export function dayLabel(date: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!match) return date;
  return Number(match[3]) + ' ' + (MONTHS[Number(match[2]) - 1] ?? '');
}

export const whole = (value: number): string => value.toLocaleString('en-US');

/** A time from the server: "≈ 120 ms", or a dash when the day or hour had too few timings. */
export function latencyText(quantile: Quantile | null | undefined): string {
  return quantile?.valueMs == null ? '—' : '≈ ' + Math.round(quantile.valueMs).toLocaleString('en-US') + ' ms';
}

export interface TrendView { kind: RollupTrend | 'none'; arrow: string; text: string }

/** The arrow and the words of a day. A grey or partial day has no arrow, and says why. */
export function trendView(day: RollupDay): TrendView {
  if (day.lowTraffic) return { kind: 'none', arrow: '', text: 'Not compared: fewer than 100 requests' };
  if (day.partial) return { kind: 'none', arrow: '', text: 'Not compared: the span cuts this day' };
  if (!day.vsPrev || day.vsPrevPct == null) return { kind: 'none', arrow: '', text: 'No earlier day to compare with' };
  const percent = Math.round(Math.abs(day.vsPrevPct));
  const since = day.vsPrevDate ? ' than ' + dayLabel(day.vsPrevDate) : '';
  if (day.vsPrev === 'up') return { kind: 'up', arrow: '▲', text: percent + '% more requests' + since };
  if (day.vsPrev === 'down') return { kind: 'down', arrow: '▼', text: percent + '% fewer requests' + since };
  return { kind: 'flat', arrow: '▬', text: 'About the same' + since.replace(' than ', ' as ') };
}

/** "23 h day" or "25 h day" when the clocks changed; nothing for a normal day. */
export function dayLengthNote(day: { hours: number }): string {
  if (day.hours === 24) return '';
  const text = Number.isInteger(day.hours) ? String(day.hours) : day.hours.toFixed(1);
  return text + ' h day';
}

// ---- CSV ----

const FORMULA_START = /^[=+\-@\t\r]/;
// Characters that show nothing, so a formula can hide behind them.
const INVISIBLE = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f­͏؜ᅟᅠ឴឵᠋-᠎​-‏‪-‮⁠-⁯ㅤ︀-️﻿ﾠ]/;

/**
 * One value made safe for a spreadsheet: a value that starts with = + - @, a tab or a carriage return (also behind
 * spaces, hidden characters, or in full-width form) gets a leading apostrophe. It matches the server's csvsafe rule.
 */
export function csvSafe(value: string): string {
  if (!value || value.startsWith("'")) return value;
  if (FORMULA_START.test(value)) return "'" + value;
  const head = Array.from(value.normalize('NFKC').slice(0, 256));
  for (const ch of head) {
    if (FORMULA_START.test(ch)) return "'" + value;
    if (/\s/.test(ch) || INVISIBLE.test(ch)) continue;
    return value;
  }
  return value;
}

/** One CSV field: made safe, then quoted when it holds a comma, a quote or a line break. */
export function csvCell(value: string | number | boolean | null | undefined): string {
  // A finite number is a figure the table made, never text from a source, so it stays a number in the sheet.
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  const text = csvSafe(value === null || value === undefined ? '' : String(value));
  return /[",\r\n]/.test(text) ? '"' + text.replace(/"/g, '""') + '"' : text;
}

export const dailyCsvHeader = [
  'Date', 'Hours in day', 'Partial day', 'Requests', 'Synchronous', 'Background', 'Other', '4xx', '5xx',
  'P50 ms', 'P95 ms', 'Fewer than 100 requests', 'Compared with an earlier day', 'Compared with'
];

/** The daily table as CSV text, one line per day, with CRLF line ends. The header names the zone the days follow. */
export function dailyCsv(days: RollupDay[], zone: string): string {
  const ms = (quantile: Quantile | null) => quantile?.valueMs == null ? '' : String(Math.round(quantile.valueMs));
  const header = dailyCsvHeader.map((name, index) => index === 0 ? name + ' (' + zone + ')' : name);
  const lines = [header.map(csvCell).join(',')];
  for (const day of days) {
    const trend = trendView(day);
    lines.push([
      day.date, day.hours, day.partial ? 'yes' : 'no', day.total, day.sync, day.background, day.other, day.count4xx, day.count5xx,
      ms(day.p50), ms(day.p95), day.lowTraffic ? 'yes' : 'no', trend.kind === 'none' ? '' : trend.text, day.vsPrevDate ?? ''
    ].map(csvCell).join(','));
  }
  return lines.join('\r\n') + '\r\n';
}

// ---- The hour grid ----

export type GridMetric = 'p95' | 'slow';
export const gridMetrics: { id: GridMetric; label: string }[] = [
  { id: 'p95', label: 'P95 time' },
  { id: 'slow', label: 'Share of slow requests' }
];

/** The value a cell is shaded by, or null when it has too few requests to say. */
export function cellValue(cell: HourCell, metric: GridMetric): number | null {
  if (cell.noise || cell.count <= 0) return null;
  if (metric === 'p95') return cell.p95?.valueMs ?? null;
  return cell.latencyCount > 0 ? cell.slowCount / cell.latencyCount : null;
}

/** The largest shaded value in the grid; the darkest square. Zero when nothing can be shaded. */
export function gridMax(cells: HourCell[], metric: GridMetric): number {
  let max = 0;
  for (const cell of cells) max = Math.max(max, cellValue(cell, metric) ?? 0);
  return max;
}

/** 0 for no shade, then 1 to 4 (the four heat colours of the app). */
export function shadeLevel(value: number | null, max: number): 0 | 1 | 2 | 3 | 4 {
  if (value == null || !(max > 0) || !(value > 0)) return 0;
  const level = Math.ceil((value / max) * 4);
  return Math.min(4, Math.max(1, level)) as 1 | 2 | 3 | 4;
}

export interface GridRow { date: string; hours: number; partial: boolean; cells: (HourCell | null)[] }

/**
 * The grid in rows of 24. A day with 23 hours has one empty square; a repeated hour is one cell on its first
 * position. A cell the response holds for a day it does not list still gets its own row.
 */
export function gridRows(grid: Pick<HourGridResponse, 'days' | 'cells'>): GridRow[] {
  const rows = new Map<string, GridRow>();
  for (const day of grid.days) rows.set(day.date, { date: day.date, hours: day.hours, partial: day.partial, cells: Array<HourCell | null>(HOURS_PER_ROW).fill(null) });
  for (const cell of grid.cells) {
    let row = rows.get(cell.date);
    if (!row) { row = { date: cell.date, hours: 24, partial: false, cells: Array<HourCell | null>(HOURS_PER_ROW).fill(null) }; rows.set(cell.date, row); }
    if (Number.isInteger(cell.hour) && cell.hour >= 0 && cell.hour < HOURS_PER_ROW) row.cells[cell.hour] = cell;
  }
  return [...rows.values()].sort((a, b) => a.date < b.date ? -1 : a.date > b.date ? 1 : 0);
}

/** The window a cell selects, or null for a square with no requests: there is nothing to look at in it. */
export function cellWindow(cell: HourCell | null | undefined): RollupWindow | null {
  if (!cell || cell.count <= 0) return null;
  const start = Date.parse(cell.start), end = Date.parse(cell.end);
  if (!Number.isFinite(start) || !Number.isFinite(end) || !(start < end)) return null;
  return { start: cell.start, end: cell.end };
}

/**
 * The query fields for a chosen hour: the window itself, and the reference window of the same length right
 * before it, with a bucket size that fits. It is what the quick ranges set, so the usual checks still hold.
 */
export function hourWindowQuery(window: RollupWindow): Pick<Query, 'start' | 'end' | 'baselineStart' | 'baselineEnd' | 'bucketSeconds'> {
  const start = Date.parse(window.start), end = Date.parse(window.end);
  if (!Number.isFinite(start) || !Number.isFinite(end) || !(start < end)) throw new RangeError('The hour has no valid start and end.');
  const baselineStart = new Date(start - (end - start)).toISOString();
  const range = { start: new Date(start).toISOString(), end: new Date(end).toISOString(), baselineStart, baselineEnd: new Date(start).toISOString() };
  return { ...range, bucketSeconds: bucketForWindows(range.start, range.end, range.baselineStart, range.baselineEnd) };
}

/** The tooltip and the screen reader name of a square. */
export function cellLabel(cell: HourCell | null, zone: string, slowMs: number): string {
  if (!cell) return 'No such hour on this day';
  const when = formatTimeRange(cell.start, cell.end, { zone, precision: 'minute' });
  if (cell.count <= 0) return when + ': no requests';
  const parts = [whole(cell.count) + (cell.count === 1 ? ' request' : ' requests')];
  if (cell.noise) parts.push('too few to trust the shade');
  else {
    parts.push('P95 ' + latencyText(cell.p95));
    parts.push(whole(cell.slowCount) + ' slow (' + slowMs.toLocaleString('en-US') + ' ms or more)');
  }
  return when + ': ' + parts.join(', ');
}

/** Moves one square in the grid, by arrow key, over squares that exist. Returns the same spot when there is none. */
export function moveInGrid(rows: GridRow[], row: number, col: number, key: string): [number, number] {
  const step: Record<string, [number, number]> = { ArrowLeft: [0, -1], ArrowRight: [0, 1], ArrowUp: [-1, 0], ArrowDown: [1, 0] };
  if (key === 'Home') return [row, 0];
  if (key === 'End') return [row, HOURS_PER_ROW - 1];
  const delta = step[key];
  if (!delta) return [row, col];
  let r = row, c = col;
  for (;;) {
    r += delta[0]; c += delta[1];
    if (r < 0 || r >= rows.length || c < 0 || c >= HOURS_PER_ROW) return [row, col];
    if (rows[r].cells[c]) return [r, c];
    // Sideways moves skip an empty square; vertical moves stop at one so the column stays put.
    if (delta[0] !== 0) return [row, col];
  }
}
