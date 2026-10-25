import { CaseApiError, casePath, fetchImportJob, cancelImportJob, getHostEntries, isJobId, json, putHostEntries, send, type ImportJob } from './cases';
import { unreadableText } from './copy/discoverCopy';

/**
 * Client for the "This computer" routes (host-discovery spec, section 7), and the pure rules the discover panel follows.
 * Nothing here holds copy: the panel turns states and reasons into words with copy/discoverCopy.ts.
 *
 * The contract this file mocks (JSON is camelCase; every request carries X-Audit-Token):
 *   GET    /api/discover/capabilities              -> { supported, elevated, os }
 *   POST   /api/discover/scans            {}       -> 202 { scanId }                          (409 scan-busy while one runs)
 *   GET    /api/discover/scans/{scan}              -> { state, startedAt, findings[], gaps[], host, progress? }
 *   DELETE /api/discover/scans/{scan}              -> 204
 *   POST   /api/cases/{id}/discover/imports {scanId, findingIds, window, ackGating, ackText}
 *                                                  -> 202 { jobs: [{ findingId, jobId }] }   (429 import-busy)
 *   GET    /api/cases/{id}/imports/jobs/{job}      -> the existing job answer (cases.ts)
 *   POST   /api/cases/{id}/sampler {intervalSec, ackGating, ackText} -> 202 { state }         (409 sampler-busy)
 *   GET    /api/cases/{id}/sampler                 -> { state, since, samples, stopReason }
 *   DELETE /api/cases/{id}/sampler                 -> 204
 *   POST   /api/discover/scans/{scan}/findings/{finding}/locate {path} -> { verified, files, reason, finding? }   (locateFinding)
 *   POST   /api/probe/reachable {host, port}       -> { reachable, reason }                                       (probeReachable)
 *
 * The only path the client sends is one the person typed, in the body of locateFinding. A finding is otherwise named by the opaque id the
 * server gave it, and the Finding type here has no path field, so a path the server sent by mistake is dropped when the answer is read.
 */

export const SCAN_ID_PATTERN = /^[0-9a-f]{32}$/;
export const isScanId = (id: string): boolean => SCAN_ID_PATTERN.test(id);
/** A finding id is opaque. This only checks it is short and made of safe characters before it is put in a request or on the screen. */
export const FINDING_ID_PATTERN = /^[A-Za-z0-9_-]{4,64}$/;
export const isFindingId = (id: string): boolean => FINDING_ID_PATTERN.test(id);

/** The server takes at most this many findings in one import call, and a window no longer than this. */
export const MAX_FINDINGS_PER_CALL = 64;
export const MAX_WINDOW_MS = 31 * 24 * 3600 * 1000;
/** The server drops a scan after an hour. */
export const SCAN_TTL_MS = 3600 * 1000;
/** A source this big or bigger is "large" whatever the server said: it has no default tick and needs the window. */
export const LARGE_BYTES = 1024 * 1024 * 1024;
export const MEDIUM_BYTES = 64 * 1024 * 1024;

/**
 * A cancel button can sit where the start button was a moment ago, so the second click of a double-click lands on it. A click on a
 * cancel control inside this many milliseconds of the step starting is ignored.
 */
export const CANCEL_GUARD_MS = 700;
export const cancelAllowed = (shownAtMs: number, nowMs: number): boolean => nowMs - shownAtMs >= CANCEL_GUARD_MS;

export const SAMPLER_INTERVALS = [5, 15, 30, 60] as const;
export const DEFAULT_SAMPLER_INTERVAL = 15;

const asObject = (value: unknown): Record<string, unknown> | null => (value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null);
const asString = (value: unknown, max = 400): string => (typeof value === 'string' ? value.slice(0, max) : '');
const asNumber = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const asBool = (value: unknown): boolean => value === true;
const asArray = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);
const asTime = (value: unknown): string => { const text = asString(value, 40); return text !== '' && Number.isFinite(Date.parse(text)) ? text : ''; };

/* ------------------------------------------------------------------ capabilities */

export interface Capabilities { supported: boolean; elevated: boolean; os: string }

export function parseCapabilities(body: unknown): Capabilities | null {
  const raw = asObject(body);
  if (!raw || typeof raw.supported !== 'boolean') return null;
  return { supported: raw.supported, elevated: asBool(raw.elevated), os: asString(raw.os, 40) };
}

/* ------------------------------------------------------------------ findings and gaps */

export const FINDING_KINDS = ['log-file', 'log-set', 'event-channel', 'live-series', 'export-needed', 'remote-only'] as const;
export type FindingKind = typeof FINDING_KINDS[number];
export type Cost = 'small' | 'medium' | 'large';

