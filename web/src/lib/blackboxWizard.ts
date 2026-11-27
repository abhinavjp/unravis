import {
  INTERVALS,
  defaultSettings,
  extraCountersProblem,
  folderFault,
  folderKind,
  nameProblem,
  settingsProblem,
  targetsProblem
} from './blackbox';
import type { Settings, TargetRef, ThisPc } from './blackbox';
import type { DriveInfo } from './blackboxDrives';
import { DEFAULT_QUERY_TEXT_SIZE, type SqlProfile } from './blackboxSql';

/**
 * The state machine of the Place a Blackbox pop-up (contract C-1, payload contract C-5). Pure TypeScript: no Svelte,
 * no fetch, no DOM, so node can test every rule. The components hold one WizardState and call these functions;
 * they never decide a rule themselves. Words live in copy/blackboxWizardCopy.ts, so a problem here is a short code.
 *
 * Every function returns a new state and never changes the one given.
 */

export type StepId = 'name' | 'where' | 'record' | 'sql' | 'redis' | 'watch' | 'keep' | 'folder' | 'review';
export type Where = 'pc' | 'pack';
export type PresetId = 'recommended' | 'light' | 'custom';
export type WatchMode = 'none' | 'some';
export type RedisProfile = 'standard' | 'everything';
export type RedisRowId = 'slowlog' | 'latency' | 'clients' | 'slowArgs';
/** A Redis starting point and the rows the owner switched against it. A row left out follows the starting point. */
export interface RedisChoice {
  profile: RedisProfile;
  rows: Partial<Record<RedisRowId, boolean>>;
}

/** The three collector rows of the Record step: Server facts, Extra Windows counters, Log and config file paths. */
export interface CollectorRows { facts: boolean; counters: boolean; paths: boolean }
export type CollectorRowId = keyof CollectorRows;

/** What the counters box holds when the row is first switched on. */
export const COUNTERS_PREFILL = '\\Process(w3wp)\\Page Faults/sec\n\\Memory\\Pool Nonpaged Bytes';
/** Catalogue ids the server knows these rows by (blackbox.Config collectors). An absent id means on. */
const COLLECTOR_IDS = { facts: 'host.facts', paths: 'paths.files' } as const;

export interface WizardState {
  step: StepId;
  /** Index of the furthest step reached; the rail unlocks 0..maxSeen. */
  maxSeen: number;
  /** Set by a Review "Change" link: the next Next goes straight back to Review. */
  returnTo: 'review' | null;
  where: Where | null;
  /** The one payload; starts from defaultSettings(). */
  settings: Settings;
  /** Textarea mirror of settings.logPaths: one folder per line. */
  logPathsText: string;
  /** Which of the three collector rows are on. Recommended: facts and paths on, counters off. */
  collectorRows: CollectorRows;
  /** Textarea for settings.counters.extra: one counter path per line. Only used while collectorRows.counters is on. */
  countersText: string;
  watchMode: WatchMode;
  /** The owner typed or picked a folder; stop the automatic default. */
  folderTouched: boolean;
  /** The C: tick. */
  confirmSystemDrive: boolean;
  /** SQL Server seen in the this-pc services list (B-01). */
  sqlSeen: boolean;
  /** Redis (or Memurai) seen in the this-pc services list. The Redis step appears on the pack path, or when this is true. */
  redisSeen: boolean;
  /** The SQL starting point and the query-text tick, as the SQL step shows them. Standard with no text is the plain install. */
  sqlChoice: { profile: SqlProfile; text: boolean };
  /** The Redis starting point and the single-row overrides on top of it. */
  redisChoice: RedisChoice;
  /**
   * Letter of the drive Windows runs from, as the drives route reported it. Optional: when it is absent the
   * system drive is C. Added to C-1 so the C: tick guards the right drive on a machine where Windows is not on C:.
   */
  systemDrive?: string;
  /**
   * Letters of the USB and network drives of this PC, as the drives route reported them (BL-137). The recorder only
   * records on a local hard drive, so on this PC a folder on one of these cannot be used. Optional: absent means none known.
   */
  nonLocalDrives?: string[];
}

