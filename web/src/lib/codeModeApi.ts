import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import { CODE_TEXT } from './copy/codeModeCopy';
import { parseSettings, type CodeSettings, type SettingsPatch } from './codeMode';

/**
 * The code-mode settings calls (server: internal/codeapi). Every request carries X-Audit-Token.
 *   GET /api/code/settings   -> { enabled, allowDegraded, keepResults, allowIdentifiers, cliOpenUntil, damaged, cliUnlimited? }
 *   PUT /api/code/settings   { enabled?, allowDegraded?, keepResults?, cliMinutes?, cliUnlimited? }  -> the same document
 * The server refuses a body with a key it does not know, so `SettingsPatch` holds only the keys above. Errors are { code, message }.
 * The Date header of a reply is returned with it: the settings document has no "now", and the countdown needs the server's clock.
 */

export class CodeApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(message: string, status: number, code: string) {
    super(message);
    this.name = 'CodeApiError';
    this.status = status;
    this.code = code;
  }
}

/** Code mode is not mounted in this run of the app (the server answers 503 code_mode_unavailable). */
export const UNAVAILABLE_CODE = 'code_mode_unavailable';

export interface SettingsReply {
  settings: CodeSettings;
  /** The Date header of the reply (the server's clock, whole seconds), or null when it had none. */
  serverDate: string | null;
}

async function call(method: 'GET' | 'PUT', body?: unknown, signal?: AbortSignal): Promise<SettingsReply> {
  const token = readSessionToken();
  if (!token) throw new CodeApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  const headers: Record<string, string> = { 'X-Audit-Token': token };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch('/api/code/settings', { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: 'same-origin', signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new CodeApiError(CODE_TEXT.unreachable, 0, 'unreachable');
  }
  let parsed: unknown = null;
  try { parsed = await response.json(); } catch { /* an empty body is fine */ }
  if (response.status === 401) {
    forgetSession();
    throw new CodeApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  }
  if (!response.ok) {
    const fields = parsed && typeof parsed === 'object' ? (parsed as { code?: unknown; message?: unknown }) : {};
    const message = typeof fields.message === 'string' && fields.message ? fields.message : CODE_TEXT.refusedBase;
    throw new CodeApiError(message, response.status, typeof fields.code === 'string' ? fields.code : '');
  }
  const settings = parseSettings(parsed);
  if (!settings) throw new CodeApiError(CODE_TEXT.unreadable, response.status, 'unreadable');
  return { settings, serverDate: response.headers.get('Date') };
}

export const codeApi = {
  get: (signal?: AbortSignal): Promise<SettingsReply> => call('GET', undefined, signal),
  put: (patch: SettingsPatch, signal?: AbortSignal): Promise<SettingsReply> => call('PUT', patch, signal)
};
