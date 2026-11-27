import { checkWindows, parseFieldUtc, parseStoredWindows, type WindowFields } from './tracing/windows';

/**
 * The Servers screen's own logic, with no DOM and no network: the shapes the backend answers with, how a case window becomes a pull
 * range, how a failure becomes words, which machines may be ticked, and how every request body is built. A password or key
 * passphrase is never a field of anything here that a component keeps; it is an argument of `buildAddBody` and `buildSecretBody` only.
 */

/* ---------------------------------------------------------------- shapes */

export type ServerOs = 'windows' | 'linux';
export type AuthKind = 'password' | 'key';

/** One saved server as the backend lists it. It never holds a secret. */
export interface ServerEntry {
  id: string;
  name: string;
  host: string;
  port: number;
  user: string;
  os: ServerOs;
  auth: AuthKind;
  hasSecret: boolean;
  hostKey?: string;
  hostKeyPinnedAt?: string;
  transports: string[];
  minionIds?: string[];
  saltSudo?: boolean;
  folders?: Record<string, string[]>;
  blackboxAddr?: string;
  blackboxId?: string;
  addedAt?: string;
  /** The id of the pull running on this server now (list only). */
  running?: string;
}

export interface HostKey { algorithm: string; fingerprint: string }

export interface MachineView {
  id: string;
  /** accepted, pending, denied or rejected. */
  state: string;
  /** Absent when the master could not say. */
  online?: boolean;
  chosen: boolean;
}

export interface FolderCheck { kind: string; path: string; ok: boolean; files: number; error?: string }
export interface TestResult {
  host: { os: string; description: string; clockOffset: number };
  folders: FolderCheck[];
  blackbox?: { ok: boolean; error?: string; status?: { state?: string; name?: string; version?: string; samples?: number; lastSample?: string } };
  machine?: string;
}

export interface PlanFile { name: string; size: number; mtime: string }
export interface SkippedFile { name: string; reason: string }
export interface PlanView { kind: string; dir: string; files: PlanFile[]; skipped: SkippedFile[]; bytes: number; problem?: string }

export interface FileResult {
  name: string; outName: string; rawBytes: number; outBytes: number; sha256: string; serverDigest?: string; verified: boolean;
  changedWhileCopying?: boolean; linesScrubbed: number; linesDropped: number; status: string; note?: string;
}
export interface PulledResult {
  jobId: string; server: string; kind: string; serverFolder: string; stagedAt: string; started: string; finished: string;
  files: FileResult[]; skipped: SkippedFile[]; interrupted: boolean; bytesCopied: number;
}
export interface JobProgress { file: string; fileBytes: number; jobBytes: number; filesTotal: number; filesDone: number }
export type JobState = 'running' | 'done' | 'failed' | 'stopped';
export interface JobView {
  id: string; server: string; machine?: string; kind: string; dir: string; state: JobState; started: string; finished?: string;
  progress?: JobProgress; result?: PulledResult; failure?: Failure; blackboxId?: string; blackboxName?: string; blackboxNote?: string;
}

/** What a refused call says. `error` and `next` are the backend's own sentences. */
export interface Failure { status: number; error: string; reason: string; next?: string; pinned?: string; presented?: string }

/** A refused Servers call. It holds what the server said and nothing that was sent: no secret can be in it. */
export class ServersError extends Error {
  readonly failure: Failure;
  constructor(failure: Failure) {
    super(failure.error);
    this.name = 'ServersError';
    this.failure = failure;
  }
}

/* -------------------------------------------------------------- failures */

/** Words for a reason the backend sent no sentence for, or the page itself found (no session, no network). */
export const FAILURE_TEXT: Record<string, string> = {
  offline: 'Unravis could not be reached. Check that it is still running.',
  no_session: 'Open Unravis from the link it printed when it started, or restart it.',
  unavailable: 'Servers are not available in this build.',
  'host-key-changed': 'This server shows a different key than the one you trusted. Unravis did not sign in.',
  'need-host-key': 'Trust this server first. Unravis has not seen its key yet.',
  'sign-in': 'The server refused the user name or password.',
  unreachable: 'Nothing answered at that address and port.',
  timeout: 'The server took too long to answer.',
  'machine-offline': 'That machine did not answer through the Salt master.',
  'salt-master': 'The Salt master did not give a usable answer.',
  busy: 'A pull is already running on this server.',
  rate: 'Too many tries in a minute. Wait a minute and try again.',
  cancelled: 'That was cancelled.',
  'too-large': 'This is more than one pull may copy. Choose a shorter time range or fewer files.',
  'folder-not-allowed': 'That folder is not on the list of folders Unravis may read.',
  other: 'Something went wrong. Nothing was changed.'
};

