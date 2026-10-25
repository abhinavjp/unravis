import type { Analysis, Breakdown, BreakdownDim, ConnectionInput, ConnectionTestResult, CoverageRefused, CoverageReport, Info, Page, Payload, Query, RecentConnection, RecordDetail, RememberConnectionInput, Source, SourceInput, PatternInput, PatternReport, EndpointDaily, Hypothesis, HypothesisInput, ExportPreviewInput, ExportPreview, ExportFile } from './types';
import { rollupPath, type DailyResponse, type HourGridResponse, type RollupParams } from './rollup';

const base = '/api';

/** Shown whenever this tab has no session token, or the dashboard refused it. */
export const NO_SESSION_MESSAGE = 'Open Unravis from the link it printed when it started, or restart it.';
const SESSION_KEY = 'unravis.session';

function storage(): Storage | null {
  try { return typeof sessionStorage === 'undefined' ? null : sessionStorage; } catch { return null; }
}

/**
 * The dashboard never writes its session token into the page. The launch link
 * carries it in the fragment (#t=...), which the browser does not send to the
 * server. It is read once, kept for this tab (sessionStorage survives a reload,
 * not a new tab), and removed from the address bar and history entry.
 */
export function readSessionToken(): string {
  let fromLink = '';
  try {
    if (typeof location !== 'undefined' && location.hash.length > 1) {
      const params = new URLSearchParams(location.hash.slice(1));
      fromLink = params.get('t') ?? '';
      if (params.has('t') && typeof history !== 'undefined') history.replaceState(history.state, '', location.pathname + location.search);
    }
  } catch { /* an unreadable fragment is the same as none */ }
  const store = storage();
  if (fromLink) {
    try { store?.setItem(SESSION_KEY, fromLink); } catch { /* the token still works for this page */ }
    return fromLink;
  }
  try { return store?.getItem(SESSION_KEY) ?? ''; } catch { return ''; }
}

let token = readSessionToken();

/** Whether this tab holds a session token right now. A refused token is dropped, so read this before a call to know what the tab had. */
export const hasSessionToken = (): boolean => token !== '';

/** Drops the token this tab holds, in memory and in sessionStorage, once the dashboard has refused it. */
export function forgetSession() {
  token = '';
  try { storage()?.removeItem(SESSION_KEY); } catch { /* nothing stored */ }
}

function noSession(): ApiError {
  return new ApiError(NO_SESSION_MESSAGE, 'no_session', undefined, 401);
}

/** The server's bare 400 for a filter or window it could not use. It names nothing, so the screen says what to check. */
export const VALIDATION_MESSAGE = 'The server could not use these filters or this time window. Check them and try again.';
const BARE_VALIDATION = 'Request validation failed.';
export const plainApiMessage = (status: number, message: string): string => (status === 400 && message.trim() === BARE_VALIDATION ? VALIDATION_MESSAGE : message);

export class ApiError extends Error {
  readonly code?: string;
  readonly stage?: string;
  readonly status?: number;
  /** Plain-language reasons a guard blocked a ledger change (422, code guard_blocked). */
  readonly reasons?: string[];
  constructor(message: string, code?: string, stage?: string, status?: number, reasons?: string[]) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.stage = stage;
    this.status = status;
    this.reasons = reasons;
  }
}