/** One source the scan found, as the client sees it. There is no path field: the server resolves the id itself. */
export interface Finding {
  id: string;
  detector: string;
  kind: FindingKind;
  importer: string;
  host: string;
  display: string;
  confidence: number;
  /** A wording key (such as "discover.iis.shows") the panel maps to text. */
  shows: string;
  /** For export-needed and remote-only: a wording key for the exact step. */
  how: string;
  sizeBytes: number;
  files: number;
  oldest: string;
  newest: string;
  needsAdmin: boolean;
  cost: Cost;
  /** True only when the scan opened a sample of the source and its importer recognised it. Nothing unverified can be ticked. */
  verified: boolean;
  /** For a source found but not readable: a wording key (such as "discover.wrongformat.shows") for why. '' when there is no problem. */
  unreadable: string;
  /** Words only, taken from folder names: a guess at the Portal and API name for this source. Null when nothing usable was found. */
  suggest: Suggestion | null;
}

/** The Portal and API names the scan guessed from a source's folders. Either may be ''. */
export interface Suggestion { portal: string; api: string }

export type GapReason = 'needs-elevation' | 'not-found' | 'not-supported-os' | 'unreadable' | 'too-large' | 'unsafe-path' | 'config-unparsed';
export const GAP_REASONS: readonly string[] = ['needs-elevation', 'not-found', 'not-supported-os', 'unreadable', 'too-large', 'unsafe-path', 'config-unparsed'];

/** A place the scan looked and could not use. `reason` is one of GAP_REASONS, or '' when the server sent a name this build does not know. */
export interface Gap { detector: string; kind: string; display: string; reason: GapReason | '' }

const costFromSize = (bytes: number): Cost => (bytes >= LARGE_BYTES ? 'large' : bytes >= MEDIUM_BYTES ? 'medium' : 'small');
const COST_RANK: Record<Cost, number> = { small: 0, medium: 1, large: 2 };

/** The cost the panel acts on: the server's, or the one the size gives, whichever is larger. A big source is never treated as small. */
export function effectiveCost(finding: Pick<Finding, 'cost' | 'sizeBytes'>): Cost {
  const bySize = costFromSize(finding.sizeBytes);
  return COST_RANK[bySize] > COST_RANK[finding.cost] ? bySize : finding.cost;
}

export function parseFinding(body: unknown): Finding | null {
  const raw = asObject(body);
  if (!raw) return null;
  const id = asString(raw.id, 80);
  const kind = FINDING_KINDS.find((k) => k === raw.kind);
  if (!isFindingId(id) || !kind) return null;
  const sizeBytes = Math.max(0, asNumber(raw.sizeBytes));
  const declared = raw.cost === 'small' || raw.cost === 'medium' || raw.cost === 'large' ? raw.cost : costFromSize(sizeBytes);
  // Only the fields below are copied. Anything else the server sent (a path, for example) stops here.
  return {
    id, kind,
    detector: asString(raw.detector, 60),
    importer: asString(raw.importer, 60),
    host: asString(raw.host, 80),
    display: asString(raw.display, 200),
    confidence: Math.max(0, Math.min(100, Math.round(asNumber(raw.confidence)))),
    shows: asString(raw.shows, 80),
    how: asString(raw.how, 80),
    sizeBytes,
    files: Math.max(0, Math.floor(asNumber(raw.files))),
    oldest: asTime(raw.oldest),
    newest: asTime(raw.newest),
    needsAdmin: asBool(raw.needsAdmin),
    cost: declared,
    // Only a literal true counts: a missing or odd value reads as "not verified".
    verified: raw.verified === true,
    unreadable: asString(raw.unreadable, 80),
    suggest: parseSuggestion(raw.suggest)
  };
}

function parseSuggestion(body: unknown): Suggestion | null {
  const raw = asObject(body);
  if (!raw) return null;
  const portal = asString(raw.portal, 40), api = asString(raw.api, 40);
  return portal === '' && api === '' ? null : { portal, api };
}

export function parseGap(body: unknown): Gap | null {
  const raw = asObject(body);
  if (!raw) return null;
  const reason = GAP_REASONS.find((r) => r === raw.reason) as GapReason | undefined;
  return { detector: asString(raw.detector, 60), kind: asString(raw.kind, 40), display: asString(raw.display, 200), reason: reason ?? '' };
}

/* ------------------------------------------------------------------ scans */

export type ScanState = 'running' | 'done' | 'cancelled' | 'failed';
const SCAN_STATES: readonly string[] = ['running', 'done', 'cancelled', 'failed'];

export type DetectorState = 'waiting' | 'running' | 'done' | 'skipped' | 'failed';
const DETECTOR_STATES: readonly string[] = ['waiting', 'running', 'done', 'skipped', 'failed'];
export interface DetectorProgress { detector: string; state: DetectorState }