/**
 * Why a step cannot be left. A short code, never prose: the pop-up turns it into words. Null means the step is fine.
 *   name:    name-empty | name-long | name-chars
 *   where:   where-missing
 *   record:  interval | logs | counters (only while the extra counters row is on)
 *   watch:   targets-many | targets-kind | targets-empty | targets-long | targets-chars | targets-duplicate
 *   keep:    keep | size | free
 *   folder:  folder-invalid (not a full path) | folder-slash (forward slashes) | folder-chars (a character Windows refuses)
 *            | folder-unsafe (a drive root or system folder) | folder-not-local (a network path, or a USB or network
 *            drive of this PC: the recorder refuses any folder that is not on a local hard drive) | folder-system-drive
 *            | port | bind
 *   review:  the first problem of any step before it
 */
export type StepProblem = string;

/** The folder the recorder is placed in when the owner changes nothing, on the chosen drive. */
const FOLDER_NAME = 'UnravisBlackbox';
/** The system drive when the drives route has not said otherwise. */
const DEFAULT_SYSTEM_DRIVE = 'C';
/** The server accepts at most this many log folders (maxLogPaths in config.go). */
const LOG_PATHS_MAX = 20;

/* ---------------------------------------------------------------- the steps */

/** The steps in order: the SQL and Redis steps apply on the pack path, or when that product was seen on this PC. 9 at most, 7 at least. */
export function steps(s: Pick<WizardState, 'where' | 'sqlSeen' | 'redisSeen'>): StepId[] {
  const list: StepId[] = ['name', 'where', 'record'];
  if (s.where === 'pack' || s.sqlSeen) list.push('sql');
  if (s.where === 'pack' || s.redisSeen) list.push('redis');
  list.push('watch', 'keep', 'folder', 'review');
  return list;
}

/** The position shown as "Step X of N": 1-based. */
export function stepIndex(s: WizardState): number {
  return steps(s).indexOf(s.step) + 1;
}

/** Whether the rail lets the owner go to a step: it is in the list and not past the furthest step reached. */
export function canVisit(s: WizardState, id: StepId): boolean {
  const at = steps(s).indexOf(id);
  return at >= 0 && at <= s.maxSeen;
}

/* ---------------------------------------------------------------- folder helpers */

/** The drive letter of a typed path ('D' for D:\x, upper case), or null for a network path or anything else. */
export function driveOfPath(path: string): string | null {
  const m = /^([A-Za-z]):/.exec(path.trim());
  return m ? m[1].toUpperCase() : null;
}

/**
 * The folder the Folder step starts with (S-11, B-05). The first plain fixed drive, in letter order, that is not the
 * Windows drive: X:\UnravisBlackbox. With no such drive the Windows drive is used and the C: tick is needed. With no
 * drives list at all (the pack path, or the route failed) it is the fallback, which is D:\UnravisBlackbox in the wizard.
 */
export function defaultFolder(drives: DriveInfo[] | null, fallback: string): { folder: string; needsSystemTick: boolean } {
  if (drives) {
    const plain = drives
      .filter((d) => d.type === 'fixed' && !d.system)
      .map((d) => d.letter.toUpperCase())
      .sort();
    if (plain.length > 0) return { folder: plain[0] + ':\\' + FOLDER_NAME, needsSystemTick: false };
    const windows = drives.find((d) => d.system);
    if (windows) return { folder: windows.letter.toUpperCase() + ':\\' + FOLDER_NAME, needsSystemTick: true };
  }
  const letter = driveOfPath(fallback);
  const system = (drives?.find((d) => d.system)?.letter ?? DEFAULT_SYSTEM_DRIVE).toUpperCase();
  return { folder: fallback, needsSystemTick: letter !== null && letter === system };
}

