import { ApiError } from './api';
import { CaseApiError, casePath, isCaseGone, json, send } from './cases';
import { RUN_TEXT } from './copy/sourcesCopy';
import type { SavedSqlConnection, SqlTestRequest } from './types';

/**
 * The SQL Server and MongoDB lanes of a case (P7): the four SQL checks, the Mongo profiler check, the grant scripts shown for the owner
 * to run, and the days already added. Everything here only reads. A script is text for a person to copy; nothing in this file runs one.
 * A saved password never comes back from the server, so none is held here: a password typed for one run goes out once and is dropped.
 */

/** A source is read in one of four states. These words are fixed (decision A5). */
export type LaneState = 'Ready' | 'Partly ready' | 'Cannot test yet' | 'Failed';
const STATES: readonly LaneState[] = ['Ready', 'Partly ready', 'Cannot test yet', 'Failed'];

/** A connection test says only one of these three (decision A5). They are not the readiness words. */
export type ConnectionWord = 'Works' | 'Failed' | 'Not tested';

/** Up to 20 saved connections of each kind (A28). The server refuses the 21st with 409. */
export const CONNECTION_LIMIT = 20;

export interface LaneWindow { start: string; end: string }
export interface GrantFix { permission: string; script: string }
export interface LaneCheck { id: string; title: string; state: LaneState; summary: string; feeds: string[]; fix: GrantFix | null }
export interface ProfilerGuide { title: string; why: string; cost: string; steps: string[]; check: string; switchOn: string; switchOff: string }
export interface LaneResult {
  incident: LaneWindow; reference: LaneWindow; era: string; checks: LaneCheck[]; answers: Record<string, string>;
  onset: string; sources: string[]; guide: ProfilerGuide | null; state: LaneState;
}
export interface LaneDay { id: number; at: string; kind: 'sql' | 'mongo'; label: string; database: string; state: LaneState; incident: LaneWindow; reference: LaneWindow; lane: LaneResult | null }

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
const obj = (v: unknown): Record<string, unknown> => (v && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : {});
/** An unknown state word is shown as Cannot test yet: a word the page does not know is never read as a pass. */
export const toState = (v: unknown): LaneState => (STATES.includes(v as LaneState) ? v as LaneState : 'Cannot test yet');
const toWindow = (v: unknown): LaneWindow => { const w = obj(v); return { start: str(w.start), end: str(w.end) }; };
const toFix = (v: unknown): GrantFix | null => {
  const f = obj(v);
  return str(f.script) === '' ? null : { permission: str(f.permission), script: str(f.script) };
};
export const toGuide = (v: unknown): ProfilerGuide | null => {
  const g = obj(v);
  return str(g.title) === '' ? null : { title: str(g.title), why: str(g.why), cost: str(g.cost), steps: strs(g.steps), check: str(g.check), switchOn: str(g.switchOn), switchOff: str(g.switchOff) };
};

export function toLane(raw: unknown): LaneResult | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = obj(raw);
  const checks: LaneCheck[] = (Array.isArray(r.checks) ? r.checks : []).map((c) => {
    const k = obj(c);
    return { id: str(k.id), title: str(k.title), state: toState(k.state), summary: str(k.summary), feeds: strs(k.feeds), fix: toFix(k.fix) };
  });
  const answers: Record<string, string> = {};
  for (const [id, v] of Object.entries(obj(r.answers))) if (typeof v === 'string') answers[id] = v;
  return {
    incident: toWindow(r.incident), reference: toWindow(r.reference), era: str(r.era), checks, answers, onset: str(r.onset), sources: strs(r.sources),
    guide: toGuide(r.guide), state: overallState(checks)
  };
}

/** The word for a whole lane: Ready when every check is, Failed when every check failed, Cannot test yet when none could run, otherwise Partly ready. */
export function overallState(checks: LaneCheck[]): LaneState {
  if (checks.length === 0) return 'Cannot test yet';
  const n = (s: LaneState) => checks.filter((c) => c.state === s).length;
  if (n('Ready') === checks.length) return 'Ready';
  if (n('Failed') === checks.length) return 'Failed';
  if (n('Ready') === 0 && n('Failed') === 0) return 'Cannot test yet';
  return n('Ready') === 0 ? 'Failed' : 'Partly ready';
}

