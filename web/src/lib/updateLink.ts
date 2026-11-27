import { isNetworkError } from './copy/startupCopy';
import { isUnreachableError } from './copy/unreachableCopy';

// When an update restarts Unravis, the app comes back on a new address with a new session, so the page that pressed the button can no
// longer reach it. These are the rules for noticing that, and the words for it. Pure: no DOM, no fetch.

/** The note shown in place of the update steps once this page has lost the app. */
export const SERVER_GONE_NOTE = 'Unravis has restarted, so this page can no longer reach it. A new Unravis page may have opened: use that one and close this page. If none opened, open Unravis from the Start menu or its tray icon.';

/** Added to the note when "Check again" could not reach the app either. */
export const STILL_GONE_NOTE = 'Still cannot reach it.';

/** Shown when a button press could not reach the app but a second look found it alive (a passing hiccup). */
export const HICCUP_NOTE = 'Could not reach Unravis just now. Try again.';

/** How many reads in a row must fail before a page that was waiting for the restart says the app is gone (one lost read is only a hiccup). */
export const GONE_AFTER_FAILURES = 2;

/** How often the page looks for the app while an update is being applied. */
export const GONE_CHECK_MS = 3000;

/** True when the answer says the app is not there (or not the same app): the connection failed, or the session is no longer known. */
export function lostServer(cause: unknown): boolean {
  // The request layer turns a failed fetch into an error with code `unreachable` and status 0 (W-01), which isNetworkError does not read as one.
  if (isNetworkError(cause) || isUnreachableError(cause)) return true;
  const code = cause && typeof cause === 'object' ? (cause as { code?: unknown }).code : undefined;
  const status = cause && typeof cause === 'object' ? (cause as { status?: unknown }).status : undefined;
  return code === 'no_session' || status === 401;
}
