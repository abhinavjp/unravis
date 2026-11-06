/**
 * The tracing client (WP-16a): the shape of `POST /api/cases/{id}/tracing`, shape-checked parsing with the
 * server's own defaults, a per-tab monotonic request sequence so a late answer is never shown over a newer
 * one, and one error-to-sentence map. Pure TypeScript; nothing here is mounted on a screen yet (WP-16b).
 *
 * Field names are copied letter for letter from the WP-14 response contract (server: internal/server/tracing.go).
 * If that contract and this file ever disagree, this file is wrong, not the server.
 */
import { send, casePath, CaseApiError, isCaseLocked, isTimeLimit } from '../cases';
import { isTimeoutError, READ_TIMEOUT_MS, TIMEOUT_CODE, timeoutMessage } from '../requestTimeout';
import { TRACING_TEXT } from '../copy/tracingCopy';

// ---------------------------------------------------------------------------------------------------------------------------------
// Types. Lane order (audit, sql, iis) is the order everywhere: request defaults, response lanes, the chart.

export type TracingLaneKey = 'audit' | 'sql' | 'iis';
export type TracingLaneState = 'events' | 'no-data' | 'clock-unchecked' | 'out-of-range' | 'none-seen' | 'incomplete';
export type TracingReason = 'timeout' | 'rowcap' | 'error' | 'broad-scan';
export type TracingConsistency = 'live-non-snapshot' | 'stored-as-of' | 'snapshot';
export type TracingOrigin = 'live' | 'case';

/** RFC3339 UTC, half-open [start, end). Used for the request windows and the window the server echoes back. */
export interface TracingRange { start: string; end: string }

/** The response `window`: start/end echo the incident; axisStart/axisEnd are the server's floor-aligned axis. */
export interface TracingWindow {
  start: string;
  end: string;
  axisStart: string;
  axisEnd: string;
  bucketSeconds: number;
}

/** Sent per lane and at the top level, exactly as the server sends it. */
export interface TracingSourceRef { sourceId: string; label: string }

/**
 * The server bucket is `{start,count,errors?}`; SQL and IIS have no 5xx, so `errors` is absent there and
 * `parseTracing` fills it with 0. The server also sends `end` and `clipped`; this client ignores them. `complete`
 * is kept: a cell the server did not read to the end (a chunked audit read that stopped early) is `complete: false`
 * and its count of 0 means "not read", never "no traffic".
 */
export interface TracingBucket {
  start: string;
  count: number;
  errors: number;
  /** False only when the server said so: the cell was not read to the end. A missing field is read as complete. */
  complete?: boolean;
  p50Ms?: number;
  p95Ms?: number;
  /** How many requests in the bucket carried a timing; absent when none did. */
  timedCount?: number;
  /** The slowest timed request in the bucket; absent when none did. */
  maxMs?: number;
}

export interface TracingLane {
  key: TracingLaneKey;
  label: string;
  state: TracingLaneState;
  /** The server's own wording for a missing or partial state; may be empty. */
  reason: string;
  /** Read from the lane field `reasonCode` when present. */
  incompleteReason?: TracingReason;
  origin: TracingOrigin;
  asOf: string;
  consistency: TracingConsistency;
  complete: boolean;
  truncated: boolean;
  /** The lane field `disabled` ("tz-rule" or absent); an empty string becomes absent. */
  disabled?: string;
  scanned: number;
  hidden: number;
  sources: TracingSourceRef[];
  buckets: TracingBucket[];
  /** A window over 24 hours is read one day at a time: `chunksRead` of `chunksTotal` days finished (absent = a single read). */
  chunksTotal?: number;
  chunksRead?: number;
  /** RFC3339: the end of the last finished day, set when the read stopped early. Cells after it were not read. */
  readThrough?: string;
  /** The audit sources this lane read, by name (the audit lane only). */
  sourcesRead?: TracingSourceRef[];
  /** Audit databases the case saved that no open connection carries, by saved name (the audit lane only). */
  savedNotConnected?: string[];
}

export interface TracingResponse {
  window: TracingWindow;
  lanes: TracingLane[];
  sources: TracingSourceRef[];
  allLanesStored: boolean;
  mixedConsistency: boolean;
  /** Server-derived (true when any lane is truncated); `parseTracing` only falls back when the field is missing. */
  truncated: boolean;
  warnings: string[];
  /** The live audit sources open in the process (the whole listing); empty when the request did not list them. */
  auditSources: TracingSourceRef[];
  /** Audit databases the case saved that no open connection carries; empty when the request did not list them. */
  savedNotConnected: string[];
  /** Echoed only when it was sent, so the caller compares it with the seq it sent, not with a field it may lack. */
  requestSeq?: number;
}

