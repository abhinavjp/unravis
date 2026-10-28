// The setup window's rules, kept apart from the screens so they can be tested without a browser. No imports: the tests load this
// file on its own. The shapes follow internal/setup (UIState, Check, StepEvent, Result, WorkRequest).

export type Op = 'install' | 'upgrade' | 'repair' | 'uninstall';
export type Scope = 'user' | 'machine';
export type CheckStatus = 'ready' | 'partly' | 'failed';
export type StepState = 'waiting' | 'running' | 'done' | 'failed' | 'undone' | 'left' | 'skipped';
export type Tone = 'ok' | 'warn' | 'bad' | 'pending' | 'running' | 'neutral';
export type Outcome = 'done' | 'done-with-leftovers' | 'preflight-failed' | 'invalid' | 'cancelled-at-uac' | 'rolled-back' | 'rollback-incomplete' | 'cancelled';
export type Screen = 'welcome' | 'options' | 'approval' | 'progress' | 'done' | 'failed' | 'uninstall';

export interface Check { id: string; title: string; status: CheckStatus; detail: string; fix?: string }
export interface StepEvent { id: string; title: string; state: StepState; detail?: string }
export interface BlackboxOptions { name: string; folder: string; keepDays: number; maxGB: number; minFreePercent: number; port: number }
export interface Options {
  scope: Scope;
  location: string;
  blackbox: BlackboxOptions | null;
  firewall: { remoteAddresses: string[] } | null;
  startAtSignIn: boolean;
  openWhenDone: boolean;
  closeRunningApp: boolean;
}
export interface UninstallOptions { deleteCases: boolean; deleteRecordings: boolean; confirmCases?: string; confirmRecordings?: string; closeRunningApp: boolean }
export interface WorkRequest { op: Op; options: Options; uninstall: UninstallOptions }
export interface UIState {
  version: string;
  mode: Op;
  installed?: { version: string; scope: Scope; location: string; blackboxName?: string };
  defaults: { userLocation: string; machineLocation: string; blackbox: BlackboxOptions };
  isAdmin: boolean;
  logPath: string;
  data?: { kind: 'cases' | 'recordings'; folder: string; bytes: number }[];
  capture?: boolean;
}
export interface UIChanges { needsAdmin: boolean; willChange: string[]; willNotChange: string[] }
export interface Result {
  outcome: Outcome;
  op: Op;
  message: string;
  failed?: string;
  fix?: string;
  left?: string[];
  checks?: Check[];
  readKey?: string;
  version?: string;
  appExe?: string;
  restartApp?: boolean;
  kept?: string[];
}
export interface Problem { field: string; message: string }
export interface RailStep { id: string; label: string }

export const CONFIRM_DELETE_WORD = 'DELETE';

export function checkWord(status: CheckStatus): string {
  return status === 'ready' ? 'Ready' : status === 'partly' ? 'Partly ready' : 'Failed';
}

export function checkTone(status: CheckStatus): Tone {
  return status === 'ready' ? 'ok' : status === 'partly' ? 'warn' : 'bad';
}

/** The step words: Waiting, Running, Done, Failed, and the three a stopped run adds. */
export function stepWord(state: StepState): string {
  switch (state) {
    case 'waiting': return 'Waiting';
    case 'running': return 'Running';
    case 'done': return 'Done';
    case 'failed': return 'Failed';
    case 'undone': return 'Put back';
    case 'left': return 'Left behind';
    default: return 'Skipped';
  }
}

export function stepTone(state: StepState): Tone {
  switch (state) {
    case 'waiting': return 'pending';
    case 'running': return 'running';
    case 'done': return 'ok';
    case 'failed': return 'bad';
    case 'left': return 'warn';
    default: return 'neutral';
  }
}

export function blocked(checks: Check[]): boolean {
  return checks.some((check) => check.status === 'failed');
}

/** The rail for one kind of run. Upgrade and repair keep the choices of the install, so they have no Options step. */
export function railSteps(op: Op): RailStep[] {
  if (op === 'uninstall') return [{ id: 'uninstall', label: 'Remove' }, { id: 'approval', label: 'Approval' }, { id: 'progress', label: 'Progress' }, { id: 'done', label: 'Done' }];
  const work = op === 'upgrade' ? 'Upgrade' : op === 'repair' ? 'Repair' : 'Install';
  const steps: RailStep[] = [{ id: 'welcome', label: 'Welcome' }];
  if (op === 'install') steps.push({ id: 'options', label: 'Options' });
  steps.push({ id: 'approval', label: 'Approval' }, { id: 'progress', label: work }, { id: 'done', label: 'Done' });
  return steps;
}