/** The computer the scan ran on, as the server proposes to name it. `proposed` is true when the label is new and has not been confirmed. */
export interface ScanHost { label: string; proposed: boolean; tz: string; cores: number; aliases: string[] }

export interface Scan {
  state: ScanState;
  startedAt: string;
  findings: Finding[];
  gaps: Gap[];
  host: ScanHost | null;
  /** Optional on the server: absent means the panel shows one plain line instead of a list. */
  progress: DetectorProgress[];
}

export function parseScan(body: unknown): Scan | null {
  const raw = asObject(body);
  if (!raw || typeof raw.state !== 'string' || !SCAN_STATES.includes(raw.state)) return null;
  const host = asObject(raw.host);
  const progress: DetectorProgress[] = [];
  for (const item of asArray(raw.progress)) {
    const entry = asObject(item);
    if (entry && typeof entry.detector === 'string' && typeof entry.state === 'string' && DETECTOR_STATES.includes(entry.state)) progress.push({ detector: entry.detector.slice(0, 60), state: entry.state as DetectorState });
  }
  return {
    state: raw.state as ScanState,
    startedAt: asTime(raw.startedAt),
    findings: asArray(raw.findings).map(parseFinding).filter((f): f is Finding => f !== null),
    gaps: asArray(raw.gaps).map(parseGap).filter((g): g is Gap => g !== null),
    host: host ? { label: asString(host.label, 80), proposed: asBool(host.proposed), tz: asString(host.tz, 80), cores: Math.max(0, Math.floor(asNumber(host.cores))), aliases: Array.isArray(host.aliases) ? host.aliases.filter((a): a is string => typeof a === 'string' && a.length > 0 && a.length <= 253).slice(0, 4) : [] } : null,
    progress
  };
}

/** True when a finished scan is too old for the server to still hold it. The server drops it after an hour; this is the client's own check. */
export const scanExpired = (startedAt: string, nowMs: number): boolean => {
  const started = Date.parse(startedAt);
  return Number.isFinite(started) && nowMs - started >= SCAN_TTL_MS;
};

/* ------------------------------------------------------------------ the checklist */

export const GROUPS = ['web', 'windows', 'app', 'databases', 'live'] as const;
export type GroupId = typeof GROUPS[number];

const WEB = /iis|httperr|http\.sys|nginx|apache|w3c/i;
const DATABASE = /sql|mongo|redis/i;
const WINDOWS = /event|win-|perf|counter|windows|procsnap/i;

/** Which of the five checklist groups a row belongs to. A name nothing matches goes to App logs. */
export function groupOf(item: { detector: string; importer?: string; kind: string }): GroupId {
  if (item.kind === 'live-series') return 'live';
  for (const text of [item.detector, item.importer ?? '']) {
    if (WEB.test(text)) return 'web';
    if (DATABASE.test(text)) return 'databases';
    if (WINDOWS.test(text)) return 'windows';
  }
  return 'app';
}

const isFileKind = (kind: string): boolean => kind === 'log-file' || kind === 'log-set' || kind === 'event-channel';

/**
 * Only a source with an importer that is a file, a set of files or an event channel can be ticked, and only when the scan checked it
 * (`verified`). The rest show their steps or the reason they cannot be read. A finding with no `verified` field is not verified.
 */
export const isSelectable = (finding: Pick<Finding, 'kind' | 'importer'> & { verified?: boolean }): boolean =>
  isFileKind(finding.kind) && finding.importer !== '' && finding.verified === true;

/**
 * The plain-language reason a file or event source cannot be ticked, or '' when it can be (or when it never was a readable kind, such as a
 * source with steps to follow). A source with no reason from the server but not verified gets a general line, never an empty one.
 */
export function readinessLabel(finding: Pick<Finding, 'kind' | 'importer' | 'unreadable'> & { verified?: boolean }): string {
  if (isSelectable(finding) || !isFileKind(finding.kind)) return '';
  return unreadableText(finding.unreadable);
}

/**
 * What is ticked when the list first shows: every small or medium source that can be read as this user. A large source is never ticked
 * for you, and neither is one that needs administrator rights while Unravis is running as a standard user.
 */
export function defaultSelection(findings: readonly Finding[], elevated: boolean): Set<string> {
  const picked = new Set<string>();
  for (const f of findings) if (isSelectable(f) && effectiveCost(f) !== 'large' && (elevated || !f.needsAdmin)) picked.add(f.id);
  return picked;
}

/** "Select all" only ever adds sources that are not large. A large source is ticked one at a time. */
export function selectAllSmall(findings: readonly Finding[], current: ReadonlySet<string>): Set<string> {
  const next = new Set(current);
  for (const f of findings) if (isSelectable(f) && effectiveCost(f) !== 'large') next.add(f.id);
  return next;
}

