import type { Query } from './types';

export const toLocalInput = (date: Date) => {
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 23);
};
export const fromLocalInput = (value: string) => new Date(value).toISOString();
export const toLocalDatePart = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
export const toLocalTimePart = (date: Date) => `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}:${String(date.getSeconds()).padStart(2, '0')}.${String(date.getMilliseconds()).padStart(3, '0')}`;
export const fromLocalParts = (datePart: string, timePart: string) => {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(datePart) || !/^\d{2}:\d{2}(?::\d{2}(?:\.\d{1,3})?)?$/.test(timePart)) throw new RangeError('Enter a valid date and time.');
  const normalizedTime = timePart.length === 5 ? `${timePart}:00.000` : timePart.includes('.') ? `${timePart.slice(0, timePart.indexOf('.') + 1)}${timePart.slice(timePart.indexOf('.') + 1).padEnd(3, '0')}` : `${timePart}.000`;
  const parsed = new Date(`${datePart}T${normalizedTime}`);
  if (!Number.isFinite(parsed.getTime()) || toLocalDatePart(parsed) !== datePart || toLocalTimePart(parsed) !== normalizedTime) {
    throw new RangeError('That local time is not valid on this date, possibly because the clocks change. Choose another time.');
  }
  return parsed.toISOString();
};
export const utcPreview = (value: string) => value ? new Date(value).toISOString() : 'Choose a time';
export const formatLocal = (value: string, zone: string, options: Intl.DateTimeFormatOptions = {}) =>
  new Intl.DateTimeFormat(undefined, { timeZone: zone, ...options }).format(new Date(value));
export const zones = (() => {
  try { return ['UTC', ...Intl.supportedValuesOf('timeZone').filter(zone => zone !== 'UTC')]; }
  catch { return ['UTC', 'Asia/Dubai', 'Europe/London', 'America/New_York']; }
})();
export const defaultQuery = (now: Date, sourceIds: string[]): Query => {
  const end = now;
  const start = new Date(end.getTime() - 15 * 60_000);
  const baselineEnd = start;
  const baselineStart = new Date(baselineEnd.getTime() - 15 * 60_000);
  return {
    sourceIds, start: start.toISOString(), end: end.toISOString(),
    baselineStart: baselineStart.toISOString(), baselineEnd: baselineEnd.toISOString(),
    kind: 'synchronous', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: ''
  };
};
export const countEpochBuckets = (start: string | number, end: string | number, bucketSeconds: number) => {
  const startMs = typeof start === 'number' ? start : Date.parse(start);
  const endMs = typeof end === 'number' ? end : Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs >= endMs || bucketSeconds <= 0) return 0;
  const width = bucketSeconds * 1000;
  return Math.ceil(endMs / width) - Math.floor(startMs / width);
};
export const bucketForWindows = (start: string, end: string, baselineStart: string, baselineEnd: string) => {
  let width = Math.max(10, Math.ceil(Math.max(Date.parse(end) - Date.parse(start), Date.parse(baselineEnd) - Date.parse(baselineStart)) / 1000 / 120));
  while (width < 86400 && (countEpochBuckets(start, end, width) > 120 || countEpochBuckets(baselineStart, baselineEnd, width) > 120)) width++;
  return Math.min(86400, width);
};

// Quick range presets: the investigated window ends at "now" and the reference
// is the equal-length window right before it. Nothing is longer than the 7 day
// cap the server enforces per window.
export type QuickRangeId = '15m' | '1h' | '4h' | '24h' | '7d';
export const quickRanges: { id: QuickRangeId; label: string; minutes: number }[] = [
  { id: '15m', label: 'Last 15 min', minutes: 15 },
  { id: '1h', label: 'Last hour', minutes: 60 },
  { id: '4h', label: 'Last 4 hours', minutes: 240 },
  { id: '24h', label: 'Last 24 hours', minutes: 1440 },
  { id: '7d', label: 'Last 7 days', minutes: 10080 }
];
const maxWindowMs = 7 * 86_400_000;
export const quickRange = (id: QuickRangeId, now: Date): Pick<Query, 'start' | 'end' | 'baselineStart' | 'baselineEnd' | 'bucketSeconds'> => {
  const preset = quickRanges.find(range => range.id === id);
  if (!preset) throw new RangeError(`Unknown quick range: ${id}`);
  const span = Math.min(preset.minutes * 60_000, maxWindowMs);
  const end = new Date(now.getTime());
  const start = new Date(end.getTime() - span);
  const baselineStart = new Date(start.getTime() - span);
  const range = { start: start.toISOString(), end: end.toISOString(), baselineStart: baselineStart.toISOString(), baselineEnd: start.toISOString() };
  return { ...range, bucketSeconds: bucketForWindows(range.start, range.end, range.baselineStart, range.baselineEnd) };
};

