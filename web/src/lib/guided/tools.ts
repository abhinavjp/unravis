import type { Frame } from './frame';
import { fmtChange, fmtMs, markFor, stepOf, suggestedChecks, suspectGroups, type ChangeMark, type StickerTone } from './stepModel';
import type { ChecklistItem, Readiness, Result, SavedView } from './types';

/**
 * Manual tools: the same facts as the guided steps, one tool at a time, in any order. This file lists the tools, says whether each one can run
 * with the sources the case has, and reads and writes the settings of a saved view. A tool that is missing its source says so and shows nothing
 * rather than a guess.
 */

export type SourceNeed = 'audit' | 'iis' | 'sql' | 'mongo' | 'host';

export const NEED_WORD: Readonly<Record<SourceNeed, string>> = {
  audit: 'an audit source', iis: 'an IIS log source', sql: 'a SQL Server source', mongo: 'a MongoDB source', host: 'a Blackbox source'
};

export type ToolId =
  | 'response-time' | 'slowest-apis' | 'slowest-calls' | 'time-split' | 'calls-in-flight' | 'nearby-activity' | 'changes' | 'sql-waits' | 'mongo-health'
  | 'host-resources' | 'sql-host' | 'after-fix' | 'suspect-summary';

export interface ToolDef {
  id: ToolId;
  title: string;
  blurb: string;
  /** The guided step whose page this tool shows, if it shows one. */
  step?: number;
  needs: SourceNeed[];
  /** Sources shown on the card, in words. */
  sources: string;
}

export const TOOLS: readonly ToolDef[] = [
  { id: 'response-time', title: 'Response time over time', blurb: 'How the slow end moved, minute by minute, and which APIs changed.', step: 2, needs: ['audit'], sources: 'Audit' },
  { id: 'slowest-apis', title: 'Slowest APIs', blurb: 'APIs ranked by their slow end, against the normal period.', needs: ['audit'], sources: 'Audit' },
  { id: 'slowest-calls', title: 'Slowest calls', blurb: 'The individual calls that took longest in a window.', needs: ['audit'], sources: 'Audit' },
  { id: 'time-split', title: 'Time split A C P O', blurb: 'Where the time of a call went: audit set-up, action, payload record, outside time.', step: 3, needs: ['audit', 'iis'], sources: 'Audit, IIS' },
  { id: 'calls-in-flight', title: 'Calls in flight', blurb: 'How many calls were open at once, against the pool limit if you enter one.', step: 4, needs: ['audit'], sources: 'Audit' },
  { id: 'nearby-activity', title: 'Nearby activity', blurb: 'What every source recorded around one moment.', needs: ['audit'], sources: 'Audit, IIS, SQL Server, MongoDB, Host' },
  { id: 'changes', title: 'Changes timeline', blurb: 'Releases and changes you list, set beside the minute things moved.', step: 5, needs: ['audit'], sources: 'Audit, your change list' },
  { id: 'sql-waits', title: 'SQL blocking and waits', blurb: 'Blocking and waits recorded on SQL Server.', step: 7, needs: ['sql'], sources: 'SQL Server' },
  { id: 'mongo-health', title: 'MongoDB health', blurb: 'Slow operations, queues and the state of the replica set.', step: 8, needs: ['mongo'], sources: 'MongoDB' },
  { id: 'host-resources', title: 'Host CPU, memory and disk', blurb: 'What the application host recorded.', step: 9, needs: ['host'], sources: 'Blackbox' },
  { id: 'sql-host', title: 'SQL host disk, memory and network', blurb: 'What the SQL Server host recorded.', step: 9, needs: ['host'], sources: 'Blackbox on the SQL host' },
  { id: 'after-fix', title: 'After the fix', blurb: 'The same hours on a later day, set beside the incident day.', needs: ['audit'], sources: 'Audit' },
  { id: 'suspect-summary', title: 'Suspect summary', blurb: 'Every suspect with its colour and its cheapest check.', step: 10, needs: ['audit'], sources: 'Audit and any other source in the case' }
];