export function toggled(current: ReadonlySet<string>, id: string, on: boolean): Set<string> {
  const next = new Set(current);
  if (on) next.add(id); else next.delete(id);
  return next;
}

/** The ticked findings, in list order. An id the list does not hold (or one that cannot be ticked) is dropped. */
export function pickedFindings(findings: readonly Finding[], picked: ReadonlySet<string>): Finding[] {
  return findings.filter((f) => picked.has(f.id) && isSelectable(f));
}

/* ------------------------------------------------------------------ the time window */

export const WINDOW_CHOICES = ['incident', '1h', '6h', '24h', '7d', 'custom'] as const;
export type WindowChoice = typeof WINDOW_CHOICES[number];
export interface TimeWindow { from: string; to: string }

const HOURS: Partial<Record<WindowChoice, number>> = { '1h': 1, '6h': 6, '24h': 24, '7d': 168 };

const isoUtc = (ms: number): string => new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z');

/** The window on screen when it is a real period, so "incident window" is offered only when there is one. */
export function incidentWindow(start: string, end: string): TimeWindow | null {
  const from = Date.parse(start), to = Date.parse(end);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) return null;
  return { from: isoUtc(from), to: isoUtc(to) };
}

/** The first choice: the incident window when there is one, else the last 24 hours. */
export const defaultWindowChoice = (incident: TimeWindow | null): WindowChoice => (incident ? 'incident' : '24h');

/** A custom time is two date and time values read as UTC: "2001-09-11T08:00". Null when it is not a real time. */
export function parseCustomTime(value: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})$/.exec(value.trim());
  if (!match) return null;
  const [y, mo, d, h, mi] = match.slice(1).map(Number);
  const ms = Date.UTC(y, mo - 1, d, h, mi);
  const check = new Date(ms);
  // Date.UTC rolls 31 February to March; a rolled date is refused.
  return check.getUTCFullYear() === y && check.getUTCMonth() === mo - 1 && check.getUTCDate() === d && check.getUTCHours() === h ? ms : null;
}

/** Why one custom time cannot be read. `empty` is a field left blank while the other has text. */
export type TimeFieldProblem = 'empty' | 'zone' | 'seconds' | 'date' | 'format';

/** The most specific reason a custom time is refused, or null when it reads as a real UTC time. */
export function customTimeProblem(value: string): TimeFieldProblem | null {
  const text = value.trim();
  if (text === '') return 'empty';
  if (parseCustomTime(text) !== null) return null;
  const head = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}/.exec(text);
  if (!head) return 'format';
  const tail = text.slice(head[0].length).trim();
  // The form is right, so a date or time that does not exist is the only thing left to refuse.
  if (tail === '') return 'date';
  if (/(?:z|[+-]\d{2}(?::?\d{2})?)$/i.test(tail)) return 'zone';
  if (/^:\d{2}(?:\.\d+)?$/.test(tail)) return 'seconds';
  return 'format';
}

export function windowFor(choice: WindowChoice, nowMs: number, incident: TimeWindow | null, custom: { from: string; to: string }): TimeWindow | null {
  if (choice === 'incident') return incident;
  if (choice === 'custom') {
    const from = parseCustomTime(custom.from), to = parseCustomTime(custom.to);
    return from === null || to === null ? null : { from: isoUtc(from), to: isoUtc(to) };
  }
  const hours = HOURS[choice];
  return hours ? { from: isoUtc(nowMs - hours * 3600 * 1000), to: isoUtc(nowMs) } : null;
}

export type WindowProblem = 'missing' | 'order' | 'too-long';

export function windowProblem(window: TimeWindow | null): WindowProblem | null {
  if (!window) return 'missing';
  const from = Date.parse(window.from), to = Date.parse(window.to);
  if (!Number.isFinite(from) || !Number.isFinite(to)) return 'missing';
  if (to <= from) return 'order';
  return to - from > MAX_WINDOW_MS ? 'too-long' : null;
}

/**
 * About how many bytes reading the ticked sources over the window means: each source's size times the share of its own time span the
 * window covers. A source with no usable span counts whole. This is the client's estimate; the server cuts the real slices.
 */
export function estimateBytes(findings: readonly Pick<Finding, 'sizeBytes' | 'oldest' | 'newest'>[], window: TimeWindow): number {
  const from = Date.parse(window.from), to = Date.parse(window.to);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) return 0;
  let total = 0;
  for (const f of findings) {
    const oldest = Date.parse(f.oldest), newest = Date.parse(f.newest);
    if (!Number.isFinite(oldest) || !Number.isFinite(newest) || newest <= oldest) { total += f.sizeBytes; continue; }
    const overlap = Math.max(0, Math.min(to, newest) - Math.max(from, oldest));
    total += f.sizeBytes * (overlap / (newest - oldest));
  }
  return Math.round(total);
}

