import { CODE_APP_LOCKED, CODE_ARCHIVED, CODE_CHECK_FAILED, CODE_NOT_FOUND } from './copy/enterCopy';
import { UNREACHABLE_MESSAGE, isUnreachableError } from './copy/unreachableCopy';

/**
 * Entering a case by its code. Pure: no DOM; the one request is passed in. The 26-character case id is the code.
 * Every code the server says is wrong (too short, too long, wrong characters, unknown, erased: 404 or 400) reads as the same
 * sentence, so the screen never reveals which case ids exist on this computer.
 * Three things are not "wrong code" and say so: a locked Unravis (423: it cannot answer yet), any other failure to check (a server error,
 * a time limit, no answer: "could not be checked", W-17), and (UI-R5-03) an archived case, which the server answers with 409 case-archived.
 */

export { CODE_APP_LOCKED, CODE_ARCHIVED, CODE_CHECK_FAILED, CODE_NOT_FOUND };

/** The case id shape (the same as cases.ts CASE_ID_PATTERN, kept here so this file stays free of the request code). */
export const CODE_PATTERN = /^[a-z2-7]{26}$/;
/** A pasted code is never longer than this before cleaning; anything longer is not a code and is not scanned further. */
const MAX_TYPED = 256;
/** Spaces, dashes of every kind, and every invisible format character a copy and paste can bring along (zero-width, soft hyphen, BOM, direction marks). */
const IGNORED = /[\s\-‐-―−\p{Cf}]/gu;

/** The code with spaces, dashes and case removed, or null when it is not a 26-character case code. */
export function normaliseCode(input: string): string | null {
  if (typeof input !== 'string' || input.length > MAX_TYPED) return null;
  // Full-width and other look-alike letters and digits are folded to plain ones first (NFKC); the code alphabet is plain ASCII, so a wrong code cannot start to match.
  const code = input.normalize('NFKC').replace(IGNORED, '').toLowerCase();
  return CODE_PATTERN.test(code) ? code : null;
}

/** True when the field holds nothing a person typed (blank or only spaces and dashes): the button waits, and no sentence is shown. */
export function codeIsEmpty(input: string): boolean {
  return typeof input !== 'string' || input.replace(IGNORED, '') === '';
}

/** What is wrong with the typed text before anything is asked of the server: nothing typed, a character no code uses, too short, too long, or nothing (ok). */
export type CodeFormat = 'ok' | 'empty' | 'chars' | 'short' | 'long';

export function codeFormat(input: string): CodeFormat {
  if (codeIsEmpty(input)) return 'empty';
  if (input.length > MAX_TYPED) return 'long';
  const code = input.normalize('NFKC').replace(IGNORED, '').toLowerCase();
  if (/[^a-z2-7]/.test(code)) return 'chars';
  if (code.length < 26) return 'short';
  if (code.length > 26) return 'long';
  return 'ok';
}

export type CodeOutcome = { ok: true; id: string } | { ok: false; message: string };

/**
 * Turns typed text into a case id the server confirmed, or one message. `check` asks the server about the case (it throws on any refusal:
 * 404, 400, 423, no network). Only an abort passes through, because that means the screen went away and nothing is to be shown.
 */
export async function resolveCode(input: string, check: (id: string) => Promise<unknown>): Promise<CodeOutcome> {
  const id = normaliseCode(input);
  if (!id) return { ok: false, message: CODE_NOT_FOUND };
  try {
    await check(id);
    return { ok: true, id };
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    // A case with its own key answers "locked" until its key is typed: the code is right, and the case opens on its key card.
    // A legacy case an older version made answers "untrusted-case": the code is right, and its page offers Restore and Erase.
    const reason = error && typeof error === 'object' ? (error as { reason?: unknown }).reason : undefined;
    if (reason === 'case-locked' || reason === 'untrusted-case') return { ok: true, id };
    // An archived case answers 409: the code is right, but the case opens from the Saved cases list, not from here.
    if (reason === 'case-archived') return { ok: false, message: CODE_ARCHIVED };
    // A lost connection says so. A timeout is not that (the app may be busy, not gone), so it falls to the general sentence below.
    if (isUnreachableError(error)) return { ok: false, message: UNREACHABLE_MESSAGE };
    // Only a 404 or 400 says the code is wrong. A locked Unravis (423) and every other failure say what happened, not "not found".
    const status = error && typeof error === 'object' ? (error as { status?: unknown }).status : undefined;
    if (status === 404 || status === 400) return { ok: false, message: CODE_NOT_FOUND };
    if (status === 423) return { ok: false, message: CODE_APP_LOCKED };
    return { ok: false, message: CODE_CHECK_FAILED };
  }
}
