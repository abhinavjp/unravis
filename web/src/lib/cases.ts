import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import { WRONG_CASE_KEY_REASON, signalIfAppLocked } from './lockSignal';
import { guardSignal, isAbortLike, READ_TIMEOUT_MS, TIMEOUT_CODE, timeoutMessage } from './requestTimeout';
import { parseTimelineView, type TimelineView } from './timeline';
import { parseResource, parseSeries, type ResourceView, type SeriesView } from './resource';
import { parseCollect, type CollectItem } from './collect';
import { parseArchive, type ArchiveInfo } from './archiveModel';
import type { EvidenceExport, ExportFile, Query } from './types';

/**
 * Client for the case and import routes (importer wave plan, card API), and the pure rules the import screen follows.
 * Nothing here holds copy: the screen turns a `reason` into words with importCopy.ts.
 *
 * The contract this file mocks (JSON is camelCase; every request carries X-Audit-Token):
 *   GET    /api/cases                          -> { cases: CaseInfo[] }
 *   POST   /api/cases            {title, requestId} -> CaseInfo (201 made, 200 when this request id already made it)
 *   GET    /api/cases/{id}                     -> CaseView (CaseInfo + history + retries)
 *   POST   /api/cases/{id}/close               -> CaseInfo   (X-Expect-State: what the page believes; a mismatch is 409 case-changed)
 *   POST   /api/cases/{id}/reopen              -> CaseInfo   (Closed to Open only; same header)
 *   PUT    /api/cases/{id}/retries {items}     -> { retries }  (replaces the "needs another try" list)
 *   DELETE /api/cases/{id}/retries/{retryId}   -> 204
 *   POST   /api/cases/{id}/retries/{retryId}/retry -> 204 (environments and plain tags only; others answer 422 retry-in-page)
 *   DELETE /api/cases/{id}       {confirm:id}  -> 204, or {} (any 2xx)
 *   GET    /api/cases/{id}/imports             -> { imports: ImportRecord[] }
 *   POST   /api/cases/{id}/imports?importer=&host=&preview=1
 *          multipart, in this order: ackGating=true, ackText (a commit only), then file
 *                                              -> { importer, score, tied, report?, record? }
 *   POST   /api/cases/{id}/imports?async=1&...  the same form; the file is checked, then read in the background
 *                                              -> 202 { id, state, bytesRead, bytesTotal }
 *   GET    /api/cases/{id}/imports/jobs/{job}  -> { id, state: queued|running|done|failed, bytesRead, bytesTotal,
 *                                                   status?, result? (the answer above, when done), failure? ({ error, reason?, column?, importId? }, when failed) }
 *   DELETE /api/cases/{id}/imports/jobs/{job}  -> 204 (cancels a running job, forgets a finished one)
 *   GET    /api/hosts                          -> { version: 1, hosts: [{ label, aliases?, role?, tz?, logicalCores?, runtime? }] }
 *   PUT    /api/hosts   {version: 1, hosts}    -> the saved map
 *   POST   /api/cases/{id}/timeline {query}    -> the timeline view (lib/timeline.ts)
 *   POST   /api/export/preview {caseId}        -> { previewId, export }   (then /api/export/confirm {previewId})
 * Errors are JSON { error, reason?, column?, importId? } with the importer Reason enum, never content from the file.
 * Without `importer`, a preview asks the server to detect it; a tie returns `tied` (two or more names) and no report.
 */

const base = '/api';

/** The case id shape (storage doc): 26 base32 characters. It is checked before an id is put into a URL. */
export const CASE_ID_PATTERN = /^[a-z2-7]{26}$/;
export const isCaseId = (id: string): boolean => CASE_ID_PATTERN.test(id);
/** The short code shown beside a case, and the word typed to erase it. It is also the case8 in evidence ids. */
export const caseCode = (id: string): string => id.slice(0, 8);

/** True only when the typed text is this case's code (case does not matter; spaces and dashes inside it do not, as the server reads it) or its whole id. */
export function eraseConfirmed(typed: string, id: string): boolean {
  if (!isCaseId(id)) return false;
  if (typed.trim() === id) return true;
  return typed.toLowerCase().replace(/[ \t-]/g, '') === caseCode(id);
}

/** A case's place in its life (decisions A7). The page sends what it believes back as X-Expect-State. */
export type CaseState = 'open' | 'closed' | 'archiving' | 'archived' | 'untrusted' | 'erased';

export interface CaseInfo {
  id: string;
  title: string;
  created: string;
  closed?: string | null;
  /** The state the server reported when the page read the case. Missing from an old server. */
  state?: CaseState;
  /** The case has a key of its own (the PC key is not used for it). */
  ownKey?: boolean;
  /** The case has its own key and it is not unlocked now: the title is hidden until it is. */
  keyLocked?: boolean;
  /** The last four characters of the key's check value ("Key ending xxxx"). Empty when the case has no key of its own. */
  keyEnding?: string;
  /** True for a folder the server refuses (sealed by Windows only, not known when the master key was set up). Its title is a fixed placeholder. */
  untrusted?: boolean;
  bytes: number;
  imports: number;
  /** True when the working copy is gone without a Free ("Working copy missing"). Only Erase, and Restore as new when archived, are offered. */
  missing?: boolean;
  /** How many case files were made from this case ("Case files made earlier: n"). */
  filesMade?: number;
  /** The archive record: the case file in force, whether the space was freed, and (in a case view) the whole list. */
  archive?: ArchiveInfo;
  /** How far a running archive has got, on an Archiving row. */
  percent?: number;
}

/** What happened to a case: made, closed, reopened, archived, restored, freed (A8). `detail` is a short fact such as a file name or a code, never a key or a path. */
export interface HistoryEntry { at: string; kind: string; detail?: string }

/** One thing that did not go through, kept in the case until it is retried or dropped (A14, C19). */
export interface RetryItem {
  id: number;
  /** environments | tag | audit-source | import. "Not added to analysis" (audit-source) and "import failed" (import) stay apart (C19). */
  kind: string;
  key: string;
  label: string;
  reason: string;
  payload?: unknown;
  created: string;
}

export interface CaseView extends CaseInfo { history: HistoryEntry[]; retries: RetryItem[] }

export interface SourceQualityInfo {
  sourceVersion?: string;
  threshold?: string;
  coverageFirst?: string;
  coverageLast?: string;
  sampling?: string;
  timezoneRule?: string;
}

export interface RowError { line: number; reason: string }

export interface ImportReport {
  importer: string;
  rows: number;
  emitted: number;
  rejected: number;
  errors: RowError[];
  observedHosts: string[];
  first?: string | null;
  last?: string | null;
  quality: SourceQualityInfo;
  fieldRules: Record<string, string>;
  knownGaps: string[];
}

export interface ImportRecord {
  id: number;
  importer: string;
  fileName: string;
  sha256: string;
  bytes: number;
  rows: number;
  rejected: number;
  hostLabel: string;
  tzRule: string;
  quality: SourceQualityInfo;
  knownGaps: string[];
  imported: string;
  status: string;
  reason?: string;
  /** What the owner supplied for this import (key to value). Empty when nothing was. Shown on screen only, never logged. */
  supplied: Record<string, string>;
}

/** What a preview or a commit answers. `tied` holds two or more names when the server could not choose one importer. */
export interface ImportAnswer {
  importer: string;
  score: number;
  tied: string[];
  report: ImportReport | null;
  record: ImportRecord | null;
}

/** A refusal from the server. `reason` is the importer Reason (or an API reason such as "duplicate"); never file content. */
export class CaseApiError extends Error {
  readonly status: number;
  readonly reason: string;
  readonly column?: string;
  readonly importId?: number;
  /** For an unknown-host refusal: the server name the file itself names (read through the check route), so the screen can offer to add it. */
  serverName?: string;
  constructor(message: string, status: number, reason = '', column?: string, importId?: number) {
    super(message);
    this.name = 'CaseApiError';
    this.status = status;
    this.reason = reason;
    this.column = column;
    this.importId = importId;
  }
}