// ---------------------------------------------------------------------------------------------------------------------
// One way to show a timestamp. Every screen formats through formatTime so the pattern is always "24 Oct 15:19:37 UTC",
// and the UTC / Local choice (see timeZone.svelte.ts) flips every timestamp together.
// ---------------------------------------------------------------------------------------------------------------------
export type TimePrecision = 'minute' | 'second' | 'ms';
export interface FormatTimeOptions {
  /** IANA zone name. Defaults to UTC. An unknown zone falls back to UTC. */
  zone?: string;
  /** Smallest unit shown. Defaults to second. */
  precision?: TimePrecision;
  /** Show the day and month. Defaults to true. */
  date?: boolean;
  /** Show the year too (use it when a range may cross years). Defaults to false. */
  year?: boolean;
  /** Print the zone suffix ("UTC", "GMT+4"). Defaults to true; a table can drop it and name the zone once in its heading. */
  suffix?: boolean;
  /** Text for a missing or unreadable value. Defaults to "N/A". */
  fallback?: string;
}
const MONTH_NAMES = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const clockFormatters = new Map<string, Intl.DateTimeFormat>();
const clockFormatter = (zone: string, precision: TimePrecision): Intl.DateTimeFormat => {
  const key = zone + '|' + precision;
  let formatter = clockFormatters.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-GB', {
      timeZone: zone, year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'shortOffset',
      ...(precision === 'minute' ? {} : { second: '2-digit' as const }),
      ...(precision === 'ms' ? { fractionalSecondDigits: 3 as const } : {})
    });
    clockFormatters.set(key, formatter);
  }
  return formatter;
};
const toMillis = (value: string | number | Date | null | undefined): number | null => {
  if (value === null || value === undefined || value === '') return null;
  const millis = value instanceof Date ? value.getTime() : typeof value === 'number' ? value : Date.parse(value);
  return Number.isFinite(millis) ? millis : null;
};
export interface TimeParts { day: string; year: string; clock: string; zone: string }
/** The pieces of a timestamp in a zone: "24 Oct", "2026", "15:19:37", "UTC". Null when the value is not a time. */
export const timeParts = (value: string | number | Date | null | undefined, zone = 'UTC', precision: TimePrecision = 'second'): TimeParts | null => {
  const millis = toMillis(value);
  if (millis === null) return null;
  let parts: Intl.DateTimeFormatPart[];
  let usedZone = zone || 'UTC';
  try { parts = clockFormatter(usedZone, precision).formatToParts(new Date(millis)); }
  catch { usedZone = 'UTC'; parts = clockFormatter('UTC', precision).formatToParts(new Date(millis)); }
  const get = (type: string) => parts.find(part => part.type === type)?.value ?? '';
  const monthIndex = Number(get('month')) - 1;
  const fraction = get('fractionalSecond');
  return {
    day: Number(get('day')) + ' ' + (MONTH_NAMES[monthIndex] ?? ''),
    year: get('year'),
    clock: get('hour') + ':' + get('minute') + (precision === 'minute' ? '' : ':' + get('second')) + (precision === 'ms' ? '.' + fraction : ''),
    zone: usedZone === 'UTC' ? 'UTC' : get('timeZoneName')
  };
};
/** One timestamp in the app-wide pattern, for example "24 Oct 15:19:37 UTC". */
export const formatTime = (value: string | number | Date | null | undefined, options: FormatTimeOptions = {}): string => {
  const { zone = 'UTC', precision = 'second', date = true, year = false, suffix = true, fallback = 'N/A' } = options;
  const parts = timeParts(value, zone, precision);
  if (!parts) return fallback;
  return [date ? parts.day + (year ? ' ' + parts.year : '') : '', parts.clock, suffix ? parts.zone : ''].filter(Boolean).join(' ');
};
/** The zone's short name at a moment ("UTC", "GMT+4"), for a table heading that names the zone once. */
export const zoneAbbreviation = (zone = 'UTC', at: string | number | Date = Date.now()): string => timeParts(at, zone, 'minute')?.zone ?? 'UTC';
/**
 * A window in the same pattern, with the zone printed once (twice when a daylight-saving change makes the two ends differ): "24 Oct 15:04:37–15:19:37 UTC" on one day,
 * "24 Oct 23:50:00 – 25 Oct 00:05:00 UTC" across days. When the two ends are the same instant it prints that instant once.
 */
export const formatTimeRange = (start: string | number | Date | null | undefined, end: string | number | Date | null | undefined, options: FormatTimeOptions = {}): string => {
  const { zone = 'UTC', precision = 'second', year = false, suffix = true, fallback = 'N/A' } = options;
  const from = timeParts(start, zone, precision);
  const to = timeParts(end, zone, precision);
  if (!from || !to) return fallback;
  const tail = suffix ? ' ' + from.zone : '';
  const label = (parts: TimeParts) => parts.day + (year ? ' ' + parts.year : '');
  // A range across a daylight-saving change has a different abbreviation at each end: name both, and never fold two identical clocks into one.
  if (suffix && from.zone !== to.zone) {
    const sameDay = from.day === to.day && from.year === to.year;
    return label(from) + ' ' + from.clock + ' ' + from.zone + (sameDay ? ' – ' : ' – ' + label(to) + ' ') + to.clock + ' ' + to.zone;
  }
  if (from.clock === to.clock && from.day === to.day && from.year === to.year) return label(from) + ' ' + from.clock + tail;
  if (from.day === to.day && from.year === to.year) return label(from) + ' ' + from.clock + '–' + to.clock + tail;
  return label(from) + ' ' + from.clock + ' – ' + label(to) + ' ' + to.clock + tail;
};
/** "ms" when two of these moments fall in the same second (so whole seconds would show identical times), otherwise "second". */
export const precisionFor = (values: Array<string | number | Date | null | undefined>): TimePrecision => {
  const seen = new Set<number>();
  for (const value of values) {
    const millis = toMillis(value);
    if (millis === null) continue;
    const second = Math.floor(millis / 1000);
    if (seen.has(second)) return 'ms';
    seen.add(second);
  }
  return 'second';
};
