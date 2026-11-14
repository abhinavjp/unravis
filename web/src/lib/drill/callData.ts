import { casePath, json, send } from '../cases';

// Imported SQL Server and HTTP.sys events for the time window of one slow call.
// Reads the existing events endpoint; only the fields the screen needs are kept, so record text never travels further.

export const EVENT_SOURCES = ['sql-xe', 'sql-errorlog', 'iis-httperr'] as const;
export const EVENT_LIMIT = 500;

export interface CallEvent {
  evidenceId: string;
  source: string;
  kind: string;
  host?: string;
  start: string;
  end?: string | null;
  durationMs?: number | null;
  severity?: string;
}

export interface CallEvents { events: CallEvent[]; truncated: boolean }

/** The events address for a window. It never asks for the sensitive extras. */
export function eventsPath(caseId: string, fromIso: string, toIso: string): string {
  const query = new URLSearchParams({ from: fromIso, to: toIso, source: EVENT_SOURCES.join(','), limit: String(EVENT_LIMIT) });
  return casePath(caseId, '/events') + '?' + query.toString();
}

const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const optionalText = (value: unknown): string | undefined => (typeof value === 'string' ? value : undefined);
const optionalNumber = (value: unknown): number | null => (typeof value === 'number' && Number.isFinite(value) ? value : null);

function toCallEvent(row: unknown): CallEvent | null {
  if (!row || typeof row !== 'object') return null;
  const r = row as Record<string, unknown>;
  const event: CallEvent = {
    evidenceId: text(r.evidenceId),
    source: text(r.source),
    kind: text(r.kind),
    start: text(r.start),
    end: typeof r.end === 'string' ? r.end : null,
    durationMs: optionalNumber(r.durationMs)
  };
  const host = optionalText(r.host);
  if (host !== undefined) event.host = host;
  const severity = optionalText(r.severity);
  if (severity !== undefined) event.severity = severity;
  return event;
}

export async function fetchCallEvents(caseId: string, fromIso: string, toIso: string, signal?: AbortSignal): Promise<CallEvents> {
  const body = await json<{ events?: unknown; truncated?: unknown }>(await send(eventsPath(caseId, fromIso, toIso), { method: 'GET' }, signal));
  if (!body || !Array.isArray(body.events)) return { events: [], truncated: false };
  const events = body.events.map(toCallEvent).filter((e): e is CallEvent => e !== null);
  return { events, truncated: body.truncated === true };
}
