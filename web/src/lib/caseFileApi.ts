import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import { casePath, isCaseId } from './cases';
import { LOCKED_STATUS, signalCasesLocked } from './lockSignal';
import { retryAfterOf } from './restoreCase';

/**
 * The two case file calls (server: internal/server/case_file.go). Every request carries X-Audit-Token.
 *   POST /api/cases/restore       {path, passphrase} -> 201 the NEW case { id, title, created, closed, bytes, imports }
 *   POST /api/cases/{id}/export   {path, passphrase} -> 200 { path, name, bytes }
 * A restore always makes a new case; it never changes an existing one. A case file that does not open is 422 "bad-case-file" for every reason.
 *
 * Secret handling (I5): the passphrase appears only in the JSON body of these two calls. It is never put in a URL, a header, storage, the
 * console or an error. These calls do not use cases.ts `send`, because `send` forgets the session on any 401 and a wrong export key is a 401.
 * An error carries only a status, a reason code and a wait time; its message is fixed text and never the server's, and never a key or a path.
 */

const base = '/api';

/** A refusal or a failed call. `status` 0 means the server could not be reached. */
export class CaseFileApiError extends Error {
  readonly status: number;
  readonly reason: string;
  readonly retryAfterSeconds?: number;
  constructor(message: string, status: number, reason = '', retryAfterSeconds?: number) {
    super(message);
    this.name = 'CaseFileApiError';
    this.status = status;
    this.reason = reason;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

async function call(path: string, body: Record<string, string>, signal?: AbortSignal): Promise<unknown> {
  const token = readSessionToken();
  if (!token) throw new CaseFileApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  const headers = new Headers({ 'X-Audit-Token': token, 'Content-Type': 'application/json' });
  let response: Response;
  try {
    response = await fetch(base + path, { method: 'POST', headers, body: JSON.stringify(body), credentials: 'same-origin', signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new CaseFileApiError('The server could not be reached.', 0, 'unreachable');
  }
  let parsed: unknown = null;
  try { parsed = await response.json(); } catch { /* an empty or unreadable body is fine */ }
  if (response.status === LOCKED_STATUS) signalCasesLocked();
  if (!response.ok) {
    const fields = parsed && typeof parsed === 'object' ? parsed as { reason?: unknown; retryAfterSeconds?: unknown } : {};
    const reason = typeof fields.reason === 'string' ? fields.reason : '';
    // A 401 that is not a wrong export key is the dashboard refusing the token: forget it, as every other case route does.
    if (response.status === 401 && reason !== 'wrong-key') {
      forgetSession();
      throw new CaseFileApiError(NO_SESSION_MESSAGE, 401, 'no_session');
    }
    const wait = retryAfterOf(response.headers?.get?.('Retry-After'), fields.retryAfterSeconds);
    throw new CaseFileApiError('The case file request was refused (' + response.status + ').', response.status, reason, wait);
  }
  return parsed;
}

export interface RestoredCase { id: string; title: string }

/** Restores the file at `path` as a NEW case, using the master key the file was saved with. Throws CaseFileApiError. */
export async function restoreCaseFile(path: string, passphrase: string, signal?: AbortSignal): Promise<RestoredCase> {
  const raw = await call('/cases/restore', { path, passphrase }, signal);
  const body = raw && typeof raw === 'object' ? raw as { id?: unknown; title?: unknown } : {};
  if (typeof body.id !== 'string' || !isCaseId(body.id)) throw new CaseFileApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return { id: body.id, title: typeof body.title === 'string' ? body.title : '' };
}

export interface ExportedCaseFile { path: string; name: string; bytes: number }

/** Saves one case to a case file at `path` (a full file name ending .unravis-case, or an existing folder), sealed with this PC's master key. */
export async function exportCaseFile(id: string, path: string, passphrase: string, signal?: AbortSignal): Promise<ExportedCaseFile> {
  const raw = await call(casePath(id, '/export'), { path, passphrase }, signal);
  const body = raw && typeof raw === 'object' ? raw as { path?: unknown; name?: unknown; bytes?: unknown } : {};
  if (typeof body.path !== 'string' || typeof body.name !== 'string') throw new CaseFileApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return { path: body.path, name: body.name, bytes: typeof body.bytes === 'number' && Number.isFinite(body.bytes) ? body.bytes : 0 };
}