/** True when the server says the case is gone (erased in another tab or window), so the case list on screen is out of date. */
export const isCaseGone = (error: unknown): boolean => error instanceof CaseApiError && error.reason === 'case-not-found';

/** True when the case changed in another tab since this page read it (409 case-changed, message M2). The page reloads it. */
export const isCaseChanged = (error: unknown): boolean => error instanceof CaseApiError && error.status === 409 && error.reason === 'case-changed';

/** True when the case has its own key and it is not entered now (423, reason case-locked): for example another tab locked it. */
export const isCaseLocked = (error: unknown): boolean => error instanceof CaseApiError && error.status === 423 && error.reason === 'case-locked';

/** The header that carries what the page believes about the case. */
export const EXPECT_STATE_HEADER = 'X-Expect-State';

/* ------------------------------------------------------------------ requests */

/** `options.timeoutMs`: how long a read may run before it is stopped with a plain sentence. Reads (GET) get the read limit unless told otherwise; null means no limit (a job poll or a download). */
export interface SendOptions { timeoutMs?: number | null }

export async function send(path: string, init: RequestInit = {}, signal?: AbortSignal, options: SendOptions = {}): Promise<Response> {
  const method = (init.method ?? 'GET').toUpperCase();
  const timeoutMs = options.timeoutMs === undefined ? (method === 'GET' ? READ_TIMEOUT_MS : null) : options.timeoutMs;
  // Only the wait for the answer is limited: the guard is released as soon as the response head is here, because the body is read by the caller.
  const guard = guardSignal(signal, timeoutMs);
  try {
    return await sendOnce(path, init, guard.signal);
  } catch (cause) {
    if (guard.timedOut() && isAbortLike(cause)) throw new CaseApiError(timeoutMessage(), 0, TIMEOUT_CODE);
    throw cause;
  } finally { guard.done(); }
}

async function sendOnce(path: string, init: RequestInit, signal: AbortSignal | undefined): Promise<Response> {
  const token = readSessionToken();
  if (!token) throw new CaseApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  const headers = new Headers(init.headers);
  headers.set('X-Audit-Token', token);
  // A JSON body gets its type here; a FormData body is left alone so the browser adds the multipart boundary.
  if (typeof init.body === 'string') headers.set('Content-Type', 'application/json');
  let response: Response;
  try {
    response = await fetch(base + path, { ...init, headers, credentials: 'same-origin', signal });
  } catch (error) {
    if (isAbortLike(error)) throw error;
    throw new CaseApiError('The server could not be reached.', 0, 'unreachable');
  }
  if (response.status === 401) {
    // A wrong case key is also a 401 (reason wrong-case-key): the token is fine, so it is kept and the refusal goes to the caller.
    const refused = await refusal(response);
    if (refused.reason === WRONG_CASE_KEY_REASON) throw refused;
    // The dashboard refused the token: forget it so nothing keeps sending it, and show the usual message.
    forgetSession();
    throw new CaseApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  }
  // 423: the master key is locked. Tell the app so it shows the unlock screen; the caller still gets the refusal.
  await signalIfAppLocked(response);
  if (!response.ok) throw await refusal(response);
  return response;
}

async function refusal(response: Response): Promise<CaseApiError> {
  let message = 'Request failed (' + response.status + ')';
  let reason = '';
  let column: string | undefined;
  let importId: number | undefined;
  try {
    const body = await response.json() as { error?: unknown; reason?: unknown; code?: unknown; column?: unknown; importId?: unknown };
    if (typeof body.error === 'string') message = body.error;
    if (typeof body.reason === 'string') reason = body.reason;
    else if (typeof body.code === 'string') reason = body.code;
    if (typeof body.column === 'string') column = body.column;
    if (typeof body.importId === 'number') importId = body.importId;
  } catch { /* an empty or unreadable body keeps the generic message */ }
  return new CaseApiError(message, response.status, reason, column, importId);
}

/** True for the error the time limit (AbortSignal.timeout) leaves behind; a cancel by the person is an AbortError instead. */
export const isTimeLimit = (cause: unknown): boolean => typeof cause === 'object' && cause !== null && (cause as { name?: unknown }).name === 'TimeoutError';

export async function json<T>(response: Response): Promise<T> {
  try { return await response.json() as T; } catch (cause) {
    // The time limit can end a read while its body is still arriving: that is a timeout, not a damaged answer.
    if (isTimeLimit(cause)) throw new CaseApiError(timeoutMessage(), 0, TIMEOUT_CODE);
    throw new CaseApiError('The server sent an answer that could not be read.', response.status, 'unreadable');
  }
}

export function casePath(id: string, tail = ''): string {
  if (!isCaseId(id)) throw new CaseApiError('That is not a case id.', 400, 'bad-case-id');
  return '/cases/' + id + tail;
}

const asNumber = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const asString = (value: unknown): string => (typeof value === 'string' ? value : '');
const asStrings = (value: unknown): string[] => (Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []);

/** A supplied-settings object from the server: only string values under keys of the right shape are kept. */
function asSupplied(value: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (!value || typeof value !== 'object' || Array.isArray(value)) return out;
  for (const [key, v] of Object.entries(value)) if (SUPPLIED_KEY_PATTERN.test(key) && typeof v === 'string') out[key] = v;
  return out;
}

const CASE_STATES: readonly string[] = ['open', 'closed', 'archiving', 'archived', 'untrusted', 'erased'];

export function toCase(raw: Partial<CaseInfo>): CaseInfo {
  const stated = asString(raw.state);
  const out: CaseInfo = { id: asString(raw.id), title: asString(raw.title), created: asString(raw.created), closed: raw.closed ? String(raw.closed) : null, bytes: asNumber(raw.bytes), imports: asNumber(raw.imports) };
  if (CASE_STATES.includes(stated)) out.state = stated as CaseState;
  if (raw.untrusted === true) out.untrusted = true;
  if (raw.ownKey === true) {
    out.ownKey = true;
    if (raw.keyLocked === true) out.keyLocked = true;
    const ending = asString(raw.keyEnding);
    if (/^[0-9a-f]{4}$/i.test(ending)) out.keyEnding = ending.toLowerCase();
  }
  if (raw.missing === true) out.missing = true;
  if (typeof raw.filesMade === 'number' && raw.filesMade > 0) out.filesMade = Math.floor(raw.filesMade);
  const archive = parseArchive(raw.archive);
  if (archive) out.archive = archive;
  if (typeof raw.percent === 'number' && Number.isFinite(raw.percent)) out.percent = Math.max(0, Math.min(100, Math.floor(raw.percent)));
  return out;
}

function toRetry(raw: Partial<RetryItem> | null | undefined): RetryItem | null {
  if (!raw || typeof raw !== 'object' || typeof raw.id !== 'number') return null;
  return { id: raw.id, kind: asString(raw.kind), key: asString(raw.key), label: asString(raw.label), reason: asString(raw.reason), ...(raw.payload !== undefined ? { payload: raw.payload } : {}), created: asString(raw.created) };
}

const toRetries = (raw: unknown): RetryItem[] => (Array.isArray(raw) ? raw.map(toRetry).filter((r): r is RetryItem => r !== null) : []);

export function toView(raw: Partial<CaseView>): CaseView {
  const history = Array.isArray(raw.history)
    ? raw.history.filter((h) => h && typeof h.kind === 'string').map((h): HistoryEntry => (typeof h.detail === 'string' && h.detail ? { at: asString(h.at), kind: h.kind, detail: h.detail } : { at: asString(h.at), kind: h.kind }))
    : [];
  return { ...toCase(raw), history, retries: toRetries(raw.retries) };
}

/** The state a page believes a case is in, for X-Expect-State. A closed stamp means closed even from an old server. */
export function believedState(c: Pick<CaseInfo, 'state' | 'closed'>): CaseState {
  if (c.state) return c.state;
  return c.closed ? 'closed' : 'open';
}

