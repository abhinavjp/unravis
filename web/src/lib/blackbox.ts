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
 *   POST   /api/blackbox/this-pc/update            -> { ok, updated, wasRunning, restarted, oldHash?, newHash?, newVersion? }
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
  /** The interval the recorder really runs at now. It is slower than intervalSeconds when the PC is busy. Absent from an older server. */
  effectiveIntervalSeconds?: number;
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
  /** The version the installed recorder reports and the one that ships with Unravis. Either can be missing when it is not known. */
  recorderVersion?: string;
  programVersion?: string;
  /** True when the recorder installed on this PC is older than the one that ships with Unravis. Update replaces it. */
  updateAvailable?: boolean;
  /** What the installed service runs with (blackbox.json holds no secret). Apply settings starts from it. */
  settings?: Settings;
}

/** The answer of Update the recorder. `updated` is false when the installed recorder already was the shipped one. */
export interface UpdateAnswer {
  ok: boolean;
  updated: boolean;
  wasRunning: boolean;
  restarted: boolean;
  oldHash?: string;
  newHash?: string;
  newVersion?: string;
}

/** One folder the search found. `fromService` is true when the running recorder says it writes there. */
export interface FolderCandidate { path: string; drive: string; recordings: number; newest: string; fromService: boolean; recommended: boolean }
export interface FoldersAnswer { folders: FolderCandidate[]; complete: boolean }

export interface BlackboxList { items: BlackboxItem[]; thisPc: ThisPc; canPack: boolean }

/** The kind of an owner-selected target: an IIS app pool or a Windows service (BBX-1-12). */
export type TargetKind = 'appPool' | 'service';

/** One owner-selected target. Only kind and name are kept: never a path, command line or key. */
export interface TargetRef { kind: TargetKind; name: string }

/** One detected app pool or service, as GET /api/blackbox/this-pc/targets lists it. Names only. */
export interface TargetInfo {
  name: string;
  /** Present only when the running state was actually read; absent means unknown. */
  running?: boolean;
  /** Fixed operational code from the server: running, stopped or unknown. */
  state?: 'running' | 'stopped' | 'unknown';
}
export interface TargetsAnswer {
  appPools: TargetInfo[]; services: TargetInfo[]; detected: boolean;
  /** Per-source detection: detected, unavailable or not-found. Optional for older servers. */
  appPoolsState?: string; servicesState?: string;
}

/** What is actually known about a detected target (W2-07). Configuration presence is
 *  never a running claim, and an unreadable state is unknown, not stopped. An older
 *  server that always sends a boolean still reads correctly. */
export type TargetRunState = 'running' | 'stopped' | 'unknown';
export function targetRunState(t: TargetInfo): TargetRunState {
  if (t.state === 'running' || t.state === 'stopped' || t.state === 'unknown') return t.state;
  if (t.running === true) return 'running';
  if (t.running === false) return 'stopped';
  return 'unknown';
}

/**
 * What the picker can honestly say about one selected target (W3-06). A name that is in the
 * detected list reads its state through targetRunState. A name that is absent is never guessed
 * to mean stopped: the endpoint offers running services only and caps each list at 256, so an
 * absent name may be stopped, may be beyond the cap, or may sit in a list that could not be read.
 * The per-source state tells those apart. `answer` is null when nothing could be read at all.
 */
export type PickedTargetState = 'running' | 'stopped' | 'unknown' | 'not-in-list' | 'list-unreadable' | 'source-not-found';
export function pickedTargetState(kind: TargetKind, name: string, answer: TargetsAnswer | null): PickedTargetState {
  if (!answer) return 'list-unreadable';
  const list = kind === 'appPool' ? answer.appPools : answer.services;
  const found = (list ?? []).find((t) => t.name === name);
  if (found) return targetRunState(found);
  const source = kind === 'appPool' ? answer.appPoolsState : answer.servicesState;
  if (source === 'unavailable') return 'list-unreadable';
  if (source === 'not-found') return 'source-not-found';
  // 'detected', or an older server that sends no per-source state but did detect something:
  // the list was read and this name is simply not in it. Absence is unknown, never stopped.
  if (source === 'detected' || answer.detected) return 'not-in-list';
  return 'list-unreadable';
}

