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
      if (reply.install?.state === 'downloading') pollWhileDownloading(); // also after a page reload mid-download
    }
  } catch { /* the bar just stays as it was */ }
}

/** "Not now": hides the bar for this version. A newer version shows it again. */
export function dismissUpdate(version: string): void {
  updates.dismissed = version;
  try { localStorage.setItem(DISMISSED_KEY, version); } catch { /* it then lasts until the page reloads */ }
}

/** Whether the bar should show for this answer. A running download, or a finished one waiting for Install, keeps the bar open;
 *  every other state can be hidden with "Not now" (for that version only). */
export function updateBarVisible(reply: UpdateCheckReply | null, dismissed: string): boolean {
  if (!reply || reply.status !== 'available' || !reply.latest) return false;
  const state = reply.install?.state;
  if (state === 'downloading' || state === 'ready') return true;
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

/** Starts reading the status. Returns the function that stops it. */
export function startUpdateWatch(): () => void {
  void refreshUpdateStatus();
  const timer = setInterval(() => { void refreshUpdateStatus(); }, REFRESH_MS);
  return () => clearInterval(timer);
}
