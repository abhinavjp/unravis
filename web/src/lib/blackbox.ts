import { NO_SESSION_MESSAGE, forgetSession, readSessionToken } from './api';

/**
 * Client for the Blackbox routes (internal/server/blackbox.go) and the pure rules the Blackbox panel follows.
 * Nothing here holds copy: the panel gets its words from copy/blackboxCopy.ts.
 *
 * The contract (JSON is camelCase; every request carries X-Audit-Token):
 *   GET    /api/blackbox                         -> { items, thisPc, canPack }
 *   POST   /api/blackbox          {name, location, key}   -> 201 item | 409 duplicate | 422 failure
 *   POST   /api/blackbox/test     {name, location, key}   -> { ok, name, state, facts }   (nothing is saved)
 *   POST   /api/blackbox/{id}/check                -> item
 *   PUT    /api/blackbox/{id}/key {key}            -> item
 *   DELETE /api/blackbox/{id}                      -> { removed, note }
 *   POST   /api/blackbox/validate | /pack  settings -> { ok } | a zip
 *   GET    /api/blackbox/this-pc                   -> thisPc
 *   POST   /api/blackbox/this-pc/{install,start,stop,apply,remove,snapshot}
 *   POST   /api/blackbox/{id}/host-reading {incident, reference} -> the Step 9 answer
 * A refusal is JSON { error, reason, what?, next?, state?, existing? }.
 * A read key goes up in one request and never comes back: no answer holds one.
 */

const base = '/api/blackbox';

export type PageState = 'running' | 'running-late' | 'stopped' | 'disk-low' | 'clock-differs' | 'not-found' | 'unreachable' | 'incomplete' | 'not-placed';
export type Tone = 'good' | 'warn' | 'bad' | 'idle';

/** The nine states of A36, in the order the page documents them. */
export const ALL_STATES: PageState[] = ['running', 'running-late', 'stopped', 'disk-low', 'clock-differs', 'not-found', 'unreachable', 'incomplete', 'not-placed'];

export interface StateInfo {
  state: PageState;
  label: string;
  tone: Tone;
  detail: string;
  /** The fixed A5 word for using this Blackbox as a source now. */
  readiness: string;
  reason?: string;
  skewSeconds?: number;
}

export interface Facts {
  name: string;
  host: string;
  version?: string;
  intervalSeconds: number;
  state: string;
  firstSample?: string;
  lastSample?: string;
  files: number;
  samples: number;
  gaps: number;
  /** Length in seconds of the longest gap. Absent when there is none (or from an older server). */
  longestGapSeconds?: number;
  /** Recording files that were not read to the end (dropped or missing). Absent when none. */
  unread?: number;
  freePercent?: number;
}

export type LocationKind = 'folder' | 'share' | 'url';

export interface BlackboxItem {
  id: string;
  name: string;
  kind: LocationKind;
  location: string;
  canon: string;
  thisPc: boolean;
  recorderHost?: string;
  recorderName?: string;
  /** True when a read key is sealed on this PC. The key itself never reaches the page. */
  hasKey: boolean;
  addedAt: string;
  state: StateInfo;
  facts?: Facts;
}

export interface ThisPc {
  available: boolean;
  scanned: boolean;
  installed: boolean;
  running: boolean;
  name?: string;
  folder?: string;
  port?: number;
  bind?: string;
  configUnreadable?: boolean;
  listed: boolean;
  listId?: string;
  problem?: string;
  /** What the installed service runs with (blackbox.json holds no secret). Apply settings starts from it. */
  settings?: Settings;
}

export interface BlackboxList { items: BlackboxItem[]; thisPc: ThisPc; canPack: boolean }

export interface Settings {
  name: string;
  folder: string;
  intervalSeconds: number;
  keepDays: number;
  maxGB: number;
  minFreePercent: number;
  port: number;
  bind: string;
  record: { processes: boolean; logsCatalogue: boolean; copyLogsOnSnapshot: boolean };
  logPaths: string[];
  probe: { enabled: boolean };
}

export interface Refusal {
  status: number;
  error: string;
  reason: string;
  what?: string;
  next?: string;
  state?: StateInfo;
  existing?: BlackboxItem;
}