/** The sentence to show for a failure: the backend's own words first, its next step after them. */
export function failureText(failure: Failure): string {
  const said = failure.error.trim() || FAILURE_TEXT[failure.reason] || FAILURE_TEXT.other;
  const next = (failure.next ?? '').trim();
  return next && !said.includes(next) ? said + ' ' + next : said;
}

/** Words for any thrown value. Never a stack, never what was sent. */
export function errorText(error: unknown): string {
  if (error instanceof ServersError) return failureText(error.failure);
  return FAILURE_TEXT.offline;
}

/** Where a failure is fixed. The page uses it to offer the one button that helps. */
export type FailureFix = 'trust' | 'connect' | 'folders' | 'machines' | 'wait' | 'none';
export function failureFix(reason: string): FailureFix {
  switch (reason) {
    case 'host-key-changed': case 'need-host-key': case 'key-not-shown': return 'trust';
    case 'sign-in': case 'no-salt': case 'no-ssh': case 'no-exec': return 'connect';
    case 'folder-not-allowed': case 'not-allowed': return 'folders';
    case 'need-machine': case 'machine-not-listed': return 'machines';
    case 'rate': case 'busy': return 'wait';
    default: return 'none';
  }
}

/** Builds a Failure from an HTTP status and the JSON body of a refusal. Anything unreadable becomes a plain general one. */
export function failureFromResponse(status: number, body: unknown): Failure {
  const b = (typeof body === 'object' && body !== null ? body : {}) as Record<string, unknown>;
  const text = (value: unknown): string => (typeof value === 'string' ? value : '');
  const out: Failure = { status, error: text(b.error), reason: text(b.reason) || 'other' };
  if (text(b.next)) out.next = text(b.next);
  if (text(b.pinned)) out.pinned = text(b.pinned);
  if (text(b.presented)) out.presented = text(b.presented);
  if (!out.error) out.error = FAILURE_TEXT[out.reason] ?? 'The request failed (' + status + ').';
  return out;
}

/* ---------------------------------------------------------------- format */

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '0 B';
  if (n >= 1e9) return (n / 1e9).toFixed(1) + ' GB';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + ' MB';
  if (n >= 1e3) return Math.round(n / 1e3) + ' KB';
  return Math.round(n) + ' B';
}

const pad = (n: number): string => String(n).padStart(2, '0');

/** `2026-11-26 14:20 UTC`, or an empty string for a time that does not parse. */
export function formatUtc(value: string | number): string {
  const ms = typeof value === 'number' ? value : Date.parse(value);
  if (!Number.isFinite(ms)) return '';
  const d = new Date(ms);
  return d.getUTCFullYear() + '-' + pad(d.getUTCMonth() + 1) + '-' + pad(d.getUTCDate()) + ' ' + pad(d.getUTCHours()) + ':' + pad(d.getUTCMinutes()) + ' UTC';
}

export function countText(n: number, one: string, many: string = one + 's'): string {
  return n.toLocaleString('en-US') + ' ' + (n === 1 ? one : many);
}

/** The backend sends the server clock minus this PC's clock in nanoseconds. Under two seconds is not worth saying. */
export function clockOffsetText(ns: number): string {
  if (!Number.isFinite(ns)) return '';
  const seconds = Math.round(Math.abs(ns) / 1e9);
  if (seconds < 2) return 'The server clock matches this PC.';
  const span = seconds < 120 ? seconds + ' seconds' : seconds < 7200 ? Math.round(seconds / 60) + ' minutes' : (seconds / 3600).toFixed(1) + ' hours';
  return 'The server clock is about ' + span + (ns > 0 ? ' ahead of' : ' behind') + ' this PC.';
}

