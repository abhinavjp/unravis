import { SAVED_FILE_TEXT } from './copy/savedFileCopy';

// RV-DAT-1 (W-13): the saved MongoDB connections file fails closed. When the server cannot read it, it answers the code below, and this is
// the only case where the page offers "Set aside and start a new list" instead of "Try again".
export const SAVED_FILE_DAMAGED = 'saved-file-damaged';
/** The server refused to set aside a file that reads fine. */
export const SAVED_FILE_NOT_DAMAGED = 'saved-file-not-damaged';
/** Three damaged files are already kept; the server refuses a fourth and says how to make room. */
export const SET_ASIDE_FULL = 'set-aside-full';

const codeOf = (cause: unknown): string => (cause !== null && typeof cause === 'object' && typeof (cause as { code?: unknown }).code === 'string' ? (cause as { code: string }).code : '');

export const isSavedFileDamaged = (cause: unknown): boolean => codeOf(cause) === SAVED_FILE_DAMAGED;

/** healthy: the file reads fine after all, so the page just reloads the list. Otherwise text is what to tell the person. */
export function setAsideFailure(cause: unknown): { healthy: boolean; text: string } {
  const code = codeOf(cause);
  if (code === SAVED_FILE_NOT_DAMAGED) return { healthy: true, text: SAVED_FILE_TEXT.readsFine };
  // The server's own sentence for a full set-aside folder says what to do about it.
  if (code === SET_ASIDE_FULL && cause instanceof Error && cause.message !== '') return { healthy: false, text: cause.message };
  return { healthy: false, text: SAVED_FILE_TEXT.failed };
}
