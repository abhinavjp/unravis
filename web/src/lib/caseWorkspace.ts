import { createInvestigationSnapshot, parseInvestigationSnapshot, type InvestigationSnapshot } from './investigation';
import type { Query, RecentConnection, Source } from './types';

/**
 * The case workspace: what a case needs to open the way it was left. Which saved MongoDB connection and databases it used, and the
 * investigation filters (the same snapshot the tab keeps). It is saved with the case (PUT /api/cases/{id}/workspace).
 *
 * Pure: no DOM, no fetch, no state. It NEVER holds a password, an address, a host or a user name. A connection is named only by the opaque id
 * of its saved record, and a database only by its name. The server refuses a workspace that has any key of those names; `findForbiddenKey`
 * is the same check, run here before anything is sent.
 */

export const WORKSPACE_VERSION = 1;
export const MAX_WORKSPACE_DATABASES = 8;
const MAX_ID = 128;
const MAX_NAME = 256;

/** One connected database. `collection` is the audit collection the source read, so it comes back the same even if the saved record has another. */
export interface WorkspaceDatabase { name: string; selectionKey?: string; collection?: string }
export interface Workspace {
  v: 1;
  mongo?: { savedConnectionId: string; databases: WorkspaceDatabase[] };
  investigation?: InvestigationSnapshot;
}

/** The key names the server refuses at any depth, compared after keeping only lower-case letters and digits (case, underscores, hyphens and spaces do not matter). */
export const FORBIDDEN_KEYS: readonly string[] = [
  'password', 'passwd', 'pwd', 'pass', 'secret', 'token', 'uri', 'url', 'host', 'hostname', 'address', 'username', 'user', 'connectionstring',
  'credential', 'credentials', 'auth', 'authsource', 'cacertificate', 'cert', 'certificate', 'privatekey', 'apikey'
];
const normalKey = (key: string): string => key.toLowerCase().replace(/[^a-z0-9]/g, '');

/**
 * The first key at any depth that the server would refuse, or null: a forbidden name, or two keys of one object that read the same once
 * normalised. A cycle or a very deep value is treated as refused.
 */
export function findForbiddenKey(value: unknown, depth = 0): string | null {
  if (depth > 12) return '(too deep)';
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = findForbiddenKey(item, depth + 1);
      if (found) return found;
    }
    return null;
  }
  if (value && typeof value === 'object') {
    const seen = new Set<string>();
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      const normal = normalKey(key);
      if (FORBIDDEN_KEYS.includes(normal) || seen.has(normal)) return key;
      seen.add(normal);
      const found = findForbiddenKey(item, depth + 1);
      if (found) return found;
    }
  }
  return null;
}

/** The server refuses any text value that looks like an address ("scheme://"). */
export const hasAddressText = (text: string): boolean => text.includes('://');

/**
 * A copy of a value without the text that looks like an address: a property whose text holds "://" is left out, and so is such an item of a
 * list. Used on the filters, where a person may have typed a URL fragment: that one filter is not remembered, the rest still is.
 */
export function withoutAddressText<T>(value: T, depth = 0): T {
  if (depth > 12) return value;
  if (Array.isArray(value)) {
    return value.filter(item => !(typeof item === 'string' && hasAddressText(item))).map(item => withoutAddressText(item, depth + 1)) as unknown as T;
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      if (typeof item === 'string' && hasAddressText(item)) continue;
      out[key] = withoutAddressText(item, depth + 1);
    }
    return out as T;
  }
  return value;
}

/** The first text value at any depth that looks like an address, or null. */
export function findAddressText(value: unknown, depth = 0): string | null {
  if (depth > 12) return '(too deep)';
  if (typeof value === 'string') return hasAddressText(value) ? value : null;
  if (Array.isArray(value)) {
    for (const item of value) { const found = findAddressText(item, depth + 1); if (found) return found; }
  } else if (value && typeof value === 'object') {
    for (const item of Object.values(value as Record<string, unknown>)) { const found = findAddressText(item, depth + 1); if (found) return found; }
  }
  return null;
}

export interface BuildInput {
  query: Query;
  sources: Source[];
  zone: string;
  targetMs: number | null;
  /** The investigation the tab still holds for sources that are not connected right now, so they are not forgotten. */
  intent: InvestigationSnapshot | null;
  /** The saved connection id of a source, or '' when it is not known. */
  savedIdOf: (source: Source) => string;
  /** The last workspace this case is known to hold. Its connection is kept when no source can name its saved connection. */
  previous: Workspace | null;
}