export const toolById = (id: string): ToolDef | undefined => TOOLS.find((t) => t.id === id);

/** The four fixed readiness words, plus Waiting for a tool that has nothing to show until the steps have been run. */
export type ToolWord = Readiness | 'Waiting';

export interface ToolState { word: ToolWord; missing: SourceNeed[]; why: string }

/** Tools that read the audit source on their own, with their own button. They need no guided run, so they are never Waiting. */
const READS_ITSELF: ReadonlySet<ToolId> = new Set<ToolId>(['slowest-calls', 'nearby-activity']);

export interface ToolContext {
  /** The kinds of source the case holds. The audit source is the one every guided tool reads. */
  have: ReadonlySet<SourceNeed>;
  result: Result | null | undefined;
}

/**
 * Whether a tool can run. A tool whose source is missing is "Cannot test yet" and names the source. With an engine answer, a tool that shows a
 * step takes the step's own word. Without an engine answer a tool that reads the run is Waiting; Slowest calls and Nearby activity are Ready, and
 * the time split without IIS is Partly ready.
 */
export function toolState(tool: ToolDef, ctx: ToolContext): ToolState {
  // IIS only completes the time split, so beside the audit source it is a soft need: a tool that lists both runs without it.
  const soft = (n: SourceNeed) => n === 'iis' && tool.needs.length > 1;
  const absent = tool.needs.filter((n) => !soft(n) && !ctx.have.has(n));
  if (absent.length) return { word: 'Cannot test yet', missing: absent, why: `This tool needs ${NEED_WORD[absent[0]]}, and this case has none.` };
  const step = tool.step !== undefined ? stepOf(ctx.result, tool.step) : undefined;
  if (step) return { word: step.readiness, missing: [], why: step.reasons[0] ?? '' };
  const partly = tool.needs.some((n) => soft(n) && !ctx.have.has(n));
  if (partly) return { word: 'Partly ready', missing: [], why: 'The audit part runs. The outside time needs an IIS log source.' };
  // Source present, nothing run yet: a card with no numbers behind it must not read as a pass.
  if (!ctx.result && !READS_ITSELF.has(tool.id)) return { word: 'Waiting', missing: [], why: '' };
  return { word: 'Ready', missing: [], why: '' };
}

const WORD_TONE: Readonly<Record<ToolWord, StickerTone>> = {
  Ready: 'ready', 'Partly ready': 'partly', 'Cannot test yet': 'cannot', Failed: 'failed', Waiting: 'waiting'
};

export interface ToolSticker { word: ToolWord; tone: StickerTone; outOfDate: boolean }

/**
 * The sticker of a tool card or of the open tool. `stale` is the store's out-of-date flag. A tool that cannot run, or has not run, has nothing
 * to be out of date, so only Ready, Partly ready and Failed carry the Out of date mark.
 */
export function toolSticker(state: ToolState, stale: boolean): ToolSticker {
  const mark = stale && state.word !== 'Cannot test yet' && state.word !== 'Waiting';
  return { word: state.word, tone: WORD_TONE[state.word], outOfDate: mark };
}

/* ----------------------------------------------------------------- saved views */

export const MAX_VIEW_NAME = 60;
export const MAX_VIEWS = 200;

/** The plain text of a view name: control characters become spaces, runs of space collapse, the ends are trimmed. */
// eslint-disable-next-line no-control-regex
const plainName = (raw: string): string => raw.replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim();

/** A view name is shown in lists and in exports, so it is trimmed to plain text. Returns '' when nothing usable is left. */
export function cleanViewName(raw: string): string {
  return Array.from(plainName(raw)).slice(0, MAX_VIEW_NAME).join('');
}

/** True when the name, once plain, is longer than a view name may be (counted in characters, as the server counts). */
export const viewNameTooLong = (raw: string): boolean => Array.from(plainName(raw)).length > MAX_VIEW_NAME;

