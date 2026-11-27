import { SAVED_FILE_TEXT } from './copy/savedFileCopy';

// RV-DAT-1 (W-13): the saved MongoDB connections file fails closed. When the server cannot read it, it answers the code below, and this is
// the only case where the page offers "Set aside and start a new list" instead of "Try again".
export const SAVED_FILE_DAMAGED = 'saved-file-damaged';
/** The server refused to set aside a file that reads fine. */
export const SAVED_FILE_NOT_DAMAGED = 'saved-file-not-damaged';
/** Three damaged files are already kept; the server refuses a fourth and says how to make room. */
export const SET_ASIDE_FULL = 'set-aside-full';

const codeOf = (cause: unknown): string => (cause !== null && typeof cause === 'object' && typeof (cause as { code?: unknown }).code === 'string' ? (cause as { code: string }).code : '');
const statusOf = (cause: unknown): number => (cause !== null && typeof cause === 'object' && typeof (cause as { status?: unknown }).status === 'number' ? (cause as { status: number }).status : 0);

export const isSavedFileDamaged = (cause: unknown): boolean => codeOf(cause) === SAVED_FILE_DAMAGED;

/** The server's own sentence for a damaged list (it names the real folder), and whether the list is the whole PC's (reason "shared"). */
export function damagedInfo(cause: unknown): { detail: string; shared: boolean } {
  if (!isSavedFileDamaged(cause)) return { detail: '', shared: false };
  const message = cause instanceof Error ? cause.message : '';
  const reason = (cause as { reason?: unknown }).reason;
  return { detail: message !== '' ? message : SAVED_FILE_TEXT.damaged, shared: reason === 'shared' };
}

/**
 * healthy: the file reads fine after all, so the page just reloads the list. Otherwise text is what to tell the person.
 * recheck: the page should read the list again, so the way out is only offered while the file really is unreadable.
 */
export function setAsideFailure(cause: unknown): { healthy: boolean; text: string; recheck: boolean } {
  const code = codeOf(cause);
  if (code === SAVED_FILE_NOT_DAMAGED) return { healthy: true, text: SAVED_FILE_TEXT.readsFine, recheck: false };
  const message = cause instanceof Error ? cause.message : '';
  // The server's own sentence for a full set-aside folder says what to do about it; nothing changed, so there is nothing to read again.
  if (code === SET_ASIDE_FULL && message !== '') return { healthy: false, text: message, recheck: false };
  // Any other refusal of the server (the file was busy, the key keeper was stopped) carries its own sentence and "left as it was".
  if (statusOf(cause) === 503 && message !== '') return { healthy: false, text: message, recheck: true };
  return { healthy: false, text: SAVED_FILE_TEXT.failed, recheck: true };
}
