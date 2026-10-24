import { formatTime } from './time';

/**
 * Typed view model for the thin timeline (card P8). It mirrors the JSON of internal/timeline.View.
 * The server builds every row's wording from the roadmap 6.6 templates and lints it, so this file only
 * checks the shape, groups rows and words the screen around them. It never writes a cause.
 */

export type TimelineLabel = 'Symptom' | 'Correlation';
export type TimelineRowKind = 'audit' | 'overlap' | 'absence';
export type TimelineJoin = 'unique' | 'ambiguous' | 'none';

export interface TimelineSource {
  name: string;
  hosts: string[];
  tzRules: string[];
  validated: boolean;
  files: number;
}

export interface TimelineRow {
  kind: TimelineRowKind;
  /** UTC, RFC 3339. */
  time: string;
  end: string;
  intervalId: string;
  source?: string;
  host?: string;
  eventKind?: string;
  evidenceId?: string;
  durationMs?: number;
  join?: TimelineJoin;
  /** The number of candidates, on every row of an ambiguous match. */
  candidates?: number;
  label: TimelineLabel;
  /** The exact permitted sentence for this row. */
  wording: string;
}

export interface TimelineView {
  enabled: boolean;
  reasonCode?: string;
  /** Set exactly when the timeline is off. */
  reason?: string;
  sources: TimelineSource[];
  rows: TimelineRow[];
  unlisted: number;
  truncated: boolean;
}

const KINDS: readonly string[] = ['audit', 'overlap', 'absence'];
const JOINS: readonly string[] = ['unique', 'ambiguous', 'none'];
const LABELS: readonly string[] = ['Symptom', 'Correlation'];

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const isText = (v: unknown): v is string => typeof v === 'string';
const optText = (v: unknown): boolean => v === undefined || isText(v);
const validTime = (v: unknown): boolean => isText(v) && Number.isFinite(Date.parse(v));

function validRow(r: unknown): r is TimelineRow {
  return isObject(r)
    && isText(r.kind) && KINDS.includes(r.kind)
    && validTime(r.time) && validTime(r.end)
    && isText(r.intervalId)
    && isText(r.label) && LABELS.includes(r.label)
    && isText(r.wording) && r.wording !== ''
    && optText(r.source) && optText(r.host) && optText(r.eventKind) && optText(r.evidenceId)
    && (r.durationMs === undefined || (typeof r.durationMs === 'number' && Number.isFinite(r.durationMs)))
    && (r.join === undefined || (isText(r.join) && JOINS.includes(r.join)))
    && (r.candidates === undefined || (typeof r.candidates === 'number' && Number.isInteger(r.candidates) && r.candidates >= 0));
}

function validSource(s: unknown): s is TimelineSource {
  return isObject(s) && isText(s.name) && typeof s.validated === 'boolean' && typeof s.files === 'number'
    && Array.isArray(s.hosts) && s.hosts.every(isText) && Array.isArray(s.tzRules) && s.tzRules.every(isText);
}

/**
 * Checks a server answer and returns it typed, or null when it does not have the shape. A disabled view
 * must carry a reason and no rows, and an enabled one must carry no reason: anything else is refused.
 */
export function parseTimelineView(raw: unknown): TimelineView | null {
  if (!isObject(raw) || typeof raw.enabled !== 'boolean') return null;
  const rows = raw.rows ?? [];
  const sources = raw.sources ?? [];
  if (!Array.isArray(rows) || !rows.every(validRow) || !Array.isArray(sources) || !sources.every(validSource)) return null;
  if (!optText(raw.reason) || !optText(raw.reasonCode)) return null;
  if (raw.enabled && raw.reason) return null;
  if (!raw.enabled && (!raw.reason || rows.length > 0)) return null;
  const unlisted = typeof raw.unlisted === 'number' && Number.isFinite(raw.unlisted) && raw.unlisted >= 0 ? raw.unlisted : 0;
  return {
    enabled: raw.enabled, reasonCode: raw.reasonCode as string | undefined, reason: raw.reason as string | undefined,
    sources: sources as TimelineSource[], rows: rows as TimelineRow[], unlisted, truncated: raw.truncated === true
  };
}

/** One audit interval with the rows that refer to it, in clock order. */
export interface TimelineGroup {
  intervalId: string;
  interval: TimelineRow;
  /** Overlap and absence rows. An ambiguous match lists every candidate here. */
  rows: TimelineRow[];
}

/** Groups rows under their audit interval. Groups and rows keep the server's clock order. Rows without an interval row are dropped. */
export function timelineGroups(view: TimelineView): TimelineGroup[] {
  const groups = new Map<string, TimelineGroup>();
  for (const row of view.rows) if (row.kind === 'audit' && !groups.has(row.intervalId)) groups.set(row.intervalId, { intervalId: row.intervalId, interval: row, rows: [] });
  for (const row of view.rows) if (row.kind !== 'audit') groups.get(row.intervalId)?.rows.push(row);
  return [...groups.values()];
}

/** A row's time in a zone, to the second, without the zone name (the heading names it once). */
export function rowClock(row: TimelineRow, zone: string): string {
  return formatTime(row.time, { zone, precision: 'second', suffix: false });
}

const RULE_WORDS: Record<string, string> = {
  'utc-field': 'UTC in the file',
  'offset-in-field': 'offset in each time',
  'header-bias': 'fixed offset in the file header',
  'host-local': 'host time zone'
};

/** Plain words for a time zone rule; an unknown rule is shown as "not checked". */
export function ruleWords(rule: string): string {
  return RULE_WORDS[rule] ?? TIMELINE_TEXT.notChecked;
}

/** Fixed screen text. The timeline test lints every string here. */
export const TIMELINE_TEXT = {
  title: 'Timeline across sources',
  intro: 'This list lines up audit times with events from imported files, all on one UTC clock. Each line is a clue to look at, not an answer.',
  off: 'Timeline is off',
  sources: 'Sources',
  source: 'Source',
  host: 'Host',
  timeRule: 'Time rule',
  files: 'Files',
  time: 'Time',
  label: 'Label',
  what: 'What the record shows',
  noRows: 'No audit intervals fall in this window.',
  truncated: 'Only the first 50,000 events were read, so this list may be missing some.',
  notChecked: 'not checked'
} as const;

/** How many events were read but not listed. Empty when none. */
export function unlistedLine(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '';
  const count = Math.floor(n);
  return `${count} other ${count === 1 ? 'event was' : 'events were'} read but not listed. ${count === 1 ? 'It' : 'They'} did not overlap an interval, or had no length of time.`;
}

/** The row's host and event kind as one short label, such as "sql-a, lock wait". */
export function rowSubject(row: TimelineRow): string {
  const kind = row.eventKind ? row.eventKind.replace(/-/g, ' ') : '';
  return [row.source, row.host, kind].filter(Boolean).join(', ');
}
