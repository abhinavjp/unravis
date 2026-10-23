import type { Analysis, ConnectionInput, ConnectionTestResult, Info, Page, Payload, Query, RecentConnection, RecordDetail, RememberConnectionInput, Source, SourceInput } from './types';

const token = document.querySelector<HTMLMetaElement>('meta[name="audit-session"]')?.content ?? '';
const base = '/api';

export class ApiError extends Error {
  readonly code?: string;
  readonly stage?: string;
  constructor(message: string, code?: string, stage?: string) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.stage = stage;
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
    throw new ApiError(message, code, stage);
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
  analyze: (query: Query, signal?: AbortSignal) => request<Analysis>('/analyze', json(query), signal),
  slowest: (query:Query,signal?:AbortSignal)=>request<Page>('/slowest',json({query,limit:50,apiKey:'',cursor:''}),signal),
  /** state narrows the page to one quality state (complete, incomplete, invalid, pending, unclassified); an empty apiKey pages the whole query. */
  requests: (query: Query, apiKey: string, cursor = '', signal?: AbortSignal, state = '') =>
    request<Page>('/requests', json({ query, apiKey, cursor, limit: 100, state }), signal),
  payload: (sourceId: string, id: string, signal?: AbortSignal) =>
    request<Payload>('/payload', json({ sourceId, id }), signal),
  record: (sourceId: string, id: string, signal?: AbortSignal) =>
    request<RecordDetail>('/record', json({ sourceId, id }), signal),
  activity: (input: import('./types').ActivityInput, signal?: AbortSignal) => request<Page>('/activity', json(input), signal),
  export: async (query: Query, apiKey: string, signal?: AbortSignal, state = '') => {
    const response = await fetch(base + '/export', {
      method: 'POST', credentials: 'same-origin', signal,
      headers: { 'X-Audit-Token': token, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, apiKey, cursor: '', limit: 100, state })
    });
    if (!response.ok) {
      let message = 'Export failed (' + response.status + ')';
      try { message = ((await response.json()) as { error?: string }).error ?? message; } catch { /* status is enough */ }
      throw new Error(message);
    }
    return { blob: await response.blob(), limited: response.headers.get('X-Export-Truncated') === 'true' };
  }
};
