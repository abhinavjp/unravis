// Story so far, case counts and the file list are read when the case page opens and again after every add made on this page. A change made
// somewhere this page cannot see (another browser tab, the API, a second window) shows only when the person comes back to the page, so
// that is the other time the page reads the case again (BL-162, BL-164 leftover). Pure rules here; the page does the reading.

/** Two returns closer than this are one return: a window that is clicked back to fires `visibilitychange` and `focus` together. */
export const RETURN_GAP_MS = 2000;

export interface ReturnState {
  /** The page shows a case (not loading, gone, locked or on the key or erase screens). */
  ready: boolean;
  /** The page is hidden (another browser tab, a minimised window). */
  hidden: boolean;
  /** A read of the page is on its way, or a move on the case (close, reopen, retry) is running. */
  busy: boolean;
  /** Another tab moved the case first and the page is waiting for the person to reload: it is not changed behind their back. */
  changedElsewhere: boolean;
  /** When the case was last asked for (the start of a read, so a server that fails is not asked again at once). */
  lastReadAt: number;
  now: number;
}

/** True when coming back to the page should read the case again, quietly. At most one read per RETURN_GAP_MS, never two at once. */
export function readOnReturn(state: ReturnState): boolean {
  if (!state.ready || state.hidden || state.busy || state.changedElsewhere) return false;
  return state.now - state.lastReadAt >= RETURN_GAP_MS;
}

/**
 * Calls `onReturn` each time the page is shown again (the browser tab comes back, the window is focused). It does not decide whether to read:
 * the page asks `readOnReturn`. Returns the function that stops it.
 */
export function watchReturn(onReturn: () => void, win: Pick<Window, 'addEventListener' | 'removeEventListener'> | undefined = typeof window === 'undefined' ? undefined : window,
  doc: Pick<Document, 'addEventListener' | 'removeEventListener'> | undefined = typeof document === 'undefined' ? undefined : document): () => void {
  if (!win || !doc) return () => {};
  win.addEventListener('focus', onReturn);
  doc.addEventListener('visibilitychange', onReturn);
  return () => {
    win.removeEventListener('focus', onReturn);
    doc.removeEventListener('visibilitychange', onReturn);
  };
}

/**
 * True when two answers of the same read say the same thing. The page keeps the answer it already has in that case, so a read that finds
 * nothing new redraws nothing (no flicker, no moved focus, no new layout).
 */
export function sameRead(before: unknown, after: unknown): boolean {
  if (before === after) return true;
  try { return JSON.stringify(before) === JSON.stringify(after); } catch { return false; }
}

/**
 * "The read that started last wins" for one kind of answer (the health checks, say) that several reads can bring: the page's own read, the
 * read after an add, the read on return, and the one a finished check asks for. A read takes a number when it starts and puts its answer in only
 * while that number is still the newest, so an older list that was slow can never replace a newer one.
 */
export function newestWins(): { begin: () => number; latest: (token: number) => boolean } {
  let last = 0;
  return { begin: () => ++last, latest: (token) => token === last };
}