/** The letters of the USB and network drives in a drives list, upper case. The Windows drive is never one of them. */
export function nonLocalLetters(drives: DriveInfo[]): string[] {
  return drives.filter((d) => !d.system && d.type !== 'fixed').map((d) => d.letter.toUpperCase());
}

function sameLetters(a: string[] | undefined, b: string[] | undefined): boolean {
  const x = a ?? [];
  const y = b ?? [];
  return x.length === y.length && x.every((l, i) => l === y[i]);
}

/**
 * Whether the recorder would refuse the typed folder because it is not on a local hard drive (BL-137). A network path
 * is refused on both paths: the recorder checks the drive type where it is installed. A drive letter is only judged on
 * this PC, where the drives list is known.
 */
export function folderNotLocal(s: WizardState): boolean {
  const folder = s.settings.folder;
  if (folderKind(folder) === 'unc') return true;
  if (s.where !== 'pc') return false;
  const letter = driveOfPath(folder);
  return letter !== null && (s.nonLocalDrives ?? []).includes(letter);
}

function systemDriveOf(s: WizardState): string {
  return (s.systemDrive ?? DEFAULT_SYSTEM_DRIVE).toUpperCase();
}

/** Whether the typed folder is on the drive Windows runs from. */
function onSystemDrive(s: WizardState): boolean {
  return driveOfPath(s.settings.folder) === systemDriveOf(s);
}

/**
 * Puts the default folder in, unless the owner has already typed or picked one. Called when the drives list arrives
 * (or fails: pass null) and when the pack path is chosen. The system-drive tick starts off.
 */
export function applyDefaultFolder(s: WizardState, drives: DriveInfo[] | null, fallback: string): WizardState {
  const windows = drives?.find((d) => d.system);
  const systemDrive = windows ? windows.letter.toUpperCase() : s.systemDrive;
  const nonLocalDrives = drives ? nonLocalLetters(drives) : s.nonLocalDrives;
  if (s.folderTouched) {
    const same = systemDrive === s.systemDrive && sameLetters(nonLocalDrives, s.nonLocalDrives);
    return same ? s : { ...s, systemDrive, nonLocalDrives };
  }
  const { folder } = defaultFolder(drives, fallback);
  return { ...s, systemDrive, nonLocalDrives, settings: { ...s.settings, folder }, confirmSystemDrive: false };
}

/** The "Reset to default" link: forget what the owner typed and take the default again. */
export function resetFolder(s: WizardState, drives: DriveInfo[] | null, fallback: string): WizardState {
  return applyDefaultFolder({ ...s, folderTouched: false }, drives, fallback);
}

/**
 * The owner typed or picked a folder. The automatic default stops. The C: tick only means something for the
 * drive it was given for, so it is cleared when the path moves to another drive.
 */
export function setFolder(s: WizardState, folder: string): WizardState {
  const moved = driveOfPath(folder) !== driveOfPath(s.settings.folder);
  return { ...s, folderTouched: true, confirmSystemDrive: moved ? false : s.confirmSystemDrive, settings: { ...s.settings, folder } };
}

/* ---------------------------------------------------------------- the rules per step */

/**
 * The folders typed in the log box, one per line, blanks dropped. A folder typed twice is kept once (Windows paths
 * ignore case), with the first spelling and the first position, so Review can list each folder as its own row.
 */
function parseLogPaths(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const line of text.split(/\r?\n/)) {
    const path = line.trim();
    if (!path) continue;
    const key = path.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(path);
  }
  return out;
}

/**
 * The counters typed in the counters box, one per line, trimmed, blanks dropped. Unlike the log folders a repeat is kept:
 * the server refuses a duplicate, so it is a problem for the owner to fix, not something to hide.
 */
function parseCounters(text: string): string[] {
  return text.split(/\r?\n/).map((line) => line.trim()).filter((line) => line !== '');
}