const databaseOf = (source: Source): WorkspaceDatabase => ({
  name: source.database,
  ...(source.selectionKey ? { selectionKey: source.selectionKey } : {}),
  ...(source.collection && source.collection.length <= MAX_NAME ? { collection: source.collection } : {})
});

/**
 * Builds the workspace to save, or null when the result would hold a key the server refuses (it never should). One saved connection is kept:
 * the one that owns a selected source, otherwise the first one with a known saved id. When no source can say which saved connection it came
 * from, the connection already on record stays, so a half-restored state never wipes a good one.
 */
export function buildWorkspace(input: BuildInput): Workspace | null {
  const groups = new Map<string, WorkspaceDatabase[]>();
  const selected = new Set(input.query.sourceIds);
  let chosen = '';
  for (const source of input.sources) {
    if (!source.database || hasAddressText(source.database) || hasAddressText(source.selectionKey ?? '') || hasAddressText(source.collection ?? '')) continue;
    const savedId = input.savedIdOf(source);
    if (!savedId || savedId.length > MAX_ID) continue;
    const list = groups.get(savedId) ?? [];
    if (!list.some(item => item.name === source.database && item.selectionKey === source.selectionKey)) list.push(databaseOf(source));
    groups.set(savedId, list);
    if (!chosen && selected.has(source.id)) chosen = savedId;
  }
  if (!chosen) chosen = groups.keys().next().value ?? '';
  const workspace: Workspace = { v: WORKSPACE_VERSION };
  if (chosen) workspace.mongo = { savedConnectionId: chosen, databases: (groups.get(chosen) ?? []).slice(0, MAX_WORKSPACE_DATABASES) };
  else if (input.previous?.mongo) workspace.mongo = { savedConnectionId: input.previous.mongo.savedConnectionId, databases: input.previous.mongo.databases.map(item => ({ ...item })) };
  // A filter the person typed that holds an address (a URL fragment) is left out; the rest of the save goes through.
  workspace.investigation = withoutAddressText(createInvestigationSnapshot(input.query, input.sources, input.zone, input.targetMs, input.intent));
  return findForbiddenKey(workspace) || findAddressText(workspace) ? null : workspace;
}

const asRecord = (value: unknown): Record<string, unknown> | null => (value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null);

function parseMongo(value: unknown): Workspace['mongo'] | undefined {
  const raw = asRecord(value);
  if (!raw) return undefined;
  const id = raw.savedConnectionId;
  if (typeof id !== 'string' || !id || id.length > MAX_ID) return undefined;
  const databases: WorkspaceDatabase[] = [];
  if (Array.isArray(raw.databases)) {
    for (const item of raw.databases) {
      const entry = asRecord(item);
      if (!entry || typeof entry.name !== 'string' || !entry.name || entry.name.length > MAX_NAME) continue;
      const key = typeof entry.selectionKey === 'string' && entry.selectionKey && entry.selectionKey.length <= MAX_ID ? entry.selectionKey : undefined;
      if (databases.some(known => known.name === entry.name && known.selectionKey === key)) continue;
      const collection = typeof entry.collection === 'string' && entry.collection.trim() && entry.collection.length <= MAX_NAME ? entry.collection : undefined;
      databases.push({ name: entry.name, ...(key ? { selectionKey: key } : {}), ...(collection ? { collection } : {}) });
      if (databases.length >= MAX_WORKSPACE_DATABASES) break;
    }
  }
  return databases.length ? { savedConnectionId: id, databases } : undefined;
}

/**
 * Reads what the server returned. Tolerant: it never throws, drops anything it does not know or cannot use, and bounds every size. An empty
 * object is an empty workspace; anything that is not an object, or is a newer version, is null.
 */
export function parseWorkspace(raw: unknown): Workspace | null {
  const value = asRecord(raw);
  if (!value) return null;
  if (!Object.keys(value).length) return { v: WORKSPACE_VERSION };
  if (value.v !== WORKSPACE_VERSION) return null;
  const workspace: Workspace = { v: WORKSPACE_VERSION };
  try {
    const mongo = parseMongo(value.mongo);
    if (mongo) workspace.mongo = mongo;
    if (value.investigation !== undefined) {
      const investigation = parseInvestigationSnapshot(JSON.stringify(value.investigation));
      if (investigation) workspace.investigation = investigation;
    }
  } catch { /* a value that cannot be read is the same as none */ }
  return workspace;
}

