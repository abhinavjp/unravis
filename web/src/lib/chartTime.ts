/**
 * Every timestamp a chart draws goes through this file. When the shared formatter in lib/time.ts (formatTime) lands, swap the body of
 * chartTime here and nothing else changes: charts, tooltips and the text under them all call chartTime / chartRange.
 */
import { zoneAbbreviation } from './time';
export interface ChartTimeOptions { /** Print the day and month. Default true. */ date?: boolean; /** Print seconds. Default true. */ seconds?: boolean; /** Print the zone after the time. Default true. */ zone?: boolean; /** Print the clock. Default true; false gives the day only. */ time?: boolean }

const formatters = new Map<string, Intl.DateTimeFormat>();
const partsFormatter = (zone: string, seconds: boolean) => {
  const key = zone + '|' + seconds;
  let formatter = formatters.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-GB', { timeZone: zone, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', ...(seconds ? { second: '2-digit' } : {}), hourCycle: 'h23' });
    formatters.set(key, formatter);
  }
  return formatter;
};
const toMs = (value: number | string | Date) => typeof value === 'number' ? value : value instanceof Date ? value.getTime() : Date.parse(value);

/** "24 Oct 15:19:37 UTC": day and month, 24 h clock, zone suffix. */
export function chartTime(value: number | string | Date, zone: string, options: ChartTimeOptions = {}): string {
  const ms = toMs(value);
  if (!Number.isFinite(ms)) return 'Unknown time';
  const { date = true, seconds = true, zone: withZone = true, time = true } = options;
  const part = Object.fromEntries(partsFormatter(zone, seconds).formatToParts(new Date(ms)).map(item => [item.type, item.value]));
  const clock = part.hour + ':' + part.minute + (seconds ? ':' + part.second : '');
  const label = withZone ? ' ' + zoneAbbreviation(zone, ms) : '';
  if (!time) return part.day + ' ' + part.month + label;
  return (date ? part.day + ' ' + part.month + ' ' : '') + clock + label;
}

const dayKey = (ms: number, zone: string) => { const p = partsFormatter(zone, false).formatToParts(new Date(ms)); return p.find(i => i.type === 'day')?.value + ' ' + p.find(i => i.type === 'month')?.value; };

/** One range with the zone printed once: "24 Oct 15:04:37 to 15:19:37 UTC". A range whose ends are the same instant reads as "at 24 Oct 15:05:37 UTC". */
export function chartRange(start: number | string | Date, end: number | string | Date, zone: string, options: Pick<ChartTimeOptions, 'seconds'> = {}): string {
  const a = toMs(start), b = toMs(end);
  if (!Number.isFinite(a) || !Number.isFinite(b)) return 'Unknown time range';
  if (a === b) return 'at ' + chartTime(a, zone, options);
  const sameDay = dayKey(a, zone) === dayKey(b, zone);
  return chartTime(a, zone, { ...options, zone: false }) + ' to ' + chartTime(b, zone, { ...options, date: !sameDay, zone: false }) + ' ' + zoneAbbreviation(zone, a);
}

const SECOND = 1000, MINUTE = 60 * SECOND, HOUR = 60 * MINUTE, DAY = 24 * HOUR;
/** Round steps an axis label may land on, shortest first. */
export const tickLadderMs = [10 * SECOND, 30 * SECOND, MINUTE, 2 * MINUTE, 5 * MINUTE, 10 * MINUTE, 15 * MINUTE, 30 * MINUTE, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY];

/** The smallest round step that keeps the label count at or under maxLabels for a span. */
export function pickTickStep(spanMs: number, maxLabels = 7): number {
  for (const step of tickLadderMs) if (spanMs / step <= maxLabels) return step;
  return tickLadderMs[tickLadderMs.length - 1];
}

const offsetFormatters = new Map<string, Intl.DateTimeFormat>();
/** The zone's offset from UTC in milliseconds at an instant (positive east of UTC). */
export function zoneOffsetMs(ms: number, zone: string): number {
  let formatter = offsetFormatters.get(zone);
  if (!formatter) { formatter = new Intl.DateTimeFormat('en-US', { timeZone: zone, year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric', hourCycle: 'h23' }); offsetFormatters.set(zone, formatter); }
  const p = Object.fromEntries(formatter.formatToParts(new Date(ms)).map(item => [item.type, item.value]));
  return Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second) - Math.floor(ms / 1000) * 1000;
}

/** Round clock times (in the display zone) inside [startMs, endMs], stepped by stepMs. */
export function roundTicks(startMs: number, endMs: number, stepMs: number, zone: string): number[] {
  const ticks: number[] = [];
  const offset = zoneOffsetMs(startMs, zone);
  for (let local = Math.ceil((startMs + offset) / stepMs) * stepMs; ticks.length < 1000; local += stepMs) {
    const at = local - zoneOffsetMs(local - offset, zone);
    if (at > endMs) break;
    if (at >= startMs) ticks.push(at);
  }
  return ticks;
}

/**
 * One label per chart slot for a category time axis: an empty string except on the slot that contains a round time, which carries that
 * round time ("15:30"). The first label and the first one after midnight also carry the day ("24 Oct 15:30"); steps of a day or more show only the day.
 * slotStarts are the start of each slot in ms, in order; slots are as wide as the smallest gap between neighbours.
 */
export function axisLabels(slotStarts: number[], zone: string, maxLabels = 7): string[] {
  const labels = slotStarts.map(() => '');
  if (!slotStarts.length) return labels;
  if (slotStarts.length < 2) { labels[0] = chartTime(slotStarts[0], zone, { seconds: false, zone: false }); return labels; }
  let width = Infinity;
  for (let i = 1; i < slotStarts.length; i++) { const gap = slotStarts[i] - slotStarts[i - 1]; if (gap > 0 && gap < width) width = gap; }
  if (!Number.isFinite(width)) return labels;
  const first = slotStarts[0], end = slotStarts[slotStarts.length - 1] + width;
  const step = pickTickStep(end - first, maxLabels);
  let previousDay = '';
  let firstLabel = true;
  for (const tick of roundTicks(first, end, step, zone)) {
    const index = Math.min(slotStarts.length - 1, Math.max(0, Math.floor((tick - first) / width)));
    if (labels[index] !== '') continue;
    const day = dayKey(tick, zone);
    const withDate = step >= DAY || firstLabel || day !== previousDay;
    labels[index] = step >= DAY ? chartTime(tick, zone, { time: false, zone: false }) : chartTime(tick, zone, { date: withDate, seconds: step < MINUTE, zone: false });
    previousDay = day; firstLabel = false;
  }
  // A range too short for any round time still needs one anchor.
  if (firstLabel) labels[0] = chartTime(first, zone, { seconds: width < MINUTE, zone: false });
  return labels;
}