function logPathsOk(paths: string[]): boolean {
  return paths.length <= LOG_PATHS_MAX && paths.every((p) => /^[A-Za-z]:[\\/]/.test(p));
}

/**
 * What stops a step from being left, or null. The rules are blackbox.ts's own (nameProblem, targetsProblem, the
 * settingsProblem ranges); this only decides which step each one belongs to.
 */
export function stepProblem(s: WizardState, id: StepId): StepProblem | null {
  const st = s.settings;
  switch (id) {
    case 'name': {
      const p = nameProblem(st.name);
      return p ? 'name-' + p : null;
    }
    case 'where':
      return s.where === null ? 'where-missing' : null;
    case 'record': {
      if (!INTERVALS.includes(st.intervalSeconds as 5 | 15 | 30 | 60)) return 'interval';
      // The log folders only matter while the logs catalogue is on; the box is hidden otherwise (B-07).
      if (st.record.logsCatalogue && !logPathsOk(parseLogPaths(s.logPathsText))) return 'logs';
      // The counters box only matters while its row is on.
      if (s.collectorRows.counters && extraCountersProblem(parseCounters(s.countersText))) return 'counters';
      return null;
    }
    case 'sql':
    case 'redis':
      return null;
    case 'watch': {
      if (s.watchMode === 'none') return null;
      const p = targetsProblem(st.targets);
      return p ? 'targets-' + p : null;
    }
    case 'keep': {
      const p = settingsProblem({ ...defaultSettings(st.name), keepDays: st.keepDays, maxGB: st.maxGB, minFreePercent: st.minFreePercent });
      return p === 'keep' || p === 'size' || p === 'free' ? p : null;
    }
    case 'folder': {
      const fault = folderFault(st.folder);
      if (fault === 'full') return 'folder-invalid';
      if (fault === 'slash') return 'folder-slash';
      if (fault === 'chars') return 'folder-chars';
      if (fault === 'system') return 'folder-unsafe';
      if (folderNotLocal(s)) return 'folder-not-local';
      if (onSystemDrive(s) && !s.confirmSystemDrive) return 'folder-system-drive';
      const p = settingsProblem({ ...defaultSettings(st.name), port: st.port, bind: st.bind });
      return p === 'port' || p === 'bind' ? p : null;
    }
    case 'review': {
      for (const earlier of steps(s)) {
        if (earlier === 'review') break;
        const found = stepProblem(s, earlier);
        if (found) return found;
      }
      // Belt and braces: the whole payload must pass the one check the server repeats.
      return settingsProblem(buildSettings(s));
    }
  }
}

/* ---------------------------------------------------------------- moving */

/** Remaps the current step and the furthest step reached onto a new step list, by step, not by position. */
function withList(s: WizardState, next: WizardState): WizardState {
  const before = steps(s);
  const after = steps(next);
  // The furthest step the owner reached that still exists. A step that went away (SQL) unlocks nothing after it.
  let maxSeen = 0;
  for (let i = Math.min(s.maxSeen, before.length - 1); i >= 0; i--) {
    const found = after.indexOf(before[i]);
    if (found >= 0) {
      maxSeen = found;
      break;
    }
  }
  let step = s.step;
  if (!after.includes(step)) step = after[Math.min(before.indexOf(step), after.length - 1)];
  // The step being shown is always unlocked, even when its own step went away and the next one took its place.
  return { ...next, step, maxSeen: Math.max(maxSeen, after.indexOf(step)) };
}

/** Moves one step on. Refused (the same state comes back) while the step has a problem. Honours returnTo. */
export function next(s: WizardState): WizardState {
  if (stepProblem(s, s.step)) return s;
  const list = steps(s);
  const at = list.indexOf(s.step);
  if (at < 0 || at >= list.length - 1) return s;
  if (s.returnTo === 'review') {
    const review = list.length - 1;
    return { ...s, step: 'review', returnTo: null, maxSeen: Math.max(s.maxSeen, review) };
  }
  return { ...s, step: list[at + 1], maxSeen: Math.max(s.maxSeen, at + 1) };
}