export function toDay(raw: unknown): LaneDay {
  const r = obj(raw);
  const lane = toLane(r.lane);
  return {
    id: typeof r.id === 'number' ? r.id : 0, at: str(r.at), kind: r.kind === 'mongo' ? 'mongo' : 'sql', label: str(r.label), database: str(r.database),
    state: toState(r.state), incident: toWindow(r.incident), reference: toWindow(r.reference), lane
  };
}

export async function fetchLaneDays(caseId: string, signal?: AbortSignal): Promise<LaneDay[]> {
  const body = await json<{ days?: unknown }>(await send(casePath(caseId, '/lanes'), {}, signal));
  return Array.isArray(body.days) ? body.days.map(toDay) : [];
}

export interface LaneRunResult { saved: boolean; saveError: string; day: LaneDay }
const toRun = (raw: unknown): LaneRunResult => {
  const b = obj(raw);
  const run = obj(b.run);
  // The run row carries the report; the lane day shape is rebuilt from it so the page has one shape for both.
  const report = obj(run.report);
  const lane = toLane(report.lane);
  return {
    saved: b.saved === true, saveError: str(b.saveError),
    day: {
      id: typeof run.id === 'number' ? run.id : 0, at: str(run.at), kind: run.kind === 'mongo' ? 'mongo' : 'sql', label: str(run.label), database: str(report.database),
      state: toState(b.state), incident: lane?.incident ?? { start: '', end: '' }, reference: lane?.reference ?? { start: '', end: '' }, lane
    }
  };
};

/** The sentence for a finished read: kept in the case (good) or read but not kept (bad). Said on the page so it outlives the runner. */
export function runNotice(result: LaneRunResult): { good: boolean; text: string } {
  return result.saved ? { good: true, text: RUN_TEXT.done } : { good: false, text: RUN_TEXT.notSaved + (result.saveError || RUN_TEXT.failed) };
}

export interface LaneWindows { incident: LaneWindow; reference: LaneWindow }
export interface MongoLaneTarget { savedConnectionId: string; uri: string; username: string; password: string; authSource: string; tlsMode: string; caCertificate: string; database: string; collection: string }

export async function runSqlLane(caseId: string, sql: SqlTestRequest, windows: LaneWindows, signal?: AbortSignal): Promise<LaneRunResult> {
  const body = { ...sql, incident: windows.incident, reference: windows.reference };
  return toRun(await json<unknown>(await send(casePath(caseId, '/lanes/sql'), { method: 'POST', body: JSON.stringify(body) }, signal)));
}
export async function runMongoLane(caseId: string, mongo: MongoLaneTarget, windows: LaneWindows, signal?: AbortSignal): Promise<LaneRunResult> {
  const body = { ...mongo, incident: windows.incident, reference: windows.reference };
  return toRun(await json<unknown>(await send(casePath(caseId, '/lanes/mongo'), { method: 'POST', body: JSON.stringify(body) }, signal)));
}

export interface SqlGrantScripts { version: string; script: string; checks: Record<string, GrantFix> }
const query = (pairs: Record<string, string>): string =>
  '?' + Object.entries(pairs).filter(([, v]) => v !== '').map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(v)).join('&');

export async function fetchSqlGrantScripts(version: '2019' | '2022', login: string, database: string, signal?: AbortSignal): Promise<SqlGrantScripts> {
  const b = obj(await json<unknown>(await send('/grant-scripts' + query({ kind: 'sql', version, login, database }), {}, signal)));
  const checks: Record<string, GrantFix> = {};
  for (const [id, f] of Object.entries(obj(b.checks))) { const fix = toFix(f); if (fix) checks[id] = fix; }
  return { version: str(b.version), script: str(b.script), checks };
}
export async function fetchMongoGrantScript(login: string, database: string, signal?: AbortSignal): Promise<string> {
  return str(obj(await json<unknown>(await send('/grant-scripts' + query({ kind: 'mongo', login, database }), {}, signal))).script);
}
export async function fetchProfilerGuide(database: string, signal?: AbortSignal): Promise<ProfilerGuide | null> {
  return toGuide(obj(await json<unknown>(await send('/grant-scripts' + query({ kind: 'mongo-profiler', database }), {}, signal))).guide);
}