/**
 * What the Settings step can honestly show for one detected list (UX-5). Pure: no state, no DOM, no fetch.
 * `read` is false while the single mount-time read is in flight; `failed` is true when that read was refused
 * or never reached the server. A failed or unreadable list is never an empty healthy list: 'failed' and
 * 'unavailable' say it could not be read, 'not-found' says the source is not on this PC, 'empty' means the
 * list was read and holds no names (its fieldset is skipped), and 'list' means names were read. An older
 * server sends no per-source state, so `detected` decides: nothing detected means nothing could be read.
 */
export type TargetListState = 'loading' | 'failed' | 'not-found' | 'unavailable' | 'empty' | 'list';
export function targetListState(list: TargetInfo[] | null | undefined, source: string | undefined, detected: boolean, read: boolean, failed: boolean): TargetListState {
  if (failed) return 'failed';
  if (!read) return 'loading';
  if ((list ?? []).length > 0) return 'list';
  if (source === 'not-found') return 'not-found';
  if (source === 'unavailable') return 'unavailable';
  if (source === 'detected' || detected) return 'empty';
  return 'unavailable';
}

/** The picked target names split by kind, each group in the order picked. Nothing is dropped. */
export function targetNamesByKind(targets: TargetRef[]): { appPools: string[]; services: string[] } {
  const appPools: string[] = [];
  const services: string[] = [];
  for (const t of targets) (t.kind === 'appPool' ? appPools : services).push(t.name);
  return { appPools, services };
}

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
  /** The app pools and services the owner picked for per-process detail. Empty means machine-wide only. */
  targets: TargetRef[];
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

/** The machine reason of a refused call ('offline', 'no_session', 'rate' ...), or '' for any other thrown value. */
export function refusalReason(error: unknown): string {
  return error instanceof BlackboxError ? error.refusal.reason : '';
}

/** True when the page itself lost Unravis (it restarted or this tab is stale), so a reload is the next step. */
export function needsReload(error: unknown): boolean {
  const reason = refusalReason(error);
  return reason === 'offline' || reason === 'no_session';
}

/**
 * Which service buttons a this-PC Blackbox gets. The state of a recorder that cannot be read says nothing about the
 * service, so the scan's `running` counts too: a service that runs but cannot be read can be stopped, and one that
 * is not running can be started. Never both.
 */