/** Moves one step back. Going back always works, and it ends a Change detour from Review. */
export function back(s: WizardState): WizardState {
  const list = steps(s);
  const at = list.indexOf(s.step);
  if (at <= 0) return s;
  return { ...s, step: list[at - 1], returnTo: null };
}

/**
 * Jumps to a step the rail has unlocked. Going backwards is always allowed; going forwards is refused while the step
 * being left has a problem. A jump from the rail ends a Change detour.
 */
export function goTo(s: WizardState, id: StepId): WizardState {
  if (!canVisit(s, id)) return s;
  const list = steps(s);
  if (list.indexOf(id) > list.indexOf(s.step) && stepProblem(s, s.step)) return s;
  return { ...s, step: id, returnTo: null };
}

/** A Review "Change" link: go to that step, and come straight back to Review after it. */
export function changeStep(s: WizardState, id: StepId): WizardState {
  if (!canVisit(s, id) || id === 'review') return s;
  return { ...s, step: id, returnTo: 'review' };
}

/* ---------------------------------------------------------------- choices that change the step list */

/**
 * Which Where cards can be chosen (B-11), and why none can. `pc` is true only while this PC can take a Blackbox and has
 * none yet. `none` is 'helper' when this build has no helper program beside the app (nothing can be placed or built),
 * 'other' when both are off for another reason (a Blackbox is already here and no pack can be built), else null.
 * `scanFailed` is true when this PC could be asked but the answer did not come, so "already installed" is not known.
 */
export function whereChoices(
  thisPc: Pick<ThisPc, 'available' | 'scanned' | 'installed'>,
  canPack: boolean
): { pc: boolean; pack: boolean; none: 'helper' | 'other' | null; scanFailed: boolean } {
  const pc = thisPc.available && !thisPc.installed;
  const none = pc || canPack ? null : !thisPc.available ? 'helper' : 'other';
  return { pc, pack: canPack, none, scanFailed: thisPc.available && !thisPc.scanned };
}

/** The owner chose where the recorder goes. The pack path always shows the SQL step, so the list can change. */
export function setWhere(s: WizardState, where: Where): WizardState {
  if (s.where === where) return withList(s, { ...s, where });
  // A real change (this PC <-> pack): the folder default and the C: tick belonged to the old answer. A Change detour
  // from Review ends, so Next walks on through Folder (which puts the new default in) before Review. While the folder
  // is still the automatic default, Review stays locked until Folder has been visited again.
  const moved = withList(s, { ...s, where, returnTo: null, confirmSystemDrive: false });
  if (s.folderTouched) return moved;
  const folderAt = steps(moved).indexOf('folder');
  return { ...moved, maxSeen: Math.min(moved.maxSeen, folderAt) };
}

/** SQL Server was seen (or not) in the this-pc services list. The SQL step appears or goes away. */
export function setSqlSeen(s: WizardState, sqlSeen: boolean): WizardState {
  return withList(s, { ...s, sqlSeen });
}

/** Redis or Memurai was seen (or not) in the this-pc services list. The Redis step appears or goes away. */
export function setRedisSeen(s: WizardState, redisSeen: boolean): WizardState {
  return withList(s, { ...s, redisSeen });
}

/* ---------------------------------------------------------------- the SQL and Redis choices */

/** The SQL starting point. Query text exists only in the deep profile, so any other choice drops the tick. */
export function setSqlProfile(s: WizardState, profile: SqlProfile): WizardState {
  return { ...s, sqlChoice: { profile, text: profile === 'deep' ? s.sqlChoice.text : false } };
}

/** The query-text tick. Refused (same state) unless the deep profile is chosen. */
export function setSqlText(s: WizardState, text: boolean): WizardState {
  return s.sqlChoice.profile === 'deep' ? { ...s, sqlChoice: { ...s.sqlChoice, text } } : s;
}

