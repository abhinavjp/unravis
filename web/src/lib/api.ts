import type { Analysis, Breakdown, BreakdownDim, ConnectionInput, ConnectionTestResult, CoverageRefused, CoverageReport, Info, Page, Payload, Query, RecentConnection, RecordDetail, RememberConnectionInput, Source, SourceInput } from './types';

const token = document.querySelector<HTMLMetaElement>('meta[name="audit-session"]')?.content ?? '';
const base = '/api';

export class ApiError extends Error {
  readonly code?: string;
  readonly stage?: string;
  readonly status?: number;
  constructor(message: string, code?: string, stage?: string, status?: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.stage = stage;
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('X-Audit-Token', token);
  if (init.body) headers.set('Content-Type', 'application/json');
  const response = await fetch(base + path, { ...init, headers, credentials: 'same-origin', signal });
  if (!response.ok) {
    let message = 'Request failed (' + response.status + ')';
    let code: string | undefined;
    let stage: string | undefined;
    try {
      const body = await response.json() as { error?: string; code?: string; stage?: string };
      if (body.error) message = body.error;
      if (body.code) code = body.code;
      if (body.stage) stage = body.stage;
    } catch { /* keep status message */ }
    throw new ApiError(message, code, stage, response.status);
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
  analyze: (query: Query, signal?: AbortSignal) => request<Analysis>('/analyze', json(query), signal),
  breakdown: (query: Query, dim: BreakdownDim, top = 20, signal?: AbortSignal) =>
    request<Breakdown>('/breakdown', json({ query, dim, top }), signal),
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
    const response = await fetch(base + '/export', {
      method: 'POST', credentials: 'same-origin', signal,
      headers: { 'X-Audit-Token': token, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, apiKey, cursor: '', limit: 100, state, ...(order ? { order } : {}) })
    });
    if (!response.ok) {
      let message = 'Export failed (' + response.status + ')';
      try { message = ((await response.json()) as { error?: string }).error ?? message; } catch { /* status is enough */ }
      throw new Error(message);
    }
    return { blob: await response.blob(), limited: response.headers.get('X-Export-Truncated') === 'true' };
  }
};
