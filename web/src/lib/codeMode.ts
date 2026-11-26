import { CODE_TEXT, NO_LIMIT_PHRASE } from './copy/codeModeCopy';
import { ago, type McpList } from './mcpGrants';

/**
 * Code mode settings: the pure model behind the popup and the header badge (server: internal/codeapi, GET and PUT /api/code/settings).
 *
 * What the server says is the only truth. The page shows what the GET returned and never invents a field:
 *   enabled, allowDegraded, keepResults, allowIdentifiers (read-only), cliOpenUntil (RFC 3339 or empty), damaged,
 *   and `cliUnlimited` when the server has it. An older server has no such key: then the page treats it as false, never sends it,
 *   and keeps the "no time limit" choice off (`hasUnlimited` is false).
 * The countdown is a display only. The gate that lets the command line run is on the server and uses the server's clock.
 */

export interface CodeSettings {
  enabled: boolean;
  allowDegraded: boolean;
  keepResults: boolean;
  allowIdentifiers: boolean;
  /** When the timed command-line window ends, in ms since 1970; 0 while no timed window is open. */
  cliOpenUntil: number;
  damaged: boolean;
  cliUnlimited: boolean;
  /** The GET carried a `cliUnlimited` key, so this server can take it back in a PUT. */
  hasUnlimited: boolean;
}

/** Reads the answer of GET or PUT /api/code/settings. Anything that is not an object with a true or false `enabled` is unreadable (null). */
export function parseSettings(raw: unknown): CodeSettings | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  if (typeof r.enabled !== 'boolean') return null;
  const until = typeof r.cliOpenUntil === 'string' && r.cliOpenUntil ? Date.parse(r.cliOpenUntil) : 0;
  return {
    enabled: r.enabled,
    allowDegraded: r.allowDegraded === true,
    keepResults: r.keepResults === true,
    allowIdentifiers: r.allowIdentifiers === true,
    cliOpenUntil: Number.isFinite(until) ? until : 0,
    damaged: r.damaged === true,
    cliUnlimited: r.cliUnlimited === true,
    hasUnlimited: typeof r.cliUnlimited === 'boolean'
  };
}

/** The gap between the server clock and this browser's clock. The Date header has whole seconds, so it can be up to a second behind. */
export function clockOffsetMs(dateHeader: string | null | undefined, receivedAtMs: number): number {
  if (!dateHeader) return 0;
  const server = Date.parse(dateHeader);
  return Number.isFinite(server) ? server - receivedAtMs : 0;
}

export type CliKind = 'off' | 'shut' | 'open' | 'unlimited';
export interface CliState { kind: CliKind; leftMs: number }

/** What the command line is doing now, by the server's answer and the corrected clock. Off whenever code mode is off. */
export function cliState(s: CodeSettings | null, nowMs: number, offsetMs: number): CliState {
  if (!s || !s.enabled) return { kind: 'off', leftMs: 0 };
  if (s.cliUnlimited) return { kind: 'unlimited', leftMs: 0 };
  const left = s.cliOpenUntil - (nowMs + offsetMs);
  return left > 0 ? { kind: 'open', leftMs: left } : { kind: 'shut', leftMs: 0 };
}

/** mm:ss, rounded up to the next second, so the last second reads 00:01 and never 00:00 while open. */
export function fmtClock(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  return String(Math.floor(s / 60)).padStart(2, '0') + ':' + String(s % 60).padStart(2, '0');
}

