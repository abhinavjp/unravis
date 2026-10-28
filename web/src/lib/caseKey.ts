import { MAX_PASSPHRASE_BYTES, MIN_PASSPHRASE_CHARS, byteCount, charCount, keysMatch, setupProblem, strengthHint } from './masterKey';

/**
 * Pure rules of the optional per-case key (decisions A12, A13). No DOM, no fetch. The server judges every key; these rules only keep a
 * button off and pick a sentence. Nothing here stores, logs or returns a key.
 */

export interface CaseKeyStatus { ownKey: boolean; locked: boolean; ending?: string }

const ENDING = /^[0-9a-f]{4}$/i;

/** Reads the status body. Anything that is not an object with a boolean `ownKey` is refused. The ending is kept only when it is four hex digits. */
export function parseCaseKeyStatus(raw: unknown): CaseKeyStatus | null {
  if (!raw || typeof raw !== 'object') return null;
  const b = raw as { ownKey?: unknown; locked?: unknown; ending?: unknown };
  if (typeof b.ownKey !== 'boolean') return null;
  const ending = typeof b.ending === 'string' && ENDING.test(b.ending) ? b.ending.toLowerCase() : undefined;
  return { ownKey: b.ownKey, locked: b.ownKey && b.locked === true, ...(b.ownKey && ending ? { ending } : {}) };
}

/** The sentence under a key field uses the same rules as the PC key: 12 characters, 8 that show, matching entries. */
export type NewKeyProblem = 'too-short' | 'blank' | 'too-long' | 'invalid-characters' | 'mismatch';

/** The first thing wrong with a new case key and its repeat, or null when it can be sent. The PC key's tick box does not exist here. */
export function newKeyProblem(pass: string, confirm: string): NewKeyProblem | null {
  const p = setupProblem(pass, confirm, true);
  return p === 'acknowledgement-required' ? null : p;
}

/** The hint line while typing a new case key: length and mix only. */
export const newKeyHint = (pass: string): string => strengthHint(pass);

/** Whether the repeat is shown as not matching: only once something was typed in it. */
export const repeatMismatch = (pass: string, confirm: string): boolean => confirm.length > 0 && !keysMatch(pass, confirm);

/** The "Key ending abcd" sticker text, or an empty string when the ending is unknown. */
export function keyEndingLabel(ending: string | undefined): string {
  return ending && ENDING.test(ending) ? 'Key ending ' + ending.toLowerCase() : '';
}

/** What a failed key call carried (never a key), reduced to the small set the screens act on. */
export type CaseKeyFailureKind =
  | 'wrong-key' | 'throttled' | 'network' | 'no-case-key' | 'has-case-key' | 'in-use' | 'erasing' | 'gone'
  | 'too-short' | 'blank' | 'too-long' | 'invalid-characters' | 'mismatch' | 'unavailable' | 'app-locked' | 'server' | 'other';

export interface CaseKeyFailure { kind: CaseKeyFailureKind; retryAfterSeconds?: number }

interface Shape { status?: unknown; reason?: unknown; retryAfterSeconds?: unknown }

export function caseKeyFailureOf(error: unknown): CaseKeyFailure {
  const e: Shape = error && typeof error === 'object' ? error as Shape : {};
  const status = typeof e.status === 'number' ? e.status : -1;
  const wait = typeof e.retryAfterSeconds === 'number' && e.retryAfterSeconds > 0 ? e.retryAfterSeconds : undefined;
  if (status === 429 || e.reason === 'throttled') return { kind: 'throttled', retryAfterSeconds: wait ?? 5 };
  if (status === 0 || e.reason === 'unreachable') return { kind: 'network' };
  switch (e.reason) {
    case 'wrong-case-key': return { kind: 'wrong-key' };
    case 'no-case-key': return { kind: 'no-case-key' };
    case 'has-case-key': return { kind: 'has-case-key' };
    case 'case-in-use': return { kind: 'in-use' };
    case 'case-erasing': return { kind: 'erasing' };
    case 'not-found': case 'case-not-found': return { kind: 'gone' };
    case 'too-short': case 'blank': case 'too-long': case 'invalid-characters': case 'mismatch': return { kind: e.reason };
    case 'case-key-unavailable': return { kind: 'unavailable' };
    default: break;
  }
  if (status === 404) return { kind: 'gone' };
  // A 423 that carries no case-key reason is the master gate: the app lock screen has already been raised.
  if (status === 423) return { kind: 'app-locked' };
  if (status >= 500) return { kind: 'server' };
  return { kind: 'other' };
}

/** Whether the failure means the typed key was judged and refused (the field is cleared and focus goes back to it). */
export const isWrongKey = (f: CaseKeyFailure): boolean => f.kind === 'wrong-key';

/** Which of the two Keys page forms is chosen once the case has a key. */
export type KeyChange = 'change' | 'remove';

/** What the Keys page can send: the current key is always needed once a key exists. */
export function changeReady(mode: KeyChange, current: string, next: string, confirm: string): boolean {
  if (current.length === 0) return false;
  return mode === 'remove' ? true : newKeyProblem(next, confirm) === null;
}

export { MAX_PASSPHRASE_BYTES, MIN_PASSPHRASE_CHARS, byteCount, charCount };