export const MAX_ITEM_TEXT = 300;

/** True when a checklist item or note is longer than the server accepts (300 characters once trimmed). */
export const itemTooLong = (raw: string): boolean => Array.from(raw.trim()).length > MAX_ITEM_TEXT;

export interface ViewSettings {
  incidentStart?: string;
  incidentEnd?: string;
  referenceStart?: string;
  referenceEnd?: string;
  route?: string;
  /** Slowest APIs: the smallest number of calls an API needs to be listed. */
  minCalls?: number;
  /** Slowest APIs: only APIs whose name contains this text. */
  match?: string;
}

const KEYS: ReadonlyArray<keyof ViewSettings> = ['incidentStart', 'incidentEnd', 'referenceStart', 'referenceEnd', 'route', 'minCalls', 'match'];
const TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;

/**
 * The settings of a view: windows, the one API and the tool's own filters. Nothing else is ever saved, so a view holds no result, no row of
 * data and nothing that looks like a secret. Text is cut short and a value with a URL in it is dropped.
 */
export function viewSettings(frame: Frame, extra: Pick<ViewSettings, 'minCalls' | 'match'> = {}): Record<string, unknown> {
  const out: ViewSettings = {
    incidentStart: frame.incidentStart, incidentEnd: frame.incidentEnd, referenceStart: frame.referenceStart, referenceEnd: frame.referenceEnd
  };
  // The server refuses a value with a URL in it, so the view is saved without the API rather than not saved at all.
  if (frame.route && frame.route.length <= 256 && !frame.route.includes('://')) out.route = frame.route;
  if (typeof extra.minCalls === 'number' && Number.isFinite(extra.minCalls)) out.minCalls = Math.max(0, Math.min(1_000_000, Math.floor(extra.minCalls)));
  if (typeof extra.match === 'string' && extra.match && !extra.match.includes('://')) out.match = extra.match.slice(0, 100);
  return out as Record<string, unknown>;
}

/** Reads a saved view back. Unknown keys, wrong types and malformed times are ignored, so a damaged view opens with what is good in it. */
export function readViewSettings(raw: Record<string, unknown> | undefined): ViewSettings {
  const out: ViewSettings = {};
  if (!raw || typeof raw !== 'object') return out;
  for (const key of KEYS) {
    const value = raw[key];
    if (key === 'minCalls') { if (typeof value === 'number' && Number.isFinite(value) && value >= 0) out.minCalls = Math.floor(value); continue; }
    if (typeof value !== 'string') continue;
    if (key === 'route' || key === 'match') { if (value.length <= 256 && !value.includes('://')) out[key] = value; continue; }
    if (TIME.test(value)) out[key] = value;
  }
  return out;
}

/** A saved view's windows applied to a frame. A view that holds no window leaves the frame as it is. */
export function applyViewToFrame(frame: Frame, settings: ViewSettings): Frame {
  return {
    ...frame,
    incidentStart: settings.incidentStart ?? frame.incidentStart, incidentEnd: settings.incidentEnd ?? frame.incidentEnd,
    referenceStart: settings.referenceStart ?? frame.referenceStart, referenceEnd: settings.referenceEnd ?? frame.referenceEnd,
    route: settings.route ?? frame.route
  };
}

export const viewsFull = (views: readonly SavedView[]): boolean => views.length >= MAX_VIEWS;

/** The windows of a view as one short line for the list: `26 Oct 10:30 to 11:00`. */
export function viewWindows(settings: ViewSettings): string {
  const part = (s?: string, e?: string) => (s && e ? s.replace('T', ' ') + ' to ' + e.replace('T', ' ') : '');
  const inc = part(settings.incidentStart, settings.incidentEnd);
  return inc || 'No window saved';
}

/* ----------------------------------------------------------------- slowest APIs */

export interface SlowApiRow { route: string; calls: number; p95: string; reference: string; change: string; mark: ChangeMark }