function toReport(raw: Partial<ImportReport> | null | undefined): ImportReport | null {
  if (!raw || typeof raw !== 'object') return null;
  const errors = Array.isArray(raw.errors) ? raw.errors.map((e) => ({ line: asNumber(e?.line), reason: asString(e?.reason) })) : [];
  return {
    importer: asString(raw.importer), rows: asNumber(raw.rows), emitted: asNumber(raw.emitted), rejected: asNumber(raw.rejected), errors,
    observedHosts: asStrings(raw.observedHosts), first: raw.first ? String(raw.first) : null, last: raw.last ? String(raw.last) : null,
    quality: raw.quality && typeof raw.quality === 'object' ? raw.quality : {}, fieldRules: raw.fieldRules && typeof raw.fieldRules === 'object' ? raw.fieldRules : {},
    knownGaps: asStrings(raw.knownGaps)
  };
}

function toRecord(raw: Partial<ImportRecord> | null | undefined): ImportRecord | null {
  if (!raw || typeof raw !== 'object') return null;
  return {
    id: asNumber(raw.id), importer: asString(raw.importer), fileName: asString(raw.fileName), sha256: asString(raw.sha256), bytes: asNumber(raw.bytes), rows: asNumber(raw.rows),
    rejected: asNumber(raw.rejected), hostLabel: asString(raw.hostLabel), tzRule: asString(raw.tzRule), quality: raw.quality && typeof raw.quality === 'object' ? raw.quality : {},
    knownGaps: asStrings(raw.knownGaps), imported: asString(raw.imported), status: asString(raw.status) || 'ok', reason: raw.reason ? String(raw.reason) : undefined,
    supplied: asSupplied(raw.supplied)
  };
}

/** One erased case on the start list: its id and when it was erased. Nothing else is kept (no title, no content). */
export interface ErasedLine { id: string; at: string }

export const toErased = (raw: unknown): ErasedLine | null => {
  if (!raw || typeof raw !== 'object') return null;
  const line = raw as { id?: unknown; at?: unknown };
  const id = asString(line.id);
  const at = asString(line.at);
  return isCaseId(id) && at ? { id, at } : null;
};

/** The case list with the Erased lines beside it. */
export async function listCasesAndErased(signal?: AbortSignal): Promise<{ cases: CaseInfo[]; erased: ErasedLine[] }> {
  const body = await json<{ cases?: Partial<CaseInfo>[]; erased?: unknown }>(await send('/cases', {}, signal));
  const erased = Array.isArray(body.erased) ? body.erased.map(toErased).filter((l): l is ErasedLine => l !== null) : [];
  return { cases: (body.cases ?? []).map(toCase).filter((c) => isCaseId(c.id)), erased };
}

export async function listCases(signal?: AbortSignal): Promise<CaseInfo[]> {
  return (await listCasesAndErased(signal)).cases;
}

/** Clear on an Erased line: only the line goes. */
export async function clearErasedLine(id: string, signal?: AbortSignal): Promise<void> {
  if (!isCaseId(id)) throw new CaseApiError('That is not a case id.', 400, 'bad-case-id');
  await send('/cases/erased/' + id, { method: 'DELETE' }, signal);
}

/** Makes a case. With a request id (kept for one wizard run) a repeated call, a reload or a restart returns the same case. */
export async function createCase(title: string, requestId?: string, signal?: AbortSignal, key?: { key: string; confirm: string }): Promise<CaseInfo> {
  const body: { title: string; requestId?: string; caseKey?: string; caseKeyConfirm?: string } = { title: title.trim() };
  if (requestId) body.requestId = requestId;
  // The case's own key goes only in this body: never in a URL, storage or a log.
  if (key) { body.caseKey = key.key; body.caseKeyConfirm = key.confirm; }
  return toCase(await json<Partial<CaseInfo>>(await send('/cases', { method: 'POST', body: JSON.stringify(body) }, signal)));
}

const expecting = (believed?: CaseState): RequestInit => (believed ? { headers: { [EXPECT_STATE_HEADER]: believed } } : {});

/** One case with its history and its "needs another try" list. */
export async function getCaseView(id: string, signal?: AbortSignal): Promise<CaseView> {
  return toView(await json<Partial<CaseView>>(await send(casePath(id), {}, signal)));
}

export async function closeCase(id: string, believed?: CaseState, signal?: AbortSignal): Promise<CaseInfo> {
  return toCase(await json<Partial<CaseInfo>>(await send(casePath(id, '/close'), { method: 'POST', ...expecting(believed) }, signal)));
}

/** Closed to Open. It applies nothing else and starts nothing. The saved Mongo link and filters are read back when the case is opened (a Closed case reads them too), and saving them resumes now. */
export async function reopenCase(id: string, believed?: CaseState, signal?: AbortSignal): Promise<CaseInfo> {
  return toCase(await json<Partial<CaseInfo>>(await send(casePath(id, '/reopen'), { method: 'POST', ...expecting(believed) }, signal)));
}

/** The payload of an environments or plain-tag item is what the server needs to do it again ({environments} or the tag fields). */
export interface RetryInput { kind: string; key: string; label: string; reason: string; payload?: unknown }

/** Replaces the list with what is still not done. Reasons are short codes, never text from a file. */
export async function putRetries(id: string, items: readonly RetryInput[], signal?: AbortSignal): Promise<RetryItem[]> {
  const body = await json<{ retries?: unknown }>(await send(casePath(id, '/retries'), { method: 'PUT', body: JSON.stringify({ items }) }, signal));
  return toRetries(body.retries);
}

export async function deleteRetry(id: string, retryId: number, believed?: CaseState, signal?: AbortSignal): Promise<void> {
  await send(casePath(id, '/retries/' + encodeURIComponent(String(retryId))), { method: 'DELETE', ...expecting(believed) }, signal);
}

/** Asks the server to do one item again and drop it from the list. Only environments and plain tags can be done there; the rest answer 422 retry-in-page. */
export async function runRetry(id: string, retryId: number, believed?: CaseState, signal?: AbortSignal): Promise<void> {
  await send(casePath(id, '/retries/' + encodeURIComponent(String(retryId)) + '/retry'), { method: 'POST', ...expecting(believed) }, signal);
}

export const retryInPage = (error: unknown): boolean => error instanceof CaseApiError && error.reason === 'retry-in-page';

/**
 * Erases the case. The confirmation is the case code the person typed (the first 8 characters of the id; the server also takes the whole
 * id). Without one the whole id is sent, for callers that have no typed code.
 */
export async function eraseCase(id: string, typed?: string, believed?: CaseState, signal?: AbortSignal): Promise<void> {
  const confirm = typed !== undefined ? typed.trim() : id;
  await send(casePath(id), { method: 'DELETE', body: JSON.stringify({ confirm }), ...expecting(believed) }, signal);
}

export async function listImports(id: string, signal?: AbortSignal): Promise<ImportRecord[]> {
  const body = await json<{ imports?: Partial<ImportRecord>[] }>(await send(casePath(id, '/imports'), {}, signal));
  return (body.imports ?? []).map(toRecord).filter((r): r is ImportRecord => r !== null);
}

/** The host labels the owner has set up, sorted. Aliases, zones and the rest of hosts.json stay on the server. */
export async function listHosts(signal?: AbortSignal): Promise<string[]> {
  const body = await json<{ hosts?: { label?: unknown }[] }>(await send('/hosts', {}, signal));
  return (body.hosts ?? []).map((h) => asString(h?.label)).filter(Boolean).sort((a, b) => a.localeCompare(b));
}

/** One host as the owner wrote it (hosts.json). Only labels reach the import screen's picker; the editor holds the whole entry. */
export interface HostEntry {
  label: string;
  aliases: string[];
  role: string;
  tz: string;
  /** Null when the owner left it blank. */
  logicalCores: number | null;
  runtime: string;
}

export const HOST_ROLES = ['iis', 'sql', 'mongo', 'app'] as const;

function toHostEntry(raw: Partial<Record<keyof HostEntry, unknown>> | null | undefined): HostEntry {
  const cores = typeof raw?.logicalCores === 'number' && Number.isInteger(raw.logicalCores) ? raw.logicalCores : null;
  return { label: asString(raw?.label), aliases: asStrings(raw?.aliases), role: asString(raw?.role), tz: asString(raw?.tz), logicalCores: cores, runtime: asString(raw?.runtime) };
}