/* --------------------------------------------------------------- windows */

/** A case window or its comparison period may be this long and no longer. */
export const MAX_CASE_WINDOW_DAYS = 7;
export const MAX_CASE_WINDOW_MS = MAX_CASE_WINDOW_DAYS * 24 * 3600_000;
/** The most hours the backend takes for one pull (366 days). */
export const MAX_SINCE_HOURS = 24 * 366;
const HOUR_MS = 3600_000;

export type CaseRange =
  | { state: 'none' }
  | { state: 'default-shaped' }
  | { state: 'too-long'; which: 'window' | 'comparison' }
  | { state: 'too-old' }
  | { state: 'ok'; incident: { start: number; end: number }; comparison: { start: number; end: number }; windowHours: number; bothHours: number };

/** Whole hours back from `nowMs` to `startMs`, at least 1, so the pull always reaches the start. */
export function sinceHoursFrom(startMs: number, nowMs: number): number {
  return Math.max(1, Math.ceil((nowMs - startMs) / HOUR_MS));
}

/**
 * True for the windows the timeline starts with when nobody chose any: a one-hour incident window with a one-hour comparison period
 * right before it. Those are not a choice about this case, so they are not used as a pull range.
 */
export function looksLikeDefaultWindows(w: WindowFields): boolean {
  const iS = parseFieldUtc(w.incidentStart), iE = parseFieldUtc(w.incidentEnd), rS = parseFieldUtc(w.referenceStart), rE = parseFieldUtc(w.referenceEnd);
  return iE - iS === HOUR_MS && rE - rS === HOUR_MS && rE === iS;
}

/**
 * What the case says about the time to pull. `saved` is what the case keeps (read only, from the case workspace). Anything that is not four
 * real UTC times, an untouched default, a window over 7 days, or a start more than a year back is not used, and the state says why.
 */
export function caseRange(saved: WindowFields | null | undefined, nowMs: number): CaseRange {
  const w = parseStoredWindows(saved ?? null);
  if (!w) return { state: 'none' };
  if (looksLikeDefaultWindows(w)) return { state: 'default-shaped' };
  const check = checkWindows(w);
  if (!check.body) return { state: 'none' };
  const iS = parseFieldUtc(w.incidentStart), iE = parseFieldUtc(w.incidentEnd), rS = parseFieldUtc(w.referenceStart), rE = parseFieldUtc(w.referenceEnd);
  if (iE - iS > MAX_CASE_WINDOW_MS) return { state: 'too-long', which: 'window' };
  if (rE - rS > MAX_CASE_WINDOW_MS) return { state: 'too-long', which: 'comparison' };
  const earliest = Math.min(iS, rS);
  if (sinceHoursFrom(earliest, nowMs) > MAX_SINCE_HOURS) return { state: 'too-old' };
  return { state: 'ok', incident: { start: iS, end: iE }, comparison: { start: rS, end: rE }, windowHours: sinceHoursFrom(iS, nowMs), bothHours: sinceHoursFrom(earliest, nowMs) };
}

export type RangeChoice = 'case' | 'window' | '24' | '72' | '168';
export const RANGE_HOURS: Record<'24' | '72' | '168', number> = { '24': 24, '72': 72, '168': 168 };

/** The `sinceHours` for a chosen range, or null when the choice needs a case range the case does not have. */
export function rangeHours(choice: RangeChoice, range: CaseRange, nowMs: number): number | null {
  if (choice === '24' || choice === '72' || choice === '168') return RANGE_HOURS[choice];
  if (range.state !== 'ok') return null;
  return nowMs >= 0 ? (choice === 'case' ? range.bothHours : range.windowHours) : null;
}

/** The ranges the person may pick now. The two case ranges are listed only when the case has usable windows. */
export function rangeChoices(range: CaseRange): RangeChoice[] {
  return range.state === 'ok' ? ['case', 'window', '24', '72', '168'] : ['24', '72', '168'];
}

export function rangeLabel(choice: RangeChoice): string {
  switch (choice) {
    case 'case': return 'Since the comparison period starts';
    case 'window': return 'Since the incident window starts';
    case '24': return 'Last 24 hours';
    case '72': return 'Last 3 days';
    default: return 'Last 7 days';
  }
}

