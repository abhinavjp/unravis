import { UNLOCK_MESSAGE, blankHint, edgeSpaceNote, strengthText, unlockMessage as unlockText } from './copy/masterKeyCopy';
import type { ResetFailureKind, SetupFailureKind, UnlockFailure } from './copy/masterKeyCopy';

/**
 * The master key state model and the rules the screens follow. Pure: no DOM, no fetch, and nothing in here keeps a key.
 * The words live in copy/masterKeyCopy.ts; the five server calls live in masterKeyApi.ts.
 */

/** What the server says about the key: none set yet, set and locked, set and open, or a key file that no passphrase can open (damaged). */
export type LockState = 'none' | 'locked' | 'unlocked' | 'damaged';
export const LOCK_STATES: readonly LockState[] = ['none', 'locked', 'unlocked', 'damaged'];

/** The states in which the cases cannot be read, so the start screen shows nothing from them and the unlock screen takes over. */
export const closesCases = (state: LockState | 'unknown' | null): boolean => state === 'locked' || state === 'damaged';

/** `dpapiCases` is how many cases are still protected the older way, so they could be re-wrapped. */
export interface KeyStatus { state: LockState; dpapiCases: number; freedArchives?: number; reset?: ResetImpact }

/**
 * What starting over with a new master key would do (present only while the key file is damaged): cases sealed with the old master
 * key stay unreadable, cases with their own key are left alone, and archived cases whose working copy is freed can only be opened
 * with the old key (M12). Counts only.
 */
export interface ResetImpact { masterKeyCases: number; ownKeyCases: number; archiveOnly: number }

const whole = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0);

/** Reads the reset block of a status answer. Null when it is absent or not an object. */
export function parseResetImpact(raw: unknown): ResetImpact | null {
  if (!raw || typeof raw !== 'object') return null;
  const body = raw as { masterKeyCases?: unknown; ownKeyCases?: unknown; archiveOnly?: unknown };
  return { masterKeyCases: whole(body.masterKeyCases), ownKeyCases: whole(body.ownKeyCases), archiveOnly: whole(body.archiveOnly) };
}

export const MIN_PASSPHRASE_CHARS = 12;
export const MAX_PASSPHRASE_BYTES = 1024;

export type { UnlockFailure };
export { UNLOCK_MESSAGE };

export const isLockState = (value: unknown): value is LockState => typeof value === 'string' && (LOCK_STATES as readonly string[]).includes(value);

/** Reads a status answer. Null when it is not one, so a strange answer is never taken as "unlocked". */
export function parseStatus(raw: unknown): KeyStatus | null {
  if (!raw || typeof raw !== 'object') return null;
  const body = raw as { state?: unknown; dpapiCases?: unknown; freedArchives?: unknown; reset?: unknown };
  if (!isLockState(body.state)) return null;
  const count = whole(body.dpapiCases);
  // M12's N: the server sends it only while a start-over is offered; kept only when more than 0.
  const freed = whole(body.freedArchives);
  const reset = parseResetImpact(body.reset);
  return { state: body.state, dpapiCases: count, ...(freed > 0 ? { freedArchives: freed } : {}), ...(reset ? { reset } : {}) };
}

/** Characters as a person counts them (one emoji is one), not UTF-16 units. */
export const charCount = (text: string): number => [...text].length;
export const byteCount = (text: string): number => new TextEncoder().encode(text).length;

/**
 * Characters that show nothing. The same list as the server (internal/masterkey Visible): spaces of every width, controls, zero-width and
 * direction marks, surrogates, private-use and unassigned code points, variation selectors, and the blank filler letters.
 */
const BLANK = /[\p{Z}\p{Cc}\p{Cf}\p{Cs}\p{Co}\p{Cn}᠋-᠎︀-️\u{e0100}-\u{e01ef}͏ᅟᅠ឴឵⠀ㅤﾠ]/u;
/** A key needs this many characters that show something, as well as 12 characters in all (the server's MinVisibleLen). */
export const MIN_VISIBLE_CHARS = 8;
/** A lone surrogate or U+FFFD: what a broken paste leaves, and what the server cannot store without two keys colliding. */
const INVALID_TEXT = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]|�/;

/** The key as the server reads it: the composed (NFC) form, so a letter typed as two code points counts once. */
const composed = (text: string): string => text.normalize('NFC');

/** How many characters of the key can be seen. A key of blanks or invisible characters counts as 0, so it cannot pass as a key. */
export function visibleCount(text: string): number {
  let seen = 0;
  for (const ch of composed(text)) if (!BLANK.test(ch)) seen++;
  return seen;
}

/** Whether the two entries are the same key. They are compared composed (NFC), as the server does, so a letter typed as two code points matches the one-piece letter. */
export const keysMatch = (pass: string, confirm: string): boolean => composed(pass) === composed(confirm);

/** True when the key starts or ends with a space (or another blank). The key is kept exactly as typed; this only lets the screen say so. */
export const hasEdgeSpace = (pass: string): boolean => pass.length > 0 && (BLANK.test([...pass][0]) || BLANK.test([...pass].at(-1) as string));

export type SetupProblem = 'invalid-characters' | 'too-short' | 'blank' | 'too-long' | 'mismatch' | 'acknowledgement-required';

