import type { Check, Result, StepEvent, UIChanges, UIState, WorkRequest } from './logic.ts';

// The setup window talks only to the setup program that opened it. The key comes in the address fragment (#k=...), is kept for this
// tab only and is taken out of the address bar at once, the way the app handles its launch token.

const KEY_STORE = 'unravis-setup-key';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

function readKey(): string {
  const match = /[#&]k=([^&]+)/.exec(location.hash);
  if (match) {
    const key = decodeURIComponent(match[1]);
    try { sessionStorage.setItem(KEY_STORE, key); } catch { /* a private window keeps it in memory only */ }
    history.replaceState(null, '', location.pathname + location.search);
    return key;
  }
  try { return sessionStorage.getItem(KEY_STORE) ?? ''; } catch { return ''; }
}

const key = readKey();
// Screenshot builds only: ?scenario=<name> picks one of the fake runs. A real setup ignores the header.
const scenario = new URLSearchParams(location.search).get('scenario') ?? '';

async function request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'X-Setup-Key': key };
  if (scenario) headers['X-Setup-Scenario'] = scenario;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let res: Response;
  try {
    res = await fetch('/api/' + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store' });
  } catch {
    throw new ApiError(0, 'Setup has stopped. Close this window and start setup again.');
  }
  let data: unknown = null;
  try { data = await res.json(); } catch { /* not JSON */ }
  if (!res.ok) {
    const message = data && typeof data === 'object' && 'error' in data && typeof data.error === 'string' ? data.error : 'Setup could not do that (' + res.status + ').';
    throw new ApiError(res.status, message);
  }
  return data as T;
}

export interface Progress { events: StepEvent[]; next: number; running: boolean; result: Result | null }

export const api = {
  hasKey: () => key !== '',
  state: () => request<UIState>('GET', 'state'),
  preflight: (req: WorkRequest) => request<{ checks: Check[]; changes: UIChanges }>('POST', 'preflight', req),
  run: (req: WorkRequest) => request<{ ok: boolean }>('POST', 'run', req),
  progress: (since: number) => request<Progress>('GET', 'progress?since=' + since),
  cancel: () => request<{ ok: boolean }>('POST', 'cancel'),
  openApp: () => request<{ ok: boolean }>('POST', 'open-app'),
  openLogFolder: () => request<{ ok: boolean }>('POST', 'open-log-folder'),
  close: () => request<{ ok: boolean }>('POST', 'close')
};
