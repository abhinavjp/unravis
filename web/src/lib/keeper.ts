/**
 * The key keeper (P14). On a PC set up for everyone (the shared data folder), saved passwords, saved MongoDB links with a password and
 * Blackbox read keys live only in the Unravis Key Keeper service. This page code never sees one: it only asks the server whether the keeper
 * runs and whether the person's old per-user saved files still wait to be moved.
 */

import { KEEPER_TEXT } from './copy/sourcesCopy';

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
const previewListeners = new Set<() => void>();

/** Calls back when the address names a different preview (the screenshot tool sets it after the page has loaded). */
export function onKeeperPreview(listener: () => void): () => void {
  previewListeners.add(listener);
  return () => { previewListeners.delete(listener); };
}

/** Notes a preview named in the address and keeps it for this tab, so moving around the app (which changes the address) keeps it. */
function noticePreview(): void {
  const found = keeperPreviewFrom(location.hash);
  if (!found) return;
  const changed = found !== preview;
  preview = found;
  try { sessionStorage.setItem(PREVIEW_KEY, found); } catch { /* the preview then lasts until the page reloads */ }
  if (changed) for (const listener of previewListeners) listener();
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

/** The sticker on an audit source the key keeper opened from a saved link (P14b); '' for a source this Unravis opened itself. */
export const heldSourceText = (source: { keeper?: 'held' | 'stopped' }): string =>
  source.keeper === 'held' ? KEEPER_TEXT.held : source.keeper === 'stopped' ? KEEPER_TEXT.heldDown : '';

/**
 * A read refused because of the keeper: it is not running, or it closed a source it had open (restarted, or idle too long). The page
 * then reads the source list again, so the stickers say which sources cannot be read and closed sources leave the list.
 */
export const keeperSourceChanged = (error: unknown): boolean =>
  isKeeperDown(error) || (error as { code?: string } | null)?.code === 'keeper-source-gone';

const refusalListeners = new Set<() => void>();

/** Calls back whenever any server read is refused because of the keeper (see keeperSourceChanged), whichever read it was. */
export function onKeeperRefusal(listener: () => void): () => void {
  refusalListeners.add(listener);
  return () => { refusalListeners.delete(listener); };
}

/** Called once for every failed server answer, so coverage, requests, payloads, records and grids refresh the source list like an analysis. */
export function noticeKeeperRefusal(error: unknown): void {
  if (!keeperSourceChanged(error)) return;
  for (const listener of [...refusalListeners]) listener();
}

/** A source the key keeper holds but cannot use now: it cannot be chosen until the keeper runs again. */
export const keeperUnreadable = (source: { keeper?: 'held' | 'stopped' }): boolean => source.keeper === 'stopped';

/**
 * Where Tab goes inside a small dialog with `count` focusable controls when the focus is on control `current` (-1 when the focus is
 * outside them). The focus wraps at both ends; with no controls there is nowhere to go (-1).
 */
export function trapTarget(count: number, current: number, shift: boolean): number {
  if (count <= 0) return -1;
  if (current < 0) return shift ? count - 1 : 0;
  return (current + (shift ? -1 : 1) + count) % count;
}

/**
 * Makes everything outside `dialog` inert (not reachable by keyboard, mouse or screen reader): walks up from the dialog to the page body
 * and marks every sibling on the way. Returns the function that gives all of it back; call it whenever the dialog goes away.
 */
export function holdBackground(dialog: HTMLElement): () => void {
  const body = dialog.ownerDocument.body;
  const held: HTMLElement[] = [];
  for (let node: HTMLElement | null = dialog; node && node !== body; node = node.parentElement) {
    const parent: HTMLElement | null = node.parentElement;
    if (!parent) break;
    for (const sibling of Array.from(parent.children) as HTMLElement[]) {
      if (sibling === node || sibling.inert) continue;
      sibling.inert = true;
      held.push(sibling);
    }
  }
  return () => { for (const item of held) item.inert = false; };
}