/** What each Redis row is under a starting point. Matches RedisSettings in internal/blackbox/redis.go. */
export function redisRowDefault(profile: RedisProfile, row: RedisRowId): boolean {
  if (row === 'slowArgs') return false;
  if (row === 'slowlog') return true;
  return profile === 'everything';
}

/** Whether a Redis row is on: the owner's override, else the starting point's answer. */
export function redisRowOn(c: RedisChoice, row: RedisRowId): boolean {
  const own = c.rows[row];
  return own === undefined ? redisRowDefault(c.profile, row) : own;
}

/** The starting point shown: custom as soon as any row differs from the chosen starting point. */
export function redisPresetNow(c: RedisChoice): RedisProfile | 'custom' {
  const rows: RedisRowId[] = ['slowlog', 'latency', 'clients', 'slowArgs'];
  return rows.some((r) => redisRowOn(c, r) !== redisRowDefault(c.profile, r)) ? 'custom' : c.profile;
}

/** Picks a Redis starting point. Row overrides go: the starting point answers every row again. */
export function setRedisProfile(s: WizardState, profile: RedisProfile): WizardState {
  return { ...s, redisChoice: { profile, rows: {} } };
}

/** Switches one Redis row. A value equal to the starting point's answer is not kept as an override. */
export function setRedisRow(s: WizardState, row: RedisRowId, on: boolean): WizardState {
  const rows = { ...s.redisChoice.rows };
  if (on === redisRowDefault(s.redisChoice.profile, row)) delete rows[row];
  else rows[row] = on;
  return { ...s, redisChoice: { ...s.redisChoice, rows } };
}

/* ---------------------------------------------------------------- presets and defaults */

/** The preset the three record switches add up to (B-03). Anything else is Custom. */
export function presetOf(record: Settings['record'], rows: CollectorRows): PresetId {
  if (record.processes && record.logsCatalogue && !record.copyLogsOnSnapshot && rows.facts && !rows.counters && rows.paths) return 'recommended';
  if (!record.processes && !record.logsCatalogue && !record.copyLogsOnSnapshot && !rows.facts && !rows.counters && !rows.paths) return 'light';
  return 'custom';
}

/** Sets the three record switches and the three collector rows to a preset. Any single change afterwards moves presetOf to custom by itself. */
export function applyPreset(s: WizardState, p: Exclude<PresetId, 'custom'>): WizardState {
  const on = p === 'recommended';
  return {
    ...s,
    settings: { ...s.settings, record: { processes: on, logsCatalogue: on, copyLogsOnSnapshot: false } },
    collectorRows: { facts: on, counters: false, paths: on }
  };
}

/**
 * "Use defaults, go to review": the Record, SQL, Redis and Watch answers go back to the defaults, then Review. Refused until the name and the place are answered. Every step is then reached.
 * The Keep answers (days, size, free space) are not part of the shortcut unless the shortcut is taken from the Keep
 * step itself (resetKeep): there the owner asks for the defaults of that very step.
 */
export function useDefaultsToReview(s: WizardState, resetKeep = false): WizardState {
  if (stepProblem(s, 'name') || stepProblem(s, 'where')) return s;
  const d = defaultSettings(s.settings.name);
  const settings: Settings = {
    ...s.settings,
    record: { ...d.record },
    intervalSeconds: d.intervalSeconds,
    logPaths: [...d.logPaths],
    targets: []
  };
  if (resetKeep) {
    settings.keepDays = d.keepDays;
    settings.maxGB = d.maxGB;
    settings.minFreePercent = d.minFreePercent;
  }
  const review = steps(s).length - 1;
  return {
    ...s,
    settings,
    logPathsText: d.logPaths.join('\n'),
    collectorRows: { facts: true, counters: false, paths: true },
    countersText: COUNTERS_PREFILL,
    watchMode: 'none',
    sqlChoice: { profile: 'standard', text: false },
    redisChoice: { profile: 'standard', rows: {} },
    step: 'review',
    returnTo: null,
    maxSeen: Math.max(s.maxSeen, review)
  };
}