/** A refused Blackbox call. `refusal` holds what the server said, with no key in it. */
export class BlackboxError extends Error {
  readonly refusal: Refusal;
  constructor(refusal: Refusal) {
    super(refusal.error);
    this.name = 'BlackboxError';
    this.refusal = refusal;
  }
}

/**
 * Words for any thrown value. The server's own `what` and `next` win; then the table by reason; then the server's
 * error text. Never a stack, never a key.
 */
export function refusalText(error: unknown, table: Record<string, string>): string {
  if (error instanceof BlackboxError) {
    const r = error.refusal;
    if (r.what) return r.next ? r.what + ' ' + r.next : r.what;
    if (table[r.reason]) return table[r.reason];
    return r.next ? r.error + ' ' + r.next : r.error;
  }
  return table.offline ?? 'The request failed.';
}

export interface Span { window: string; start: string; end: string; minutes: number; kind: string }
export interface HostStep {
  number: number;
  name: string;
  readiness: string;
  reasons: string[];
  untestedSpans?: Span[];
  nutshell: string;
  findings: string[];
}
export interface HostAnswer {
  step: HostStep;
  h1: 'yes' | 'no' | 'unknown';
  gaps: Span[];
  onset?: string;
  topProcess?: string;
  covered: number;
  note?: string;
  read: { files: number; samples: number; torn: number; dropped: string[]; missing: string[] };
  state: StateInfo;
}

export interface AddBody { name: string; location: string; key: string }
export interface InstallAnswer { installed: boolean; name: string; folder: string; port: number; entry: BlackboxItem }
export interface RemoveAnswer { serviceRemoved: boolean; recordingsDeleted: boolean; filesDeleted: number }
export interface SnapshotAnswer { dir: string; bytes: number; recordings: number; logs: number }
export interface TestAnswer { ok: boolean; name: string; state: StateInfo; facts: Facts | null }

/* ---------------------------------------------------------------- calls */

async function readRefusal(response: Response): Promise<Refusal> {
  let body: Partial<Refusal> = {};
  try { body = (await response.json()) as Partial<Refusal>; } catch { /* the status is enough */ }
  return {
    status: response.status,
    error: typeof body.error === 'string' && body.error ? body.error : 'The Blackbox request failed (' + response.status + ').',
    reason: typeof body.reason === 'string' && body.reason ? body.reason : 'other',
    what: body.what, next: body.next, state: body.state, existing: body.existing
  };
}

async function send(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
  const token = readSessionToken();
  if (!token) throw new BlackboxError({ status: 401, error: NO_SESSION_MESSAGE, reason: 'no_session' });
  const headers: Record<string, string> = { 'X-Audit-Token': token };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch(base + path, { method, credentials: 'same-origin', signal, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new BlackboxError({ status: 0, error: 'Unravis could not be reached. Check that it is still running.', reason: 'offline' });
  }
  if (response.status === 401) {
    forgetSession();
    throw new BlackboxError({ status: 401, error: NO_SESSION_MESSAGE, reason: 'no_session' });
  }
  if (!response.ok) throw new BlackboxError(await readRefusal(response));
  return response;
}

async function call<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await send(method, path, body, signal);
  return (await response.json()) as T;
}

