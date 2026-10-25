import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import { parseTimelineView, type TimelineView } from './timeline';
import { parseResource, parseSeries, type ResourceView, type SeriesView } from './resource';
import { parseCollect, type CollectItem } from './collect';
import type { EvidenceExport, ExportFile, Query } from './types';

/**
 * Client for the case and import routes (importer wave plan, card API), and the pure rules the import screen follows.
 * Nothing here holds copy: the screen turns a `reason` into words with importCopy.ts.
 *
 * The contract this file mocks (JSON is camelCase; every request carries X-Audit-Token):
 *   GET    /api/cases                          -> { cases: CaseInfo[] }
 *   POST   /api/cases            {title}       -> CaseInfo
 *   POST   /api/cases/{id}/close               -> CaseInfo
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

/** True only when the typed text is this case's code (case does not matter, spaces around it do not). */
export function eraseConfirmed(typed: string, id: string): boolean {
  return isCaseId(id) && typed.trim().toLowerCase() === caseCode(id);
}

export interface CaseInfo {
  id: string;
  title: string;
  created: string;
  closed?: string | null;
  bytes: number;
  imports: number;
}

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

/* ------------------------------------------------------------------ requests */

export async function send(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<Response> {
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
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new CaseApiError('The server could not be reached.', 0, 'unreachable');
  }
  if (response.status === 401) {
    // The dashboard refused the token: forget it so nothing keeps sending it, and show the usual message.
    forgetSession();
    throw new CaseApiError(NO_SESSION_MESSAGE, 401, 'no_session');
  }
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

export async function json<T>(response: Response): Promise<T> {
  try { return await response.json() as T; } catch { throw new CaseApiError('The server sent an answer that could not be read.', response.status, 'unreadable'); }
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

function toCase(raw: Partial<CaseInfo>): CaseInfo {
  return { id: asString(raw.id), title: asString(raw.title), created: asString(raw.created), closed: raw.closed ? String(raw.closed) : null, bytes: asNumber(raw.bytes), imports: asNumber(raw.imports) };
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

export async function listCases(signal?: AbortSignal): Promise<CaseInfo[]> {
  const body = await json<{ cases?: Partial<CaseInfo>[] }>(await send('/cases', {}, signal));
  return (body.cases ?? []).map(toCase).filter((c) => isCaseId(c.id));
}

export async function createCase(title: string, signal?: AbortSignal): Promise<CaseInfo> {
  return toCase(await json<Partial<CaseInfo>>(await send('/cases', { method: 'POST', body: JSON.stringify({ title: title.trim() }) }, signal)));
}

export async function closeCase(id: string, signal?: AbortSignal): Promise<CaseInfo> {
  return toCase(await json<Partial<CaseInfo>>(await send(casePath(id, '/close'), { method: 'POST' }, signal)));
}

/** Erases the case. The server wants the id echoed back as the confirmation; the screen has already had the code typed. */
export async function eraseCase(id: string, signal?: AbortSignal): Promise<void> {
  await send(casePath(id), { method: 'DELETE', body: JSON.stringify({ confirm: id }) }, signal);
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
  bytesRead: number;
  bytesTotal: number;
  answer: ImportAnswer | null;
  failure: CaseApiError | null;
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
  const job: ImportJob = { id: raw.id, state: raw.state, bytesRead: Math.max(0, asNumber(raw.bytesRead)), bytesTotal: Math.max(0, asNumber(raw.bytesTotal)), answer: null, failure: null };
  if (job.state === 'done') {
    const result = asObject(raw.result);
    if (!result) return null;
    job.answer = importAnswer(result as Parameters<typeof importAnswer>[0]);
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

export async function fetchImportJob(caseId: string, jobId: string, signal?: AbortSignal): Promise<ImportJob> {
  return readJob(await send(jobPath(caseId, jobId), {}, signal));
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
 * Builds the timeline of one case against the audit window on screen. The server reads the case's events and the audit
 * intervals itself; the answer is checked against the shape before it is used, and a bad shape is refused, not shown.
 */
export async function fetchTimeline(id: string, query: Query, signal?: AbortSignal): Promise<TimelineView> {
  const response = await send(casePath(id, '/timeline'), { method: 'POST', body: JSON.stringify({ query }) }, signal);
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

/** Using the preview up: the server answers once, with the JSON file and the events CSV, each with its SHA-256. */
export async function confirmCaseExport(previewId: string, signal?: AbortSignal): Promise<ExportFile> {
  const file = await json<Partial<ExportFile>>(await send('/export/confirm', { method: 'POST', body: JSON.stringify({ previewId }) }, signal));
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