/** The local time a window ends, for a sentence ("until 14:32"). */
export function clockTime(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

/* ---------------------------------------------------------------- the badge */

export type BadgeTone = 'on' | 'warn' | 'danger';
export interface BadgeModel {
  tone: BadgeTone;
  icon: 'term' | 'alert';
  /** The long label, shown on desktop: `full` then ` · ` then `detail` (the time is set in the mono face). */
  full: string;
  detail: string;
  /** True when `detail` is a countdown. */
  isTime: boolean;
  /** The short label on a phone. */
  short: string;
  /** The accessible name: the full state in words. */
  aria: string;
}

/** The header badge, or null when nothing is to be shown: code mode off, not read yet, or not available in this run. */
export function badgeModel(s: CodeSettings | null, nowMs: number, offsetMs: number): BadgeModel | null {
  if (!s) return null;
  const T = CODE_TEXT;
  if (s.damaged) return { tone: 'warn', icon: 'alert', full: T.badgeOff, detail: T.badgeDamaged, isTime: false, short: T.badgeShortOff, aria: T.badgeAriaDamaged };
  if (!s.enabled) return null;
  const cli = cliState(s, nowMs, offsetMs);
  if (cli.kind === 'unlimited') return { tone: 'danger', icon: 'alert', full: T.badgeOn, detail: T.badgeNoLimit, isTime: false, short: T.badgeShortNoLimit, aria: T.badgeAriaNoLimit };
  if (cli.kind === 'open') {
    const clock = fmtClock(cli.leftMs);
    return { tone: cli.leftMs < 60000 ? 'warn' : 'on', icon: 'term', full: T.badgeOn, detail: T.badgeLeft(clock), isTime: true, short: clock, aria: T.badgeAriaOpen(Math.ceil(cli.leftMs / 60000)) };
  }
  return { tone: 'on', icon: 'term', full: T.badgeOn, detail: T.badgeShut, isTime: false, short: T.badgeShortOn, aria: T.badgeAriaShut };
}

/* ---------------------------------------------------------------- the draft */

export type CliChoice = 'keep' | 'shut' | 'timed' | 'unlimited';

/** What the person is choosing in the popup. Nothing here is saved until the Review step is confirmed. */
export interface Draft {
  enabled: boolean;
  keep: boolean;
  deg: boolean;
  mode: CliChoice;
  /** The minutes box as typed. */
  minsRaw: string;
  typed: string;
}

export const MIN_MINUTES = 1;
export const MAX_MINUTES = 60;
export const DEFAULT_MINUTES = 15;
export const MINUTE_CHIPS: readonly number[] = [5, 15, 30, 60];

/** A fresh draft from what is true now. An open window (timed or no limit) starts as "leave it as it is". */
export function newDraft(s: CodeSettings | null, cli: CliState): Draft {
  return {
    enabled: s?.enabled === true,
    keep: s?.keepResults === true,
    deg: s?.allowDegraded === true,
    mode: s?.enabled && (cli.kind === 'open' || cli.kind === 'unlimited') ? 'keep' : 'shut',
    minsRaw: String(DEFAULT_MINUTES),
    typed: ''
  };
}

/** A whole number of minutes, or NaN for anything else (empty, a sign, a decimal, letters). */
export function parseMinutes(raw: string): number {
  const t = raw.trim();
  return /^\d{1,9}$/.test(t) ? Number(t) : NaN;
}

/** The sentence under the minutes box. Empty when the number is fine or 0 (0 means shut). */
export function minutesError(raw: string): string {
  const n = parseMinutes(raw);
  if (Number.isNaN(n)) return CODE_TEXT.minutesBad;
  if (n > MAX_MINUTES) return CODE_TEXT.minutesTooBig;
  return '';
}

export const phraseOk = (typed: string): boolean => typed.trim().toLowerCase() === NO_LIMIT_PHRASE;

/** Step numbers (0 based): 0 What it is, 1 Switch on, 2 Assistants, 3 Safety, 4 Command line, 5 Review, 6 Result. */
export const STEP_REVIEW = 5;
export const STEP_RESULT = 6;
export const stepSkipped = (d: Draft, i: number): boolean => !d.enabled && i >= 2 && i <= 4;
export const nextStep = (d: Draft, i: number): number => (i === 1 && !d.enabled ? STEP_REVIEW : i + 1);
export const backStep = (d: Draft, i: number): number => (i === STEP_REVIEW && !d.enabled ? 1 : i - 1);

/** Whether Next is on at this step. Only the command-line step ever says no. */
export function canNext(d: Draft, step: number, hasUnlimited: boolean): boolean {
  if (step !== 4) return true;
  if (d.mode === 'timed') {
    const n = parseMinutes(d.minsRaw);
    return n >= MIN_MINUTES && n <= MAX_MINUTES;
  }
  if (d.mode === 'unlimited') return hasUnlimited && phraseOk(d.typed);
  return true;
}

/* ---------------------------------------------------------------- the save */

/** The PUT body. Only keys the server knows are ever sent: the server refuses an unknown key. */
export interface SettingsPatch {
  enabled?: boolean;
  allowDegraded?: boolean;
  keepResults?: boolean;
  cliMinutes?: number;
  cliUnlimited?: boolean;
}

export class PatchError extends Error {}

/**
 * The one save of the last step. Switching code mode off sends no window key: the server closes the window itself.
 * `cliUnlimited` goes out only when the GET had that key, and never together with minutes above 0.
 */
export function buildPatch(s: CodeSettings, d: Draft): SettingsPatch {
  const patch: SettingsPatch = { enabled: d.enabled };
  if (!d.enabled) return patch;
  patch.allowDegraded = d.deg;
  patch.keepResults = d.keep;
  if (d.mode === 'keep') return patch;
  if (d.mode === 'shut') {
    patch.cliMinutes = 0;
    if (s.hasUnlimited) patch.cliUnlimited = false;
  } else if (d.mode === 'timed') {
    const n = parseMinutes(d.minsRaw);
    if (n === 0) {
      patch.cliMinutes = 0;
    } else {
      if (!(n >= MIN_MINUTES && n <= MAX_MINUTES)) throw new PatchError(minutesError(d.minsRaw));
      patch.cliMinutes = n;
    }
  } else {
    if (!s.hasUnlimited || !phraseOk(d.typed)) throw new PatchError(CODE_TEXT.noLimitSoon);
    patch.cliUnlimited = true;
  }
  return patch;
}

export type QuickAction = 'closecli' | 'tofifteen' | 'off';

/** The save behind a button on the Result step. */
export function quickPatch(s: CodeSettings, action: QuickAction): SettingsPatch {
  if (action === 'off') return { enabled: false };
  if (action === 'tofifteen') return { cliMinutes: DEFAULT_MINUTES };
  return s.hasUnlimited ? { cliMinutes: 0, cliUnlimited: false } : { cliMinutes: 0 };
}

export type Applied = 'ok' | 'notApplied' | 'noLimitStillOn';

/**
 * Checks the server's reply against what was asked, so a save the server did not apply is never shown as done.
 * Only what the patch asked for is checked.
 */
export function checkApplied(patch: SettingsPatch, reply: CodeSettings, nowMs: number, offsetMs: number): Applied {
  if (patch.enabled !== undefined && reply.enabled !== patch.enabled) return 'notApplied';
  if (!reply.enabled) return 'ok';
  if (patch.allowDegraded !== undefined && reply.allowDegraded !== patch.allowDegraded) return 'notApplied';
  if (patch.keepResults !== undefined && reply.keepResults !== patch.keepResults) return 'notApplied';
  const cli = cliState(reply, nowMs, offsetMs);
  if (patch.cliUnlimited === true) return cli.kind === 'unlimited' ? 'ok' : 'notApplied';
  if (patch.cliMinutes === 0) return cli.kind === 'unlimited' ? 'noLimitStillOn' : cli.kind === 'open' ? 'notApplied' : 'ok';
  if (patch.cliMinutes !== undefined && patch.cliMinutes > 0) return cli.kind === 'open' ? 'ok' : cli.kind === 'unlimited' ? 'noLimitStillOn' : 'notApplied';
  return 'ok';
}

/* ---------------------------------------------------------------- the assistants (read from AI access) */

export type AssistantTone = 'allowed' | 'notAllowed' | 'waiting' | 'gone';
export interface AssistantRow { id: string; name: string; tone: AssistantTone; meta: string }

/**
 * The connected assistants and whether each may run scripts. This is only a reading of /api/mcp/profiles: the `code` flag is set when a
 * grant is approved, so code mode's own settings never change it. Refused requests are left out.
 */
export function assistantRows(list: McpList): AssistantRow[] {
  const rows: AssistantRow[] = [];
  for (const p of list.profiles) {
    if (p.state === 'denied') continue;
    const tone: AssistantTone = p.state === 'pending' ? 'waiting' : p.state === 'active' && !p.expired ? (p.code ? 'allowed' : 'notAllowed') : 'gone';
    const used = p.lastCallAt ? CODE_TEXT.s2Last + ' ' + ago(list.now, p.lastCallAt) : CODE_TEXT.s2Never;
    rows.push({ id: p.id, name: p.hostName || p.id, tone, meta: [p.exe, used].filter(Boolean).join(' · ') });
  }
  return rows;
}
