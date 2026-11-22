import { zoneOffsetMs } from './chartTime';
import { localToInstant, safeZone } from './zoom';

/**
 * Typing a time in the zone the header chose ("Times in"). The date and time boxes of the When filters show the wall clock of that zone and turn
 * what is typed back into a UTC instant, so a London user who types 10:00 gets 10:00 London time, not 10:00 UTC (ANALYSIS-01). Both directions use
 * the zone's own rules (chartTime.zoneOffsetMs and zoom.localToInstant, the helpers the charts and the window picker already use), so a clock change is
 * handled the same way for London, Sydney or this computer's zone, and nothing here reads the browser's time zone.
 */
const pad = (value: number, width = 2) => String(value).padStart(width, '0');

/** The date ("2026-10-24") and the time ("15:19:37.123") on the wall clock of `zone` at an instant. Null for a value that is not a time. */
export const toZonedParts = (value: string | number | Date, zone: string): { date: string; time: string } | null => {
  const ms = value instanceof Date ? value.getTime() : typeof value === 'number' ? value : Date.parse(value);
  if (!Number.isFinite(ms)) return null;
  const z = safeZone(zone);
  const local = new Date(ms + zoneOffsetMs(ms, z));
  return {
    date: `${pad(local.getUTCFullYear(), 4)}-${pad(local.getUTCMonth() + 1)}-${pad(local.getUTCDate())}`,
    time: `${pad(local.getUTCHours())}:${pad(local.getUTCMinutes())}:${pad(local.getUTCSeconds())}.${pad(local.getUTCMilliseconds(), 3)}`
  };
};

/** The box value ("2026-10-24T15:19:37.123") for an instant, on the wall clock of `zone`. Empty for a value that is not a time. */
export const toZonedInput = (value: string | number | Date, zone: string): string => {
  const parts = toZonedParts(value, zone);
  return parts ? `${parts.date}T${parts.time}` : '';
};

/**
 * The UTC instant (ISO text) for a date and a time typed on the wall clock of `zone`. A time that happens twice when the clocks go back takes the first one,
 * unless `previous` (the instant the box held before this edit) was itself in the second pass of that same repeated hour: then the edit keeps the second pass, so
 * changing only the date or the seconds of "01:30 GMT" does not silently move the window to "01:30 GMT+1", an hour earlier.
 * A time the clocks skip (the hour lost when they go forward) does not exist: it throws a RangeError that says so, and the person chooses another time.
 */
export const fromZonedParts = (datePart: string, timePart: string, zone: string, previous?: string): string => {
  const dateMatch = /^(\d{4})-(\d{2})-(\d{2})$/.exec(datePart);
  const timeMatch = /^(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/.exec(timePart);
  if (!dateMatch || !timeMatch) throw new RangeError('Enter a valid date and time.');
  const [year, month, day] = [+dateMatch[1], +dateMatch[2], +dateMatch[3]];
  const [hour, minute, second] = [+timeMatch[1], +timeMatch[2], +(timeMatch[3] ?? 0)];
  const milli = Number((timeMatch[4] ?? '').padEnd(3, '0') || 0);
  const wall = Date.UTC(year, month - 1, day, hour, minute, second, milli);
  const probe = new Date(wall);
  if (probe.getUTCFullYear() !== year || probe.getUTCMonth() !== month - 1 || probe.getUTCDate() !== day || hour > 23 || minute > 59 || second > 59) {
    throw new RangeError('That is not a valid time on this date.');
  }
  const z = safeZone(zone);
  const at = localToInstant(wall, z);
  // localToInstant moves a skipped time to just after the gap; a typed time that does not read back as itself is one the clocks skip.
  if (at + zoneOffsetMs(at, z) !== wall) throw new RangeError('That time does not exist in this time zone on this date because the clocks change. Choose another time.');
  if (previous) {
    const later = laterPassOf(wall, previous, z);
    if (later !== null) return new Date(later).toISOString();
  }
  return new Date(at).toISOString();
};

const DAY_MS = 86_400_000;
/** Every instant whose wall clock in `zone` reads `wall`: none in the hour a clock change skips, two in the hour it repeats. Oldest first. */
const instantsFor = (wall: number, zone: string): number[] => {
  const found = new Set<number>();
  for (const probe of [wall - DAY_MS, wall + DAY_MS]) {
    const at = wall - zoneOffsetMs(probe, zone);
    if (at + zoneOffsetMs(at, zone) === wall) found.add(at);
  }
  return [...found].sort((a, b) => a - b);
};
/** The later pass of a repeated wall time, when `previous` was the later pass of the same repeated hour; otherwise null. */
const laterPassOf = (wall: number, previous: string, zone: string): number | null => {
  const now = instantsFor(wall, zone);
  const was = Date.parse(previous);
  if (now.length !== 2 || !Number.isFinite(was)) return null;
  const wasWall = was + zoneOffsetMs(was, zone);
  const before = instantsFor(wasWall, zone);
  const repeat = now[1] - now[0];
  return before.length === 2 && was === before[1] && Math.abs(wall - wasWall) < repeat ? now[1] : null;
};
