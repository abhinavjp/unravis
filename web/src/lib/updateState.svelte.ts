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
    if (reply && typeof reply.status === 'string') updates.reply = reply;
  } catch { /* the bar just stays as it was */ }
}

/** "Not now": hides the bar for this version. A newer version shows it again. */
export function dismissUpdate(version: string): void {
  updates.dismissed = version;
  try { localStorage.setItem(DISMISSED_KEY, version); } catch { /* it then lasts until the page reloads */ }
}

/** Whether the bar should show for this answer. */
export function updateBarVisible(reply: UpdateCheckReply | null, dismissed: string): boolean {
  return !!reply && reply.status === 'available' && !!reply.latest && reply.latest !== dismissed;
}

/** Starts reading the status. Returns the function that stops it. */
export function startUpdateWatch(): () => void {
  void refreshUpdateStatus();
  const timer = setInterval(() => { void refreshUpdateStatus(); }, REFRESH_MS);
  return () => clearInterval(timer);
}