/** A plain sentence for a case range that cannot be used. Empty when it is fine. */
export function caseRangeProblem(range: CaseRange): string {
  switch (range.state) {
    case 'none': return 'This case has no time windows yet. Choose a range for each item, or set the windows in the Investigation timeline.';
    case 'default-shaped': return 'The windows in this case are the timeline starting ones, so Unravis does not treat them as your choice. Choose a range for each item, or set your own windows in the Investigation timeline.';
    case 'too-long': return 'The ' + (range.which === 'window' ? 'incident window' : 'comparison period') + ' is longer than ' + MAX_CASE_WINDOW_DAYS + ' days. Shorten it in the Investigation timeline, or choose a range for each item.';
    case 'too-old': return 'The windows in this case start more than a year ago. Choose a range for each item.';
    default: return '';
  }
}

/* -------------------------------------------------------------- machines */

/** At most this many machines may be ticked for one server. */
export const MAX_MACHINES = 20;

export interface MachineChoice { selectable: boolean; label: string; tone: 'ok' | 'bad' | 'pending'; note: string }

/** Which machines may be ticked and what to say about each one. Only a machine the master accepted, and that is not known to be off, may be ticked. */
export function machineChoice(m: Pick<MachineView, 'state' | 'online'>): MachineChoice {
  if (m.state === 'accepted') {
    if (m.online === true) return { selectable: true, label: 'Online', tone: 'ok', note: 'Checked in the next step.' };
    if (m.online === false) return { selectable: false, label: 'Offline', tone: 'bad', note: 'Cannot be reached right now.' };
    return { selectable: true, label: 'Not sure', tone: 'pending', note: 'The master could not say if it is on. Unravis finds out in the next step.' };
  }
  if (m.state === 'pending') return { selectable: false, label: 'Not accepted yet', tone: 'pending', note: 'Whoever runs the Salt master has to accept it first.' };
  return { selectable: false, label: 'Refused by the master', tone: 'bad', note: 'The Salt master does not accept this machine.' };
}

/** The ids ticked when the list opens: the ones already saved for this server that may still be ticked. */
export function initialPicks(machines: MachineView[]): string[] {
  return machines.filter((m) => m.chosen && machineChoice(m).selectable).map((m) => m.id).slice(0, MAX_MACHINES);
}

/** Ticks or unticks one machine. A machine that may not be ticked, or a tick past the limit, changes nothing. */
export function togglePick(picks: readonly string[], machines: MachineView[], id: string, on: boolean): string[] {
  const m = machines.find((x) => x.id === id);
  const has = picks.includes(id);
  if (!on) return picks.filter((p) => p !== id);
  if (!m || !machineChoice(m).selectable || has || picks.length >= MAX_MACHINES) return [...picks];
  return [...picks, id];
}

/** Counts for the heading of the machine list. */
export function machineCounts(machines: MachineView[]): { accepted: number; online: number; offline: number; waiting: number; refused: number } {
  const c = { accepted: 0, online: 0, offline: 0, waiting: 0, refused: 0 };
  for (const m of machines) {
    if (m.state === 'accepted') { c.accepted++; if (m.online === true) c.online++; else if (m.online === false) c.offline++; }
    else if (m.state === 'pending') c.waiting++;
    else c.refused++;
  }
  return c;
}

/* --------------------------------------------------------------- folders */

export interface FolderKindInfo { kind: string; pullKind: string; label: string; hint: string; suggestions: string[] }

/** The folder groups the server may allow, with the pull kind each is read as. */
export const FOLDER_KINDS: FolderKindInfo[] = [
  { kind: 'blackbox', pullKind: 'blackbox', label: 'Blackbox recordings', hint: 'The folder the Blackbox recorder writes to.', suggestions: ['D:\\UnravisBlackbox', 'C:\\UnravisBlackbox'] },
  { kind: 'iis', pullKind: 'iis', label: 'IIS logs', hint: 'The folder that holds the web site log files.', suggestions: ['C:\\inetpub\\logs\\LogFiles\\W3SVC1'] },
  { kind: 'sql', pullKind: 'sqlserver', label: 'SQL Server logs', hint: 'The folder that holds the SQL Server error logs.', suggestions: [] },
  { kind: 'redis', pullKind: 'redis', label: 'Redis logs', hint: 'The folder that holds the Redis log files.', suggestions: ['/var/log/redis'] },
  { kind: 'other', pullKind: 'applog', label: 'Other logs', hint: 'Any other log folder you want to read.', suggestions: [] }
];

