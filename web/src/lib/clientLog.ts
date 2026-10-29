/**
 * clientLog — the page's half of the journey log (see internal/applog and
 * POST /api/client-log). The page reports three kinds of entry: nav (a screen
 * was shown, name only), action (something the user did) and error (an error
 * the user was shown). Entries are batched and sent in the background; a
 * failure to send is silent and never blocks or breaks the UI.
 *
 * Nothing here ever carries field contents, ids or query text: callers pass
 * screen names, action names, error codes and the server's own error message,
 * and the server re-checks every field against its allow-list anyway.
 */

export type ClientLogKind = 'nav' | 'action' | 'error';

export interface ClientLogEntry {
  kind: ClientLogKind;
  screen?: string;
  action?: string;
  code?: string;
  message?: string;
  stage?: string;
  reqId?: string;
}

export interface ClientLogDeps {
  /** The session token this tab holds, or '' when there is none. */
  token: () => string;
  /** Hands one JSON batch to the server; throwing or returning false loses the batch, silently. */
  send: (body: string, token: string) => boolean;
  schedule?: (fn: () => void, ms: number) => unknown;
  cancel?: (handle: unknown) => void;
}

const ENDPOINT = '/api/client-log';
const MAX_QUEUE = 50;
const BATCH_SIZE = 10;
const FLUSH_MS = 1000;
const MAX_MESSAGE = 300;

function capMessage(message: string | undefined): string | undefined {
  if (message === undefined) return undefined;
  return message.length > MAX_MESSAGE ? message.slice(0, MAX_MESSAGE) : message;
}

/** A batched, silent client-log sender. Exported as a factory so tests can drive it. */
export function createClientLog(deps: ClientLogDeps) {
  const schedule = deps.schedule ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
  const cancel = deps.cancel ?? ((handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>));
  let queue: ClientLogEntry[] = [];
  let timer: unknown = null;

  function flush(): void {
    try {
      if (timer !== null) { cancel(timer); timer = null; }
      if (queue.length === 0) return;
      const entries = queue;
      queue = [];
      const token = deps.token();
      if (!token) return; // no session: the batch is dropped, never queued forever
      deps.send(JSON.stringify({ entries }), token);
    } catch { /* logging must never break the page */ }
  }

  function report(entry: ClientLogEntry): void {
    try {
      if (queue.length >= MAX_QUEUE) queue.shift();
      queue.push({ ...entry, message: capMessage(entry.message) });
      if (queue.length >= BATCH_SIZE) { flush(); return; }
      if (timer === null) timer = schedule(() => { timer = null; flush(); }, FLUSH_MS);
    } catch { /* logging must never break the page */ }
  }

  /** Throws the queued entries away without sending (the session is gone). */
  function drop(): void {
    try {
      if (timer !== null) { cancel(timer); timer = null; }
      queue = [];
    } catch { /* silent */ }
  }

  return { report, flush, drop, pending: () => queue.length };
}

/**
 * The default transport: a keepalive fetch (sendBeacon cannot carry the
 * session token header). The result is ignored on purpose.
 */
function defaultSend(body: string, token: string): boolean {
  try {
    if (typeof fetch !== 'function') return false;
    void fetch(ENDPOINT, {
      method: 'POST',
      credentials: 'same-origin',
      keepalive: true,
      headers: { 'Content-Type': 'application/json', 'X-Audit-Token': token },
      body,
    }).catch(() => { /* silent */ });
    return true;
  } catch { return false; }
}

let singleton: ReturnType<typeof createClientLog> | null = null;

/** Wires the shared sender to the tab's session token. Called once by api.ts. */
export function configureClientLog(token: () => string): void {
  singleton = createClientLog({ token, send: defaultSend });
}

export function reportClient(entry: ClientLogEntry): void {
  try { singleton?.report(entry); } catch { /* silent */ }
}

export function flushClientLog(): void {
  try { singleton?.flush(); } catch { /* silent */ }
}

export function dropClientLog(): void {
  try { singleton?.drop(); } catch { /* silent */ }
}

/** A screen was shown. Screen names only — never ids or query text. */
export function reportNav(screen: string): void {
  reportClient({ kind: 'nav', screen });
}

/** Something the user did, on a named screen. */
export function reportAction(screen: string, action: string, stage?: string): void {
  reportClient({ kind: 'action', screen, action, ...(stage ? { stage } : {}) });
}

/** An error the user was shown: status, server code and stage, and the request id the server gave it. */
export function reportError(input: { screen?: string; status?: number; code?: string; stage?: string; message?: string; reqId?: string }): void {
  reportClient({
    kind: 'error',
    screen: input.screen ?? 'app',
    code: input.code ?? (input.status !== undefined ? 'http_' + input.status : undefined),
    stage: input.stage,
    message: input.message,
    reqId: input.reqId,
  });
}

let handlersInstalled = false;

/**
 * Reports window.onerror and unhandledrejection as error entries. Only the
 * message is sent (capped, and re-sanitized server-side) — never a stack,
 * never page data.
 */
export function installClientErrorHandlers(): void {
  if (handlersInstalled || typeof window === 'undefined') return;
  handlersInstalled = true;
  window.addEventListener('error', (event: ErrorEvent) => {
    reportError({ code: 'window_error', message: String(event.message ?? 'Script error') });
  });
  window.addEventListener('unhandledrejection', (event: PromiseRejectionEvent) => {
    const reason: unknown = event.reason;
    const message = reason instanceof Error ? reason.message : String(reason ?? 'Unhandled rejection');
    reportError({ code: 'unhandled_rejection', message });
  });
}