/** The whole host map, for the editor. */
export async function getHostEntries(signal?: AbortSignal): Promise<HostEntry[]> {
  const body = await json<{ hosts?: Partial<Record<keyof HostEntry, unknown>>[] }>(await send('/hosts', {}, signal));
  return (body.hosts ?? []).map(toHostEntry);
}

/** The JSON the server reads: empty optional fields are left out so they are never saved as blanks. */
export function hostsBody(entries: HostEntry[]): { version: 1; hosts: Record<string, unknown>[] } {
  return {
    version: 1,
    hosts: entries.map((e) => {
      const out: Record<string, unknown> = { label: e.label.trim() };
      const aliases = e.aliases.map((a) => a.trim()).filter(Boolean);
      if (aliases.length) out.aliases = aliases;
      if (e.role) out.role = e.role;
      if (e.tz.trim()) out.tz = e.tz.trim();
      if (e.logicalCores !== null) out.logicalCores = e.logicalCores;
      if (e.runtime.trim()) out.runtime = e.runtime.trim();
      return out;
    })
  };
}

/** Replaces the whole host map. The server checks it and answers with what it kept. */
export async function putHostEntries(entries: HostEntry[], signal?: AbortSignal): Promise<HostEntry[]> {
  const body = await json<{ hosts?: Partial<Record<keyof HostEntry, unknown>>[] }>(await send('/hosts', { method: 'PUT', body: JSON.stringify(hostsBody(entries)) }, signal));
  return (body.hosts ?? []).map(toHostEntry);
}

export interface ImportRequest {
  caseId: string;
  file: File;
  /** Empty asks the server to detect it. */
  importer?: string;
  /** A host label from the host list; empty when the file names its own hosts. */
  host?: string;
  /** Settings the owner told you, key to value. Only sent with an importer, as s.<key> query parameters. Blank values are never in it. */
  supplied?: Record<string, string>;
}

function importAnswer(body: Partial<{ importer: unknown; score: unknown; tied: unknown; report: Partial<ImportReport>; record: Partial<ImportRecord> }>): ImportAnswer {
  return { importer: asString(body.importer), score: asNumber(body.score), tied: asStrings(body.tied), report: toReport(body.report), record: toRecord(body.record) };
}

function importQuery(request: ImportRequest, preview: boolean, background = false): string {
  const query = new URLSearchParams();
  if (request.importer) query.set('importer', request.importer);
  if (request.host) query.set('host', request.host);
  if (request.importer) {
    // The contract (S2): s.<key>=<value>, URL-encoded here, never in the path. The server checks each against the importer's declared keys.
    for (const key of Object.keys(request.supplied ?? {}).sort().slice(0, MAX_SUPPLIED_KEYS)) {
      const value = request.supplied?.[key] ?? '';
      if (SUPPLIED_KEY_PATTERN.test(key) && value !== '') query.set('s.' + key, value);
    }
  }
  if (preview) query.set('preview', '1');
  if (background) query.set('async', '1');
  const text = query.toString();
  return text ? '?' + text : '';
}

/** Reads the whole file once on the server in a transaction that is rolled back. Nothing is kept, so no acknowledgement is sent. */
export async function previewImport(request: ImportRequest, signal?: AbortSignal): Promise<ImportAnswer> {
  const form = new FormData();
  form.append('file', request.file, request.file.name);
  const response = await send(casePath(request.caseId, '/imports') + importQuery(request, true), { method: 'POST', body: form }, signal);
  return importAnswer(await json(response));
}

/** Keeps the file. The importer is required, and the acknowledgement travels with the file: ackGating=true and the exact ackText. */
export async function commitImport(request: ImportRequest & { importer: string; ackText: string }, signal?: AbortSignal): Promise<ImportAnswer> {
  const form = new FormData();
  // The server reads the form as a stream and wants the acknowledgement before the file, so the file goes last.
  form.append('ackGating', 'true');
  form.append('ackText', request.ackText);
  form.append('file', request.file, request.file.name);
  const response = await send(casePath(request.caseId, '/imports') + importQuery(request, false), { method: 'POST', body: form }, signal);
  return importAnswer(await json(response));
}

/* ------------------------------------------------------------------ background import jobs */
// A big file is read in the background. The POST answers at once with a job; the screen asks about it once a second until it is done.

export type ImportJobState = 'queued' | 'running' | 'done' | 'failed';

/** What the server says about one background import. `answer` is set when it is done, `failure` when it failed. */
export interface ImportJob {
  id: string;
  state: ImportJobState;
  /** 'check' for a check-only job (nothing is kept); 'import' for a preview or a commit. */
  kind: 'import' | 'check';
  bytesRead: number;
  bytesTotal: number;
  answer: ImportAnswer | null;
  /** Set when a check job is done. */
  checkReport: CheckReport | null;
  failure: CaseApiError | null;
}

/* ------------------------------------------------------------------ check (no import kept) */
// The check reads each file the way a preview does and says what would happen to it. Nothing is kept.

export type CheckVerdict = 'ready' | 'needs-computer' | 'needs-zone' | 'not-supported' | 'no-computer';

/** The verdict for one file. `key` is the client's id for the file; `serverName` is the computer name the file names (needs-computer). */
export interface CheckFileVerdict {
  key: string;
  name: string;
  size: number;
  verdict: CheckVerdict;
  /** The detected kind, or '' when none was detected. */
  importer: string;
  /** Two or more kinds when the server could not choose one. */
  tied: string[];
  serverName: string;
  needsZone: boolean;
  /** The not-supported reason code, or ''. */
  reason: string;
  rows: number;
  emitted: number;
  rejected: number;
}

export interface CheckReport {
  files: CheckFileVerdict[];
  checkedAt: string;
}

const CHECK_VERDICTS: readonly string[] = ['ready', 'needs-computer', 'needs-zone', 'not-supported', 'no-computer'];
const isCheckVerdict = (value: unknown): value is CheckVerdict => typeof value === 'string' && CHECK_VERDICTS.includes(value);

function toCheckFile(raw: Record<string, unknown> | null): CheckFileVerdict | null {
  if (!raw || typeof raw.key !== 'string' || typeof raw.name !== 'string' || !isCheckVerdict(raw.verdict)) return null;
  return {
    key: raw.key, name: raw.name, size: asNumber(raw.size), verdict: raw.verdict, importer: asString(raw.importer), tied: asStrings(raw.tied),
    serverName: asString(raw.serverName), needsZone: raw.needsZone === true, reason: asString(raw.reason),
    rows: asNumber(raw.rows), emitted: asNumber(raw.emitted), rejected: asNumber(raw.rejected)
  };
}

/** A check answer checked against its shape. Any file that does not fit refuses the whole report (null), never a partial one. */
export function parseCheckReport(body: unknown): CheckReport | null {
  const raw = asObject(body);
  if (!raw || !Array.isArray(raw.files)) return null;
  const files: CheckFileVerdict[] = [];
  for (const entry of raw.files) {
    const file = toCheckFile(asObject(entry));
    if (!file) return null;
    files.push(file);
  }
  return { files, checkedAt: asString(raw.checkedAt) };
}

/** An import the person started that is still running. The screen keeps it outside the import drawer, so the drawer can show it again. */
export interface RunningImport { caseId: string; jobId: string }

/** The job id shape (the server's): 32 lowercase hex characters. It is checked before an id is put into a URL. */
export const JOB_ID_PATTERN = /^[0-9a-f]{32}$/;
export const isJobId = (id: string): boolean => JOB_ID_PATTERN.test(id);

const JOB_STATES: readonly string[] = ['queued', 'running', 'done', 'failed'];
const isJobState = (value: unknown): value is ImportJobState => typeof value === 'string' && JOB_STATES.includes(value);
const asObject = (value: unknown): Record<string, unknown> | null => (value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null);

