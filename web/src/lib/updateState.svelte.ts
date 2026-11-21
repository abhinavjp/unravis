import { api } from './api';
import { updatePreviewNow } from './updateCheck';
import type { UpdateCheckReply } from './types';

// The update status shown across the app: the "Version X is available" bar and the Help panel's Updates section.
// The program checks on its own timer; this only reads the last answer from the local server (no network call), when the page
// opens and every ten minutes after, so a long-open window still hears about a new version.

const DISMISSED_KEY = 'unravis.updateDismissed';
const REFRESH_MS = 10 * 60 * 1000;

export const updates = $state<{ reply: UpdateCheckReply | null; dismissed: string }>({ reply: null, dismissed: readDismissed() });

function readDismissed(): string {
  try { return localStorage.getItem(DISMISSED_KEY) ?? ''; } catch { return ''; }
}

/** Replaces the shared answer, for example with the reply to "Check for updates" or to a settings change. */
export function setUpdateReply(reply: UpdateCheckReply | null): void { updates.reply = reply; }

export async function refreshUpdateStatus(): Promise<void> {
  try {
    const reply = await api.updateStatus(updatePreviewNow());
    if (reply && typeof reply.status === 'string') {
      updates.reply = reply;
      if (reply.install?.state === 'ready' || reply.install?.state === 'launched') installing.error = ''; // an old refusal is stale now
      if (reply.install?.state === 'downloading') pollWhileDownloading(); // also after a page reload mid-download
    }
  } catch { /* the bar just stays as it was */ }
}

/** "Not now": hides the bar for this version. A newer version shows it again. */
export function dismissUpdate(version: string): void {
  updates.dismissed = version;
  try { localStorage.setItem(DISMISSED_KEY, version); } catch { /* it then lasts until the page reloads */ }
}

/** Whether the bar should show for this answer. A running download keeps the bar open; every other state can be hidden with
 *  "Not now" or "Later" (for that version only). A hidden bar does not stop an automatic apply while nobody uses the app. */
export function updateBarVisible(reply: UpdateCheckReply | null, dismissed: string): boolean {
  if (!reply || reply.status !== 'available' || !reply.latest) return false;
  const state = reply.install?.state;
  if (state === 'downloading') return true;
  return reply.latest !== dismissed;
}

let poll: ReturnType<typeof setInterval> | undefined;
function pollWhileDownloading(): void {
  if (poll) return;
  poll = setInterval(async () => {
    await refreshUpdateStatus();
    if (updates.reply?.install?.state !== 'downloading') { clearInterval(poll); poll = undefined; }
  }, 1000);
}

/** busy: a button was pressed and its answer has not come back. error: the reason the last press was refused, in plain words. */
export const installing = $state<{ busy: boolean; error: string }>({ busy: false, error: '' });

function failureText(cause: unknown): string {
  return cause instanceof Error && cause.message ? cause.message : 'That did not work. Try again.';
}

/** "Update now": starts the download, then watches its progress. */
export async function startUpdateDownload(): Promise<void> {
  if (installing.busy) return;
  installing.busy = true;
  installing.error = '';
  try {
    const reply = await api.updateDownload(updatePreviewNow());
    if (reply && typeof reply.status === 'string') updates.reply = reply;
    if (reply?.install?.state === 'downloading') pollWhileDownloading();
  } catch (cause) { installing.error = failureText(cause); await refreshUpdateStatus(); } finally { installing.busy = false; }
}

/** "Install": opens the installer for the downloaded file. */
export async function startUpdateInstall(): Promise<void> {
  if (installing.busy) return;
  installing.busy = true;
  installing.error = '';
  try {
    const reply = await api.updateInstall(updatePreviewNow());
    if (reply && typeof reply.status === 'string') updates.reply = reply;
  } catch (cause) { installing.error = failureText(cause); await refreshUpdateStatus(); } finally { installing.busy = false; }
}

/** The person has read what an update nobody watched did. */
export async function dismissUpdateResult(): Promise<void> {
  try {
    const reply = await api.updateResultDismiss();
    if (reply && typeof reply.status === 'string') updates.reply = reply;
  } catch { updates.reply = updates.reply ? { ...updates.reply, result: undefined } : null; }
}

// Presence: while the page is visible and the person touched it in the last five minutes, tell the program every 45 seconds, so an
// update is never applied while the app is being used. The program decides "in use" from these pings (two minutes after the last).
const PRESENCE_MS = 45 * 1000;
const TOUCH_WINDOW_MS = 5 * 60 * 1000;
let lastTouch = Date.now();

function startPresence(): () => void {
  const touch = () => { lastTouch = Date.now(); };
  const events = ['pointerdown', 'keydown', 'wheel', 'touchstart'] as const;
  for (const name of events) window.addEventListener(name, touch, { passive: true });
  const ping = () => {
    if (document.visibilityState !== 'visible' || Date.now() - lastTouch > TOUCH_WINDOW_MS) return;
    void api.uiPresence().catch(() => { /* a missed ping only makes the app look idle for a moment */ });
  };
  ping();
  const timer = setInterval(ping, PRESENCE_MS);
  return () => { clearInterval(timer); for (const name of events) window.removeEventListener(name, touch); };
}

/** Starts reading the status. Returns the function that stops it. */
export function startUpdateWatch(): () => void {
  void refreshUpdateStatus();
  const timer = setInterval(() => { void refreshUpdateStatus(); }, REFRESH_MS);
  const stopPresence = startPresence();
  return () => { clearInterval(timer); stopPresence(); };
}