export const folderKindInfo = (kind: string): FolderKindInfo | undefined => FOLDER_KINDS.find((k) => k.kind === kind || k.pullKind === kind);

const WIN_ABS = /^[A-Za-z]:[\\/]/;

/** A first check of a folder the person typed. The backend checks again and has the last word. Empty means it looks usable. */
export function folderProblem(path: string): string {
  const p = path.trim();
  if (!p) return 'Type the full path of a folder.';
  if (!WIN_ABS.test(p) && !p.startsWith('/')) return 'Use a full path, such as D:\\Logs or /var/log/redis.';
  if (/[*?"<>|\0]/.test(p)) return 'A folder path cannot hold * ? " < > or |.';
  if (p.includes('..')) return 'A folder path cannot hold "..".';
  if (/^[A-Za-z]:[\\/]?$/.test(p) || p === '/') return 'Choose a folder, not a whole drive.';
  return '';
}

export type FolderMap = Record<string, string[]>;

/** The folder map with one folder added. A path that is already there, or does not look usable, changes nothing. */
export function withFolder(folders: FolderMap | undefined, kind: string, path: string): FolderMap {
  const next: FolderMap = {};
  for (const [k, list] of Object.entries(folders ?? {})) next[k] = [...list];
  const p = path.trim();
  if (folderProblem(p) || !FOLDER_KINDS.some((k) => k.kind === kind)) return next;
  const list = next[kind] ?? [];
  if (!list.includes(p)) list.push(p);
  next[kind] = list;
  return next;
}

export function withoutFolder(folders: FolderMap | undefined, kind: string, path: string): FolderMap {
  const next: FolderMap = {};
  for (const [k, list] of Object.entries(folders ?? {})) {
    const kept = k === kind ? list.filter((p) => p !== path) : [...list];
    if (kept.length) next[k] = kept;
  }
  return next;
}

export interface PullTarget { machine: string; kind: string; pullKind: string; label: string; dir: string; key: string }

/** Every machine and allowed folder a pull could read, in a steady order. */
export function pullTargets(machines: readonly string[], folders: FolderMap | undefined): PullTarget[] {
  const out: PullTarget[] = [];
  for (const machine of machines) {
    for (const info of FOLDER_KINDS) {
      for (const dir of folders?.[info.kind] ?? []) out.push({ machine, kind: info.kind, pullKind: info.pullKind, label: info.label, dir, key: machine + '|' + info.kind + '|' + dir });
    }
  }
  return out;
}

/** The folders a test could read, as pull targets. A folder that failed its check is left out: it can not be pulled. */
export function readableTargets(machine: string, result: Pick<TestResult, 'folders'>): PullTarget[] {
  const out: PullTarget[] = [];
  for (const f of result.folders) {
    const info = folderKindInfo(f.kind);
    if (!f.ok || !info) continue;
    out.push({ machine, kind: info.kind, pullKind: info.pullKind, label: info.label, dir: f.path, key: machine + '|' + info.kind + '|' + f.path });
  }
  return out;
}

/* ------------------------------------------------------------- the forms */

export interface ConnectForm {
  host: string;
  port: string;
  user: string;
  os: ServerOs;
  auth: AuthKind;
}

export const emptyConnectForm = (): ConnectForm => ({ host: '', port: '22', user: '', os: 'linux', auth: 'password' });

function sameAddress(form: ConnectForm, entry: ServerEntry): boolean {
  return form.host.trim().toLowerCase() === entry.host.toLowerCase() && Number(form.port) === entry.port && form.user.trim() === entry.user;
}

export function matchesConnection(form: ConnectForm, entry: ServerEntry | null): boolean {
  return !!entry && sameAddress(form, entry) && form.os === entry.os && form.auth === entry.auth;
}

export function findConnection(form: ConnectForm, entries: ServerEntry[] | null): ServerEntry | undefined {
  return entries?.find((entry) => sameAddress(form, entry));
}

const MACHINE_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
export const isMachineId = (id: string): boolean => MACHINE_ID.test(id);

/** What is missing or wrong in the connect form, as plain sentences. Empty means the form may be sent. The secret is checked only for being empty. */
export function connectProblems(form: ConnectForm, hasSecret: boolean): string[] {
  const out: string[] = [];
  if (!form.host.trim()) out.push('Type the address of the server that runs the Salt master.');
  const port = Number(form.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) out.push('The port is a number from 1 to 65535. Most servers use 22.');
  if (!form.user.trim()) out.push('Type the user name you sign in with.');
  if (!hasSecret) out.push(form.auth === 'password' ? 'Type the password.' : 'Choose the key file your admin gave you.');
  return out;
}

export interface AddBody {
  name: string; host: string; port: number; user: string; os: ServerOs; auth: AuthKind; secret: string; passphrase: string;
  transports: string[]; minionIds: string[]; saltSudo: boolean;
}

/** The body that saves a new server through a Salt master. This is the one place a secret is put in a body, and it is sent once. */
export function buildAddBody(form: ConnectForm, secret: string, passphrase: string): AddBody {
  const host = form.host.trim();
  return {
    name: host, host, port: Number(form.port) || 22, user: form.user.trim(), os: form.os, auth: form.auth,
    secret, passphrase: form.auth === 'key' ? passphrase : '', transports: ['salt'], minionIds: [], saltSudo: false
  };
}

/** The body that stores a new password or key for a saved server. */
export function buildSecretBody(auth: AuthKind, secret: string, passphrase: string): { auth: AuthKind; secret: string; passphrase: string } {
  return { auth, secret, passphrase: auth === 'key' ? passphrase : '' };
}

export const buildPinBody = (fingerprint: string): { fingerprint: string } => ({ fingerprint });
export const buildReplaceBody = (oldFingerprint: string, newFingerprint: string): { old: string; new: string } => ({ old: oldFingerprint, new: newFingerprint });
export const buildMachineBody = (machine: string): { machine: string } => ({ machine });

export interface PatchBody { minionIds?: string[]; saltSudo?: boolean; folders?: FolderMap; name?: string }

/** Only the fields given go up, and none of them can be a secret. */
export function buildPatchBody(parts: { minionIds?: readonly string[]; saltSudo?: boolean; folders?: FolderMap; name?: string }): PatchBody {
  const out: PatchBody = {};
  if (parts.minionIds) out.minionIds = [...parts.minionIds];
  if (parts.saltSudo !== undefined) out.saltSudo = parts.saltSudo;
  if (parts.folders) out.folders = parts.folders;
  if (parts.name !== undefined) out.name = parts.name.trim();
  return out;
}

export interface PullBody { machine: string; kind: string; dir: string; pattern: string; sinceHours: number; resume?: string }

/** The body of a plan or a pull. `sinceHours` is a whole number of hours; zero means no limit. */
export function buildPullBody(target: Pick<PullTarget, 'machine' | 'pullKind' | 'dir'>, sinceHours: number, resume?: string): PullBody {
  const hours = Number.isFinite(sinceHours) ? Math.min(MAX_SINCE_HOURS, Math.max(0, Math.floor(sinceHours))) : 0;
  const body: PullBody = { machine: target.machine, kind: target.pullKind, dir: target.dir, pattern: '', sinceHours: hours };
  if (resume) body.resume = resume;
  return body;
}

/** Words that must never appear as a key in a body that is not the add or secret body. */
export const SECRET_KEYS = ['secret', 'password', 'passphrase', 'key', 'blackboxKey', 'token'];

/** The secret-looking keys in a body, at any depth. Used by tests to prove a body carries none. */
export function secretKeysIn(value: unknown, found: string[] = []): string[] {
  if (Array.isArray(value)) { for (const item of value) secretKeysIn(item, found); return found; }
  if (typeof value === 'object' && value !== null) {
    for (const [k, v] of Object.entries(value)) {
      if (SECRET_KEYS.includes(k)) found.push(k);
      secretKeysIn(v, found);
    }
  }
  return found;
}

/* ---------------------------------------------------------------- checks */

export interface CheckLine { id: string; ok: boolean; text: string; note?: string }

/** The lines the Reach step shows, built only from what the test answered. */
/** The system name, unless the machine's own description already says it. */
function osWord(os: string, description: string): string {
  const word = os === 'windows' ? 'Windows' : os === 'linux' ? 'Linux' : '';
  return word && !description.toLowerCase().includes(word.toLowerCase()) ? word : '';
}

export function testChecks(result: TestResult, folderCount: number): CheckLine[] {
  const lines: CheckLine[] = [
    { id: 'master', ok: true, text: 'The Salt master answered.' },
    { id: 'machine', ok: true, text: 'The machine answered.', note: [result.host.description, osWord(result.host.os, result.host.description), clockOffsetText(result.host.clockOffset)].filter(Boolean).join('. ') }
  ];
  if (folderCount === 0) lines.push({ id: 'no-folders', ok: false, text: 'No folder is allowed yet.', note: 'Add the folders Unravis may read, then check again.' });
  for (const f of result.folders) {
    const info = folderKindInfo(f.kind);
    lines.push({ id: 'folder:' + f.kind + ':' + f.path, ok: f.ok, text: (info?.label ?? 'Folder') + ': ' + f.path, note: f.ok ? countText(f.files, 'file') + ' found.' : (f.error || 'This folder could not be read.') });
  }
  if (result.blackbox) lines.push({ id: 'blackbox', ok: result.blackbox.ok, text: 'The Blackbox recorder', note: result.blackbox.ok ? (result.blackbox.status?.state ? 'It says: ' + result.blackbox.status.state + '.' : 'It answered.') : (result.blackbox.error || 'It did not answer.') });
  return lines;
}

export type Verdict = 'ready' | 'partial' | 'none';
/** Ready when every folder could be read, partial when some could, none when no folder could. */
export function testVerdict(result: TestResult | null): Verdict {
  if (!result) return 'none';
  const ok = result.folders.filter((f) => f.ok).length;
  if (result.folders.length === 0 || ok === 0) return 'none';
  return ok === result.folders.length ? 'ready' : 'partial';
}

/* ------------------------------------------------------------ plans, jobs */

export interface PlanSummary { files: number; bytes: number; skipped: number; problem: string; empty: boolean; newest: string; oldest: string }

export function planSummary(plan: PlanView): PlanSummary {
  const times = plan.files.map((f) => Date.parse(f.mtime)).filter(Number.isFinite);
  return {
    files: plan.files.length, bytes: plan.bytes, skipped: plan.skipped.length, problem: plan.problem ?? '', empty: plan.files.length === 0,
    oldest: times.length ? formatUtc(Math.min(...times)) : '', newest: times.length ? formatUtc(Math.max(...times)) : ''
  };
}

/** The chosen plans' totals. A plan with a problem is never counted: it cannot be pulled. */
export function pickedTotals(plans: readonly (PlanView | null)[]): { items: number; files: number; bytes: number } {
  let items = 0, files = 0, bytes = 0;
  for (const p of plans) {
    if (!p || p.problem || p.files.length === 0) continue;
    items++; files += p.files.length; bytes += p.bytes;
  }
  return { items, files, bytes };
}

export function jobStateLabel(state: JobState): string {
  switch (state) {
    case 'running': return 'Copying';
    case 'done': return 'Done';
    case 'stopped': return 'Stopped';
    default: return 'Stopped by a problem';
  }
}

/** Percent of the planned bytes copied so far, 0 to 100. Null when the plan size is not known. */
export function jobPercent(job: Pick<JobView, 'state' | 'progress'>, plannedBytes: number | null): number | null {
  if (job.state === 'done') return 100;
  if (!plannedBytes || plannedBytes <= 0 || !job.progress) return null;
  return Math.max(0, Math.min(99, Math.floor((job.progress.jobBytes / plannedBytes) * 100)));
}

export interface ResultSummary { files: number; verified: number; notChecked: number; changed: number; linesHidden: number; linesDropped: number; bytes: number }

export function resultSummary(result: PulledResult | undefined): ResultSummary {
  const s: ResultSummary = { files: 0, verified: 0, notChecked: 0, changed: 0, linesHidden: 0, linesDropped: 0, bytes: 0 };
  for (const f of result?.files ?? []) {
    if (f.status !== 'done') continue;
    s.files++;
    s.bytes += f.outBytes;
    if (f.verified) s.verified++; else s.notChecked++;
    if (f.changedWhileCopying) s.changed++;
    s.linesHidden += f.linesScrubbed;
    s.linesDropped += f.linesDropped;
  }
  return s;
}

/** The name pulled Blackbox sets get, matching what the backend saves: `<server> / <machine> (pulled 26 Nov 14:20)`. Shown only as the backend returns it. */
export const pulledFileUrl = (serverId: string, jobId: string, name: string): string =>
  '/api/servers/' + encodeURIComponent(serverId) + '/pulls/' + encodeURIComponent(jobId) + '/files/' + encodeURIComponent(name);

/* --------------------------------------------------- copy-ready commands */

export type CommandPart = 'key' | 'salt' | 'admin-key' | 'admin-salt';

export interface CommandInput { os: ServerOs; host: string; port: number; user: string; machines: readonly string[]; folders: FolderMap | undefined; sudo: boolean }

const SAFE_PATH = /^[A-Za-z0-9_:\\/.-]+$/;
const SAFE_HOST = /^[A-Za-z0-9._:-]+$/;

/**
 * Commands the person can run themselves, or send to the person who runs the server. Every line only reads. A host, machine or folder
 * with a character that could change a command is left out rather than quoted.
 */
export function accessCommands(part: CommandPart, c: CommandInput): string {
  const linux = c.os === 'linux';
  const sudo = linux && c.sudo ? 'sudo ' : '';
  const host = SAFE_HOST.test(c.host) ? c.host : '<address>';
  const keyOnServer = linux ? 'ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub' : 'ssh-keygen -lf $env:ProgramData\\ssh\\ssh_host_ed25519_key.pub';
  if (part === 'key') {
    return ['# 1. On your PC, in PowerShell. Shows the fingerprint the server presents.',
      '# It signs in with nothing and sends no password. A "Permission denied" line afterwards is expected.',
      'ssh -v -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=8 -p ' + c.port + ' probe@' + host + ' 2>&1 | Select-String "Server host key"', '',
      '# 2. On the server itself (your admin can run this) to compare with step 1.', keyOnServer].join('\n');
  }
  if (part === 'admin-key') {
    return 'Hello,\n\nI am setting up Unravis to read Blackbox recordings and logs from ' + host + ' (port ' + c.port + '). It only reads.\nBefore I trust the server, could you tell me its SSH host key fingerprint? You can read it on the server with:\n\n' + keyOnServer + '\n\nThank you.';
  }
  const lines = ['# Run on the ' + (linux ? 'Linux' : 'Windows') + ' Salt master after you sign in as ' + (c.user || '<user>') + '. Every line only reads.', sudo + 'salt-key -L', sudo + 'salt-run manage.status'];
  const folderLines: string[] = [];
  for (const id of c.machines.filter(isMachineId)) {
    lines.push(sudo + "salt '" + id + "' test.ping");
    const dirs = Object.values(c.folders ?? {}).flat().filter((d) => SAFE_PATH.test(d)).slice(0, 2);
    for (const d of dirs) lines.push(WIN_ABS.test(d) ? sudo + "salt '" + id + "' cmd.run 'Get-ChildItem " + d + " | Select-Object -First 3' shell=powershell" : sudo + "salt '" + id + "' cmd.run 'ls -l " + d + " | head -3'");
    folderLines.push(id + ': ' + (dirs.join(', ') || '(none listed)'));
  }
  if (part === 'salt') return lines.join('\n');
  return 'Hello,\n\nI am setting up Unravis to read Blackbox recordings and logs through the Salt master ' + host + '. It only reads and changes nothing.\nPlease check that my account (' + (c.user || '<user>') + ') can do the following, or run it and send me the output:\n\n' + lines.slice(1).join('\n') + '\n\nI also need read access to these folders:\n' + (folderLines.join('\n') || '(none listed)') + '\n\nIf any line is refused, please tell me which one. Thank you.';
}