/** A job answer checked against its shape. Anything that does not fit is refused (null), never shown. */
export function parseImportJob(body: unknown): ImportJob | null {
  const raw = asObject(body);
  if (!raw || typeof raw.id !== 'string' || !isJobId(raw.id) || !isJobState(raw.state)) return null;
  // A job from an older server has no kind: it is an import.
  const kind = raw.kind === 'check' ? 'check' : 'import';
  const job: ImportJob = { id: raw.id, state: raw.state, kind, bytesRead: Math.max(0, asNumber(raw.bytesRead)), bytesTotal: Math.max(0, asNumber(raw.bytesTotal)), answer: null, checkReport: null, failure: null };
  if (job.state === 'done') {
    const result = asObject(raw.result);
    if (!result) return null;
    if (kind === 'check') {
      job.checkReport = parseCheckReport(result);
      if (!job.checkReport) return null;
    } else {
      job.answer = importAnswer(result as Parameters<typeof importAnswer>[0]);
    }
  } else if (job.state === 'failed') {
    const failure = asObject(raw.failure);
    const status = asNumber(raw.status);
    job.failure = new CaseApiError(
      typeof failure?.error === 'string' && failure.error ? failure.error : 'Request failed (' + (status || 500) + ')',
      status || 500, asString(failure?.reason), typeof failure?.column === 'string' ? failure.column : undefined, typeof failure?.importId === 'number' ? failure.importId : undefined
    );
  }
  return job;
}

/**
 * How far along the file is, 0 to 100, or null while the size is not known yet. Reading the last byte gives 100 while the
 * server is still saving, and only a finished job reports it as done.
 */
export function importProgressPercent(job: Pick<ImportJob, 'bytesRead' | 'bytesTotal'>): number | null {
  if (!(job.bytesTotal > 0)) return null;
  return Math.max(0, Math.min(100, Math.floor((job.bytesRead / job.bytesTotal) * 100)));
}

function jobPath(caseId: string, jobId: string): string {
  if (!isJobId(jobId)) throw new CaseApiError('That is not an import id.', 400, 'bad-job-id');
  return casePath(caseId, '/imports/jobs/' + jobId);
}

async function readJob(response: Response): Promise<ImportJob> {
  const job = parseImportJob(await json<unknown>(response));
  if (!job) throw new CaseApiError('The server sent an answer that could not be read.', response.status, 'unreadable');
  return job;
}

/** Sends the file for a preview that is read in the background. Nothing is kept, so no acknowledgement is sent. */
export async function startPreviewJob(request: ImportRequest, signal?: AbortSignal): Promise<ImportJob> {
  const form = new FormData();
  form.append('file', request.file, request.file.name);
  return readJob(await send(casePath(request.caseId, '/imports') + importQuery(request, true, true), { method: 'POST', body: form }, signal));
}

/** Sends the file for a commit that is read in the background. The acknowledgement goes first, then the file, as for commitImport. */
export async function startCommitJob(request: ImportRequest & { importer: string; ackText: string }, signal?: AbortSignal): Promise<ImportJob> {
  const form = new FormData();
  form.append('ackGating', 'true');
  form.append('ackText', request.ackText);
  form.append('file', request.file, request.file.name);
  return readJob(await send(casePath(request.caseId, '/imports') + importQuery(request, false, true), { method: 'POST', body: form }, signal));
}

/** The files to check, in the order the screen lists them. `key` is the screen's id for a file; it is sent before each file and comes back on that file's verdict. */
export interface CheckRequest {
  caseId: string;
  files: { key: string; file: File }[];
}

/** Sends the files for a check that is read in the background. No importer, host or acknowledgement is sent, so nothing is kept.
 *  Each file's `key` goes in a text field just before it; the server echoes it on that file's verdict. */
export async function startCheckJob(request: CheckRequest, signal?: AbortSignal): Promise<ImportJob> {
  const form = new FormData();
  for (const { key, file } of request.files) {
    form.append('key', key);
    form.append('file', file, file.name);
  }
  return readJob(await send(casePath(request.caseId, '/imports/check') + '?async=1', { method: 'POST', body: form }, signal));
}

export async function fetchImportJob(caseId: string, jobId: string, signal?: AbortSignal): Promise<ImportJob> {
  return readJob(await send(jobPath(caseId, jobId), {}, signal, { timeoutMs: null }));
}

/** Stops a running job, or lets the server forget a finished one. */
export async function cancelImportJob(caseId: string, jobId: string, signal?: AbortSignal): Promise<void> {
  await send(jobPath(caseId, jobId), { method: 'DELETE' }, signal);
}

export const JOB_POLL_MS = 1000;
/** A few lost answers in a row are tolerated (the server may be busy); more than this and the wait gives up. */
export const JOB_MAX_UNREACHABLE = 5;

