import { NO_SESSION_MESSAGE, readSessionToken } from './api';
import { parseCaseKeyStatus } from './caseKey';
import type { CaseKeyStatus } from './caseKey';
import { isCaseId } from './cases';
import { LOCKED_STATUS, isCaseKeyReason, signalCasesLocked } from './lockSignal';

/**
 * The per-case key calls (server: internal/server/case_key.go). Every request carries X-Audit-Token.
 *   GET  /api/cases/{id}/key          -> { ownKey, locked, ending? }
 *   POST /api/cases/{id}/key/unlock   {passphrase}                                -> the status
 *   POST /api/cases/{id}/key/lock                                                 -> the status (locked)
 *   POST /api/cases/{id}/key/set      {passphrase, confirm}                       -> the status
 *   POST /api/cases/{id}/key/change   {passphrase, newPassphrase, confirm}        -> the status
 *   POST /api/cases/{id}/key/remove   {passphrase}                                -> the status (no key)
 *
 * Secret handling: a case key appears only in the body of these calls. It is never put in a URL, a header, storage, the console or an
 * error. A wrong case key is a 423 (reason "wrong-case-key"), which must not be read as "the whole app is locked": these calls do not use
 * cases.ts `send`, and the error carries only a status, a reason and a wait time. The message is fixed text and never the server's.
 */

export class CaseKeyApiError extends Error {
  readonly status: number;
  readonly reason: string;
  readonly retryAfterSeconds?: number;
  constructor(message: string, status: number, reason = '', retryAfterSeconds?: number) {
    super(message);
    this.name = 'CaseKeyApiError';
    this.status = status;
    this.reason = reason;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

function path(id: string, tail = ''): string {
  if (!isCaseId(id)) throw new CaseKeyApiError('That is not a case id.', 400, 'bad-case-id');
  return '/api/cases/' + id + '/key' + tail;
}

async function call(url: string, method: 'GET' | 'POST', body?: Record<string, unknown>, signal?: AbortSignal): Promise<unknown> {
  const token = readSessionToken();
  if (!token) throw new CaseKeyApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  const headers = new Headers({ 'X-Audit-Token': token });
  let payload: string | undefined;
  if (body) {
    headers.set('Content-Type', 'application/json');
    payload = JSON.stringify(body);
  }
  let response: Response;
  try {
    response = await fetch(url, { method, headers, body: payload, credentials: 'same-origin', signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new CaseKeyApiError('The server could not be reached.', 0, 'unreachable');
  }
  let parsed: unknown = null;
  try { parsed = await response.json(); } catch { /* an empty or unreadable body is fine */ }
  if (!response.ok) {
    const fields = parsed && typeof parsed === 'object' ? parsed as { reason?: unknown; retryAfterSeconds?: unknown } : {};
    const reason = typeof fields.reason === 'string' ? fields.reason : '';
    // The body is already read, so the reason is judged from it here (a clone of a used body throws, which used to read as
    // "the master key is locked" and threw the whole app to the unlock screen on a wrong case key).
    if (response.status === LOCKED_STATUS && !isCaseKeyReason(reason)) signalCasesLocked();
    const wait = typeof fields.retryAfterSeconds === 'number' && fields.retryAfterSeconds > 0 ? fields.retryAfterSeconds : undefined;
    throw new CaseKeyApiError('The case key request was refused (' + response.status + ').', response.status, reason, wait);
  }
  return parsed;
}

function statusOf(raw: unknown): CaseKeyStatus {
  const status = parseCaseKeyStatus(raw);
  if (!status) throw new CaseKeyApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return status;
}

/** Whether the case has its own key, whether it is locked now, and the last four characters of its check value. */
export async function getCaseKeyStatus(id: string, signal?: AbortSignal): Promise<CaseKeyStatus> {
  return statusOf(await call(path(id), 'GET', undefined, signal));
}

/** Opens a keyed case. A wrong key throws reason "wrong-case-key"; five wrong tries throw "throttled" with a wait. */
export async function unlockCase(id: string, passphrase: string, signal?: AbortSignal): Promise<CaseKeyStatus> {
  return statusOf(await call(path(id, '/unlock'), 'POST', { passphrase }, signal));
}

/** Locks this case only. The PC key and other cases are not touched. */
export async function lockCase(id: string, signal?: AbortSignal): Promise<CaseKeyStatus> {
  return statusOf(await call(path(id, '/lock'), 'POST', {}, signal));
}

export async function setCaseKey(id: string, passphrase: string, confirm: string, signal?: AbortSignal): Promise<CaseKeyStatus> {
  return statusOf(await call(path(id, '/set'), 'POST', { passphrase, confirm }, signal));
}

export async function changeCaseKey(id: string, current: string, next: string, confirm: string, signal?: AbortSignal): Promise<CaseKeyStatus> {
  return statusOf(await call(path(id, '/change'), 'POST', { passphrase: current, newPassphrase: next, confirm }, signal));
}

export async function removeCaseKey(id: string, current: string, signal?: AbortSignal): Promise<CaseKeyStatus> {
  return statusOf(await call(path(id, '/remove'), 'POST', { passphrase: current }, signal));
}

/** True for the two 423 reasons that belong to a case key (and never raise the app lock screen). */
export const isCaseKeyRefusal = (error: unknown): boolean =>
  error instanceof CaseKeyApiError && error.status === 423 && isCaseKeyReason(error.reason);
