import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import { parseList, parseProfile, type ApproveBody, type McpList, type McpProfile } from './mcpGrants';

/**
 * The AI access calls (server: internal/mcpadmin, mounted by internal/server/mcp_admin.go). Every request carries X-Audit-Token.
 *   GET  /api/mcp/profiles                   -> the list (mode, stopped, tiers, limits, profiles)
 *   POST /api/mcp/profiles/{id}/approve      {expectRev, tier, tools, sources, identifiers, identifiersMinutes, idleMinutes, acceptContested}
 *   POST /api/mcp/profiles/{id}/{deny|revoke|reissue|remove}   {expectRev?}
 *   POST /api/mcp/profiles/{id}/renew        {expectRev}
 *   POST /api/mcp/stop, /api/mcp/resume      {}
 *
 * The error text shown is the server's fixed text for that code (it never repeats anything the host sent). The host's own words are not
 * part of any error. A 409 with code "stale" means the host changed since the card was shown: the screen reloads the list.
 */

export class McpApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(message: string, status: number, code: string) {
    super(message);
    this.name = 'McpApiError';
    this.status = status;
    this.code = code;
  }
}

const SAFE_ID = /^[a-z0-9-]{1,32}$/;

async function call(method: 'GET' | 'POST', path: string, body?: unknown, signal?: AbortSignal): Promise<unknown> {
  const token = readSessionToken();
  if (!token) throw new McpApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  const headers: Record<string, string> = { 'X-Audit-Token': token };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: 'same-origin', signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new McpApiError('Unravis could not be reached. Check that it is still running.', 0, 'unreachable');
  }
  let parsed: unknown = null;
  try { parsed = await response.json(); } catch { /* an empty body is fine */ }
  if (response.status === 401) {
    forgetSession();
    throw new McpApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  }
  if (!response.ok) {
    const fields = parsed && typeof parsed === 'object' ? (parsed as { error?: unknown; code?: unknown }) : {};
    const message = typeof fields.error === 'string' && fields.error ? fields.error : 'The request was refused (' + response.status + ').';
    throw new McpApiError(message, response.status, typeof fields.code === 'string' ? fields.code : '');
  }
  return parsed;
}

function profilePath(id: string, action: string): string {
  if (!SAFE_ID.test(id)) throw new McpApiError('That is not a host name.', 400, 'bad_id');
  return '/api/mcp/profiles/' + id + '/' + action;
}

export async function listHosts(signal?: AbortSignal): Promise<McpList> {
  const list = parseList(await call('GET', '/api/mcp/profiles', undefined, signal));
  if (!list) throw new McpApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return list;
}

function profileOf(raw: unknown): McpProfile | null {
  return parseProfile(raw && typeof raw === 'object' ? (raw as { profile?: unknown }).profile : null);
}

export const mcpApi = {
  /** The body must carry expectRev (approveBody in mcpGrants.ts builds it, and throws without one). */
  approve: async (id: string, body: ApproveBody) => profileOf(await call('POST', profilePath(id, 'approve'), body)),
  deny: async (id: string, expectRev: number) => profileOf(await call('POST', profilePath(id, 'deny'), { expectRev })),
  revoke: async (id: string, expectRev: number) => profileOf(await call('POST', profilePath(id, 'revoke'), { expectRev })),
  reissue: async (id: string, expectRev: number) => profileOf(await call('POST', profilePath(id, 'reissue'), { expectRev })),
  renew: async (id: string, expectRev: number) => profileOf(await call('POST', profilePath(id, 'renew'), { expectRev })),
  remove: async (id: string, expectRev: number) => { await call('POST', profilePath(id, 'remove'), { expectRev }); },
  /** `saved` is false when the stop holds in this run but could not be written, so a restart of Unravis would lift it. */
  stop: async (): Promise<{ stopped: boolean; saved: boolean }> => {
    const r = (await call('POST', '/api/mcp/stop', {})) as { stopped?: unknown; saved?: unknown };
    return { stopped: r?.stopped === true, saved: r?.saved !== false };
  },
  resume: async (): Promise<{ stopped: boolean }> => {
    const r = (await call('POST', '/api/mcp/resume', {})) as { stopped?: unknown };
    return { stopped: r?.stopped === true };
  }
};
