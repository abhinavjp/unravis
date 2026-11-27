import { casePath, fetchSeries, json, listImports, send } from '../cases';
import type { SeriesItem } from '../resource';
import { BB_SOURCE, EVENT_KINDS, laneById } from './bbLanes';

/**
 * What the Blackbox lanes read for one window: the series of the chosen lanes (GET /series) and the recorder's events
 * (GET /events), both from the case's own files. Nothing is sent to the server except the selectors and the window.
 */

export interface BbEvent { kind: string; start: number; end: number }

export interface BbData {
  fromMs: number;
  toMs: number;
  bucketMs: number;
  /** The answer per lane id. A lane with no figure in the window has no entry. */
  series: Map<string, SeriesItem>;
  events: BbEvent[];
  /** The server stopped a read at its limit, so a lane or the event list may be incomplete. */
  partial: boolean;
}

/** Whether the case holds any Blackbox recording file. */
export async function caseHasBlackbox(caseId: string, signal?: AbortSignal): Promise<boolean> {
  const files = await listImports(caseId, signal);
  return files.some((f) => f.importer === BB_SOURCE);
}

const ms = (v: unknown): number => {
  const t = typeof v === 'string' ? Date.parse(v) : NaN;
  return Number.isFinite(t) ? t : NaN;
};

export function eventsPath(caseId: string, fromMs: number, toMs: number): string {
  const q = new URLSearchParams({ from: new Date(fromMs).toISOString(), to: new Date(toMs).toISOString(), source: BB_SOURCE, kind: EVENT_KINDS.join(','), limit: '5000' });
  return casePath(caseId, '/events') + '?' + q.toString();
}

/** The recorder's events in the window; a row that cannot be read is dropped, never guessed. */
export async function fetchBbEvents(caseId: string, fromMs: number, toMs: number, signal?: AbortSignal): Promise<{ events: BbEvent[]; truncated: boolean }> {
  const body = await json<{ events?: unknown; truncated?: unknown }>(await send(eventsPath(caseId, fromMs, toMs), { method: 'GET' }, signal));
  const rows = body && Array.isArray(body.events) ? body.events : [];
  const events: BbEvent[] = [];
  for (const row of rows) {
    if (!row || typeof row !== 'object') continue;
    const r = row as Record<string, unknown>;
    const start = ms(r.start);
    if (typeof r.kind !== 'string' || Number.isNaN(start)) continue;
    const end = ms(r.end);
    events.push({ kind: r.kind, start, end: Number.isNaN(end) ? start : end });
  }
  events.sort((a, b) => a.start - b.start);
  return { events, truncated: !!body && body.truncated === true };
}

/** Reads the chosen lanes and the events for a window. `width` is the chart width in pixels (the server cuts to it). */
export async function fetchBbData(caseId: string, laneIds: readonly string[], fromMs: number, toMs: number, width: number, signal?: AbortSignal): Promise<BbData> {
  const sels: string[] = [];
  const ids: string[] = [];
  for (const id of laneIds) {
    const def = laneById(id);
    if (def && def.sel) {
      sels.push(def.sel);
      ids.push(id);
    }
  }
  const [view, ev] = await Promise.all([
    sels.length > 0 ? fetchSeries(caseId, { fromMs, toMs, bucketMs: 0, sels, width }, signal) : Promise.resolve(null),
    fetchBbEvents(caseId, fromMs, toMs, signal)
  ]);
  const series = new Map<string, SeriesItem>();
  if (view) view.series.forEach((item, i) => { if (ids[i]) series.set(ids[i], item); });
  const bucketMs = view ? view.bucketMs : Math.max(1000, Math.ceil((toMs - fromMs) / Math.max(1, width)));
  return { fromMs, toMs, bucketMs, series, events: ev.events, partial: !!view?.partial || ev.truncated };
}