function sleepFor(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException('Aborted', 'AbortError')); return; }
    const timer = setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve(); }, ms);
    const onAbort = () => { clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')); };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

export interface WaitOptions {
  signal?: AbortSignal;
  /** Called with the job as first given and after every answer. */
  onUpdate?: (job: ImportJob) => void;
  intervalMs?: number;
  /** For tests. */
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
}

/**
 * Asks about the job until it is done or failed. Resolves with the same answer a synchronous call would give, and throws the
 * same CaseApiError a refused one would. Aborting the signal stops the asking at once and leaves the job running on the server.
 */
export async function waitForImportJob(caseId: string, first: ImportJob, options: WaitOptions = {}): Promise<ImportAnswer> {
  const sleep = options.sleep ?? sleepFor;
  let job = first;
  let lost = 0;
  options.onUpdate?.(job);
  for (;;) {
    if (job.state === 'done' && job.answer) return job.answer;
    if (job.state === 'failed') throw job.failure ?? new CaseApiError('Request failed (500)', 500);
    await sleep(options.intervalMs ?? JOB_POLL_MS, options.signal);
    try {
      job = await fetchImportJob(caseId, first.id, options.signal);
      lost = 0;
    } catch (error) {
      if (error instanceof CaseApiError && error.reason === 'unreachable' && ++lost < JOB_MAX_UNREACHABLE) continue;
      throw error;
    }
    options.onUpdate?.(job);
  }
}

/**
 * Asks about a check job until it is done or failed. Resolves with the check report, and throws the same CaseApiError a refused job
 * would. Aborting the signal stops the asking at once and leaves the job running on the server.
 */
export async function waitForCheckJob(caseId: string, first: ImportJob, options: WaitOptions = {}): Promise<CheckReport> {
  const sleep = options.sleep ?? sleepFor;
  let job = first;
  let lost = 0;
  options.onUpdate?.(job);
  for (;;) {
    if (job.checkReport) return job.checkReport;
    if (job.state === 'done') throw new CaseApiError('The server sent an answer that could not be read.', 200, 'unreadable');
    if (job.state === 'failed') throw job.failure ?? new CaseApiError('Request failed (500)', 500);
    await sleep(options.intervalMs ?? JOB_POLL_MS, options.signal);
    try {
      job = await fetchImportJob(caseId, first.id, options.signal);
      lost = 0;
    } catch (error) {
      if (error instanceof CaseApiError && error.reason === 'unreachable' && ++lost < JOB_MAX_UNREACHABLE) continue;
      throw error;
    }
    options.onUpdate?.(job);
  }
}

/* ------------------------------------------------------------------ a batch of files */
// Many files go in one after another. A file that fails never stops the next one, and every file ends as a row with a verdict.

export type BatchVerdict = 'used' | 'partly' | 'rejected';

/** How one file ended. Reasons are codes (REASON_TEXT keys, or 'duplicate'); the screen turns them into words. No file content is in here. */
export interface BatchOutcome {
  verdict: BatchVerdict;
  /** Why the whole file was refused, or ''. */
  reason: string;
  /** The most common reason rows were left out of a file that was used in part, or ''. */
  rowReason: string;
  rows: number;
  rejected: number;
  importer: string;
  importId?: number;
  column?: string;
  /** Set with the reason 'unknown-host' when the file names a computer that is not on the computer list: the name to add. */
  serverName?: string;
  /** The same file may pass on another try: the server was busy, did not answer, or was out of time. */
  transient: boolean;
}

const TRANSIENT_REASONS = new Set(['unreachable', 'timeout', 'case-import-running', 'import-busy', 'store-error', 'case-freeing', 'throttled']);
/** Refusals that mean "another import holds the case right now". The queue waits and tries the same file again. */
const BUSY_REASONS = new Set(['case-import-running', 'import-busy']);

export const isBusyRefusal = (error: unknown): boolean => error instanceof CaseApiError && BUSY_REASONS.has(error.reason);

/** The verdict for an answer. Used: every row was kept. Partly used: some rows were left out. Rejected: the file was not kept, or nothing in it was. */
export function classifyAnswer(answer: ImportAnswer): BatchOutcome {
  const record = answer.record;
  const importer = record?.importer || answer.importer || '';
  const rowReason = answer.report ? (reasonCounts(answer.report.errors)[0]?.reason ?? '') : '';
  if (!record) return { verdict: 'rejected', reason: 'import-failed', rowReason: '', rows: 0, rejected: 0, importer, transient: false };
  if (record.status === 'duplicate') return { verdict: 'rejected', reason: 'duplicate', rowReason: '', rows: 0, rejected: 0, importer, importId: record.id, transient: false };
  if (record.status !== 'ok') return { verdict: 'rejected', reason: record.reason || 'import-failed', rowReason: '', rows: record.rows, rejected: record.rejected, importer, importId: record.id, transient: false };
  if (record.rows <= 0) return { verdict: 'rejected', reason: 'no-rows', rowReason, rows: 0, rejected: record.rejected, importer, importId: record.id, transient: false };
  if (record.rejected > 0) return { verdict: 'partly', reason: '', rowReason, rows: record.rows, rejected: record.rejected, importer, importId: record.id, transient: false };
  return { verdict: 'used', reason: '', rowReason: '', rows: record.rows, rejected: 0, importer, importId: record.id, transient: false };
}

/** The verdict for a request that was refused or never answered. */
export function classifyFailure(error: unknown): BatchOutcome {
  if (error instanceof CaseApiError) {
    const reason = error.reason || 'import-failed';
    return {
      verdict: 'rejected', reason, rowReason: '', rows: 0, rejected: 0, importer: '', importId: error.importId, column: error.column, ...(error.serverName ? { serverName: error.serverName } : {}),
      transient: TRANSIENT_REASONS.has(reason) || error.status >= 500
    };
  }
  return { verdict: 'rejected', reason: 'unreachable', rowReason: '', rows: 0, rejected: 0, importer: '', transient: true };
}

/** One file to send: what the person chose for it. An empty importer asks the server to detect the type. */
export interface BatchItem {
  key: string;
  file: File;
  importer: string;
  host: string;
  supplied?: Record<string, string>;
}

/** Sends one file and answers with the server's answer. Throws a CaseApiError when refused. */
export type BatchRun = (item: BatchItem, signal?: AbortSignal) => Promise<ImportAnswer>;

export interface QueueOptions {
  signal?: AbortSignal;
  onStart?: (key: string) => void;
  onDone?: (key: string, outcome: BatchOutcome) => void;
  /** How many times a file is sent again after the case said another import was running. */
  busyRetries?: number;
  busyWaitMs?: number;
  /** For tests. */
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
}

export const BATCH_BUSY_RETRIES = 4;
export const BATCH_BUSY_WAIT_MS = 1500;

const isAbortError = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';

/**
 * Sends the files one at a time, in the order given. A file that is refused, or whose request fails, becomes a rejected outcome and the
 * queue moves on. When the case says another import is running, the same file waits a moment and goes again. Aborting the signal stops
 * the queue after the file being sent: a file whose request itself is aborted gets no outcome, a file that fails for another reason after
 * the signal was aborted is still recorded, and the files after it are not started. Returns the outcomes of the files that finished.
 */
export async function runImportQueue(items: readonly BatchItem[], run: BatchRun, options: QueueOptions = {}): Promise<Array<{ key: string; outcome: BatchOutcome }>> {
  const sleep = options.sleep ?? sleepFor;
  const retries = options.busyRetries ?? BATCH_BUSY_RETRIES;
  const done: Array<{ key: string; outcome: BatchOutcome }> = [];
  for (const item of items) {
    if (options.signal?.aborted) break;
    options.onStart?.(item.key);
    let outcome: BatchOutcome | null = null;
    for (let attempt = 0; ; attempt++) {
      try {
        outcome = classifyAnswer(await run(item, options.signal));
        break;
      } catch (error) {
        if (isAbortError(error)) return done;
        if (isBusyRefusal(error) && attempt < retries) {
          try { await sleep(options.busyWaitMs ?? BATCH_BUSY_WAIT_MS, options.signal); } catch { return done; }
          continue;
        }
        outcome = classifyFailure(error);
        break;
      }
    }
    done.push({ key: item.key, outcome });
    options.onDone?.(item.key, outcome);
  }
  return done;
}

/** What the list shows for one file of a batch: waiting its turn, being sent, or how it ended. A file with no entry has not been started. */
export type RowResult = { state: 'waiting' } | { state: 'sending' } | { state: 'done'; outcome: BatchOutcome };

/** How many files ended each way. */
export function tallyBatch(outcomes: readonly Pick<BatchOutcome, 'verdict'>[]): Record<BatchVerdict, number> {
  const tally: Record<BatchVerdict, number> = { used: 0, partly: 0, rejected: 0 };
  for (const outcome of outcomes) tally[outcome.verdict]++;
  return tally;
}

/**
 * The runner the screen uses: read the file once without keeping it to find its type (or confirm the one the person chose), then keep it
 * with the notice acknowledged. The same two steps as the single file flow, so a file is never kept as a type the server did not accept.
 */
export function importRunner(caseId: string, ackText: string, onJob?: (key: string, job: ImportJob) => void): BatchRun {
  const run = plainImportRunner(caseId, ackText, onJob);
  return async (item, signal) => {
    try {
      return await run(item, signal);
    } catch (error) {
      // The refusal carries no name. The check route reads it from the file without keeping anything, so the screen can offer to add that computer.
      if (error instanceof CaseApiError && error.reason === 'unknown-host' && !error.serverName) {
        try {
          const report = await waitForCheckJob(caseId, await startCheckJob({ caseId, files: [{ key: 'retry', file: item.file }] }, signal), { signal });
          const verdict = report.files[0];
          if (verdict?.verdict === 'needs-computer' && verdict.serverName) error.serverName = verdict.serverName;
        } catch (inner) {
          if (isAbortError(inner)) throw inner;
          /* Keep the original refusal when the name cannot be read. */
        }
      }
      throw error;
    }
  };
}

function plainImportRunner(caseId: string, ackText: string, onJob?: (key: string, job: ImportJob) => void): BatchRun {
  return async (item, signal) => {
    const base = { caseId, file: item.file, host: item.host, supplied: item.supplied };
    const tooBig = fileSizeProblem(item.file.size, item.importer);
    if (tooBig) throw new CaseApiError('File is not allowed', 413, tooBig.kind === 'empty' ? 'file-missing' : 'file-cap');
    const first = await startPreviewJob({ ...base, importer: item.importer }, signal);
    let preview: ImportAnswer;
    try {
      preview = await waitForImportJob(caseId, first, { signal, onUpdate: (job) => onJob?.(item.key, job) });
    } catch (error) {
      // Stopped while only reading: nothing is kept, so the server is told to let it go.
      if (isAbortError(error)) void cancelImportJob(caseId, first.id).catch(() => {});
      throw error;
    }
    const importer = item.importer || preview.importer;
    if (!importer || (!item.importer && preview.tied.length > 1)) throw new CaseApiError('Choose a file type', 422, 'importer-required');
    const job = await startCommitJob({ ...base, importer, ackText }, signal);
    const answer = await waitForImportJob(caseId, job, { signal, onUpdate: (next) => onJob?.(item.key, next) });
    return { ...answer, report: answer.report ?? preview.report };
  };
}

/**
 * Builds the timeline of one case against the audit window on screen. The server reads the case's events and the audit
 * intervals itself; the answer is checked against the shape before it is used, and a bad shape is refused, not shown.
 */
export async function fetchTimeline(id: string, query: Query, signal?: AbortSignal): Promise<TimelineView> {
  const response = await send(casePath(id, '/timeline'), { method: 'POST', body: JSON.stringify({ query }) }, signal, { timeoutMs: READ_TIMEOUT_MS });
  const view = parseTimelineView(await json<unknown>(response));
  if (!view) throw new CaseApiError('The server sent an answer that could not be read.', response.status, 'unreadable');
  return view;
}

/** What a case export preview holds: the redacted file, shown whole, before anything is saved. */
export interface CaseExportPreview { previewId: string; export: EvidenceExport }

export async function previewCaseExport(id: string, signal?: AbortSignal): Promise<CaseExportPreview> {
  if (!isCaseId(id)) throw new CaseApiError('That is not a case id.', 400, 'bad-case-id');
  const body = await json<Partial<CaseExportPreview>>(await send('/export/preview', { method: 'POST', body: JSON.stringify({ caseId: id }) }, signal));
  const exp = body.export;
  if (typeof body.previewId !== 'string' || !body.previewId || !exp || typeof exp !== 'object' || !exp.manifest || !exp.body) throw new CaseApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return { previewId: body.previewId, export: exp };
}

/** The server rebuilds the files from the case as it is now and answers with the JSON file and the events CSV, each with its SHA-256. A 409 means the case changed after the preview. */
export async function confirmCaseExport(previewId: string, caseId: string, signal?: AbortSignal): Promise<ExportFile> {
  if (!isCaseId(caseId)) throw new CaseApiError('That is not a case id.', 400, 'bad-case-id');
  const file = await json<Partial<ExportFile>>(await send('/export/confirm', { method: 'POST', body: JSON.stringify({ previewId, caseId }) }, signal));
  return { filename: asString(file.filename), content: asString(file.content), sha256: asString(file.sha256), eventsCsvFilename: asString(file.eventsCsvFilename), eventsCsv: asString(file.eventsCsv), eventsCsvSha256: asString(file.eventsCsvSha256) };
}

export interface CaseExportSummary { imports: number; events: number; hash: string; empty: boolean; statements: string[]; line: string }

const plural = (n: number, one: string, many: string): string => n + ' ' + (n === 1 ? one : many);

/** Counts for the line above a case export preview. A case with no imported events has nothing to export. */
export function caseExportSummary(exp: EvidenceExport): CaseExportSummary {
  const imports = exp.body.imports?.length ?? 0;
  const events = exp.body.events?.length ?? 0;
  return { imports, events, hash: exp.manifest.bodySha256, empty: imports === 0 && events === 0, statements: exp.manifest.masking?.statements ?? [], line: plural(imports, 'import', 'imports') + ', ' + plural(events, 'event', 'events') };
}

/** Save is allowed only for a preview that holds something. */
export const canSaveCaseExport = (exp: EvidenceExport | null): boolean => exp !== null && !caseExportSummary(exp).empty;

/* ------------------------------------------------------------------ settings the owner told you (card U3) */
// GET /api/importers -> { importers: [{ name, supplied: [{ key, kind: 'enum'|'number'|'text', values?, min?, max?, integer?, maxLen? }] }] }
// An importer lists only the keys it reads. The screen offers exactly those, checks each value the way the server does, and sends
// the ones that are filled in as s.<key>=<value> query parameters. The server checks them again and refuses with "bad-supplied".

/** The shape of a supplied key (the server's: a lowercase letter or digit, then lowercase letters, digits and hyphens, 40 at most). */
export const SUPPLIED_KEY_PATTERN = /^[a-z0-9][a-z0-9-]{0,39}$/;
/** The most settings one import may carry. */
export const MAX_SUPPLIED_KEYS = 16;

export type SuppliedSpec =
  | { key: string; kind: 'enum'; values: string[] }
  | { key: string; kind: 'number'; min: number; max: number; integer: boolean }
  | { key: string; kind: 'text'; maxLen: number };

export interface ImporterInfo { name: string; supplied: SuppliedSpec[] }

export type SuppliedProblem = 'not-a-number' | 'not-whole' | 'too-low' | 'too-high' | 'not-in-set' | 'too-long' | 'bad-characters';

const MAX_SUPPLIED_VALUE = 512;

function toSpec(raw: unknown): SuppliedSpec | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const key = asString(r.key);
  if (!SUPPLIED_KEY_PATTERN.test(key)) return null;
  if (r.kind === 'enum') {
    const values = asStrings(r.values);
    return values.length ? { key, kind: 'enum', values } : null;
  }
  if (r.kind === 'number') {
    if (typeof r.min !== 'number' || typeof r.max !== 'number' || !Number.isFinite(r.min) || !Number.isFinite(r.max) || r.min > r.max) return null;
    return { key, kind: 'number', min: r.min, max: r.max, integer: r.integer === true };
  }
  if (r.kind === 'text') {
    const maxLen = typeof r.maxLen === 'number' && r.maxLen > 0 ? Math.min(Math.floor(r.maxLen), MAX_SUPPLIED_VALUE) : MAX_SUPPLIED_VALUE;
    return { key, kind: 'text', maxLen };
  }
  return null;
}