/** An omitted or empty `sourceIds` means every source of the case (the WP-14 contract). The sent body adds `requestSeq`. */
export interface TracingRequest {
  sourceIds?: string[];
  incident: TracingRange;
  reference: TracingRange;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The per-tab request sequence. A caller sends `nextRequestSeq()` with the request and ignores any answer
// whose echoed `requestSeq` is stale against the latest one it sent.

let seqCounter = 0;

export function nextRequestSeq(): number {
  seqCounter += 1;
  return seqCounter;
}

export function isStale(responseSeq: number, latestSeq: number): boolean {
  return responseSeq < latestSeq;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The request body. Only the fields the server's strict decoder accepts; an unknown field is a 400.

export function buildTracingBody(req: TracingRequest, seq: number): string {
  const body: Record<string, unknown> = { incident: req.incident, reference: req.reference, requestSeq: seq };
  if (req.sourceIds && req.sourceIds.length > 0) body.sourceIds = req.sourceIds;
  return JSON.stringify(body);
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Parsing. `parseTracing` is strict about the shapes that would make the rest of the client misread the
// answer (lane keys, bucket arrays, the axis), and fills in the server's own defaults for everything else.

const isObject = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const isText = (value: unknown): value is string => typeof value === 'string';
const isNumber = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const isParseableDate = (value: string): boolean => !Number.isNaN(Date.parse(value));

const LANE_KEYS: readonly string[] = ['audit', 'sql', 'iis'];
const isLaneKey = (value: unknown): value is TracingLaneKey => isText(value) && LANE_KEYS.includes(value);

const LANE_STATES: readonly string[] = ['events', 'no-data', 'clock-unchecked', 'out-of-range', 'none-seen', 'incomplete'];
const asLaneState = (value: unknown): TracingLaneState => (isText(value) && LANE_STATES.includes(value) ? (value as TracingLaneState) : 'incomplete');

const REASON_CODES: readonly string[] = ['timeout', 'rowcap', 'error', 'broad-scan'];
const asReasonCode = (value: unknown): TracingReason | undefined => (isText(value) && REASON_CODES.includes(value) ? (value as TracingReason) : undefined);

const CONSISTENCIES: readonly string[] = ['live-non-snapshot', 'stored-as-of', 'snapshot'];
const isConsistency = (value: unknown): value is TracingConsistency => isText(value) && CONSISTENCIES.includes(value);

const unreadable = (): CaseApiError => new CaseApiError('The server sent an answer that could not be read.', 200, 'unreadable');

function parseSourceRef(value: unknown): TracingSourceRef | null {
  if (!isObject(value) || !isText(value.sourceId) || !isText(value.label)) return null;
  return { sourceId: value.sourceId, label: value.label };
}

/** Defaults to `[]`: an entry that is not the documented shape is dropped rather than failing the whole response. */
function parseSourceRefs(value: unknown): TracingSourceRef[] {
  if (!Array.isArray(value)) return [];
  const out: TracingSourceRef[] = [];
  for (const item of value) {
    const parsed = parseSourceRef(item);
    if (parsed) out.push(parsed);
  }
  return out;
}

/** Defaults to `[]`: only non-empty text entries are kept. */
function parseNames(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => isText(item) && item.trim() !== '') : [];
}

function parseBucket(value: unknown): TracingBucket | null {
  if (!isObject(value) || !isText(value.start) || !isNumber(value.count)) return null;
  const bucket: TracingBucket = { start: value.start, count: value.count, errors: isNumber(value.errors) ? value.errors : 0 };
  if (isNumber(value.p50Ms)) bucket.p50Ms = value.p50Ms;
  if (isNumber(value.p95Ms)) bucket.p95Ms = value.p95Ms;
  if (isNumber(value.timedCount)) bucket.timedCount = value.timedCount;
  if (isNumber(value.maxMs)) bucket.maxMs = value.maxMs;
  if (value.complete === false) bucket.complete = false;
  return bucket;
}

function parseBuckets(value: unknown): TracingBucket[] | null {
  if (!Array.isArray(value)) return null;
  const out: TracingBucket[] = [];
  for (const item of value) {
    const parsed = parseBucket(item);
    if (!parsed) return null;
    out.push(parsed);
  }
  return out;
}

function parseLane(value: unknown): TracingLane | null {
  if (!isObject(value) || !isLaneKey(value.key) || !isText(value.label)) return null;
  const buckets = parseBuckets(value.buckets);
  if (buckets === null) return null;
  const origin: TracingOrigin = value.origin === 'case' ? 'case' : 'live';
  const consistency = isConsistency(value.consistency) ? value.consistency : (origin === 'live' ? 'live-non-snapshot' : 'stored-as-of');
  const disabled = isText(value.disabled) && value.disabled !== '' ? value.disabled : undefined;
  const lane: TracingLane = {
    key: value.key,
    label: value.label,
    state: asLaneState(value.state),
    reason: isText(value.reason) ? value.reason : '',
    incompleteReason: asReasonCode(value.reasonCode),
    origin,
    asOf: isText(value.asOf) ? value.asOf : '',
    consistency,
    complete: value.complete === true,
    truncated: value.truncated === true,
    disabled,
    scanned: isNumber(value.scanned) ? value.scanned : 0,
    hidden: isNumber(value.hidden) ? value.hidden : 0,
    sources: parseSourceRefs(value.sources),
    buckets
  };
  // The optional fields stay off the lane when the server did not send them, so a lane read in one piece is unchanged.
  if (isNumber(value.chunksTotal) && value.chunksTotal > 0) lane.chunksTotal = Math.floor(value.chunksTotal);
  if (lane.chunksTotal !== undefined) lane.chunksRead = isNumber(value.chunksRead) && value.chunksRead > 0 ? Math.min(Math.floor(value.chunksRead), lane.chunksTotal) : 0;
  if (isText(value.readThrough) && isParseableDate(value.readThrough)) lane.readThrough = value.readThrough;
  const read = parseSourceRefs(value.sourcesRead);
  if (read.length > 0) lane.sourcesRead = read;
  const saved = parseNames(value.savedNotConnected);
  if (saved.length > 0) lane.savedNotConnected = saved;
  return lane;
}

function parseWindow(value: unknown): TracingWindow | null {
  if (!isObject(value) || !isText(value.start) || !isText(value.end) || !isText(value.axisStart) || !isText(value.axisEnd)) return null;
  if (!isParseableDate(value.axisStart) || !isParseableDate(value.axisEnd)) return null;
  if (!isNumber(value.bucketSeconds) || value.bucketSeconds <= 0) return null;
  return { start: value.start, end: value.end, axisStart: value.axisStart, axisEnd: value.axisEnd, bucketSeconds: value.bucketSeconds };
}

/**
 * Throws the one "unreadable" `CaseApiError` when the answer is not the documented WP-14 shape (lanes not an
 * array, a lane key outside the three, a lane's buckets not an array, or a window the axis cannot be read
 * from). Everything else falls back to the server's own default per the WP-14 contract.
 */
export function parseTracing(raw: unknown): TracingResponse {
  if (!isObject(raw)) throw unreadable();
  const window = parseWindow(raw.window);
  if (!window) throw unreadable();
  if (!Array.isArray(raw.lanes)) throw unreadable();
  const lanes: TracingLane[] = [];
  for (const item of raw.lanes) {
    const lane = parseLane(item);
    if (!lane) throw unreadable();
    lanes.push(lane);
  }
  const mixedConsistency = typeof raw.mixedConsistency === 'boolean'
    ? raw.mixedConsistency
    : lanes.some((lane) => lane.consistency === 'live-non-snapshot');
  const truncated = typeof raw.truncated === 'boolean' ? raw.truncated : lanes.some((lane) => lane.truncated);
  return {
    window,
    lanes,
    sources: parseSourceRefs(raw.sources),
    allLanesStored: raw.allLanesStored === true,
    mixedConsistency,
    truncated,
    warnings: Array.isArray(raw.warnings) ? raw.warnings.filter(isText) : [],
    auditSources: parseSourceRefs(raw.auditSources),
    savedNotConnected: parseNames(raw.savedNotConnected),
    requestSeq: isNumber(raw.requestSeq) ? raw.requestSeq : undefined
  };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The request.

export async function fetchTracing(caseId: string, req: TracingRequest, seq: number, signal?: AbortSignal): Promise<TracingResponse> {
  const response = await send(casePath(caseId, '/tracing'), { method: 'POST', body: buildTracingBody(req, seq) }, signal, { timeoutMs: READ_TIMEOUT_MS });
  let raw: unknown;
  try { raw = await response.json(); } catch (cause) {
    if (isTimeLimit(cause)) throw new CaseApiError(timeoutMessage(), 0, TIMEOUT_CODE);
    throw unreadable();
  }
  return parseTracing(raw);
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Errors. Never the server's own text, a URL or an id: one fixed sentence per refusal kind.

export function tracingErrorText(error: unknown): string {
  if (isTimeoutError(error)) return timeoutMessage();
  if (isCaseLocked(error)) return TRACING_TEXT.errLocked;
  if (error instanceof CaseApiError) {
    switch (error.status) {
      case 400: return TRACING_TEXT.errBadWindow;
      case 423: return TRACING_TEXT.errLocked;
      case 409: return TRACING_TEXT.errClosed;
      case 503: return TRACING_TEXT.errUnavailable;
      default: return TRACING_TEXT.errServer;
    }
  }
  return TRACING_TEXT.errServer;
}
