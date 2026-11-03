import type { LintEntry } from '../copy/copylint';

/**
 * Window rules for the tracing screen's time picker (WP-22a). Pure module, no Svelte: the incident window and
 * the comparison period are typed as `YYYY-MM-DDTHH:mm` strings read as UTC, and every refusal sentence the
 * picker can show lives here so the screen stays a thin shell.
 */

export const MINUTE_MS = 60_000;
export const MAX_TRACE_SPAN_MS = 30 * 24 * 3600 * 1000;

export interface WindowFields {
  incidentStart: string;
  incidentEnd: string;
  referenceStart: string;
  referenceEnd: string;
}

export interface WindowCheck {
  errors: string[];
  /** The picker fields the errors are about, so the screen can mark exactly those inputs invalid. */
  invalid: (keyof WindowFields)[];
  overlap: boolean;
  body: {
    incident: { start: string; end: string };
    reference: { start: string; end: string };
  } | null;
}

/** Formats a millisecond time as the picker field value `YYYY-MM-DDTHH:mm` (UTC). */
export function toFieldUtc(ms: number): string {
  return new Date(ms).toISOString().slice(0, 16);
}

const FIELD = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

/**
 * Reads a field value as UTC milliseconds. NaN unless the whole string round-trips, so a day that does not
 * exist (`2026-02-31T10:00`) or any stray text is refused.
 */
export function parseFieldUtc(value: string): number {
  const m = FIELD.exec(value);
  if (!m) return NaN;
  const ms = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  return toFieldUtc(ms) === value ? ms : NaN;
}

/**
 * The moment the default window counts back from: the app's own data reference time in demo mode (so the picker
 * matches the demo data), the real clock otherwise or when the reference time does not parse.
 */
export function windowNow(info: { demo?: boolean; now?: string } | null | undefined, realNowMs: number): number {
  if (!info?.demo || typeof info.now !== 'string') return realNowMs;
  const ms = Date.parse(info.now);
  return Number.isFinite(ms) ? ms : realNowMs;
}

/** The default picker values: the incident is the hour before now (floored to the minute), the comparison the hour before that. */
export function defaultWindows(nowMs: number): WindowFields {
  const end = Math.floor(nowMs / MINUTE_MS) * MINUTE_MS;
  return {
    incidentStart: toFieldUtc(end - 3600_000),
    incidentEnd: toFieldUtc(end),
    referenceStart: toFieldUtc(end - 2 * 3600_000),
    referenceEnd: toFieldUtc(end - 3600_000)
  };
}

/** Validates both windows. One sentence per problem, incident first. Overlap is a warning, not an error. */
export function checkWindows(f: WindowFields): WindowCheck {
  const iS = parseFieldUtc(f.incidentStart);
  const iE = parseFieldUtc(f.incidentEnd);
  const rS = parseFieldUtc(f.referenceStart);
  const rE = parseFieldUtc(f.referenceEnd);
  const errors: string[] = [];
  const invalid: (keyof WindowFields)[] = [];
  // The names are the picker's own labels ("Incident window", "Comparison period"), not the internal word "reference".
  const check = (name: string, keys: [keyof WindowFields, keyof WindowFields], start: number, end: number): boolean => {
    if (Number.isNaN(start) || Number.isNaN(end)) {
      // A box that holds text which is not a usable date (year 0001, say) is named with its value; an empty box is just missing.
      const sides = [{ key: keys[0], word: 'start', bad: Number.isNaN(start) }, { key: keys[1], word: 'end', bad: Number.isNaN(end) }].filter((side) => side.bad);
      const typed = sides.filter((side) => f[side.key].trim() !== '');
      if (typed.length < sides.length) errors.push(`The ${name} needs a start and an end, in UTC.`);
      for (const side of typed) errors.push(`The ${name} ${side.word} (${f[side.key].trim()}) is not a date that can be used. Check the year.`);
      for (const side of sides) invalid.push(side.key);
      return false;
    }
    if (end <= start) {
      errors.push(`The ${name} end must be later than the start.`);
      invalid.push(...keys);
      return false;
    }
    if (end - start > MAX_TRACE_SPAN_MS) {
      errors.push(`The longest window is 30 days. Shorten the ${name}.`);
      invalid.push(...keys);
      return false;
    }
    return true;
  };
  const incidentOk = check('incident window', ['incidentStart', 'incidentEnd'], iS, iE);
  const referenceOk = check('comparison period', ['referenceStart', 'referenceEnd'], rS, rE);
  const overlap = incidentOk && referenceOk && iS < rE && rS < iE;
  const body = errors.length > 0
    ? null
    : {
        incident: { start: new Date(iS).toISOString(), end: new Date(iE).toISOString() },
        reference: { start: new Date(rS).toISOString(), end: new Date(rE).toISOString() }
      };
  return { errors, invalid, overlap, body };
}