// ---- connection list --------------------------------------------------------------------------------------------------------------

/** One row of the connections page, for either kind. The last test is held here only: it is not stored anywhere. */
export interface ConnectionRow {
  key: string; kind: 'sql' | 'mongo'; id: string; label: string; detail: string; password: 'saved' | 'none' | 'not-needed'; word: ConnectionWord; testedAt: string; problem: string;
}

export function sqlRow(item: SavedSqlConnection, tested?: { word: ConnectionWord; at: string; problem: string }): ConnectionRow {
  return {
    key: 'sql:' + item.id, kind: 'sql', id: item.id, label: item.label || item.server,
    detail: ['SQL Server', item.auth === 'windows' ? 'Windows login' : 'SQL login ' + item.username, item.server].filter(Boolean).join(' · '),
    password: item.auth === 'windows' ? 'not-needed' : item.hasPassword ? 'saved' : 'none',
    word: tested?.word ?? 'Not tested', testedAt: tested?.at ?? '', problem: tested?.problem ?? ''
  };
}

export interface MongoSaved { id: string; label: string; uri: string; username: string; hasCredentials: boolean }
export function mongoRow(item: MongoSaved, tested?: { word: ConnectionWord; at: string; problem: string }): ConnectionRow {
  return {
    key: 'mongo:' + item.id, kind: 'mongo', id: item.id, label: item.label,
    detail: ['MongoDB', item.username ? 'user ' + item.username : ''].filter(Boolean).join(' · '),
    password: item.username === '' ? 'not-needed' : item.hasCredentials ? 'saved' : 'none',
    word: tested?.word ?? 'Not tested', testedAt: tested?.at ?? '', problem: tested?.problem ?? ''
  };
}

/** True while another SQL connection can be saved. */
export const canSaveAnother = (savedSql: number): boolean => savedSql < CONNECTION_LIMIT;

// ---- grant script choices -----------------------------------------------------------------------------------------------------

/** What a person typed or picked in the grant script panel. Kept in memory while the page is open, so the panel coming and going does not undo it. */
export interface GrantChoice { version?: '2019' | '2022'; login?: string; database?: string }
const grantMemory: { sql: GrantChoice; mongo: GrantChoice } = { sql: {}, mongo: {} };
export const recallGrant = (kind: 'sql' | 'mongo'): GrantChoice => grantMemory[kind];
export function rememberGrant(kind: 'sql' | 'mongo', choice: GrantChoice): void { grantMemory[kind] = { ...grantMemory[kind], ...choice }; }

// ---- windows ----------------------------------------------------------------------------------------------------------------------

/** A window from two "YYYY-MM-DDTHH:MM" UTC inputs, or null when either is empty, unreadable or the end is not after the start. */
export function utcWindow(start: string, end: string): LaneWindow | null {
  const a = parseUtc(start); const b = parseUtc(end);
  if (a === null || b === null || b <= a) return null;
  return { start: new Date(a).toISOString(), end: new Date(b).toISOString() };
}
function parseUtc(text: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})$/.exec(text.trim());
  if (!m) return null;
  const t = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  const d = new Date(t);
  // Date.UTC rolls 31 Feb over to March; a roll-over means the date was not real.
  return d.getUTCFullYear() === +m[1] && d.getUTCMonth() === +m[2] - 1 && d.getUTCDate() === +m[3] ? t : null;
}
/** The "YYYY-MM-DD HH:MM" UTC text of an ISO time, for an input (the same shape as the hint and the placeholder). */
export const utcInput = (iso: string): string => (Number.isNaN(Date.parse(iso)) ? '' : new Date(iso).toISOString().slice(0, 16).replace('T', ' '));
/** True when any of the typed times is filled in but is not a real "YYYY-MM-DD HH:MM" time (an empty one is the missing-window message instead). */
export const unreadableTime = (...texts: string[]): boolean => texts.some(text => text.trim() !== '' && parseUtc(text) === null);
export const sameWindow = (a: LaneWindow, b: LaneWindow): boolean => Date.parse(a.start) === Date.parse(b.start) && Date.parse(a.end) === Date.parse(b.end);
export const windowsOverlap = (a: LaneWindow, b: LaneWindow): boolean => Date.parse(a.start) < Date.parse(b.end) && Date.parse(b.start) < Date.parse(a.end);

