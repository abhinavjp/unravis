import { isNetworkError } from './copy/startupCopy';

// When an update restarts Unravis, the app comes back on a new address with a new session, so the page that pressed the button can no
// longer reach it. These are the rules for noticing that, and the words for it. Pure: no DOM, no fetch.

/** The note shown in place of the update steps once this page has lost the app. */
export const SERVER_GONE_NOTE = 'Unravis has restarted, so this page can no longer reach it. Open Unravis from the Start menu or its tray icon, then close this page.';

/** How many reads in a row must fail before a page that was waiting for the restart says the app is gone (one lost read is only a hiccup). */
export const GONE_AFTER_FAILURES = 2;

/** How often the page looks for the app while an update is being applied. */
export const GONE_CHECK_MS = 3000;

/** True when the answer says the app is not there (or not the same app): the connection failed, or the session is no longer known. */
export function lostServer(cause: unknown): boolean {
  if (isNetworkError(cause)) return true;
  const code = cause && typeof cause === 'object' ? (cause as { code?: unknown }).code : undefined;
  const status = cause && typeof cause === 'object' ? (cause as { status?: unknown }).status : undefined;
  return code === 'no_session' || status === 401;
}