/** "updated 3 min ago" as a number of minutes, or null when the newest entry has no time. Never negative. */
export function ageMinutes(newest: string, nowMs: number): number | null {
  const at = Date.parse(newest);
  return Number.isFinite(at) ? Math.max(0, Math.floor((nowMs - at) / 60000)) : null;
}

/* ------------------------------------------------------------------ import results */

export interface DiscoverImportRequest {
  scanId: string;
  findingIds: string[];
  window: TimeWindow;
  ackGating: true;
  ackText: string;
}

export interface JobRef { findingId: string; jobId: string }

/** What happened to one ticked source. */
export type RowOutcome =
  | { kind: 'added'; rows: number }
  | { kind: 'duplicate' }
  | { kind: 'refused'; reason: string }
  | { kind: 'needs-admin' }
  | { kind: 'failed'; reason: string }
  | { kind: 'cancelled' };

/** A refusal from one import job, turned into a row outcome. The reasons the server names for rights and repeats get their own lines. */
export function outcomeFromError(error: unknown): RowOutcome {
  if (!(error instanceof CaseApiError)) return { kind: 'failed', reason: 'unreachable' };
  if (error.reason === 'duplicate') return { kind: 'duplicate' };
  if (error.reason === 'needs-elevation' || error.reason === 'access-denied') return { kind: 'needs-admin' };
  if (error.reason === 'cancelled' || error.reason === 'canceled') return { kind: 'cancelled' };
  // 4xx from the importer is a refusal of the data; anything else is the server or the network failing.
  return error.status >= 400 && error.status < 500 && error.reason !== 'unreachable' ? { kind: 'refused', reason: error.reason } : { kind: 'failed', reason: error.reason || 'unreachable' };
}

export function outcomeFromJob(job: ImportJob): RowOutcome | null {
  if (job.state === 'done' && job.answer) {
    const record = job.answer.record;
    if (record && record.status && record.status !== 'ok') return { kind: 'refused', reason: record.status };
    return { kind: 'added', rows: record?.rows ?? 0 };
  }
  if (job.state === 'failed') return outcomeFromError(job.failure);
  return null;
}

/* ------------------------------------------------------------------ the sampler */

export type SamplerState = 'idle' | 'recording' | 'stopped';
export interface SamplerStatus { state: SamplerState; since: string; samples: number; stopReason: string }

export function parseSampler(body: unknown): SamplerStatus | null {
  const raw = asObject(body);
  if (!raw || typeof raw.state !== 'string') return null;
  const state: SamplerState = raw.state === 'recording' || raw.state === 'stopped' ? raw.state : 'idle';
  return { state, since: asTime(raw.since), samples: Math.max(0, Math.floor(asNumber(raw.samples))), stopReason: asString(raw.stopReason, 40) };
}

/* ------------------------------------------------------------------ the client */

/** Every call the panel makes. The real one talks to the server; a test gives the panel a fake with the same shape. */
export interface DiscoverClient {
  capabilities(signal?: AbortSignal): Promise<Capabilities>;
  startScan(signal?: AbortSignal): Promise<string>;
  getScan(scanId: string, signal?: AbortSignal): Promise<Scan>;
  cancelScan(scanId: string, signal?: AbortSignal): Promise<void>;
  startImports(caseId: string, request: DiscoverImportRequest, signal?: AbortSignal): Promise<JobRef[]>;
  getJob(caseId: string, jobId: string, signal?: AbortSignal): Promise<ImportJob>;
  cancelJob(caseId: string, jobId: string, signal?: AbortSignal): Promise<void>;
  startSampler(caseId: string, request: { intervalSec: number; ackText: string }, signal?: AbortSignal): Promise<SamplerStatus>;
  getSampler(caseId: string, signal?: AbortSignal): Promise<SamplerStatus>;
  stopSampler(caseId: string, signal?: AbortSignal): Promise<void>;
  /** Saves a proposed host label in the host list. The server refuses an import or the sampler until the label is listed. A test fake may leave it out. */
  confirmHost?(host: ScanHost, signal?: AbortSignal): Promise<void>;
}

const unreadable = (status = 200) => new CaseApiError('The server sent an answer that could not be read.', status, 'unreadable');

function scanPath(scanId: string): string {
  if (!isScanId(scanId)) throw new CaseApiError('That is not a scan id.', 400, 'bad-scan-id');
  return '/discover/scans/' + scanId;
}

