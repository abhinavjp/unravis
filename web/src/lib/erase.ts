import { CaseApiError, eraseConfirmed, type CaseInfo } from './cases';
import { believedState } from './cases';
import { errorText } from './copy/importCopy';
import { ERASE_TEXT } from './copy/eraseCopy';

/**
 * The page rules of Erase (decision A16), kept apart from the screens so a test can read them. Nothing here calls the server.
 * Erase is offered for every case except one that is being archived (M15 would refuse it), and it needs the short case code typed.
 */

/** Whether the Erase button is shown. A case that is archiving cannot be erased until the archive is cancelled. */
export function eraseOffered(c: Pick<CaseInfo, 'state' | 'closed'>): boolean {
  const state = believedState(c);
  return state !== 'archiving' && state !== 'erased';
}

/** The confirm button is on only when the typed code is the case's short code (the server checks it again). */
export const eraseReady = (typed: string, id: string): boolean => eraseConfirmed(typed, id);

/** The sentence for an erase that did not finish. Only the fixed texts are used; the server's wording and the typed code are never shown. */
export function eraseFailureText(error: unknown): string {
  if (error instanceof CaseApiError) {
    if (error.reason === 'confirm-mismatch') return ERASE_TEXT.mismatch;
    if (error.reason === 'unreachable' || error.status === 0) return ERASE_TEXT.network;
    // M4 (case in use), M5 (being erased) and M15 (cancel the archive first) have their own sentences in errorText.
    if (error.reason === 'case-in-use' || error.reason === 'case-erasing' || error.reason === 'cancel-archive-first') return errorText(error);
    if (error.reason === 'case-not-found' || error.status === 404) return errorText(error);
    return ERASE_TEXT.failed;
  }
  return ERASE_TEXT.network;
}
