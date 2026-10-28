import type { Query } from '../types';
import type { InvestigationRequest } from './types';

/**
 * Step 0: the question. Two windows in UTC, an optional API, an optional pool limit and the change list. Times are typed as `YYYY-MM-DDTHH:mm`
 * (the value of a datetime-local box) and are always read as UTC: nothing here reads the browser's time zone.
 */

export interface Frame {
  incidentStart: string;
  incidentEnd: string;
  referenceStart: string;
  referenceEnd: string;
  /** "Controller.Action", or empty for All APIs. */
  route: string;
  /** The SqlClient pool limit, as typed. Empty means none entered. */
  poolLimit: string;
  /** The change list. `entered` is true once the person has looked at it, so an empty list entered on purpose reads "No change listed". */
  releases: Array<{ at: string; label: string }>;
  releasesEntered: boolean;
}

export const MAX_SPAN_MS = 7 * 24 * 3600 * 1000;
export const MAX_RELEASES = 50;
export const MAX_POOL = 100000;
const MINUTE = 60 * 1000;

export const emptyFrame = (): Frame => ({
  incidentStart: '', incidentEnd: '', referenceStart: '', referenceEnd: '', route: '', poolLimit: '', releases: [], releasesEntered: false
});

const pad = (n: number) => String(n).padStart(2, '0');

/** A moment as `YYYY-MM-DDTHH:mm` in UTC. */
export function toLocalUtc(ms: number): string {
  const d = new Date(ms);
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
}

const SHAPE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;

/** Milliseconds for a typed UTC time, or NaN when it is not a whole `YYYY-MM-DDTHH:mm`. */
export function parseLocalUtc(value: string): number {
  if (!SHAPE.test(value)) return NaN;
  const ms = Date.parse(value + ':00Z');
  // 2026-02-31 parses in some engines as a later day: the round trip catches it.
  return Number.isFinite(ms) && toLocalUtc(ms) === value ? ms : NaN;
}

export type QuickPick = 'last-hour' | 'today' | 'yesterday';

/**
 * A quick pick sets the incident window and, next to it, a reference window of the same length that ends where the incident starts.
 * Last hour is the 60 minutes before `now`; Today runs from 00:00 UTC to `now` (at least 5 minutes); Yesterday is the whole of yesterday in UTC.
 */
export function quickPick(pick: QuickPick, now: number): Pick<Frame, 'incidentStart' | 'incidentEnd' | 'referenceStart' | 'referenceEnd'> {
  const minute = Math.floor(now / MINUTE) * MINUTE;
  const dayStart = Math.floor(now / 86400000) * 86400000;
  let start: number;
  let end: number;
  if (pick === 'last-hour') { end = minute; start = end - 60 * MINUTE; }
  else if (pick === 'yesterday') { end = dayStart; start = end - 86400000; }
  else { start = dayStart; end = Math.max(minute, dayStart + 5 * MINUTE); }
  const length = end - start;
  return { incidentStart: toLocalUtc(start), incidentEnd: toLocalUtc(end), referenceStart: toLocalUtc(start - length), referenceEnd: toLocalUtc(start) };
}

/** What stops the windows from being used, in plain words. An empty list means they can be run. */
export function frameProblems(frame: Frame): string[] {
  const out: string[] = [];
  const check = (label: string, start: string, end: string): [number, number] => {
    const s = parseLocalUtc(start);
    const e = parseLocalUtc(end);
    if (!Number.isFinite(s) || !Number.isFinite(e)) { out.push(`The ${label} window needs a start and an end, in UTC.`); return [NaN, NaN]; }
    if (e <= s) { out.push(`The ${label} window ends before it starts.`); return [NaN, NaN]; }
    if (e - s > MAX_SPAN_MS) { out.push(`The ${label} window is longer than 7 days. Shorten it.`); return [NaN, NaN]; }
    return [s, e];
  };
  const [is, ie] = check('incident', frame.incidentStart, frame.incidentEnd);
  const [rs, re] = check('reference', frame.referenceStart, frame.referenceEnd);
  if (Number.isFinite(is) && Number.isFinite(rs) && is < re && rs < ie) out.push('The two windows overlap. Pick a normal period that does not share any time with the incident.');
  if (frame.poolLimit.trim() !== '') {
    const n = Number(frame.poolLimit);
    if (!Number.isInteger(n) || n < 1 || n > MAX_POOL) out.push('The pool limit is a whole number from 1 to 100000, or empty.');
  }
  if (frame.releases.length > MAX_RELEASES) out.push('The change list holds at most 50 changes.');
  if (frame.releases.some((r) => !Number.isFinite(parseLocalUtc(r.at)))) out.push('Each change needs a time, in UTC.');
  if (frame.releases.some((r) => r.label.trim() === '')) out.push('Each change needs a name.');
  return out;
}

