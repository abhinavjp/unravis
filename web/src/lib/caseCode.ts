import { CODE_NOT_FOUND } from './copy/enterCopy';

/**
 * Entering a case by its code. Pure: no DOM; the one request is passed in. The 26-character case id is the code.
 * Every code that does not open a case (too short, too long, wrong characters, unknown, erased, locked, no network) reads as the same
 * sentence, so the screen never reveals which case ids exist on this computer.
 */

export { CODE_NOT_FOUND };

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

export type CodeOutcome = { ok: true; id: string } | { ok: false; message: string };

/**
 * Turns typed text into a case id the server confirmed, or the one message. `check` asks the server about the case (it throws on any refusal:
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
    return { ok: false, message: CODE_NOT_FOUND };
  }
}