/** The client for the real server. It uses the session token and the global fetch, so a test can replace fetch. */
export const discoverClient: DiscoverClient = {
  async confirmHost(host, signal) {
    const entries = await getHostEntries(signal);
    if (entries.some((e) => e.label === host.label)) return;
    await putHostEntries([...entries, { label: host.label, aliases: host.aliases, role: '', tz: host.tz, logicalCores: host.cores > 0 ? host.cores : null, runtime: '' }], signal);
  },
  async capabilities(signal) {
    const response = await send('/discover/capabilities', {}, signal);
    const caps = parseCapabilities(await json<unknown>(response));
    if (!caps) throw unreadable(response.status);
    return caps;
  },
  async startScan(signal) {
    const response = await send('/discover/scans', { method: 'POST', body: '{}' }, signal);
    const raw = asObject(await json<unknown>(response));
    const id = typeof raw?.scanId === 'string' ? raw.scanId : '';
    if (!isScanId(id)) throw unreadable(response.status);
    return id;
  },
  async getScan(scanId, signal) {
    const response = await send(scanPath(scanId), {}, signal);
    const scan = parseScan(await json<unknown>(response));
    if (!scan) throw unreadable(response.status);
    return scan;
  },
  async cancelScan(scanId, signal) {
    await send(scanPath(scanId), { method: 'DELETE' }, signal);
  },
  async startImports(caseId, request, signal) {
    if (!isScanId(request.scanId)) throw new CaseApiError('That is not a scan id.', 400, 'bad-scan-id');
    if (request.findingIds.length === 0 || request.findingIds.length > MAX_FINDINGS_PER_CALL || !request.findingIds.every(isFindingId)) throw new CaseApiError('Choose between 1 and ' + MAX_FINDINGS_PER_CALL + ' sources.', 400, 'bad-findings');
    // The body holds ids, a window and the notice text. It never holds a path.
    const body = JSON.stringify({ scanId: request.scanId, findingIds: request.findingIds, window: { from: request.window.from, to: request.window.to }, ackGating: true, ackText: request.ackText });
    const response = await send(casePath(caseId, '/discover/imports'), { method: 'POST', body }, signal);
    const raw = asObject(await json<unknown>(response));
    const jobs: JobRef[] = [];
    for (const item of asArray(raw?.jobs)) {
      const entry = asObject(item);
      if (entry && typeof entry.findingId === 'string' && typeof entry.jobId === 'string' && isFindingId(entry.findingId) && isJobId(entry.jobId)) jobs.push({ findingId: entry.findingId, jobId: entry.jobId });
    }
    if (jobs.length === 0) throw unreadable(response.status);
    return jobs;
  },
  getJob: (caseId, jobId, signal) => fetchImportJob(caseId, jobId, signal),
  cancelJob: (caseId, jobId, signal) => cancelImportJob(caseId, jobId, signal),
  async startSampler(caseId, request, signal) {
    const body = JSON.stringify({ intervalSec: request.intervalSec, ackGating: true, ackText: request.ackText });
    const response = await send(casePath(caseId, '/sampler'), { method: 'POST', body }, signal);
    return parseSampler(await json<unknown>(response)) ?? { state: 'recording', since: '', samples: 0, stopReason: '' };
  },
  async getSampler(caseId, signal) {
    const response = await send(casePath(caseId, '/sampler'), {}, signal);
    const status = parseSampler(await json<unknown>(response));
    if (!status) throw unreadable(response.status);
    return status;
  },
  async stopSampler(caseId, signal) {
    await send(casePath(caseId, '/sampler'), { method: 'DELETE' }, signal);
  }
};

/* ------------------------------------------------------------------ locate a folder, and check a network address */

/** Why a typed folder did not verify (the server answered 200 with verified false). */
export const LOCATE_FAILURES = ['not-found', 'cannot-open', 'empty-file', 'wrong-format', 'no-log-files', 'timed-out'] as const;
/** Why a typed path was refused before anything was read (the server answered 400). */
export const LOCATE_REFUSALS = ['path-not-absolute', 'path-not-local', 'path-unsafe'] as const;
export type LocateReason = typeof LOCATE_FAILURES[number] | typeof LOCATE_REFUSALS[number] | 'unknown';

/** The longest path the server takes. A longer one is refused here without a call. */
export const MAX_LOCATE_PATH = 1024;

/** The answer to "is this folder the one?". A refusal is a result, not an exception; a lost connection or an old scan still throws. */
export type LocateResult =
  | { ok: true; files: number; finding: Finding }
  | { ok: false; files: number; reason: LocateReason };

const isLocateReason = (value: unknown): value is LocateReason =>
  typeof value === 'string' && ((LOCATE_FAILURES as readonly string[]).includes(value) || (LOCATE_REFUSALS as readonly string[]).includes(value));

/**
 * POST /api/discover/scans/{scan}/findings/{finding}/locate {path}. The one place a path leaves the browser, and it goes in the body only:
 * the URL holds the two opaque ids. On success `finding` is the finding as the scan now holds it (verified, with an importer), to
 * replace the one in the list. Repeating the call gives the same answer.
 */