/** The first thing that keeps the set-up button off, or null when the key can be sent. Order: length, size, match, tick. */
export function setupProblem(pass: string, confirm: string, acknowledged: boolean): SetupProblem | null {
  if (INVALID_TEXT.test(pass)) return 'invalid-characters';
  if (charCount(composed(pass)) < MIN_PASSPHRASE_CHARS) return 'too-short';
  if (visibleCount(pass) < MIN_VISIBLE_CHARS) return 'blank';
  if (byteCount(composed(pass)) > MAX_PASSPHRASE_BYTES) return 'too-long';
  if (!keysMatch(pass, confirm)) return 'mismatch';
  if (!acknowledged) return 'acknowledgement-required';
  return null;
}

/** The set-up button is on only when the key is at least 12 characters, both entries match and the box is ticked. */
export function canSubmitSetup(pass: string, confirm: string, acknowledged: boolean): boolean {
  return setupProblem(pass, confirm, acknowledged) === null;
}

/** How many kinds of character the key uses: lower case, upper case, digits, anything else (0 to 4). */
export function characterKinds(pass: string): number {
  let kinds = 0;
  if (/\p{Ll}/u.test(pass)) kinds++;
  if (/\p{Lu}/u.test(pass)) kinds++;
  if (/\p{Nd}/u.test(pass)) kinds++;
  if (/[^\p{L}\p{Nd}]/u.test(pass)) kinds++;
  return kinds;
}

/** One line about the key being typed. Length and variety only; it never says the key is safe. */
export function strengthHint(pass: string): string {
  const length = charCount(composed(pass));
  if (length >= MIN_PASSPHRASE_CHARS && visibleCount(pass) < MIN_VISIBLE_CHARS) return blankHint(MIN_VISIBLE_CHARS);
  const line = strengthText(length, characterKinds(pass), MIN_PASSPHRASE_CHARS);
  return hasEdgeSpace(pass) ? line + ' ' + edgeSpaceNote() : line;
}

/** The message under the unlock field. "Not the master key" only for a refused key; other failures say what happened. */
export function unlockMessage(failure: UnlockFailure): string {
  return unlockText(failure);
}

interface FailureShape { status?: unknown; reason?: unknown; retryAfterSeconds?: unknown }

const shapeOf = (error: unknown): FailureShape => (error && typeof error === 'object' ? error as FailureShape : {});
const waitOf = (e: FailureShape): number | undefined => (typeof e.retryAfterSeconds === 'number' && e.retryAfterSeconds > 0 ? e.retryAfterSeconds : undefined);

/** Turns what a failed call carried (status and reason, never a key) into the small set the screens act on. */
export function unlockFailureOf(error: unknown): UnlockFailure {
  const e = shapeOf(error);
  const status = typeof e.status === 'number' ? e.status : -1;
  if (status === 429 || e.reason === 'throttled') return { kind: 'throttled', retryAfterSeconds: waitOf(e) ?? 5 };
  if (status === 0 || e.reason === 'unreachable') return { kind: 'network' };
  if (e.reason === 'key-file-damaged') return { kind: 'damaged' };
  if (status === 401 && e.reason === 'wrong-key') return { kind: 'wrong-key' };
  if (status === 423) return { kind: 'locked' };
  if (status === 409) return { kind: e.reason === 'not-set' ? 'not-set' : 'conflict' };
  if (status >= 500) return { kind: 'server' };
  return { kind: 'other' };
}

export interface SetupFailure { kind: SetupFailureKind; retryAfterSeconds?: number }

/** The same for a refused set-up, keeping the server's reason so the screen can say which rule was missed. */
export function setupFailureOf(error: unknown): SetupFailure {
  const e = shapeOf(error);
  const status = typeof e.status === 'number' ? e.status : -1;
  if (status === 429 || e.reason === 'throttled') return { kind: 'throttled', retryAfterSeconds: waitOf(e) ?? 5 };
  if (status === 0 || e.reason === 'unreachable') return { kind: 'network' };
  switch (e.reason) {
    case 'too-short': case 'too-long': case 'mismatch': case 'acknowledgement-required': case 'already-set': return { kind: e.reason };
    case 'blank': case 'invalid-characters': return { kind: e.reason };
    case 'key-file-damaged': return { kind: 'damaged' };
    case 'setup-incomplete': return { kind: 'incomplete' };
    default: return { kind: 'other' };
  }
}

/** The start-over button is on only once the box is ticked: that is the person saying they know the old cases stay unreadable. */
export const canReset = (acknowledged: boolean): boolean => acknowledged === true;

/** Turns what a failed start-over carried (status and reason) into the small set the damaged screen acts on. */
export function resetFailureOf(error: unknown): { kind: ResetFailureKind } {
  const e = shapeOf(error);
  const status = typeof e.status === 'number' ? e.status : -1;
  if (status === 0 || e.reason === 'unreachable') return { kind: 'network' };
  switch (e.reason) {
    case 'acknowledgement-required': return { kind: 'acknowledgement-required' };
    case 'not-damaged': return { kind: 'not-damaged' };
    case 'reset-failed': return { kind: 'failed' };
    case 'master-key-unavailable': return { kind: 'unavailable' };
    default: return { kind: 'other' };
  }
}

/** Whether Home offers to protect older cases: only while the key is open and some cases are still protected the older way. */
export const offersRewrap = (state: LockState | 'unknown' | null, dpapiCases: number): boolean => state === 'unlocked' && Number.isFinite(dpapiCases) && dpapiCases > 0;

/** Whether the start screen offers the set-up link: only while no key exists. */
export const offersSetup = (state: LockState | 'unknown'): boolean => state === 'none';

/** Whether the header offers Lock: only while a key exists and is open. */
export const offersLock = (state: LockState | 'unknown'): boolean => state === 'unlocked';
