import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import {
  ServersError, failureFromResponse, FAILURE_TEXT, buildMachineBody, buildPinBody, buildReplaceBody, buildSecretBody, pulledFileUrl,
  type AddBody, type AuthKind, type HostKey, type JobView, type MachineView, type PatchBody, type PlanView, type PullBody, type ServerEntry, type TestResult
} from './servers';

/**
 * Calls to the Servers backend. Same token handling as the Blackbox calls. A refusal keeps what the server said and never what was sent,
 * so a password or key can not end up in an error, a log or the page.
 */

const base = '/api/servers';

async function send(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
  const token = readSessionToken();
  if (!token) throw new ServersError({ status: 401, error: NO_SESSION_MESSAGE, reason: 'no_session' });
  const headers: Record<string, string> = { 'X-Audit-Token': token };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch(base + path, { method, credentials: 'same-origin', signal, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ServersError({ status: 0, error: FAILURE_TEXT.offline, reason: 'offline' });
  }
  if (response.status === 401) {
    forgetSession();
    throw new ServersError({ status: 401, error: NO_SESSION_MESSAGE, reason: 'no_session' });
  }
  if (!response.ok) {
    let parsed: unknown = null;
    try { parsed = await response.json(); } catch { /* the status is enough */ }
    throw new ServersError(failureFromResponse(response.status, parsed));
  }
  return response;
}

async function call<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await send(method, path, body, signal);
  return (await response.json()) as T;
}

async function callEmpty(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<void> {
  await send(method, path, body, signal);
}

const at = (id: string): string => '/' + encodeURIComponent(id);

export const serversApi = {
  list: (signal?: AbortSignal) => call<{ items: ServerEntry[] }>('GET', '', undefined, signal),
  add: (body: AddBody, signal?: AbortSignal) => call<ServerEntry>('POST', '', body, signal),
  patch: (id: string, body: PatchBody, signal?: AbortSignal) => call<ServerEntry>('PATCH', at(id), body, signal),
  forget: (id: string, signal?: AbortSignal) => callEmpty('DELETE', at(id), undefined, signal),
  setSecret: (id: string, auth: AuthKind, secret: string, passphrase: string, signal?: AbortSignal) => callEmpty('PUT', at(id) + '/secret', buildSecretBody(auth, secret, passphrase), signal),
  hostKeys: (id: string, signal?: AbortSignal) => call<{ keys: HostKey[] }>('POST', at(id) + '/host-keys', {}, signal),
  pin: (id: string, fingerprint: string, signal?: AbortSignal) => call<ServerEntry>('POST', at(id) + '/pin', buildPinBody(fingerprint), signal),
  replaceKey: (id: string, oldFingerprint: string, newFingerprint: string, signal?: AbortSignal) => call<ServerEntry>('POST', at(id) + '/replace-key', buildReplaceBody(oldFingerprint, newFingerprint), signal),
  machines: (id: string, signal?: AbortSignal) => call<{ machines: MachineView[] }>('POST', at(id) + '/machines', {}, signal),
  test: (id: string, machine: string) => call<TestResult>('POST', at(id) + '/test', buildMachineBody(machine)),
  plan: (id: string, body: PullBody) => call<PlanView>('POST', at(id) + '/plan', body),
  startPull: (id: string, body: PullBody) => call<JobView>('POST', at(id) + '/pulls', body),
  pulls: (id: string, signal?: AbortSignal) => call<{ items: JobView[] }>('GET', at(id) + '/pulls', undefined, signal),
  pull: (id: string, job: string, signal?: AbortSignal) => call<JobView>('GET', at(id) + '/pulls/' + encodeURIComponent(job), undefined, signal),
  cancel: (id: string, job: string) => callEmpty('DELETE', at(id) + '/pulls/' + encodeURIComponent(job))
};

/** Fetches one pulled file with the session token and hands it to the browser as a download. The file is the scrubbed copy on this PC. */
export async function downloadPulledFile(serverId: string, jobId: string, name: string): Promise<void> {
  const url = pulledFileUrl(serverId, jobId, name).slice(base.length);
  const response = await send('GET', url);
  const blob = await response.blob();
  const href = URL.createObjectURL(blob);
  try {
    const link = document.createElement('a');
    link.href = href;
    link.download = name;
    document.body.appendChild(link);
    link.click();
    link.remove();
  } finally {
    setTimeout(() => URL.revokeObjectURL(href), 10_000);
  }
}