/**
 * The Slowest APIs tool: every API of the last run ranked by its slow end in the incident window. An API with fewer calls than `minCalls`, or whose
 * name does not contain `match`, is left out. An API with no slow end (too few calls) goes last, never first.
 */
export function slowestApiRows(result: Result | null | undefined, minCalls: number, match: string): SlowApiRow[] {
  if (!result) return [];
  const needle = match.trim().toLowerCase();
  const min = Number.isFinite(minCalls) && minCalls > 0 ? Math.floor(minCalls) : 0;
  const key = (p?: number) => (typeof p === 'number' && Number.isFinite(p) ? p : -Infinity);
  return result.facts.routes
    .filter((r) => r.incident.n >= min && (!needle || r.route.toLowerCase().includes(needle)))
    .sort((a, b) => key(b.incident.p95) - key(a.incident.p95) || a.route.localeCompare(b.route))
    .map((r) => ({
      route: r.route, calls: r.incident.n, p95: fmtMs(r.incident.p95), reference: fmtMs(r.reference.p95), change: fmtChange(r.p95Change), mark: markFor(r.p95Change)
    }));
}

/* ----------------------------------------------------------------- what the case holds */

export interface StampImport { id: number; importer: string; status: string }
export interface StampTag { id: number; kind: string }

/**
 * A short fingerprint of what a case holds: the ids of its kept files (status ok) and of its source tags, sorted. Two reads of the same case give
 * the same text; a new file, a removed file or a new source tag changes it. It holds ids only, no name, path or setting.
 */
export function dataStamp(imports: readonly StampImport[], tags: readonly StampTag[]): string {
  return [...imports.filter((i) => i.status === 'ok').map((i) => 'i' + i.id), ...tags.map((t) => 't' + t.id)].sort().join(',');
}

/** True only when both fingerprints are known and differ. An unknown fingerprint is never a change. */
export const stampChanged = (then: string | null, now: string | null): boolean => then !== null && now !== null && then !== now;

/* ----------------------------------------------------------------- the sources of one case */

export interface CaseSourceLike { id: string; database: string; selectionKey?: string }
export interface CaseWorkspaceDatabase { name: string; selectionKey?: string }

/**
 * The ids of the connected sources that belong to the open case. The case's own audit source tags name databases (`auditLabels`); a source
 * belongs to the case when its database is one of them. A case with no tag of this kind (an older case) falls back to the databases its saved
 * workspace names, matched by selection key when both sides have one and by database name otherwise. Nothing else is read: with neither, the list is empty.
 */
export function caseSourceIds(
  sources: readonly CaseSourceLike[], auditLabels: readonly string[], workspaceDatabases: readonly CaseWorkspaceDatabase[]
): string[] {
  if (auditLabels.length > 0) {
    const labels = new Set(auditLabels);
    return sources.filter((s) => labels.has(s.database)).map((s) => s.id);
  }
  return sources
    .filter((s) => workspaceDatabases.some((d) => (d.selectionKey && s.selectionKey ? d.selectionKey === s.selectionKey : d.name === s.database)))
    .map((s) => s.id);
}

/* ----------------------------------------------------------------- Step 10 checks and lines */

/**
 * The checks "Add suggested checks from Step 10" adds: those of the red and orange suspects not on the list yet. The origin's one cheapest
 * check is the first of them. Grey suspects are left out on purpose; each has its own button.
 */
export function originChecks(result: Result, items: readonly ChecklistItem[]): Array<{ text: string; suspect: string }> {
  const ranked = new Set(suspectGroups(result).ranked.map((s) => s.id));
  return suggestedChecks(result, items).filter((check) => ranked.has(check.suspect));
}

/** True when Confidence reads None and an orange (Likely) suspect is listed: the case Step 10 explains with one line. */
export function likelyWithoutTier(result: Result | null | undefined): boolean {
  return !!result && result.confidence.word === 'None' && suspectGroups(result).ranked.some((s) => s.colour === 'orange');
}