/** Where a screen sits on the rail: the failed screen sits on the work step. */
export function railCurrent(screen: Screen): string {
  return screen === 'failed' ? 'progress' : screen;
}

/** The rail steps before the current one: done, or not needed (approval when no administrator rights were needed). */
export function railPosition(op: Op, screen: Screen, approvalNeeded: boolean | null): { current: string; done: string[]; skipped: string[] } {
  const steps = railSteps(op).map((step) => step.id);
  const current = railCurrent(screen);
  const index = steps.indexOf(current);
  const before = index < 0 ? [] : steps.slice(0, index);
  const skipped = approvalNeeded === false && before.includes('approval') ? ['approval'] : [];
  const done = before.filter((id) => !skipped.includes(id));
  if (screen === 'done') done.push('done');
  return { current, done, skipped };
}

export function welcomeHeading(state: Pick<UIState, 'mode' | 'version' | 'installed'>): string {
  if (state.mode === 'upgrade') return 'Upgrade Unravis ' + (state.installed?.version ?? '') + ' to ' + state.version;
  if (state.mode === 'repair') return 'Repair Unravis ' + state.version;
  if (state.mode === 'uninstall') return 'Remove Unravis';
  return 'Install Unravis';
}

const workNoun: Record<Op, string> = { install: 'Install', upgrade: 'Upgrade', repair: 'Repair', uninstall: 'Removal' };

export function progressHeading(op: Op): string {
  return op === 'upgrade' ? 'Upgrading' : op === 'repair' ? 'Repairing' : op === 'uninstall' ? 'Removing' : 'Installing';
}

export function doneHeading(op: Op): string {
  return op === 'upgrade' ? 'Unravis is upgraded' : op === 'repair' ? 'Unravis is repaired' : op === 'uninstall' ? 'Unravis is removed' : 'Unravis is installed';
}

export function failedHeading(result: Pick<Result, 'outcome' | 'op'>): string {
  const noun = workNoun[result.op] ?? 'Install';
  if (result.outcome === 'rollback-incomplete') return noun + ' did not finish. Some items are left.';
  if (result.outcome === 'cancelled') return noun + ' was cancelled. Nothing was left behind.';
  return noun + ' did not finish. Nothing was left behind.';
}

/** Folds the event stream into one row per step, in the order the steps first appeared, keeping the last detail given. */
export function mergeEvents(rows: StepEvent[], events: StepEvent[]): StepEvent[] {
  const out = rows.map((row) => ({ ...row }));
  for (const event of events) {
    const row = out.find((candidate) => candidate.id === event.id);
    if (!row) {
      out.push({ ...event });
      continue;
    }
    row.state = event.state;
    row.title = event.title || row.title;
    if (event.detail) row.detail = event.detail;
  }
  return out;
}

/** Once a run has ended, a step that never started did not run. */
export function settleSteps(rows: StepEvent[]): StepEvent[] {
  return rows.map((row) => (row.state === 'waiting' || row.state === 'running' ? { ...row, state: 'skipped' as StepState } : row));
}

export function progressPercent(rows: StepEvent[]): number {
  if (!rows.length) return 0;
  const finished = rows.filter((row) => row.state !== 'waiting' && row.state !== 'running').length;
  return Math.round((finished / rows.length) * 100);
}

/** The screen a finished run leads to. */
export function screenAfterResult(result: Pick<Result, 'outcome' | 'op'>): Screen {
  switch (result.outcome) {
    case 'done':
    case 'done-with-leftovers':
      return 'done';
    case 'cancelled-at-uac':
      return 'approval';
    case 'preflight-failed':
      return result.op === 'uninstall' ? 'uninstall' : 'welcome';
    case 'invalid':
      return result.op === 'uninstall' ? 'uninstall' : result.op === 'install' ? 'options' : 'welcome';
    default:
      return 'failed';
  }
}