export async function locateFinding(scanId: string, findingId: string, path: string, signal?: AbortSignal): Promise<LocateResult> {
  if (!isFindingId(findingId)) throw new CaseApiError('That is not a source id.', 400, 'bad-finding-id');
  const url = scanPath(scanId) + '/findings/' + findingId + '/locate';
  const typed = path.trim();
  if (typed === '') return { ok: false, files: 0, reason: 'path-not-absolute' };
  if (typed.length > MAX_LOCATE_PATH) return { ok: false, files: 0, reason: 'path-unsafe' };
  let response: Response;
  try {
    response = await send(url, { method: 'POST', body: JSON.stringify({ path: typed }) }, signal);
  } catch (error) {
    if (error instanceof CaseApiError && error.status === 400 && isLocateReason(error.reason)) return { ok: false, files: 0, reason: error.reason };
    throw error;
  }
  const raw = asObject(await json<unknown>(response));
  if (!raw || typeof raw.verified !== 'boolean') throw unreadable(response.status);
  const files = Math.max(0, Math.floor(asNumber(raw.files)));
  if (!raw.verified) return { ok: false, files, reason: isLocateReason(raw.reason) ? raw.reason : 'unknown' };
  const finding = parseFinding(raw.finding);
  if (!finding || finding.id !== findingId || !finding.verified) throw unreadable(response.status);
  return { ok: true, files, finding };
}

/** Why a network address could not be reached ('' when it could). */
export const PROBE_REASONS = ['refused', 'timed-out', 'no-such-host', 'blocked'] as const;
export type ProbeReason = typeof PROBE_REASONS[number] | '' | 'unknown';
export interface ProbeResult { reachable: boolean; reason: ProbeReason }

const HOST_NAME_MAX = 253;

/**
 * POST /api/probe/reachable {host, port}. Asks the server whether it can open a connection to that address. It sends nothing and reads
 * nothing; the answer is "reachable" or one reason. A bad host or port throws before any call; a 400, 429 or lost connection throws a CaseApiError.
 */
export async function probeReachable(host: string, port: number, signal?: AbortSignal): Promise<ProbeResult> {
  const name = host.trim();
  if (name === '' || name.length > HOST_NAME_MAX || /\s/.test(name)) throw new CaseApiError('Enter a host name or an address.', 400, 'bad-host');
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new CaseApiError('The port must be a number from 1 to 65535.', 400, 'bad-port');
  const response = await send('/probe/reachable', { method: 'POST', body: JSON.stringify({ host: name, port }) }, signal);
  const raw = asObject(await json<unknown>(response));
  if (!raw || typeof raw.reachable !== 'boolean') throw unreadable(response.status);
  if (raw.reachable) return { reachable: true, reason: '' };
  const reason = PROBE_REASONS.find((r) => r === raw.reason);
  return { reachable: false, reason: reason ?? 'unknown' };
}

/* ------------------------------------------------------------------ errors and capability state */

/** What a refusal means for this panel. `unavailable` is a route the server does not have (a build without scanning). */
export type DiscoverProblem = 'unavailable' | 'busy' | 'import-busy' | 'sampler-busy' | 'expired' | 'unreachable' | 'session' | 'other';

export function classifyError(error: unknown): DiscoverProblem {
  if (!(error instanceof CaseApiError)) return 'unreachable';
  if (error.reason === 'no_session' || error.status === 401) return 'session';
  if (error.reason === 'unreachable') return 'unreachable';
  if (error.reason === 'scan-busy') return 'busy';
  if (error.reason === 'import-busy' || error.status === 429) return 'import-busy';
  if (error.reason === 'sampler-busy') return 'sampler-busy';
  if (error.reason === 'scan-unknown' || error.reason === 'scan-expired' || error.reason === 'scan-not-found') return 'expired';
  // A 404 with no named reason is the route itself missing. A named reason (case-not-found, ...) is a real answer.
  if (error.status === 404 && error.reason === '') return 'unavailable';
  if (error.status === 404 && (error.reason === 'not-found' || error.reason === 'no-route')) return 'unavailable';
  return 'other';
}

/**
 * A scan the person cancelled is deleted on the server, so the next check of it says "not found". That is the cancel taking effect, not an old scan.
 * Only a cancel the person asked for counts; any other refusal keeps its own message.
 */
export function cancelledByUser(error: unknown, cancelWanted: boolean): boolean {
  return cancelWanted && error instanceof CaseApiError && (error.status === 404 || classifyError(error) === 'expired');
}

