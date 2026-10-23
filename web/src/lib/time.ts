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