export const blackboxApi = {
  list: (signal?: AbortSignal) => call<BlackboxList>('GET', '', undefined, signal),
  thisPc: (signal?: AbortSignal) => call<ThisPc>('GET', '/this-pc', undefined, signal),
  test: (body: AddBody) => call<TestAnswer>('POST', '/test', body),
  add: (body: AddBody) => call<BlackboxItem>('POST', '', body),
  check: (id: string) => call<BlackboxItem>('POST', '/' + encodeURIComponent(id) + '/check', {}),
  setKey: (id: string, key: string) => call<BlackboxItem>('PUT', '/' + encodeURIComponent(id) + '/key', { key }),
  forget: (id: string) => call<{ removed: boolean; note: string }>('DELETE', '/' + encodeURIComponent(id)),
  validate: (settings: Settings) => call<{ ok: boolean }>('POST', '/validate', settings),
  install: (settings: Settings) => call<InstallAnswer>('POST', '/this-pc/install', settings),
  start: () => call<{ ok: boolean; started: boolean }>('POST', '/this-pc/start', {}),
  stop: () => call<{ ok: boolean; started: boolean }>('POST', '/this-pc/stop', {}),
  apply: (settings: Settings) => call<{ ok: boolean; restarted: boolean }>('POST', '/this-pc/apply', settings),
  remove: (deleteRecordings: boolean, confirmName: string) => call<RemoveAnswer>('POST', '/this-pc/remove', { deleteRecordings, confirmName }),
  snapshot: () => call<SnapshotAnswer>('POST', '/this-pc/snapshot', {}),
  hostReading: (id: string, body: { incident: { start: string; end: string }; reference: { start: string; end: string } }) =>
    call<HostAnswer>('POST', '/' + encodeURIComponent(id) + '/host-reading', body),
  /**
   * The sealed read key of the Blackbox installed on this PC, for Show and Copy (A33, wireframe 65). A POST that is never
   * cached. The caller holds the answer only while it is on view and never stores it.
   */
  revealKey: async (id: string): Promise<string> => (await call<{ key: string }>('POST', '/' + encodeURIComponent(id) + '/reveal-key', {})).key,
  /** The pack zip for the settings. The read key is not in it: the Blackbox makes its own on its server. */
  pack: async (settings: Settings): Promise<{ blob: Blob; fileName: string }> => {
    const response = await send('POST', '/pack', settings);
    return { blob: await response.blob(), fileName: packFileName(settings.name) };
  }
};

/** Saves a blob as a download. Nothing is kept by Unravis. */
export function saveBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

/* ---------------------------------------------------------------- rules */

export const packFileName = (name: string): string => 'unravis-blackbox-' + name + '.zip';

export const NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*$/;
export const NAME_MAX = 63;

/** Null when the name is fine, else a short reason code the panel turns into words. */
export function nameProblem(name: string): 'empty' | 'long' | 'chars' | null {
  if (!name.trim()) return 'empty';
  if (name.length > NAME_MAX) return 'long';
  if (!NAME_PATTERN.test(name)) return 'chars';
  return null;
}

export const INTERVALS = [5, 15, 30, 60] as const;

/** The defaults the recorder ships with (internal/blackbox/config.go). `folder` is the D: drive default. */
export function defaultSettings(name = ''): Settings {
  return {
    name, folder: 'D:\\UnravisBlackbox', intervalSeconds: 15, keepDays: 7, maxGB: 2, minFreePercent: 10, port: 7788, bind: '127.0.0.1',
    record: { processes: true, logsCatalogue: true, copyLogsOnSnapshot: false },
    logPaths: ['C:\\inetpub\\logs\\LogFiles'], probe: { enabled: false }
  };
}

export type SettingsProblem = 'name' | 'folder' | 'interval' | 'keep' | 'size' | 'free' | 'port' | 'bind' | 'logs';

const HOST_NAME = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/;

/** Whether text can be a listen address: IPv4, IPv6 or a host name. Mirrors validBind in internal/blackbox/config.go. */
export function validBind(text: string): boolean {
  const s = text.trim();
  if (!s || s.length > 253) return false;
  if (/^[0-9.]+$/.test(s)) {
    const parts = s.split('.');
    return parts.length === 4 && parts.every(p => /^\d{1,3}$/.test(p) && Number(p) <= 255);
  }
  if (s.includes(':')) return /^[0-9A-Fa-f:.]+$/.test(s) && (s.match(/:/g) ?? []).length >= 2;
  return HOST_NAME.test(s);
}

/** Whether a folder is a drive or share root or a Windows system folder. Mirrors unsafeFolder in internal/blackbox/config.go. */
export function unsafeFolder(folder: string): boolean {
  const rest = folder.trim().replace(/^[A-Za-z]:/, '').replace(/^[\\/]+|[\\/]+$/g, '');
  if (!rest || rest === '.') return true;
  const parts = rest.split(/[\\/]+/).filter(Boolean);
  const first = parts[0].toLowerCase();
  if (['windows', 'program files', 'program files (x86)', 'system volume information', '$recycle.bin'].includes(first)) return true;
  return ['users', 'programdata', 'documents and settings'].includes(first) && parts.length === 1;
}