/** Every fixed string the window picker shows. */
export const WINDOW_TEXT = {
  incidentLabel: 'Incident window (UTC)',
  referenceLabel: 'Comparison period (UTC)',
  start: 'Start',
  end: 'End',
  startTracing: 'Start tracing',
  overlap: 'The two windows overlap. Pick a normal period that does not share any time with the incident.',
  overlapFix: 'Move comparison period before the incident',
  utcNote: 'Times are typed in UTC.',
  endNote: 'A window includes its start and stops just before its end.',
  keptPrevious: 'The chart still shows the previous window.',
  noEvents: 'No events in this window. Try a wider one.',
  noData: 'Nothing was found in this window. Add log files to the case, or try a wider window.',
  cancelledNone: 'Cancelled. Press Start tracing to read the window again.',
  cancelledKept: 'Cancelled. The chart still shows the previous result.'
} as const;

/** True when at least one lane was read and every lane that was read has no events at all (the window itself is empty). */
export function windowIsEmpty(lanes: { state: string; buckets: { count: number }[] }[]): boolean {
  const read = lanes.filter((lane) => lane.state === 'events' || lane.state === 'incomplete');
  return read.length > 0 && lanes.every((lane) => lane.buckets.every((bucket) => !(bucket.count > 0)));
}

/** True when lanes came back and none of them holds an event: the chart has nothing to draw, whatever the lane states say. */
export function windowHasNoData(lanes: { buckets: { count: number }[] }[]): boolean {
  return lanes.length > 0 && lanes.every((lane) => lane.buckets.every((bucket) => !(bucket.count > 0)));
}

const STORAGE_KEY = 'unravis.tracing.windows.';
const STORED_VALUE = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})?$/;

/** Reads the windows last typed for this case in this browser tab; null when none are kept, they are malformed, or storage is unavailable. */
export function loadWindows(caseId: string, storage?: Pick<Storage, 'getItem'>): WindowFields | null {
  try {
    const raw = (storage ?? window.sessionStorage).getItem(STORAGE_KEY + caseId);
    const value: unknown = raw ? JSON.parse(raw) : null;
    if (typeof value !== 'object' || value === null) return null;
    const read = (name: keyof WindowFields): string | null => {
      const field = (value as Record<string, unknown>)[name];
      return typeof field === 'string' && STORED_VALUE.test(field) ? field : null;
    };
    const fields = { incidentStart: read('incidentStart'), incidentEnd: read('incidentEnd'), referenceStart: read('referenceStart'), referenceEnd: read('referenceEnd') };
    return Object.values(fields).every((field) => field !== null) ? (fields as WindowFields) : null;
  } catch {
    return null;
  }
}

/** Keeps the typed windows for this case in this browser tab, so a reload shows them again. Never throws. */
export function saveWindows(caseId: string, fields: WindowFields, storage?: Pick<Storage, 'setItem'>): void {
  try {
    (storage ?? window.sessionStorage).setItem(STORAGE_KEY + caseId, JSON.stringify({
      incidentStart: fields.incidentStart, incidentEnd: fields.incidentEnd, referenceStart: fields.referenceStart, referenceEnd: fields.referenceEnd
    }));
  } catch { /* unavailable storage only means the windows are not kept */ }
}

export const windowCopyEntries = (): LintEntry[] =>
  Object.entries(WINDOW_TEXT).map(([id, text]) => ({ id: 'tracingWindow.' + id, text }));

/** Keeps the incident and moves the comparison period to the same length, ending exactly where the incident starts. */
export function fixOverlap(f: WindowFields): WindowFields {
  const start = parseFieldUtc(f.incidentStart);
  const end = parseFieldUtc(f.incidentEnd);
  if (Number.isNaN(start) || Number.isNaN(end) || end <= start) return { ...f };
  return {
    incidentStart: f.incidentStart,
    incidentEnd: f.incidentEnd,
    referenceStart: toFieldUtc(start - (end - start)),
    referenceEnd: toFieldUtc(start)
  };
}
