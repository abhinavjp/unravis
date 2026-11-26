import { api } from './api';
import { updatePreviewNow } from './updateCheck';
import type { UpdateCheckReply } from './types';
import { visiblePoll } from './visibility';
import { GONE_AFTER_FAILURES, GONE_CHECK_MS, HICCUP_NOTE, lostServer } from './updateLink';
import { normalizeVersion, parseableVersion, readSeen, shouldAutoOpen, writeSeen } from './updateNotes';

// The update status shown across the app: the "Version X is available" bar and the Help panel's Updates section.
// The program checks on its own timer; this only reads the last answer from the local server (no network call), when the page
// opens and every ten minutes after, so a long-open window still hears about a new version.
// While the page is hidden nothing is read (BL-429, visibility.ts): the first look after it is shown again happens at once when a full
// interval has passed, so a window that was minimised for an hour is current as soon as it is seen.

const DISMISSED_KEY = 'unravis.updateDismissed';
const REFRESH_MS = 10 * 60 * 1000;

export const updates = $state<{ reply: UpdateCheckReply | null; dismissed: string }>({ reply: null, dismissed: readDismissed() });

function readDismissed(): string {
  try { return localStorage.getItem(DISMISSED_KEY) ?? ''; } catch { return ''; }
}

/** Replaces the shared answer, for example with the reply to "Check for updates" or to a settings change. */
export function setUpdateReply(reply: UpdateCheckReply | null): void { updates.reply = reply; }

/** Whether this page has lost the app: it was restarted (an update) and now answers on another address, or not at all. */
export const appLink = $state<{ gone: boolean; recheckFailed: boolean }>({ gone: false, recheckFailed: false });
let failedReads = 0;

/** Notes that the app answered: whatever was thought lost is back. */
function appAnswered(): void { failedReads = 0; appLink.gone = false; appLink.recheckFailed = false; }

/** Notes a read that failed. A page that is waiting for the restart (the update was launched) gives up after a few misses. */
function readFailed(cause: unknown): void {
  if (!lostServer(cause)) return;
  failedReads += 1;
  if (failedReads >= GONE_AFTER_FAILURES && updates.reply?.install?.state === 'launched') markGone();
}

function markGone(): void {
  appLink.gone = true;
  installing.error = '';
  stopDownloadPoll?.();
  stopDownloadPoll = undefined;
  stopLaunchPoll?.();
  stopLaunchPoll = undefined;
}

export async function refreshUpdateStatus(): Promise<void> {
  try {
    const reply = await api.updateStatus(updatePreviewNow());
    appAnswered();
    if (reply && typeof reply.status === 'string') {
      updates.reply = reply;
      if (reply.install?.state === 'ready' || reply.install?.state === 'launched') installing.error = ''; // an old refusal is stale now
      if (reply.install?.state === 'downloading') pollWhileDownloading(); // also after a page reload mid-download
      if (reply.install?.state === 'launched') pollWhileLaunched(); // also after a page reload mid-update
    }
  } catch (cause) { readFailed(cause); /* the bar otherwise stays as it was */ }
}

/** "Check again" on the lost-app note: one more look; the note goes away when the app answers. */
export async function checkAppAgain(): Promise<void> {
  failedReads = 0;
  await refreshUpdateStatus();
  appLink.recheckFailed = appLink.gone; // still no answer: the note says so
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

let stopDownloadPoll: (() => void) | undefined;
// The progress read while a download runs: once a second while the bar can be seen, not at all while it cannot. On show the next read
// is at once (more than a second has passed), so the bar jumps to where the download is.
function pollWhileDownloading(): void {
  if (stopDownloadPoll) return;
  stopDownloadPoll = visiblePoll(async () => {
    await refreshUpdateStatus();
    if (updates.reply?.install?.state === 'downloading') return true;
    stopDownloadPoll = undefined; // the poll ends itself when its run answers false
    return false;
  }, 1000);
}

let stopLaunchPoll: (() => void) | undefined;
// While an update is being applied the app quits and starts again, usually on another address. Look every few seconds, so the bar tells
// the person it is gone instead of waiting for a button press to fail. It ends when the app answers with another state, or is gone.
function pollWhileLaunched(): void {
  if (stopLaunchPoll || appLink.gone) return;
  stopLaunchPoll = visiblePoll(async () => {
    await refreshUpdateStatus();
    if (updates.reply?.install?.state === 'launched' && !appLink.gone) return true;
    stopLaunchPoll = undefined;
    return false;
  }, GONE_CHECK_MS);
}

/** busy: a button was pressed and its answer has not come back. error: the reason the last press was refused, in plain words. */
export const installing = $state<{ busy: boolean; error: string }>({ busy: false, error: '' });

/** A press that fails because the app is gone is not an error to show raw ("Failed to fetch"): the bar says the app is gone instead. */
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
    appAnswered();
    if (reply && typeof reply.status === 'string') updates.reply = reply;
    if (reply?.install?.state === 'downloading') pollWhileDownloading();
  } catch (cause) { await pressFailed(cause); } finally { installing.busy = false; }
}

