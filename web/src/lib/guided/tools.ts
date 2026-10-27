import type { Frame } from './frame';
import { fmtChange, fmtMs, markFor, stepOf, type ChangeMark } from './stepModel';
import type { Readiness, Result, SavedView } from './types';

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

export interface ToolState { word: Readiness; missing: SourceNeed[]; why: string }

export interface ToolContext {
  /** The kinds of source the case holds. The audit source is the one every guided tool reads. */
  have: ReadonlySet<SourceNeed>;
  result: Result | null | undefined;
}

/**
 * Whether a tool can run. A tool whose source is missing is "Cannot test yet" and names the source. With an engine answer, a tool that shows a
 * step takes the step's own word; without one it is "Ready" only when every source it needs is in the case.
 */
export function toolState(tool: ToolDef, ctx: ToolContext): ToolState {
  // IIS only completes the time split, so beside the audit source it is a soft need: a tool that lists both runs without it.
  const soft = (n: SourceNeed) => n === 'iis' && tool.needs.length > 1;
  const absent = tool.needs.filter((n) => !soft(n) && !ctx.have.has(n));
  if (absent.length) return { word: 'Cannot test yet', missing: absent, why: `This tool needs ${NEED_WORD[absent[0]]}, and this case has none.` };
  const step = tool.step !== undefined ? stepOf(ctx.result, tool.step) : undefined;
  if (step) return { word: step.readiness, missing: [], why: step.reasons[0] ?? '' };
  const partly = tool.needs.some((n) => soft(n) && !ctx.have.has(n));
  return { word: partly ? 'Partly ready' : 'Ready', missing: [], why: partly ? 'The audit part runs. The outside time needs an IIS log source.' : '' };
}

/* ----------------------------------------------------------------- saved views */

export const MAX_VIEW_NAME = 60;
export const MAX_VIEWS = 200;

/** A view name is shown in lists and in exports, so it is trimmed to plain text. Returns '' when nothing usable is left. */
export function cleanViewName(raw: string): string {
  // eslint-disable-next-line no-control-regex
  return raw.replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, MAX_VIEW_NAME);
}

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
    incidentStart: frame.incidentStart, incidentEnd: frame.incidentEnd, referenceStart: frame.referenceStart, referenceEnd: frame.referenceEnd,
    route: frame.route
  };
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
