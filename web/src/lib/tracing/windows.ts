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
  const check = (label: 'incident' | 'reference', start: number, end: number): boolean => {
    if (Number.isNaN(start) || Number.isNaN(end)) {
      errors.push(`The ${label} window needs a start and an end, in UTC.`);
      return false;
    }
    if (end <= start) {
      errors.push(`The ${label} window ends before it starts.`);
      return false;
    }
    if (end - start > MAX_TRACE_SPAN_MS) {
      errors.push(`The longest window is 30 days. Shorten the ${label} window.`);
      return false;
    }
    return true;
  };
  const incidentOk = check('incident', iS, iE);
  const referenceOk = check('reference', rS, rE);
  const overlap = incidentOk && referenceOk && iS < rE && rS < iE;
  const body = errors.length > 0
    ? null
    : {
        incident: { start: new Date(iS).toISOString(), end: new Date(iE).toISOString() },
        reference: { start: new Date(rS).toISOString(), end: new Date(rE).toISOString() }
      };
  return { errors, overlap, body };
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
  utcNote: 'Times are typed in UTC.'
} as const;

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