async function request<T>(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<T> {
  if (!token) throw noSession();
  const headers = new Headers(init.headers);
  headers.set('X-Audit-Token', token);
  if (init.body) headers.set('Content-Type', 'application/json');
  const response = await fetch(base + path, { ...init, headers, credentials: 'same-origin', signal });
  if (response.status === 401) {
    forgetSession();
    throw noSession();
  }
  if (!response.ok) {
    let message = 'Request failed (' + response.status + ')';
    let code: string | undefined;
    let stage: string | undefined;
    let reasons: string[] | undefined;
    try {
      const body = await response.json() as { error?: string; code?: string; stage?: string; reasons?: string[] };
      if (body.error) message = body.error;
      if (body.code) code = body.code;
      if (body.stage) stage = body.stage;
      if (Array.isArray(body.reasons)) reasons = body.reasons;
    } catch { /* keep status message */ }
    throw new ApiError(plainApiMessage(response.status, message), code, stage, response.status, reasons);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

const json = (value: unknown): RequestInit => ({ method: 'POST', body: JSON.stringify(value) });
export const api = {
  info: () => request<Info>('/info'),
  sources: () => request<Source[]>('/sources'),
  recentConnections: () => request<RecentConnection[]>('/connections/recent'),
  rememberConnection: (input: RememberConnectionInput) => request<RecentConnection>('/connections/remember', json(input)),
  forgetConnection: (id: string) => request<void>('/connections/recent/' + encodeURIComponent(id), { method: 'DELETE' }),
  testConnection: (input: ConnectionInput, signal?: AbortSignal) => request<ConnectionTestResult>('/connections/test', {
    method: 'POST', body: JSON.stringify(input)
  }, signal),
  addSource: (input: SourceInput) => request<Source>('/sources', json(input)),
  deleteSource: (id: string) => request<void>('/sources/' + encodeURIComponent(id), { method: 'DELETE' }),
  /** The coverage census. It is never run by itself. A 409 means the server refused a broad scan on a live source, and that comes back as { refused }. */
  coverage: async (sourceId: string, start: string, end: string, signal?: AbortSignal): Promise<CoverageReport | CoverageRefused> => {
    try {
      return await request<CoverageReport>('/sources/' + encodeURIComponent(sourceId) + '/coverage?' + new URLSearchParams({ start, end }).toString(), {}, signal);
    } catch (cause) {
      if (cause instanceof ApiError && (cause.status === 409 || cause.code === 'broad_scan_refused')) return { refused: cause.message };
      throw cause;
    }
  },
  /** P2-lite incident patterns for one window (rules B1, B2, B6, B12). */
  patterns: (input: PatternInput, signal?: AbortSignal) => request<PatternReport>('/patterns', json(input), signal),
  /** The daily p95 and count of one operation, one entry for each whole local day. A live source is refused beyond 14 days (409, code broad_scan_refused). */
  endpointDaily: (operation: string, sourceIds: string[], start: string, end: string, tz: string, signal?: AbortSignal) =>
    request<EndpointDaily>('/endpoint-daily?' + new URLSearchParams({ op: operation, sources: sourceIds.join(','), start, end, ...(tz ? { tz } : {}) }).toString(), {}, signal),
  ledgerList: () => request<Hypothesis[]>('/ledger'),
  ledgerCreate: (text: string) => request<Hypothesis>('/ledger', json({ text })),
  /** A 422 ApiError with code guard_blocked carries .reasons. */
  ledgerUpdate: (id: string, input: HypothesisInput) => request<Hypothesis>('/ledger/' + encodeURIComponent(id), { method: 'PUT', body: JSON.stringify(input) }),
  ledgerReuse: (id: string) => request<Hypothesis>('/ledger/' + encodeURIComponent(id) + '/reuse', { method: 'POST' }),
  ledgerDelete: (id: string) => request<void>('/ledger/' + encodeURIComponent(id), { method: 'DELETE' }),
  exportPreview: (input: ExportPreviewInput) => request<ExportPreview>('/export/preview', json(input)),
  exportConfirm: (previewId: string) => request<ExportFile>('/export/confirm', json({ previewId })),
  /** The daily table and the hour grid: one read of the sources each, cached for a minute by the server. */
  daily: (params: RollupParams, signal?: AbortSignal) => request<DailyResponse>(rollupPath('daily', params), {}, signal),
  hourGrid: (params: RollupParams, signal?: AbortSignal) => request<HourGridResponse>(rollupPath('hourgrid', params), {}, signal),
  analyze: (query: Query, signal?: AbortSignal) => request<Analysis>('/analyze', json(query), signal),
  breakdown: (query: Query, dim: BreakdownDim, top = 20, signal?: AbortSignal, tz?: string) =>
    request<Breakdown>('/breakdown', json({ query, dim, top, ...(tz ? { tz } : {}) }), signal),
  slowest: (query:Query,signal?:AbortSignal,options:{cursor?:string;minMs?:number|null}={})=>request<Page>('/slowest',json({query,limit:50,apiKey:'',cursor:options.cursor??'',...(options.minMs==null?{}:{minMs:options.minMs})}),signal),
  /** state narrows the page to one quality state (complete, incomplete, invalid, pending, unclassified); an empty apiKey pages the whole query. */
  requests: (query: Query, apiKey: string, cursor = '', signal?: AbortSignal, state = '', order = '') =>
    request<Page>('/requests', json({ query, apiKey, cursor, limit: 100, state, ...(order ? { order } : {}) }), signal),
  payload: (sourceId: string, id: string, signal?: AbortSignal) =>
    request<Payload>('/payload', json({ sourceId, id }), signal),
  record: (sourceId: string, id: string, signal?: AbortSignal) =>
    request<RecordDetail>('/record', json({ sourceId, id }), signal),
  activity: (input: import('./types').ActivityInput, signal?: AbortSignal) => request<Page>('/activity', json(input), signal),
  export: async (query: Query, apiKey: string, signal?: AbortSignal, state = '', order = '') => {
    if (!token) throw noSession();
    const response = await fetch(base + '/export', {
      method: 'POST', credentials: 'same-origin', signal,
      headers: { 'X-Audit-Token': token, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, apiKey, cursor: '', limit: 100, state, ...(order ? { order } : {}) })
    });
    if (response.status === 401) {
      forgetSession();
      throw noSession();
    }
    if (!response.ok) {
      let message = 'Export failed (' + response.status + ')';
      try { message = ((await response.json()) as { error?: string }).error ?? message; } catch { /* status is enough */ }
      throw new Error(plainApiMessage(response.status, message));
    }
    return { blob: await response.blob(), limited: response.headers.get('X-Export-Truncated') === 'true' };
  }
};