export function pcControls(state: PageState, thisPc: Pick<ThisPc, 'installed' | 'running'>): { start: boolean; stop: boolean } {
  const readRunning = state === 'running' || state === 'running-late' || state === 'disk-low' || state === 'incomplete' || state === 'clock-differs';
  const stop = readRunning || (thisPc.installed && thisPc.running);
  return { stop, start: thisPc.installed && !stop };
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
  /** Folders on this PC's fixed drives that hold recordings, newest first. Bounded on the server. */
  folders: (signal?: AbortSignal) => call<FoldersAnswer>('GET', '/this-pc/folders', undefined, signal),
  /** The app pools and services detected on this PC, for the wizard's target picker. Names only; read once on mount. */
  targets: (signal?: AbortSignal) => call<TargetsAnswer>('GET', '/this-pc/targets', undefined, signal),
  test: (body: AddBody) => call<TestAnswer>('POST', '/test', body),
  add: (body: AddBody) => call<BlackboxItem>('POST', '', body),
  check: (id: string) => call<BlackboxItem>('POST', '/' + encodeURIComponent(id) + '/check', {}),
  setKey: (id: string, key: string) => call<BlackboxItem>('PUT', '/' + encodeURIComponent(id) + '/key', { key }),
  forget: (id: string) => call<{ removed: boolean; note: string }>('DELETE', '/' + encodeURIComponent(id)),
  validate: (settings: Settings) => call<{ ok: boolean }>('POST', '/validate', settings),
  install: (settings: Settings) => call<InstallAnswer>('POST', '/this-pc/install', settings),
  start: () => call<{ ok: boolean; started: boolean }>('POST', '/this-pc/start', {}),
  stop: () => call<{ ok: boolean; started: boolean }>('POST', '/this-pc/stop', {}),
  apply: (settings: Settings) => call<{ ok: boolean; restarted: boolean; sqlWarning?: string }>('POST', '/this-pc/apply', settings),
  remove: (deleteRecordings: boolean, confirmName: string) => call<RemoveAnswer>('POST', '/this-pc/remove', { deleteRecordings, confirmName }),
  snapshot: () => call<SnapshotAnswer>('POST', '/this-pc/snapshot', {}),
  /** Replaces the recorder program installed on this PC with the one that ships with Unravis. One administrator approval. */
  updateThisPc: () => call<UpdateAnswer>('POST', '/this-pc/update', {}),
  hostReading: (id: string, body: { incident: { start: string; end: string }; reference: { start: string; end: string } }) =>
    call<HostAnswer>('POST', '/' + encodeURIComponent(id) + '/host-reading', body),
  /** Every recorded measurement for one window, downsampled to at most `points` buckets (server cap 600). Check the shape with readSeriesAnswer. */
  series: (id: string, body: { start: string; end: string; points?: number }, signal?: AbortSignal) =>
    call<unknown>('POST', '/' + encodeURIComponent(id) + '/series', body, signal),
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

/**
 * Add or remove one owner-selected target, matched by kind and name together (BBX-1-12). Pure: a new list
 * comes back and the one given is never changed. A target holds only kind and name: never a path, command
 * line or key. Checking a name in the wizard calls this; unchecking calls it again.
 */
export function toggleTarget(targets: TargetRef[], kind: TargetKind, name: string): TargetRef[] {
  const at = targets.findIndex((t) => t.kind === kind && t.name === name);
  if (at >= 0) return targets.filter((_, i) => i !== at);
  return [...targets, { kind, name }];
}

/** The target limits of internal/blackbox/config.go: at most 32 targets, a name of at most 128 bytes. */
export const TARGETS_MAX = 32;
export const TARGET_NAME_MAX = 128;

/** What is wrong with a target list. Null when the list is fine. The panel turns the code into words. */
export type TargetsProblem = 'many' | 'kind' | 'empty' | 'long' | 'chars' | 'duplicate';

/** The characters a target name may not hold, as the server refuses them. */
const TARGET_NAME_BAD = /[\\/:*?"<>|]/;

/** Length in UTF-8 bytes, which is how the server counts a name: an accented letter counts as more than one. */
function nameBytes(name: string): number {
  let bytes = 0;
  for (const ch of name) {
    const c = ch.codePointAt(0) ?? 0;
    bytes += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
  }
  return bytes;
}

/**
 * The first thing wrong with the targets, or null. Mirrors Validate in internal/blackbox/config.go, in the
 * same order: the count, then each target's kind, name and duplicate check. Duplicates are judged the way
 * Windows judges them: pool and service names match without regard to case, so EmailSvc and emailsvc are
 * one target. A missing list means machine-wide only, which is fine. Pure: the list given is never changed.
 */
export function targetsProblem(targets: TargetRef[] | null | undefined): TargetsProblem | null {
  const list = targets ?? [];
  if (list.length > TARGETS_MAX) return 'many';
  const seen: TargetRef[] = [];
  for (const target of list) {
    if (target.kind !== 'appPool' && target.kind !== 'service') return 'kind';
    const name = target.name ?? '';
    if (!name) return 'empty';
    if (nameBytes(name) > TARGET_NAME_MAX) return 'long';
    if (TARGET_NAME_BAD.test(name)) return 'chars';
    for (const ch of name) if ((ch.codePointAt(0) ?? 0) < 0x20) return 'chars';
    if (seen.some((prev) => prev.kind === target.kind && prev.name.toLowerCase() === name.toLowerCase())) return 'duplicate';
    seen.push(target);
  }
  return null;
}

/** The defaults the recorder ships with (internal/blackbox/config.go). `folder` is the D: drive default. */
export function defaultSettings(name = ''): Settings {
  return {
    name, folder: 'D:\\UnravisBlackbox', intervalSeconds: 15, keepDays: 7, maxGB: 2, minFreePercent: 10, port: 7788, bind: '127.0.0.1',
    record: { processes: true, logsCatalogue: true, copyLogsOnSnapshot: false },
    logPaths: ['C:\\inetpub\\logs\\LogFiles'], probe: { enabled: false },
    targets: []
  };
}

export type SettingsProblem = 'name' | 'folder' | 'interval' | 'keep' | 'size' | 'free' | 'port' | 'bind' | 'logs' | 'targets';

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

/** A folder on a drive the person can see, such as D:\UnravisBlackbox. */
const DRIVE_FOLDER = /^[A-Za-z]:[\\/]/;
/** The network volume of a full path: the leading \\server\share (or //server/share), cut the way filepath.VolumeName cuts it. */
const UNC_VOLUME = /^[\\/]{2}[^\\/]+[\\/][^\\/]+/;

/**
 * The shape of a typed folder path (B-04): a drive folder such as D:\UnravisBlackbox, a network folder such as
 * \\files01\share\UnravisBlackbox, or anything else, which is not a full Windows folder path. The Folder step uses this
 * to pick the right notice: the drive cards and the C: note for a drive, the network notice for a share.
 */
export type FolderKind = 'drive' | 'unc' | 'invalid';
export function folderKind(path: string): FolderKind {
  const s = path.trim();
  if (DRIVE_FOLDER.test(s)) return 'drive';
  if (UNC_VOLUME.test(s)) return 'unc';
  return 'invalid';
}

/**
 * Whether a folder is a drive or share root or a Windows system folder. Mirrors unsafeFolder in internal/blackbox/config.go:
 * a network path is cut at its share the way filepath.VolumeName cuts it, so \\files01\share is a share root and
 * \\files01\share\bb is a folder of its own.
 */
export function unsafeFolder(folder: string): boolean {
  let rest = folder.trim();
  if (/^[A-Za-z]:/.test(rest)) rest = rest.slice(2); // the X: volume
  else if (/^[\\/]{2}/.test(rest)) {
    const volume = UNC_VOLUME.exec(rest);
    if (!volume) return true; // \\server or bare \\: there is no share, so this is a share root
    rest = rest.slice(volume[0].length);
  }
  rest = rest.replace(/^[\\/]+|[\\/]+$/g, '');
  if (!rest || rest === '.') return true;
  const parts = rest.split(/[\\/]+/).filter(Boolean);
  const first = parts[0].toLowerCase();
  if (['windows', 'program files', 'program files (x86)', 'system volume information', '$recycle.bin'].includes(first)) return true;
  return ['users', 'programdata', 'documents and settings'].includes(first) && parts.length === 1;
}

/**
 * Why a typed folder is refused, as a short code the Folder step turns into words, or null when it is fine.
 *   full   not a full path: it has no drive letter and is not a network path (\\server\share\folder)
 *   slash  it uses forward slashes; Windows folders are written with backslashes
 *   chars  a name holds a character Windows does not allow in a folder name: < > " | ? * (a colon only follows the drive letter)
 *   system a drive root, a share root or a Windows system folder
 * The checks run in that order, so the first thing to fix is the one said.
 */
export type FolderFault = 'full' | 'slash' | 'chars' | 'system';
export function folderFault(folder: string): FolderFault | null {
  const s = folder.trim();
  if (folderKind(s) === 'invalid') return 'full';
  if (s.includes('/')) return 'slash';
  // After the drive letter and its colon (for a network path, anywhere) a colon is not allowed either.
  const rest = /^[A-Za-z]:/.test(s) ? s.slice(2) : s;
  if (/[<>:"|?*\u0000-\u001f]/.test(rest)) return 'chars';
  if (unsafeFolder(s)) return 'system';
  return null;
}

/**
 * The first thing wrong with the settings, or null. The server checks again; this only saves a round trip.
 * A folder is a full path on a drive (D:\UnravisBlackbox) or on a network share (\\files01\share\UnravisBlackbox),
 * written with backslashes and with no character Windows refuses in a folder name.
 */
export function settingsProblem(s: Settings): SettingsProblem | null {
  if (nameProblem(s.name)) return 'name';
  if (folderFault(s.folder) !== null) return 'folder';
  if (!INTERVALS.includes(s.intervalSeconds as 5 | 15 | 30 | 60)) return 'interval';
  if (!Number.isInteger(s.keepDays) || s.keepDays < 1 || s.keepDays > 365) return 'keep';
  if (!(s.maxGB >= 0.1 && s.maxGB <= 10000)) return 'size';
  if (!Number.isInteger(s.minFreePercent) || s.minFreePercent < 1 || s.minFreePercent > 50) return 'free';
  if (!Number.isInteger(s.port) || s.port < 1 || s.port > 65535) return 'port';
  if (!validBind(s.bind)) return 'bind';
  if (s.logPaths.some(p => !/^[A-Za-z]:[\\/]/.test(p.trim()))) return 'logs';
  if (targetsProblem(s.targets)) return 'targets';
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
/**
 * The first windows Step 9 offers, in minutes since the epoch: the incident is the last 30 minutes and the reference the 30
 * before that, both clamped to the recorded span (first..last sample). Start and end equal means the reference has no data.
 */
export function defaultHostWindows(firstSample: string | undefined, lastSample: string | undefined, minute = 60_000): { incStart: number; incEnd: number; refStart: number; refEnd: number } | null {
  const last = Date.parse(lastSample ?? '');
  if (!Number.isFinite(last)) return null;
  const lastMin = Math.floor(last / minute) * minute;
  const parsedFirst = Date.parse(firstSample ?? '');
  const first = Number.isFinite(parsedFirst) ? Math.min(Math.floor(parsedFirst / minute) * minute, lastMin) : lastMin - 60 * minute;
  const clamp = (t: number) => Math.min(Math.max(t, first), lastMin);
  const incStart = clamp(lastMin - 30 * minute);
  return { incStart, incEnd: lastMin, refStart: clamp(lastMin - 60 * minute), refEnd: incStart };
}

/** One setting that Apply would change: which field, and the old and new value as short text. */
export interface SettingChange { field: 'interval' | 'keepDays' | 'maxGB' | 'minFreePercent' | 'folder' | 'processes' | 'logs' | 'copyLogs' | 'logPaths' | 'targets'; from: string; to: string }

/** What an Apply would change, in the order of the form. Name, port and listening address are locked by the server, so they are not compared. */
export function settingsChanges(before: Settings, after: Settings): SettingChange[] {
  const out: SettingChange[] = [];
  const yn = (v: boolean) => (v ? 'on' : 'off');
  const add = (field: SettingChange['field'], from: string, to: string) => { if (from !== to) out.push({ field, from, to }); };
  add('interval', String(before.intervalSeconds), String(after.intervalSeconds));
  add('processes', yn(before.record.processes), yn(after.record.processes));
  add('logs', yn(before.record.logsCatalogue), yn(after.record.logsCatalogue));
  add('copyLogs', yn(before.record.copyLogsOnSnapshot), yn(after.record.copyLogsOnSnapshot));
  const names = (t: TargetRef[]) => (t ?? []).map((x) => x.kind + ':' + x.name).sort().join(',');
  add('targets', names(before.targets), names(after.targets));
  add('keepDays', String(before.keepDays), String(after.keepDays));
  add('maxGB', String(before.maxGB), String(after.maxGB));
  add('minFreePercent', String(before.minFreePercent), String(after.minFreePercent));
  add('folder', (before.folder ?? '').trim(), (after.folder ?? '').trim());
  add('logPaths', (before.logPaths ?? []).join('|'), (after.logPaths ?? []).join('|'));
  return out;
}

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