/** The importers an answer lists, each with only the keys that have a usable spec. Anything else in the answer is dropped. */
export function parseImporters(body: unknown): ImporterInfo[] {
  const list = body && typeof body === 'object' ? (body as { importers?: unknown }).importers : null;
  if (!Array.isArray(list)) return [];
  const out: ImporterInfo[] = [];
  for (const item of list) {
    const name = asString((item as { name?: unknown } | null)?.name);
    if (!name) continue;
    const rawSupplied = (item as { supplied?: unknown }).supplied;
    const supplied = (Array.isArray(rawSupplied) ? rawSupplied : []).map(toSpec).filter((s): s is SuppliedSpec => s !== null).slice(0, MAX_SUPPLIED_KEYS);
    out.push({ name, supplied });
  }
  return out;
}

export async function listImporters(signal?: AbortSignal): Promise<ImporterInfo[]> {
  return parseImporters(await json<unknown>(await send('/importers', {}, signal)));
}

const PLAIN_NUMBER = /^-?[0-9]{1,15}(\.[0-9]{1,9})?$/;
/** Control, line-break and zero-width characters: the server refuses them in any supplied value. */
const HIDDEN_CHARACTERS = /[\u0000-\u001f\u007f-\u009f\u00ad\u200b-\u200f\u2028-\u202e\u2060-\u2064\ufeff]/;

/**
 * Checks what was typed for one key. Blank (after trimming) is not a problem: it means the setting is not sent, and `value` is null.
 * Otherwise `value` is what would be sent. A problem means nothing will be sent for the key.
 */
export function checkSupplied(spec: SuppliedSpec, typed: string): { value: string | null; problem: SuppliedProblem | null } {
  const text = typed.trim();
  if (text === '') return { value: null, problem: null };
  const bad = (problem: SuppliedProblem) => ({ value: null, problem });
  if (HIDDEN_CHARACTERS.test(text)) return bad('bad-characters');
  switch (spec.kind) {
    case 'enum':
      return spec.values.includes(text) ? { value: text, problem: null } : bad('not-in-set');
    case 'number': {
      if (!PLAIN_NUMBER.test(text)) return bad('not-a-number');
      const n = Number(text);
      if (spec.integer && !Number.isInteger(n)) return bad('not-whole');
      if (n < spec.min) return bad('too-low');
      if (n > spec.max) return bad('too-high');
      return { value: text, problem: null };
    }
    default:
      return new TextEncoder().encode(text).length > spec.maxLen ? bad('too-long') : { value: text, problem: null };
  }
}