/** Moves a window by whole days. Used to prefill "Add another day" with the same hours on a later day. */
export function shiftDays(w: LaneWindow, days: number): LaneWindow {
  const move = (iso: string) => new Date(Date.parse(iso) + days * 86_400_000).toISOString();
  return { start: move(w.start), end: move(w.end) };
}

/**
 * Whether a new window for a source clashes with a day the case already holds (same kind, label and database).
 * The same window is a check of that day again (allowed). A window that overlaps without being the same is refused, as the server does.
 */
export function overlapProblem(days: LaneDay[], kind: 'sql' | 'mongo', label: string, database: string, incident: LaneWindow): LaneDay | null {
  for (const d of days) {
    if (d.kind !== kind || d.label !== label || d.database !== database) continue;
    if (!sameWindow(d.incident, incident) && windowsOverlap(d.incident, incident)) return d;
  }
  return null;
}

// ---- grouping ---------------------------------------------------------------------------------------------------------------------

/** The days of one source, newest day first. A source is the kind, the label and the database. */
export interface LaneSource { key: string; kind: 'sql' | 'mongo'; label: string; database: string; days: LaneDay[] }
export function groupSources(days: LaneDay[]): LaneSource[] {
  const map = new Map<string, LaneSource>();
  for (const d of days) {
    const key = [d.kind, d.label, d.database].join('\u0001');
    const s = map.get(key) ?? { key, kind: d.kind, label: d.label, database: d.database, days: [] };
    s.days.push(d);
    map.set(key, s);
  }
  const out = [...map.values()];
  for (const s of out) s.days.sort((a, b) => Date.parse(b.incident.start) - Date.parse(a.incident.start));
  return out.sort((a, b) => a.kind.localeCompare(b.kind) || a.label.localeCompare(b.label));
}

/** The day read most recently: the highest id, or the latest read time when ids are equal. A list in any order gives the same answer. */
export function newestDay(days: LaneDay[]): LaneDay | null {
  let best: LaneDay | null = null;
  for (const d of days) if (best === null || d.id > best.id || (d.id === best.id && Date.parse(d.at) > Date.parse(best.at))) best = d;
  return best;
}

/** Whether a source can be read for another day. Mongo's profiler keeps only the latest entries, so it cannot. */
export const readableByDay = (kind: 'sql' | 'mongo'): boolean => kind === 'sql';

/**
 * A read that answered but found nothing to read: no check ran, none failed, and none has a permission to fix.
 * (A missing permission has its own screen: the check says what to grant.) The case is not broken; the hours held nothing.
 */
export const nothingRead = (lane: LaneResult | null): boolean =>
  lane !== null && lane.checks.length > 0 && lane.checks.every((c) => c.state === 'Cannot test yet' && c.fix === null);

// ---- failures ---------------------------------------------------------------------------------------------------------------------

/**
 * The sentence for a failed read. 409 is busy, but also the overlap and the 21st connection: those carry their own sentence from the
 * server, which says nothing was read or removed, so it is shown as it is. 400 shows the server's message. Nothing else is passed on.
 */
export function runProblem(error: unknown): { text: string; gone: boolean } {
  const gone = isCaseGone(error);
  const status = error instanceof CaseApiError || error instanceof ApiError ? (error.status ?? 0) : 0;
  const message = error instanceof Error ? error.message : '';
  // A closed case refuses a read before any server is asked; the reason says so, whatever the status words are.
  if (error instanceof CaseApiError && error.reason === 'case-closed') return { text: RUN_TEXT.closed, gone };
  // No session token (or a refused one) is not a failed read: the page has to be reopened, and the sentence says how.
  if (error instanceof CaseApiError && (error.reason === 'no_session' || error.status === 401)) return { text: RUN_TEXT.signedOut, gone };
  if (status === 409) return { text: /already/i.test(message) ? message : RUN_TEXT.busy, gone };
  if (status === 429) return { text: RUN_TEXT.limited, gone };
  if (status === 423) return { text: RUN_TEXT.locked, gone };
  if (status === 503) return { text: RUN_TEXT.demo, gone };
  if (status === 400 && message !== '' && message !== 'Request validation failed.') return { text: message, gone };
  return { text: RUN_TEXT.failed, gone };
}
