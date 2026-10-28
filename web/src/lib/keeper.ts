/**
 * The key keeper (P14). On a PC set up for everyone (the shared data folder), saved passwords, saved MongoDB links with a password and
 * Blackbox read keys live only in the Unravis Key Keeper service. This page code never sees one: it only asks the server whether the keeper
 * runs and whether the person's old per-user saved files still wait to be moved.
 */

/** duplicates: old records the keeper holds only as someone else's copy (not in the move count; removing the old copies confirms them). */
export type KeeperPending = { sql: number; mongo: number; blackbox: number; duplicates?: number };

export type KeeperStatus = {
  /** "user": saved connections are per person, as before. "shared": the key keeper holds every saved secret. */
  mode: 'user' | 'shared';
  running: boolean;
  /** "stopped" or "impostor" when the keeper cannot be used. */
  problem?: 'stopped' | 'impostor';
  pending: KeeperPending;
  oldFilesPresent: boolean;
};

/** What the connections page shows about the keeper. */
export type KeeperView = 'none' | 'stopped' | 'impostor' | 'migrate' | 'moved' | 'ready';

const PREVIEWS = ['saved', 'stopped', 'migrate', 'moved'] as const;
export type KeeperPreview = (typeof PREVIEWS)[number];

/** The screenshot preview named in the address (#keeper=stopped and so on). The server answers it only in the demo. */
export function keeperPreviewFrom(hash: string): KeeperPreview | '' {
  const match = /(?:^#|[#&])keeper=([a-z]+)/.exec(hash);
  const name = match ? match[1] : '';
  return (PREVIEWS as readonly string[]).includes(name) ? (name as KeeperPreview) : '';
}

const PREVIEW_KEY = 'unravis.keeperPreview';
let preview: KeeperPreview | '' = '';

/** Notes a preview named in the address and keeps it for this tab, so moving around the app (which changes the address) keeps it. */
function noticePreview(): void {
  const found = keeperPreviewFrom(location.hash);
  if (!found) return;
  preview = found;
  try { sessionStorage.setItem(PREVIEW_KEY, found); } catch { /* the preview then lasts until the page reloads */ }
}

if (typeof location !== 'undefined' && typeof window !== 'undefined') {
  try { preview = keeperPreviewFrom('#keeper=' + (sessionStorage.getItem(PREVIEW_KEY) ?? '')); } catch { preview = ''; }
  noticePreview();
  // popstate comes first on a hash change, before the app's own view handling may rewrite the address.
  window.addEventListener('popstate', noticePreview);
  window.addEventListener('hashchange', noticePreview);
}

/** The screenshot preview for this tab ('' for none). */
export const keeperPreview = (): KeeperPreview | '' => preview;

export const pendingTotal = (p: KeeperPending | undefined): number => (p ? p.sql + p.mongo + p.blackbox : 0);

export function keeperView(status: KeeperStatus | null): KeeperView {
  if (!status || status.mode !== 'shared') return 'none';
  if (!status.running) return status.problem === 'impostor' ? 'impostor' : 'stopped';
  if (pendingTotal(status.pending) > 0) return 'migrate';
  if (status.oldFilesPresent) return 'moved';
  return 'ready';
}

/** A server answer that means the keeper, not the saved list, is the problem (so the page must not read it as the demo). */
export const isKeeperDown = (error: unknown): boolean => {
  const code = (error as { code?: string } | null)?.code;
  return code === 'keeper-stopped' || code === 'keeper-impostor';
};