/** Checks every typed value against the keys the importer declares. Only declared keys are looked at; the rest of `typed` is ignored. */
export function checkAllSupplied(specs: SuppliedSpec[], typed: Record<string, string>): { values: Record<string, string>; problems: Record<string, SuppliedProblem> } {
  const values: Record<string, string> = {};
  const problems: Record<string, SuppliedProblem> = {};
  for (const spec of specs) {
    const result = checkSupplied(spec, typed[spec.key] ?? '');
    if (result.problem) problems[spec.key] = result.problem;
    else if (result.value !== null) values[spec.key] = result.value;
  }
  return { values, problems };
}

/* ------------------------------------------------------------------ rules the screen follows */

const MiB = 1024 * 1024;
const GiB = 1024 * MiB;

/** Byte caps per importer (plan section 4). A file over its cap is refused whole. */
export const IMPORT_CAPS: Record<string, number> = {
  'win-counters': 512 * MiB,
  'win-events': 256 * MiB,
  'iis-w3c': 2 * GiB,
  'iis-httperr': 64 * MiB,
  'iis-freb': 256 * MiB,
  'mongod-json': 2 * GiB,
  'schedule-hangfire': 16 * MiB,
  'schedule-agent': 16 * MiB,
  'schedule-quartz': 2 * GiB,
  'sql-querystore': 512 * MiB,
  appinsights: 512 * MiB,
  'sql-xe': 512 * MiB,
  'sql-errorlog': 256 * MiB,
  'sql-pdh': 512 * MiB,
  // Wave 2 (wave-2 plan section 2.4), listed before the importers ship.
  'log4net-text': 2 * GiB,
  'log4net-xml': 2 * GiB,
  'log4net-blocks': 2 * GiB,
  'serilog-clef': 2 * GiB,
  'serilog-text': 2 * GiB,
  'nlog-text': 2 * GiB,
  'nginx-access': 2 * GiB,
  'apache-combined': 2 * GiB,
  'mel-console': 2 * GiB,
  'mel-json': 2 * GiB,
  'ancm-stdout': 256 * MiB,
  'node-jsonl': 2 * GiB,
  'dotnet-counters': 512 * MiB,
  'win-procsnap': 256 * MiB,
  'redis-slowlog': 16 * MiB,
  'redis-latency': 4 * MiB,
  'redis-info': 64 * MiB,
  'redis-log': 512 * MiB,
  'mongo-profile': 1 * GiB,
  'mongo-currentop': 256 * MiB,
  'mongo-serverstatus': 512 * MiB,
  'mongostat-json': 256 * MiB,
  'linux-sadf': 256 * MiB,
  'linux-pidstat': 256 * MiB,
  'linux-vmstat': 256 * MiB
};
/** No importer takes more than this, so a larger file is refused before it is sent. */
export const LARGEST_CAP = 2 * GiB;

/** The cap that applies: the importer's own once it is known, otherwise the largest of all. */
export const capFor = (importer?: string): number => (importer ? IMPORT_CAPS[importer] : undefined) ?? LARGEST_CAP;

/** Null when the file fits; otherwise the cap it is over. An empty file is not sent either. */
export function fileSizeProblem(bytes: number, importer?: string): { kind: 'empty' } | { kind: 'too-big'; cap: number } | null {
  if (bytes <= 0) return { kind: 'empty' };
  const cap = capFor(importer);
  return bytes > cap ? { kind: 'too-big', cap } : null;
}

/** What a preview was asked about. A preview holds only for this exact file, importer and host. */
export function previewKey(file: Pick<File, 'name' | 'size' | 'lastModified'> | null, importer: string, host: string, supplied: Record<string, string> = {}): string {
  if (!file) return '';
  const base = [file.name, file.size, file.lastModified, importer, host].join('|');
  const keys = Object.keys(supplied).sort();
  // The settings are part of what was asked, but a key with none leaves the key as it was before settings existed.
  return keys.length ? base + '|' + keys.map((k) => k + '=' + supplied[k]).join(';') : base;
}

export interface CommitGate {
  caseOpen: boolean;
  /** The key of the preview on screen, and the key of what is chosen now. */
  previewedKey: string;
  currentKey: string;
  importer: string;
  report: ImportReport | null;
  /** The preview was refused with the reason "unknown-host" (or the report still lists a host the map does not know). */
  unknownHost: boolean;
  acknowledged: boolean;
  busy: boolean;
  /** A setting the owner typed does not fit its key. The preview on screen did not use it, so the file must not be kept yet. */
  settingsProblem?: boolean;
}

/** Why the Import button is off, as a short code the screen turns into words; null means it may be pressed. */
export function commitBlock(gate: CommitGate): 'closed' | 'busy' | 'no-preview' | 'stale' | 'unknown-host' | 'settings' | 'nothing' | 'ack' | null {
  if (!gate.caseOpen) return 'closed';
  if (gate.busy) return 'busy';
  // An unknown host blocks commit before anything else: the preview was refused, so there is no report to point at.
  if (gate.unknownHost) return 'unknown-host';
  if (gate.settingsProblem) return 'settings';
  if (!gate.report || !gate.importer) return 'no-preview';
  if (gate.previewedKey !== gate.currentKey) return 'stale';
  if (gate.report.emitted <= 0) return 'nothing';
  if (!gate.acknowledged) return 'ack';
  return null;
}

/** Row errors counted by reason, most common first. The server sends only the first 100 errors, so these are a sample. */
export function reasonCounts(errors: RowError[]): { reason: string; count: number }[] {
  const counts = new Map<string, number>();
  for (const e of errors) counts.set(e.reason, (counts.get(e.reason) ?? 0) + 1);
  return [...counts.entries()].map(([reason, count]) => ({ reason, count })).sort((a, b) => b.count - a.count || a.reason.localeCompare(b.reason));
}

// ---- Resource counters (card U1): the resource answer and the series the panel draws ----
//   GET /api/cases/{id}/resource?from=&to=&ep=<start>/<end>...  -> ResourceView (lib/resource.ts)
//   GET /api/cases/{id}/series?from=&to=&bucket=<ms>&sel=...    -> SeriesView   (lib/resource.ts)
// The shape checks live in resource.ts; an answer that fails one is "unreadable", like every other route here.

export interface ResourceRequest { fromMs: number; toMs: number; episodes?: Array<{ start: number; end: number }> }
export interface SeriesRequest { fromMs: number; toMs: number; bucketMs: number; sels: string[] }

const rfc3339 = (ms: number): string => new Date(ms).toISOString();
const unreadable = (status = 200) => new CaseApiError('The server sent an answer that could not be read.', status, 'unreadable');

export async function fetchResource(id: string, request: ResourceRequest, signal?: AbortSignal): Promise<ResourceView> {
  const params = new URLSearchParams();
  params.set('from', rfc3339(request.fromMs));
  params.set('to', rfc3339(request.toMs));
  for (const span of request.episodes ?? []) params.append('ep', rfc3339(span.start) + '/' + rfc3339(span.end));
  const response = await send(casePath(id, '/resource') + '?' + params.toString(), {}, signal);
  const view = parseResource(await json<unknown>(response));
  if (!view) throw unreadable(response.status);
  return view;
}

/** GET /api/cases/{id}/collect: the logs still worth collecting, ranked. An answer that is not the documented shape is "unreadable". */
export async function fetchCollect(id: string, signal?: AbortSignal): Promise<CollectItem[]> {
  const response = await send(casePath(id, '/collect'), {}, signal);
  const items = parseCollect(await json<unknown>(response));
  if (!items) throw unreadable(response.status);
  return items;
}

export async function fetchSeries(id: string, request: SeriesRequest, signal?: AbortSignal): Promise<SeriesView> {
  const params = new URLSearchParams();
  params.set('from', rfc3339(request.fromMs));
  params.set('to', rfc3339(request.toMs));
  params.set('bucket', String(Math.max(1, Math.floor(request.bucketMs))));
  for (const sel of request.sels) params.append('sel', sel);
  const response = await send(casePath(id, '/series') + '?' + params.toString(), {}, signal);
  const view = parseSeries(await json<unknown>(response));
  if (!view) throw unreadable(response.status);
  return view;
}