/** The firewall addresses as typed: separated by commas, semicolons, spaces or new lines. */
export function parseAddresses(text: string): string[] {
  return text.split(/[\s,;]+/).map((part) => part.trim()).filter(Boolean);
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '';
  const units = ['bytes', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  if (unit === 0) return bytes + ' bytes';
  return (value >= 100 ? Math.round(value).toString() : value.toFixed(1).replace(/\.0$/, '')) + ' ' + units[unit];
}

const fullPath = /^[A-Za-z]:\\/;
const badChars = /[<>"|?*]/;

/** The checks the page can make before asking setup; setup checks everything again. */
export function validateOptions(options: Options): Problem[] {
  const out: Problem[] = [];
  const location = options.location.trim();
  if (!fullPath.test(location)) out.push({ field: 'location', message: 'Choose a folder with a full path, such as C:\\Program Files\\Unravis.' });
  else if (badChars.test(location)) out.push({ field: 'location', message: 'The folder name has characters Windows does not allow (< > " | ? *).' });
  const bb = options.blackbox;
  if (bb) {
    if (!/^[A-Za-z0-9][A-Za-z0-9-]{0,62}$/.test(bb.name.trim())) out.push({ field: 'bb-name', message: 'Give the Blackbox a name of up to 63 letters, numbers or dashes, starting with a letter or number.' });
    if (!fullPath.test(bb.folder.trim())) out.push({ field: 'bb-folder', message: 'Choose a recordings folder with a full path, such as D:\\UnravisBlackbox.' });
    if (!Number.isInteger(bb.keepDays) || bb.keepDays < 1 || bb.keepDays > 365) out.push({ field: 'bb-keep', message: 'Keep recordings for 1 to 365 days.' });
    if (!Number.isFinite(bb.maxGB) || bb.maxGB < 0.1 || bb.maxGB > 10000) out.push({ field: 'bb-max', message: 'Let the recordings use 0.1 to 10000 GB.' });
    if (!Number.isInteger(bb.minFreePercent) || bb.minFreePercent < 1 || bb.minFreePercent > 50) out.push({ field: 'bb-free', message: 'Keep 1 to 50 % of the disk free.' });
    if (!Number.isInteger(bb.port) || bb.port < 1 || bb.port > 65535) out.push({ field: 'bb-port', message: 'Choose a port from 1 to 65535.' });
  }
  if (options.firewall) {
    if (!bb) out.push({ field: 'firewall', message: 'The firewall rule is only for the Blackbox.' });
    else if (!options.firewall.remoteAddresses.length) out.push({ field: 'firewall', message: 'Name the PCs that may read the Blackbox, such as 10.0.0.5 or 10.0.1.0/24.' });
  }
  return out;
}

/** Remove is ready when every deletion that is ticked has its typed confirmation. */
export function uninstallReady(u: UninstallOptions, blackboxName: string): boolean {
  if (u.deleteCases && (u.confirmCases ?? '') !== CONFIRM_DELETE_WORD) return false;
  if (u.deleteRecordings && (!blackboxName || (u.confirmRecordings ?? '') !== blackboxName)) return false;
  return true;
}

/** The Done screen can be left once a read key shown there is saved. */
export function canFinish(result: Pick<Result, 'readKey'> | null, keySaved: boolean): boolean {
  return !result?.readKey || keySaved;
}

/** What goes to setup: only the deletions that are ticked carry a confirmation, and the window never asks for a read key file. */
export function buildRequest(op: Op, options: Options, u: UninstallOptions): WorkRequest {
  const uninstall: UninstallOptions = { deleteCases: u.deleteCases, deleteRecordings: u.deleteRecordings, closeRunningApp: options.closeRunningApp };
  if (u.deleteCases) uninstall.confirmCases = u.confirmCases ?? '';
  if (u.deleteRecordings) uninstall.confirmRecordings = u.confirmRecordings ?? '';
  return { op, options: { ...options, location: options.location.trim() }, uninstall };
}

/** The first words of the Admin approval screen: what needs the rights. */
export function approvalReason(op: Op, options: Pick<Options, 'scope' | 'blackbox'>, installedScope?: Scope): string {
  const everyone = (installedScope ?? options.scope) === 'machine';
  if (op === 'uninstall') return everyone ? 'Removing Unravis for everyone on this PC' : 'Removing Unravis';
  if (op === 'upgrade' || op === 'repair') return (op === 'upgrade' ? 'Upgrading' : 'Repairing') + ' Unravis' + (everyone ? ' for everyone on this PC' : '');
  if (options.blackbox && everyone) return 'Installing for everyone on this PC and placing the Blackbox';
  if (options.blackbox) return 'Placing the Blackbox';
  return 'Installing for everyone on this PC';
}