/* ---------------------------------------------------------------- start and payload */

/** A fresh wizard: the first step, the recommended answers, the default folder (the drives list improves it later). */
export function initialState(): WizardState {
  const d = defaultSettings();
  return {
    step: 'name',
    maxSeen: 0,
    returnTo: null,
    where: null,
    settings: d,
    logPathsText: d.logPaths.join('\n'),
    collectorRows: { facts: true, counters: false, paths: true },
    countersText: COUNTERS_PREFILL,
    watchMode: 'none',
    folderTouched: false,
    confirmSystemDrive: false,
    sqlSeen: false,
    redisSeen: false,
    sqlChoice: { profile: 'standard', text: false },
    redisChoice: { profile: 'standard', rows: {} }
  };
}

/**
 * The payload for validate, install and pack (C-5). It starts from defaultSettings() and writes only what the steps
 * own, key by key: nothing the server does not know can slip in (it refuses unknown keys). probe is always
 * { enabled: false } because the SQL probe is not part of this release. Targets are sent only when the owner chose
 * to watch some, and only as kind and name. The state is never changed and the result shares nothing with it.
 */
export function buildSettings(s: WizardState): Settings {
  const out = defaultSettings(s.settings.name);
  const st = s.settings;
  out.record = {
    processes: st.record.processes,
    logsCatalogue: st.record.logsCatalogue,
    copyLogsOnSnapshot: st.record.copyLogsOnSnapshot
  };
  out.intervalSeconds = st.intervalSeconds;
  // With the logs catalogue off the box is hidden, so a bad leftover must not block the install: the default stays.
  const typed = parseLogPaths(s.logPathsText);
  if (st.record.logsCatalogue || logPathsOk(typed)) out.logPaths = typed;
  out.targets = s.watchMode === 'some' ? st.targets.map((t): TargetRef => ({ kind: t.kind, name: t.name })) : [];
  out.keepDays = st.keepDays;
  out.maxGB = st.maxGB;
  out.minFreePercent = st.minFreePercent;
  out.folder = st.folder.trim();
  out.port = st.port;
  out.bind = st.bind.trim();
  out.probe = { enabled: false };
  // Recommended writes nothing here: an absent collector id is on, and no counters block means nothing extra.
  const rows = s.collectorRows;
  const off: Record<string, { on: boolean }> = {};
  if (!rows.facts) off[COLLECTOR_IDS.facts] = { on: false };
  if (!rows.paths) off[COLLECTOR_IDS.paths] = { on: false };
  if (Object.keys(off).length > 0) out.collectors = off;
  if (rows.counters) out.counters = { extra: parseCounters(s.countersText) };
  // The SQL and Redis blocks go in only when their step applies, and a plain Standard choice writes nothing, so a
  // plain install stays plain. Query text exists only in the deep profile.
  if (steps(s).includes('sql')) {
    const { profile, text } = s.sqlChoice;
    if (profile !== 'standard') {
      out.sql = { profile };
      if (profile === 'deep' && text) {
        out.sql.queryText = true;
        out.sql.queryTextMaxChars = DEFAULT_QUERY_TEXT_SIZE;
      }
    }
  }
  if (steps(s).includes('redis')) {
    const c = s.redisChoice;
    const block: NonNullable<Settings['redis']> = {};
    if (c.profile !== 'standard') block.profile = c.profile;
    if (c.rows.slowlog !== undefined) block.slowlog = c.rows.slowlog;
    if (c.rows.latency !== undefined) block.latency = c.rows.latency;
    if (c.rows.clients !== undefined) block.clients = c.rows.clients;
    if (c.rows.slowArgs) block.slowArgs = true;
    if (Object.keys(block).length > 0) out.redis = block;
  }
  return out;
}