/** The first thing wrong with the settings, or null. The server checks again; this only saves a round trip. */
export function settingsProblem(s: Settings): SettingsProblem | null {
  if (nameProblem(s.name)) return 'name';
  if (!/^[A-Za-z]:[\\/]/.test(s.folder.trim()) || unsafeFolder(s.folder)) return 'folder';
  if (!INTERVALS.includes(s.intervalSeconds as 5 | 15 | 30 | 60)) return 'interval';
  if (!Number.isInteger(s.keepDays) || s.keepDays < 1 || s.keepDays > 365) return 'keep';
  if (!(s.maxGB >= 0.1 && s.maxGB <= 10000)) return 'size';
  if (!Number.isInteger(s.minFreePercent) || s.minFreePercent < 1 || s.minFreePercent > 50) return 'free';
  if (!Number.isInteger(s.port) || s.port < 1 || s.port > 65535) return 'port';
  if (!validBind(s.bind)) return 'bind';
  if (s.logPaths.some(p => !/^[A-Za-z]:[\\/]/.test(p.trim()))) return 'logs';
  return null;
}

/** Whether the listen address is every address (IPv4 0.0.0.0 or IPv6 ::), so other computers can reach the status port (D-B-50). */
export function listensOnAll(bind: string): boolean {
  const s = bind.trim();
  return s === '0.0.0.0' || s === '::';
}

/**
 * The settings problem to show beside a field: the one found at the last attempt (`found`), but only while that field is still wrong,
 * so the message goes away as soon as the person fixes it (D-B-50). `logPathsText` is the log box text, one folder per line.
 */
export function fieldProblemNow(found: SettingsProblem | null, s: Settings, logPathsText: string): SettingsProblem | null {
  if (!found) return null;
  const logPaths = logPathsText.split('\n').map((p) => p.trim()).filter(Boolean);
  return settingsProblem({ ...s, logPaths }) === found ? found : null;
}

/** What kind of place the typed text looks like. The server decides; this only decides whether to ask for a read key. */
export function guessKind(location: string): LocationKind | null {
  const s = location.trim();
  if (!s) return null;
  if (s.startsWith('\\\\') || s.startsWith('//')) return 'share';
  if (/^[A-Za-z]:([\\/]|$)/.test(s)) return 'folder';
  if (s.includes('://') || /^[^\\\s/]+:\d+(\/.*)?$/.test(s)) return 'url';
  return null;
}

export const needsKey = (location: string): boolean => guessKind(location) === 'url';

/** The sticker class for a state tone. A sticker is information: it is never styled like a button. */
export const stickerTone = (tone: string): Tone => (tone === 'good' || tone === 'warn' || tone === 'bad' ? tone : 'idle');

/** A sample time as the panel shows it: date and time in UTC, or a dash. */
export function shownTime(value: string | undefined | null): string {
  if (!value) return '-';
  const t = Date.parse(value);
  if (!Number.isFinite(t)) return '-';
  return new Date(t).toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
}

/** A size as a short plain number. */
export function shownBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '-';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(0) + ' KB';
  if (bytes < 1024 * 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB';
  return (bytes / 1024 / 1024 / 1024).toFixed(1) + ' GB';
}

/** The confirm button for "Delete the recordings too" turns on only when the typed name matches exactly. */
export const removeConfirmed = (deleteRecordings: boolean, typed: string, name: string): boolean => !deleteRecordings || (name !== '' && typed === name);

/**
 * The gap warning of rule A35 for the Step 9 card. A short gap (2 minutes or less) only warns. A long one
 * makes the minutes inside it Cannot test yet. Returns null when there is no gap.
 */
export function gapSummary(gaps: Span[] | undefined): { short: Span[]; long: Span[]; shortMinutes: number; longMinutes: number } | null {
  if (!gaps || gaps.length === 0) return null;
  const short = gaps.filter(g => g.kind === 'short');
  const long = gaps.filter(g => g.kind === 'long');
  return { short, long, shortMinutes: short.reduce((n, g) => n + g.minutes, 0), longMinutes: long.reduce((n, g) => n + g.minutes, 0) };
}

/**
 * What Escape does inside the Place a Blackbox wizard (D-B-49). `none` leaves the key to the drawer (the finished screens),
 * `swallow` eats it while an install or pack is running, `back` goes one part back, `leave` goes to the list from Part 1.
 */
export type WizardEscape = 'none' | 'swallow' | 'back' | 'leave';
export function wizardEscape(part: number, busy: boolean, finished: boolean): WizardEscape {
  if (finished) return 'none';
  if (busy) return 'swallow';
  return part > 1 ? 'back' : 'leave';
}