/** Drops one source from a workspace that is kept as the baseline, so a source the person removed is not brought back. */
export function pruneWorkspace(workspace: Workspace | null, source: Pick<Source, 'database' | 'selectionKey'>): Workspace | null {
  if (!workspace?.mongo) return workspace;
  const databases = workspace.mongo.databases.filter(item => !(item.selectionKey && source.selectionKey ? item.selectionKey === source.selectionKey : item.name === source.database));
  const { mongo: _drop, ...rest } = workspace;
  return databases.length ? { ...rest, mongo: { savedConnectionId: workspace.mongo.savedConnectionId, databases } } : rest;
}

// ---- Saving ----

export interface SaveFacts {
  demo: boolean;
  caseId: string;
  /** The first investigation has been set up (the app has started). */
  ready: boolean;
  /** A restore is running for this case. */
  restoring: boolean;
  /** The case whose workspace has been read and applied (or found empty). Saves are allowed for that case only. */
  restoredCaseId: string;
}

/** Whether the workspace may be saved right now. Never in the demo, with no case, or until the case's own workspace has been read. */
export function workspaceSaveAllowed(facts: SaveFacts): boolean {
  return !facts.demo && facts.ready && !!facts.caseId && !facts.restoring && facts.restoredCaseId === facts.caseId;
}

export const SAVE_DELAY_MS = 1500;

type Timer = unknown;
export interface WorkspaceSaverOptions {
  send: (caseId: string, workspace: Workspace) => Promise<unknown>;
  /** Asked again at the moment of sending, so a save queued before a restore started is dropped. */
  allowed?: (caseId: string) => boolean;
  delayMs?: number;
  setTimer?: (run: () => void, ms: number) => Timer;
  clearTimer?: (timer: Timer) => void;
  /** Told after a send went through. */
  sent?: (caseId: string, workspace: Workspace) => void;
}

/**
 * A quiet, debounced saver. The last write wins: scheduling again replaces what is waiting. A workspace equal to the last one sent for
 * that case is skipped. A failed send is ignored, and nothing here ever throws or blocks.
 */
export function createWorkspaceSaver(options: WorkspaceSaverOptions) {
  const delay = options.delayMs ?? SAVE_DELAY_MS;
  const setTimer = options.setTimer ?? ((run: () => void, ms: number) => setTimeout(run, ms));
  const clearTimer = options.clearTimer ?? ((timer: Timer) => clearTimeout(timer as ReturnType<typeof setTimeout>));
  let timer: Timer | null = null;
  let pending: { caseId: string; workspace: Workspace } | null = null;
  let lastSent = { caseId: '', text: '' };

  function stop() { if (timer !== null) { clearTimer(timer); timer = null; } }
  function fire() {
    timer = null;
    const job = pending;
    pending = null;
    if (!job) return;
    if (options.allowed && !options.allowed(job.caseId)) return;
    const text = JSON.stringify(job.workspace);
    if (lastSent.caseId === job.caseId && lastSent.text === text) return;
    try {
      void Promise.resolve(options.send(job.caseId, job.workspace)).then(() => {
        lastSent = { caseId: job.caseId, text };
        options.sent?.(job.caseId, job.workspace);
      }, () => { /* a failed save is ignored; the next change tries again */ });
    } catch { /* the same */ }
  }
  return {
    schedule(caseId: string, workspace: Workspace) {
      if (!caseId) return;
      stop();
      pending = { caseId, workspace };
      timer = setTimer(fire, delay);
    },
    /** Sends what is waiting now (used when the person moves to another case). */
    flush() { stop(); fire(); },
    /** Drops what is waiting. */
    cancel() { stop(); pending = null; },
    /** Forgets what was last sent, so the next save always goes. */
    forget() { lastSent = { caseId: '', text: '' }; },
    get waiting() { return pending !== null; }
  };
}

/**
 * Queues the workspace for saving, or lets go of what is waiting. A workspace equal to what the case already holds needs no save, and an
 * older change still waiting (a filter set to Y and back to X inside the pause) must not go out either, so it is dropped.
 */
