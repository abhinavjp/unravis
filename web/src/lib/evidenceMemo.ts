import { formatTime } from './time';

/**
 * Evidence memo: RAM only, for this tab. It remembers the successful answers of the three evidence reads (breakdown, slowest and
 * activity) for as long as the analysis on screen stays the same. appState bumps the epoch when that analysis is replaced, when a
 * source is removed and when the session is forgotten, and a bump empties the memo. There is no timer: "Run analysis" is the refresh.
 * No browser storage of any kind is used, and record, payload and export reads never come here.
 */
export const EVIDENCE_ENDPOINTS = ['breakdown', 'slowest', 'activity'] as const;
export type EvidenceEndpoint = typeof EVIDENCE_ENDPOINTS[number];
export const MEMO_MAX_ENTRIES = 64;
export const MEMO_MAX_BYTES = 4 * 1024 * 1024;
/**
 * Completeness labels that count as a whole answer. A live source can never report plain "complete", because the server does not
 * measure documents examined there (it reports "ran_to_end", a read that ran to its end). Leaving that label out would switch the
 * memo off for every live source. "partial" and "hint" are never stored.
 */
export const STORABLE_COMPLETENESS: readonly string[] = ['complete', 'ran_to_end'];

export interface MemoResult<T> { value: T; fromMemo: boolean; fetchedAt: number }
interface Entry { value: unknown; bytes: number; fetchedAt: number }
interface Shared { promise: Promise<MemoResult<unknown>>; controller: AbortController; waiters: number; epoch: number; done: boolean }

let epoch = 0;
let totalBytes = 0;
const entries = new Map<string, Entry>(); // insertion order is recency order: the first key is the least recently used
const pending = new Map<string, Shared>();

const abortError = () => new DOMException('The request was cancelled.', 'AbortError');
const keyOf = (endpoint: EvidenceEndpoint, body: unknown) => epoch + '|' + endpoint + '|' + JSON.stringify(body);

export function currentEpoch() { return epoch; }
/** The analysis on screen changed (or the session ended): forget everything. Calls still in flight finish for their callers but are not stored. */
export function bumpEpoch() { epoch++; entries.clear(); pending.clear(); totalBytes = 0; return epoch; }
export function memoSize() { return { entries: entries.size, bytes: totalBytes }; }

/** Only a whole answer from every source is kept. A failed source or a partial read is never stored, so a retry always goes to the server. */
export function isStorable(value: unknown): boolean {
  if (value === null || typeof value !== 'object') return false;
  const body = value as { sources?: { ok?: boolean }[]; provenance?: { completeness?: string }[] };
  if (Array.isArray(body.sources) && body.sources.some(source => source?.ok === false)) return false;
  if (Array.isArray(body.provenance) && body.provenance.some(item => !STORABLE_COMPLETENESS.includes(String(item?.completeness)))) return false;
  return true;
}

function store(key: string, value: unknown, fetchedAt: number) {
  let bytes: number;
  try { bytes = JSON.stringify(value).length; } catch { return; }
  if (bytes > MEMO_MAX_BYTES) return;
  const old = entries.get(key);
  if (old) { totalBytes -= old.bytes; entries.delete(key); }
  entries.set(key, { value, bytes, fetchedAt });
  totalBytes += bytes;
  while (entries.size > MEMO_MAX_ENTRIES || totalBytes > MEMO_MAX_BYTES) {
    const oldest = entries.keys().next();
    if (oldest.done) break;
    totalBytes -= entries.get(oldest.value)!.bytes;
    entries.delete(oldest.value);
  }
}

const isNoSession = (cause: unknown) => typeof cause === 'object' && cause !== null && ((cause as { status?: number }).status === 401 || (cause as { code?: string }).code === 'no_session');

function start(key: string, load: (signal: AbortSignal) => Promise<unknown>, now: () => number): Shared {
  const controller = new AbortController();
  const shared = { controller, waiters: 0, epoch, done: false } as Shared;
  shared.promise = new Promise<unknown>(resolve => resolve(load(controller.signal))).then(value => {
    const fetchedAt = now();
    // An abandoned call, or one started before the epoch moved on, is handed to its own callers only.
    if (pending.get(key) === shared && shared.epoch === epoch && isStorable(value)) store(key, value, fetchedAt);
    return { value, fromMemo: false, fetchedAt };
  }).finally(() => { shared.done = true; if (pending.get(key) === shared) pending.delete(key); });
  // A 401 means the session is gone: nothing already shown may be served again.
  shared.promise.catch(cause => { if (isNoSession(cause)) bumpEpoch(); });
  pending.set(key, shared);
  return shared;
}

function leave(key: string, shared: Shared) {
  shared.waiters--;
  if (shared.waiters > 0 || shared.done) return;
  if (pending.get(key) === shared) pending.delete(key); // removed before the caller sees the rejection
  shared.controller.abort();
}

/**
 * Read one evidence endpoint through the memo. `body` is the request as the caller sends it; it is the key. A repeat returns the
 * stored answer without calling `load`; callers that arrive while the same call is running share it. The shared call is cancelled
 * only when every caller has cancelled. Errors and cancelled calls are never stored.
 */
export function evidenceFetch<T>(endpoint: EvidenceEndpoint, body: unknown, load: (signal: AbortSignal) => Promise<T>, signal?: AbortSignal, now: () => number = Date.now): Promise<MemoResult<T>> {
  if (!(EVIDENCE_ENDPOINTS as readonly string[]).includes(endpoint)) return Promise.reject(new TypeError('The evidence memo does not hold ' + endpoint + '.'));
  if (signal?.aborted) return Promise.reject(abortError());
  const key = keyOf(endpoint, body);
  const hit = entries.get(key);
  if (hit) {
    entries.delete(key); entries.set(key, hit); // most recently used
    return Promise.resolve({ value: hit.value as T, fromMemo: true, fetchedAt: hit.fetchedAt });
  }
  const shared = pending.get(key) ?? start(key, load, now);
  shared.waiters++;
  return new Promise<MemoResult<T>>((resolve, reject) => {
    let settled = false;
    const onAbort = () => { if (settled) return; settled = true; leave(key, shared); reject(abortError()); };
    signal?.addEventListener('abort', onAbort, { once: true });
    shared.promise.then(
      result => { if (settled) return; settled = true; signal?.removeEventListener('abort', onAbort); shared.waiters--; resolve(result as MemoResult<T>); },
      cause => { if (settled) return; settled = true; signal?.removeEventListener('abort', onAbort); shared.waiters--; reject(cause); }
    );
  });
}

/** The footer for an answer served from memory, for example "Shown from memory, as of 14:03:22 UTC. Run analysis to refresh." */
export function asOfText(fetchedAt: number, zone = 'UTC'): string {
  return 'Shown from memory, as of ' + formatTime(fetchedAt, { zone, date: false }) + '. Run analysis to refresh.';
}
