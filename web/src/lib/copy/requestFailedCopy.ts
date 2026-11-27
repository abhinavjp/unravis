import type { LintEntry } from './copylint';

/**
 * The one sentence for "a request failed and the server gave no message of its own" (an empty body, a body that is not JSON, a bare 500).
 * It replaces the old "Request failed (500)" on every screen. The status number is not shown: it stays in the error log entry
 * (the `status` of that entry), which the person can copy from Help. Pure: no DOM, no fetch.
 */
export const REQUEST_FAILED_MESSAGE = 'Something went wrong in Unravis. Try again. If it keeps happening, open Help and copy the error log.';

/** The old fallback, "Request failed (500)". Nothing writes it any more; text from an older run or a test double is still recognised as "no message". */
const LEGACY_BARE_STATUS = /^Request failed \(\d+\)$/;

/** True when the text says nothing the server wrote: the plain fallback sentence, or the old bare "Request failed (N)". */
export function isBareFailure(message: string): boolean {
  const text = message.trim();
  return text === REQUEST_FAILED_MESSAGE || LEGACY_BARE_STATUS.test(text);
}

/** Every fixed sentence above, for the copy lint. */
export const requestFailedLintEntries = (): LintEntry[] => [{ id: 'requestFailed.message', text: REQUEST_FAILED_MESSAGE }];
