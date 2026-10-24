import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';
import { parseTimelineView, type TimelineView } from './timeline';
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

async function send(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<Response> {
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

async function json<T>(response: Response): Promise<T> {
  try { return await response.json() as T; } catch { throw new CaseApiError('The server sent an answer that could not be read.', response.status, 'unreadable'); }
}

function casePath(id: string, tail = ''): string {
  if (!isCaseId(id)) throw new CaseApiError('That is not a case id.', 400, 'bad-case-id');
  return '/cases/' + id + tail;
}

const asNumber = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const asString = (value: unknown): string => (typeof value === 'string' ? value : '');
const asStrings = (value: unknown): string[] => (Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []);

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
    knownGaps: asStrings(raw.knownGaps), imported: asString(raw.imported), status: asString(raw.status) || 'ok', reason: raw.reason ? String(raw.reason) : undefined
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
}

function importAnswer(body: Partial<{ importer: unknown; score: unknown; tied: unknown; report: Partial<ImportReport>; record: Partial<ImportRecord> }>): ImportAnswer {
  return { importer: asString(body.importer), score: asNumber(body.score), tied: asStrings(body.tied), report: toReport(body.report), record: toRecord(body.record) };
}

function importQuery(request: ImportRequest, preview: boolean): string {
  const query = new URLSearchParams();
  if (request.importer) query.set('importer', request.importer);
  if (request.host) query.set('host', request.host);
  if (preview) query.set('preview', '1');
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

/* ------------------------------------------------------------------ rules the screen follows */

const MiB = 1024 * 1024;
const GiB = 1024 * MiB;

/** Byte caps per importer (plan section 4). A file over its cap is refused whole. */
export const IMPORT_CAPS: Record<string, number> = {
  'win-counters': 512 * MiB,
  'win-events': 256 * MiB,
  'iis-w3c': 2 * GiB,
  'iis-httperr': 64 * MiB,
  'mongod-json': 2 * GiB,
  'schedule-hangfire': 16 * MiB,
  'schedule-agent': 16 * MiB,
  appinsights: 512 * MiB,
  'sql-xe': 512 * MiB,
  'sql-errorlog': 256 * MiB,
  'sql-pdh': 512 * MiB,
  // Wave 2 (wave-2 plan section 2.4), listed before the importers ship.
  'log4net-text': 2 * GiB,
  'log4net-xml': 2 * GiB,
  'serilog-clef': 2 * GiB,
  'serilog-text': 2 * GiB,
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
  'linux-pidstat': 256 * MiB
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
export function previewKey(file: Pick<File, 'name' | 'size' | 'lastModified'> | null, importer: string, host: string): string {
  return file ? [file.name, file.size, file.lastModified, importer, host].join('|') : '';
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
}

/** Why the Import button is off, as a short code the screen turns into words; null means it may be pressed. */
export function commitBlock(gate: CommitGate): 'closed' | 'busy' | 'no-preview' | 'stale' | 'unknown-host' | 'nothing' | 'ack' | null {
  if (!gate.caseOpen) return 'closed';
  if (gate.busy) return 'busy';
  // An unknown host blocks commit before anything else: the preview was refused, so there is no report to point at.
  if (gate.unknownHost) return 'unknown-host';
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