export function queueWorkspace(saver: Pick<ReturnType<typeof createWorkspaceSaver>, 'schedule' | 'cancel'>, caseId: string, baseline: Workspace | null, next: Workspace): void {
  if (baseline && JSON.stringify(baseline) === JSON.stringify(next)) { saver.cancel(); return; }
  saver.schedule(caseId, next);
}

// ---- Restoring ----

export type RestorePlan =
  | { kind: 'none' }
  | { kind: 'connected' }
  | { kind: 'connect'; recent: RecentConnection; databases: WorkspaceDatabase[] }
  | { kind: 'signin'; recent: RecentConnection; databases: WorkspaceDatabase[] }
  | { kind: 'missing'; databases: WorkspaceDatabase[] };

/** Whether a source is one of the databases the workspace names (by its selection key when both have one, otherwise by database name). */
export const sourceMatches = (source: Pick<Source, 'database' | 'selectionKey'>, database: WorkspaceDatabase): boolean =>
  database.selectionKey && source.selectionKey ? database.selectionKey === source.selectionKey : database.name === source.database;

/** A saved connection that has a user name but no stored or session password cannot be opened without the person signing in. */
export const needsSignIn = (recent: RecentConnection, sessionIds: ReadonlySet<string>): boolean =>
  recent.username !== '' && !recent.hasCredentials && !sessionIds.has(recent.id);

/**
 * What opening the case has to do about MongoDB. Nothing when the workspace names no connection; nothing when every named database is already
 * connected; otherwise connect it from the saved record, ask for the sign-in, or say that the saved record is gone.
 */
export function planRestore(input: { workspace: Workspace | null; sources: readonly Pick<Source, 'database' | 'selectionKey'>[]; recents: readonly RecentConnection[]; sessionIds: ReadonlySet<string> }): RestorePlan {
  const mongo = input.workspace?.mongo;
  if (!mongo) return { kind: 'none' };
  const absent = mongo.databases.filter(database => !input.sources.some(source => sourceMatches(source, database)));
  if (!absent.length) return { kind: 'connected' };
  const recent = input.recents.find(item => item.id === mongo.savedConnectionId);
  if (!recent) return { kind: 'missing', databases: absent };
  return needsSignIn(recent, input.sessionIds) ? { kind: 'signin', recent, databases: absent } : { kind: 'connect', recent, databases: absent };
}

/** How long the whole quiet reconnect may take, all databases together, before the person is told MongoDB could not be reached. */
export const RESTORE_TIMEOUT_MS = 20000;

export type RestoreFailure = 'signin' | 'unreachable' | 'failed';

const stageOf = (cause: unknown): string => (cause && typeof cause === 'object' && 'stage' in cause ? String((cause as { stage?: unknown }).stage ?? '') : '');
const statusOf = (cause: unknown): number => (cause && typeof cause === 'object' && 'status' in cause && typeof (cause as { status?: unknown }).status === 'number' ? (cause as { status: number }).status : 0);

/**
 * What the failed reconnect means for the person, from the errors the server and the network gave. A refused login is "sign in"; a network
 * or time-out error (or a TLS one) is "could not reach MongoDB", where signing in again would not help; a refused setting is "failed".
 * With nothing to go on, a saved connection with a user name asks for the sign-in and one without a login is "failed".
 */
export function classifyRestoreFailure(causes: readonly unknown[], hasLogin: boolean): RestoreFailure {
  if (causes.some(cause => stageOf(cause) === 'authentication' || statusOf(cause) === 401 || statusOf(cause) === 403)) return 'signin';
  const unreachable = (cause: unknown): boolean => {
    const stage = stageOf(cause);
    if (stage === 'connection' || stage === 'tls') return true;
    if (stage) return false;
    if (cause instanceof TypeError) return true;
    const name = cause && typeof cause === 'object' && 'name' in cause ? String((cause as { name?: unknown }).name) : '';
    if (name === 'TimeoutError' || name === 'AbortError') return true;
    const status = statusOf(cause);
    return status === 408 || status === 502 || status === 503 || status === 504;
  };
  if (causes.some(unreachable)) return 'unreachable';
  if (causes.some(cause => stageOf(cause) === 'validation' || stageOf(cause) === 'discovery')) return 'failed';
  return hasLogin ? 'signin' : 'failed';
}