const iso = (value: string) => new Date(parseLocalUtc(value)).toISOString();

/** The request body for the engine, or null when the windows cannot be used. `includeSeries` asks for the per-minute rows the charts draw. */
export function toRequest(frame: Frame, sourceIds: readonly string[], includeSeries = true): InvestigationRequest | null {
  if (frameProblems(frame).length || !sourceIds.length) return null;
  const pool = frame.poolLimit.trim() === '' ? undefined : Number(frame.poolLimit);
  return {
    sourceIds: [...sourceIds],
    incident: { start: iso(frame.incidentStart), end: iso(frame.incidentEnd) },
    reference: { start: iso(frame.referenceStart), end: iso(frame.referenceEnd) },
    route: frame.route.trim(),
    ...(pool === undefined ? {} : { poolLimit: pool }),
    releases: frame.releases.map((r) => ({ at: iso(r.at), label: r.label.trim().slice(0, 200) })),
    releasesEntered: frame.releasesEntered,
    includeSeries
  };
}

/** One string for everything a run read. Two runs with the same key read the same thing; a different key means the answer on screen is out of date. */
export const requestKey = (request: InvestigationRequest): string => JSON.stringify([
  [...request.sourceIds].sort(), request.incident, request.reference, request.route, request.poolLimit ?? null,
  request.releases, request.releasesEntered
]);

/** The incident window moved earlier by `minutes`: the "Widen the incident window" choice. The reference window is left alone. */
export function widenIncident(frame: Frame, minutes: number): Frame {
  const start = parseLocalUtc(frame.incidentStart);
  if (!Number.isFinite(start)) return frame;
  let next = start - minutes * MINUTE;
  const end = parseLocalUtc(frame.incidentEnd);
  if (Number.isFinite(end)) next = Math.max(next, end - MAX_SPAN_MS);
  return { ...frame, incidentStart: toLocalUtc(next) };
}

/** The window's length in whole minutes, or 0 for a window that cannot be read. */
export function windowMinutes(start: string, end: string): number {
  const s = parseLocalUtc(start);
  const e = parseLocalUtc(end);
  return Number.isFinite(s) && Number.isFinite(e) && e > s ? Math.round((e - s) / MINUTE) : 0;
}

/** The most buckets the server accepts in one window (internal/audit/models.go MaxTimeBuckets). */
export const MAX_BUCKETS = 120;

/**
 * The bucket size, in seconds, for the slowest-calls query: a whole number of minutes, at least one, so that the longer of the two windows
 * stays inside the server cap. Buckets are aligned to the Unix epoch, so a window can touch one bucket more than span / size; dividing by
 * MAX_BUCKETS - 1 keeps that extra bucket inside the cap too.
 */
export function slowBucketSeconds(frame: Frame): number {
  const spans = [
    parseLocalUtc(frame.incidentEnd) - parseLocalUtc(frame.incidentStart),
    parseLocalUtc(frame.referenceEnd) - parseLocalUtc(frame.referenceStart)
  ].filter((n) => Number.isFinite(n) && n > 0);
  const seconds = Math.max(0, ...spans) / 1000;
  return Math.max(1, Math.ceil(seconds / (MAX_BUCKETS - 1) / 60)) * 60;
}

/** The query the slowest-calls list reads: the incident window, every kind of call, the one API if one is chosen. */
export function slowQuery(frame: Frame, sourceIds: readonly string[]): Query | null {
  if (frameProblems(frame).length || !sourceIds.length) return null;
  const [controller, ...rest] = frame.route.trim().split('.');
  return {
    sourceIds: [...sourceIds], start: iso(frame.incidentStart), end: iso(frame.incidentEnd),
    baselineStart: iso(frame.referenceStart), baselineEnd: iso(frame.referenceEnd),
    kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: slowBucketSeconds(frame),
    controller: controller ?? '', action: rest.join('.')
  };
}
