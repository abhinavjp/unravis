import { newestDay, overlapProblem, type LaneDay, type LaneWindow } from '../lanes';
import type { SavedSqlConnection } from '../types';

/**
 * The Timeline's own reading of the database (P5): which saved connection and database the case uses for SQL, and whether the window on
 * screen still needs a read. Pure rules only. The read itself is the existing read-only lane call; nothing here connects to anything.
 */

export interface SqlSource {
  /** The saved connection to read through. */
  connectionId: string;
  label: string;
  database: string;
  /** A SQL login saved without its password cannot be read without asking for it, so it is never read automatically. */
  needsPassword: boolean;
  /** True when a day already read through it is kept in the case; false when it is only the one saved connection there is. */
  remembered: boolean;
}

const labelOf = (item: SavedSqlConnection): string => item.label || item.server;
const needsPassword = (item: SavedSqlConnection): boolean => item.auth === 'sql' && !item.hasPassword;

/**
 * The case's SQL source. The newest saved SQL day names the connection (by label) and the database it was read from. A case with no
 * saved day uses the only saved connection there is, if there is exactly one. Anything else has no source: the person picks in Sources.
 */
export function pickSqlSource(days: readonly LaneDay[], saved: readonly SavedSqlConnection[]): SqlSource | null {
  const newest = newestDay(days.filter((day) => day.kind === 'sql'));
  if (newest) {
    const match = saved.find((item) => labelOf(item) === newest.label);
    if (!match) return null;
    return { connectionId: match.id, label: labelOf(match), database: newest.database || match.database, needsPassword: needsPassword(match), remembered: true };
  }
  if (saved.length !== 1) return null;
  const only = saved[0];
  return { connectionId: only.id, label: labelOf(only), database: only.database, needsPassword: needsPassword(only), remembered: false };
}

/** What the line above the chart says about the database. */
export type SqlStatusKind = 'suggest' | 'reading' | 'failed' | 'overlap' | 'password' | 'kept';

export type SqlAutoVerdict =
  | { kind: 'covered' }
  | { kind: 'read'; incident: LaneWindow; reference: LaneWindow }
  | { kind: 'overlap'; day: LaneDay }
  | { kind: 'skip'; why: 'no-window' | 'no-source' | 'password' | 'no-database' | 'demo' | 'closed' | 'off' };

export interface SqlAutoInput {
  source: SqlSource | null;
  days: readonly LaneDay[];
  incident: LaneWindow | null;
  reference: LaneWindow | null;
  demo: boolean;
  closed: boolean;
  enabled: boolean;
}

const contains = (outer: LaneWindow, inner: LaneWindow): boolean => Date.parse(outer.start) <= Date.parse(inner.start) && Date.parse(outer.end) >= Date.parse(inner.end);

/**
 * What the Timeline does about the database for this window. Nothing is read in the demo, on a closed case, with the toggle off, with no
 * source, or for a SQL login that needs its password typed. A window a saved day covers is not read again; one that crosses a saved day
 * without being the same is not read either (the server refuses it and says so, so the page says so first).
 */
export function decideSqlRead(input: SqlAutoInput): SqlAutoVerdict {
  if (input.demo) return { kind: 'skip', why: 'demo' };
  if (input.closed) return { kind: 'skip', why: 'closed' };
  if (!input.enabled) return { kind: 'skip', why: 'off' };
  if (!input.source) return { kind: 'skip', why: 'no-source' };
  if (input.source.needsPassword) return { kind: 'skip', why: 'password' };
  if (input.source.database.trim() === '') return { kind: 'skip', why: 'no-database' };
  if (!input.incident || !input.reference) return { kind: 'skip', why: 'no-window' };
  const { label, database } = input.source;
  const held = input.days.filter((day) => day.kind === 'sql' && day.label === label && day.database === database);
  if (held.some((day) => contains(day.incident, input.incident as LaneWindow))) return { kind: 'covered' };
  const clash = overlapProblem(input.days as LaneDay[], 'sql', label, database, input.incident);
  if (clash) return { kind: 'overlap', day: clash };
  return { kind: 'read', incident: input.incident, reference: input.reference };
}

const realWindow = (w: LaneWindow): boolean => Number.isFinite(Date.parse(w.start)) && Number.isFinite(Date.parse(w.end)) && Date.parse(w.end) > Date.parse(w.start);

/** The two windows of a Timeline read, or null when either is not a real window (an end after its start). */
export function windowsOfBody(body: { incident: LaneWindow; reference: LaneWindow } | null): { incident: LaneWindow; reference: LaneWindow } | null {
  if (!body || !realWindow(body.incident) || !realWindow(body.reference)) return null;
  return { incident: { start: body.incident.start, end: body.incident.end }, reference: { start: body.reference.start, end: body.reference.end } };
}

/** One key per window and source, so a window is read once and a filter change that keeps the window does not read again. */
export const readKey = (source: SqlSource, incident: LaneWindow): string => [source.connectionId, source.database, new Date(incident.start).toISOString(), new Date(incident.end).toISOString()].join('|');