/** "Install": opens the installer for the downloaded file. */
export async function startUpdateInstall(): Promise<void> {
  if (installing.busy) return;
  installing.busy = true;
  installing.error = '';
  try {
    const reply = await api.updateInstall(updatePreviewNow());
    appAnswered();
    if (reply && typeof reply.status === 'string') updates.reply = reply;
    if (reply?.install?.state === 'launched') pollWhileLaunched(); // look for the restart now, not only after a page reload
  } catch (cause) { await pressFailed(cause); } finally { installing.busy = false; }
}

async function pressFailed(cause: unknown): Promise<void> {
  if (lostServer(cause)) {
    // One more look before saying so: a single lost request on a live app is only a hiccup.
    try {
      const reply = await api.updateStatus(updatePreviewNow());
      appAnswered();
      if (reply && typeof reply.status === 'string') updates.reply = reply;
      // The press may have worked and only its reply was lost: if the read says the setup was launched, that is not an error. Keep watching.
      if (reply?.install?.state === 'launched') { installing.error = ''; pollWhileLaunched(); } else installing.error = HICCUP_NOTE;
    } catch (again) { if (lostServer(again)) markGone(); else installing.error = failureText(again); }
    return;
  }
  installing.error = failureText(cause);
  await refreshUpdateStatus();
}

/** True from the press of "Update now" until the installer has been started or the download failed: it spans download, check and install. */
export const updateFlow = $state<{ chaining: boolean }>({ chaining: false });
const sleep = (ms: number) => new Promise<void>(done => setTimeout(done, ms));
const DOWNLOAD_PATIENCE_MS = 15 * 60 * 1000;

/** The one-step "Update now": downloads the update, waits until it is checked and ready, then starts the installer.
 *  An update that is already downloaded goes straight to the install. A failure stops it and shows in the reply (`install.state` 'failed'). */
export async function updateNow(): Promise<void> {
  if (updateFlow.chaining) return;
  updateFlow.chaining = true;
  try {
    if (updates.reply?.install?.state !== 'ready') {
      await startUpdateDownload();
      if (installing.error) return;
      const give = Date.now() + DOWNLOAD_PATIENCE_MS;
      while (updates.reply?.install?.state === 'downloading' && Date.now() < give) await sleep(400);
    }
    if (updates.reply?.install?.state === 'ready') await startUpdateInstall();
  } finally { updateFlow.chaining = false; }
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
// Opening the page counts as a touch only while it is visible and focused (a person just opened it); a page that comes back in the
// background after an update does not.
let lastTouch = (typeof document !== 'undefined' && document.visibilityState === 'visible' && document.hasFocus()) ? Date.now() : 0;

function startPresence(): () => void {
  const touch = () => { lastTouch = Date.now(); };
  const events = ['pointerdown', 'pointermove', 'keydown', 'wheel', 'touchstart'] as const;
  for (const name of events) window.addEventListener(name, touch, { passive: true });
  // The rule is unchanged: a ping goes out only while the page is visible and was touched in the last five minutes. A hidden page sends
  // nothing, so the program sees no ping, and two minutes after the last one it counts the app as not in use. The timer itself now
  // sleeps while hidden (a wake-up that sent nothing); on show it pings at once if the last look was more than 45 seconds ago.
  const ping = () => {
    if (document.visibilityState !== 'visible' || Date.now() - lastTouch > TOUCH_WINDOW_MS) return;
    void api.uiPresence().catch(() => { /* a missed ping only makes the app look idle for a moment */ });
  };
  const stop = visiblePoll(ping, PRESENCE_MS, { immediate: true });
  return () => { stop(); for (const name of events) window.removeEventListener(name, touch); };
}

/** Starts reading the status. Returns the function that stops it. */
export function startUpdateWatch(): () => void {
  // The first read is at once; the next ones every ten minutes, only while the page is visible.
  const stopStatus = visiblePoll(async () => { await refreshUpdateStatus(); }, REFRESH_MS, { immediate: true });
  const stopPresence = startPresence();
  return () => { stopStatus(); stopPresence(); };
}

// What's new: whether the window is open, and the version whose notes the person last read (kept in this browser).
export const whatsNew = $state<{ open: boolean; after: boolean; seen: string }>({ open: false, after: false, seen: readSeen() });

/** Opens the "What's new" window. `after` is true when it opens by itself because the app was just updated. */
export function openWhatsNew(after = false): void { whatsNew.after = after; whatsNew.open = true; }

/** Closes the window and remembers that the notes up to the installed version were read. */
export function closeWhatsNew(current: string): void {
  whatsNew.open = false;
  if (parseableVersion(current)) { whatsNew.seen = normalizeVersion(current); writeSeen(current); }
}

/** Run once the installed version is known: a first run just remembers the version; a newer version than the one last read opens the window. */
export function checkWhatsNewOnStart(current: string): void {
  if (!parseableVersion(current) || whatsNew.open) return;
  if (!whatsNew.seen) { whatsNew.seen = normalizeVersion(current); writeSeen(current); return; }
  if (shouldAutoOpen(whatsNew.seen, current)) openWhatsNew(true);
}