/** The recording status that may be shown: none once the case is closed, erased or not chosen, so the panel falls back to idle. */
export const activeSampler = (sampler: SamplerStatus | null, caseReady: boolean): SamplerStatus | null => (caseReady ? sampler : null);

export type EntryState = { kind: 'loading' } | { kind: 'ready'; elevated: boolean } | { kind: 'unsupported' } | { kind: 'unavailable' } | { kind: 'hidden' };

/** What the entry button area shows. A server without the routes says "not in this build"; a failed call hides the area quietly. */
export async function loadEntryState(client: DiscoverClient = discoverClient, signal?: AbortSignal): Promise<EntryState> {
  try {
    const caps = await client.capabilities(signal);
    return caps.supported ? { kind: 'ready', elevated: caps.elevated } : { kind: 'unsupported' };
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    return classifyError(error) === 'unavailable' ? { kind: 'unavailable' } : { kind: 'hidden' };
  }
}

let entryCache: { client: DiscoverClient; promise: Promise<EntryState> } | null = null;

/**
 * The entry state, asked once and shared by every button and the panel. A quiet failure (hidden) is not kept, so the next ask tries again.
 * `force` drops what is kept. Not aborted by a caller: the answer is small and is shared.
 */
export function cachedEntryState(client: DiscoverClient = discoverClient, force = false): Promise<EntryState> {
  if (!force && entryCache && entryCache.client === client) return entryCache.promise;
  const promise = loadEntryState(client);
  const mine = { client, promise };
  entryCache = mine;
  void promise.then((state) => { if (state.kind === 'hidden' && entryCache === mine) entryCache = null; }, () => { if (entryCache === mine) entryCache = null; });
  return promise;
}

export function resetEntryCache(): void { entryCache = null; }

/* ------------------------------------------------------------------ waiting on a scan */

export const SCAN_POLL_MS = 500;
export const SCAN_MAX_UNREACHABLE = 5;

const sleepFor = (ms: number, signal?: AbortSignal): Promise<void> => new Promise((resolve, reject) => {
  if (signal?.aborted) { reject(new DOMException('Aborted', 'AbortError')); return; }
  const timer = setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve(); }, ms);
  const onAbort = () => { clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')); };
  signal?.addEventListener('abort', onAbort, { once: true });
});

export interface WaitScanOptions {
  signal?: AbortSignal;
  onUpdate?: (scan: Scan) => void;
  intervalMs?: number;
  /** For tests. */
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
}

/** Asks about the scan until it stops running. A few lost answers in a row are tolerated; more than that and the wait gives up. */
export async function waitForScan(client: DiscoverClient, scanId: string, options: WaitScanOptions = {}): Promise<Scan> {
  const sleep = options.sleep ?? sleepFor;
  let lost = 0;
  for (;;) {
    try {
      const scan = await client.getScan(scanId, options.signal);
      lost = 0;
      options.onUpdate?.(scan);
      if (scan.state !== 'running') return scan;
    } catch (error) {
      if (error instanceof CaseApiError && error.reason === 'unreachable' && ++lost < SCAN_MAX_UNREACHABLE) { await sleep(options.intervalMs ?? SCAN_POLL_MS, options.signal); continue; }
      throw error;
    }
    await sleep(options.intervalMs ?? SCAN_POLL_MS, options.signal);
  }
}

/* ------------------------------------------------------------------ waiting on the import jobs */

export interface JobWatch {
  findingId: string;
  jobId: string;
  job: ImportJob | null;
  outcome: RowOutcome | null;
}

export interface WatchOptions {
  signal?: AbortSignal;
  onUpdate?: (watch: JobWatch) => void;
  intervalMs?: number;
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
}

/**
 * Follows one import job until it is done or failed, reporting each answer. It resolves with the row's outcome and never throws for a
 * refusal: a refusal is a result for that row. Aborting the signal stops the asking and leaves the job on the server.
 */
export async function watchJob(client: DiscoverClient, caseId: string, ref: JobRef, options: WatchOptions = {}): Promise<RowOutcome> {
  const sleep = options.sleep ?? sleepFor;
  const watch: JobWatch = { ...ref, job: null, outcome: null };
  let lost = 0;
  for (;;) {
    try {
      const job = await client.getJob(caseId, ref.jobId, options.signal);
      lost = 0;
      watch.job = job;
      watch.outcome = outcomeFromJob(job);
      options.onUpdate?.({ ...watch });
      if (watch.outcome) return watch.outcome;
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw error;
      if (error instanceof CaseApiError && error.reason === 'unreachable' && ++lost < 5) { await sleep(options.intervalMs ?? 1000, options.signal); continue; }
      watch.outcome = outcomeFromError(error);
      options.onUpdate?.({ ...watch });
      return watch.outcome;
    }
    await sleep(options.intervalMs ?? 1000, options.signal);
  }
}
