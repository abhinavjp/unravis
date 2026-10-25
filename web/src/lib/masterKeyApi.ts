import { NO_SESSION_MESSAGE, readSessionToken } from './api';
import { parseStatus } from './masterKey';
import type { KeyStatus } from './masterKey';

/**
 * The five master key calls (server: internal/server/masterkey.go). Every request carries X-Audit-Token.
 *   GET  /api/master-key/status   -> { state: none|locked|unlocked, dpapiCases }
 *   POST /api/master-key/setup    {passphrase, confirm, acknowledged:true} -> the status (unlocked)
 *   POST /api/master-key/unlock   {passphrase}                             -> the status (unlocked); a wrong key is 401 reason "wrong-key"
 *   POST /api/master-key/lock                                              -> the status (locked)
 *   POST /api/master-key/rewrap   {caseId} | {all:true}                    -> { results: [{ caseId, status, reason? }] }
 *
 * Secret handling (I5): the passphrase appears only in the body of setup and unlock. It is never put in a URL, a header, storage, the
 * console or an error. These calls do not use cases.ts `send`, because `send` forgets the session on any 401 and a wrong key is a 401.
 * An error carries only a status, a reason code and a wait time; the message is fixed text and never the server's, and never a key.
 */

const base = '/api/master-key';

/** A refusal or a failed call. `status` 0 means the server could not be reached. */
export class MasterKeyApiError extends Error {
  readonly status: number;
  readonly reason: string;
  readonly retryAfterSeconds?: number;
  constructor(message: string, status: number, reason = '', retryAfterSeconds?: number) {
    super(message);
    this.name = 'MasterKeyApiError';
    this.status = status;
    this.reason = reason;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

async function call(path: string, method: 'GET' | 'POST', body?: Record<string, unknown>, signal?: AbortSignal): Promise<unknown> {
  const token = readSessionToken();
  if (!token) throw new MasterKeyApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  const headers = new Headers({ 'X-Audit-Token': token });
  let payload: string | undefined;
  if (body) {
    headers.set('Content-Type', 'application/json');
    payload = JSON.stringify(body);
  }
  let response: Response;
  try {
    response = await fetch(base + path, { method, headers, body: payload, credentials: 'same-origin', signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new MasterKeyApiError('The server could not be reached.', 0, 'unreachable');
  }
  let parsed: unknown = null;
  try { parsed = await response.json(); } catch { /* an empty or unreadable body is fine */ }
  if (!response.ok) {
    const fields = parsed && typeof parsed === 'object' ? parsed as { reason?: unknown; retryAfterSeconds?: unknown } : {};
    const reason = typeof fields.reason === 'string' ? fields.reason : '';
    const wait = typeof fields.retryAfterSeconds === 'number' && fields.retryAfterSeconds > 0 ? fields.retryAfterSeconds : undefined;
    throw new MasterKeyApiError('The master key request was refused (' + response.status + ').', response.status, reason, wait);
  }
  return parsed;
}

function statusOf(raw: unknown): KeyStatus {
  const status = parseStatus(raw);
  if (!status) throw new MasterKeyApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return status;
}

export async function getMasterKeyStatus(signal?: AbortSignal): Promise<KeyStatus> {
  return statusOf(await call('/status', 'GET', undefined, signal));
}

/** Sets the first master key. Throws MasterKeyApiError; its reason says which rule was missed. */
export async function setupMasterKey(passphrase: string, confirm: string, signal?: AbortSignal): Promise<KeyStatus> {
  return statusOf(await call('/setup', 'POST', { passphrase, confirm, acknowledged: true }, signal));
}

/** Unlocks with the key. A wrong key throws reason "wrong-key"; the caller shows one message for every failure. */
export async function unlockMasterKey(passphrase: string, signal?: AbortSignal): Promise<KeyStatus> {
  return statusOf(await call('/unlock', 'POST', { passphrase }, signal));
}

export async function lockMasterKey(signal?: AbortSignal): Promise<KeyStatus> {
  return statusOf(await call('/lock', 'POST', {}, signal));
}

/** What a start-over returns: the new status (no key), and the name (never a path) of the file that was set aside. */
export interface ResetResult { status: KeyStatus; keptAs?: string }
const KEPT_AS = /^[A-Za-z0-9._-]{1,120}$/;

/**
 * Sets a damaged key file aside so a new key can be made (POST /reset-damaged). It sends nothing unless `acknowledged` is true; the screen's
 * box is the person's yes. The server only acts while the state is `damaged`.
 */
export async function resetDamagedKey(acknowledged: boolean, signal?: AbortSignal): Promise<ResetResult> {
  if (acknowledged !== true) throw new MasterKeyApiError('The start-over was not confirmed.', 0, 'acknowledgement-required');
  const raw = await call('/reset-damaged', 'POST', { acknowledged: true }, signal);
  const status = statusOf(raw);
  const kept = raw && typeof raw === 'object' ? (raw as { keptAs?: unknown }).keptAs : undefined;
  return { status, ...(typeof kept === 'string' && KEPT_AS.test(kept) ? { keptAs: kept } : {}) };
}

export interface RewrapResult { caseId: string; status: 'rewrapped' | 'already' | 'failed'; reason?: string }

/** Protects older cases with the master key too: one case, or all of them. */
export async function rewrapCases(target: { caseId: string } | { all: true }, signal?: AbortSignal): Promise<RewrapResult[]> {
  const raw = await call('/rewrap', 'POST', target, signal);
  const list = raw && typeof raw === 'object' && Array.isArray((raw as { results?: unknown }).results) ? (raw as { results: unknown[] }).results : [];
  const out: RewrapResult[] = [];
  for (const item of list) {
    if (!item || typeof item !== 'object') continue;
    const r = item as { caseId?: unknown; status?: unknown; reason?: unknown };
    const status = r.status === 'rewrapped' || r.status === 'already' ? r.status : 'failed';
    out.push({ caseId: typeof r.caseId === 'string' ? r.caseId : '', status, ...(typeof r.reason === 'string' ? { reason: r.reason } : {}) });
  }
  return out;
}

/** How many of the results changed, and how many failed. Counts only. */
export function rewrapCounts(results: RewrapResult[]): { done: number; failed: number } {
  let done = 0;
  let failed = 0;
  for (const r of results) {
    if (r.status === 'rewrapped') done++;
    else if (r.status === 'failed') failed++;
  }
  return { done, failed };
}
