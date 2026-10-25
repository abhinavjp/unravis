/**
 * Browser Back closes the open drawer instead of leaving the page. While any drawer is open there is exactly one extra history
 * entry (same address, a marker in its state). Opening a second drawer over the first reuses that entry. Closing from the page
 * (the X button, Esc) steps back over it; Back from the browser closes the drawer that opened last, and puts the entry back when
 * another drawer is still open. Pure logic: it takes the history object and reports through callbacks, so a fake can drive it.
 */
export interface DrawerHistoryLike {
  readonly state: unknown;
  pushState(state: unknown, unused: string, url?: string | URL | null): void;
  replaceState(state: unknown, unused: string, url?: string | URL | null): void;
  back(): void;
}

/** The key in history.state that marks the drawer entry. */
export const DRAWER_MARK = 'uvDrawer';

const objectState = (state: unknown): Record<string, unknown> => (state && typeof state === 'object' ? { ...(state as Record<string, unknown>) } : {});
const marked = (state: unknown): boolean => !!state && typeof state === 'object' && (state as Record<string, unknown>)[DRAWER_MARK] === true;
const withMark = (state: unknown): Record<string, unknown> => ({ ...objectState(state), [DRAWER_MARK]: true });
const withoutMark = (state: unknown): Record<string, unknown> => { const next = objectState(state); delete next[DRAWER_MARK]; return next; };

export function createDrawerHistory(hist: DrawerHistoryLike, onCloseRequest: (id: string) => void) {
  // A reload keeps history.state, so a marked entry can survive into a fresh page. It is cleared here and opens nothing.
  if (marked(hist.state)) hist.replaceState(withoutMark(hist.state), '');

  let open: string[] = [];      // the drawers the history entry stands for, in opening order
  let want: string[] = [];      // the drawers the page says are open now
  let entry = false;            // the marked entry is the current one
  let ownBacks = 0;             // history.back() calls this module made that have not reported yet
  let later: Array<() => void> = [];

  const settled = () => ownBacks === 0 && open.length === 0 && want.length === 0;
  function flush() {
    if (!settled()) return;
    const run = later; later = [];
    for (const fn of run) fn();
  }

  function reconcile() {
    if (ownBacks > 0) return; // wait for our own Back to land before touching the history again
    const next = [...open.filter(id => want.includes(id)), ...want.filter(id => !open.includes(id))];
    const changed = next.length !== open.length || next.some((id, index) => id !== open[index]);
    const was = open;
    open = next;
    if (!changed) return;
    if (was.length === 0 && next.length > 0) { hist.pushState(withMark(hist.state), ''); entry = true; }
    else if (next.length > 0) { if (entry) hist.replaceState(withMark(hist.state), ''); }
    else if (entry) { entry = false; ownBacks++; hist.back(); }
  }

  return {
    /** Tell it which drawers are open now (call whenever that changes). */
    sync(ids: readonly string[]) { want = [...ids]; reconcile(); flush(); },
    /** Run something once no drawer entry is on the history (a view change must not land on top of it). Runs now when that is already so. */
    whenClear(fn: () => void) { if (settled()) fn(); else later.push(fn); },
    /** Call on every popstate. True when this module used the event, so the page must not treat it as a view change. */
    pop(): boolean {
      if (ownBacks > 0) {
        ownBacks--; entry = false;
        if (ownBacks === 0) { reconcile(); flush(); }
        return true;
      }
      if (open.length > 0 && entry) {
        entry = false;
        const id = open[open.length - 1];
        open = open.slice(0, -1);
        want = want.filter(item => item !== id);
        if (open.length > 0) { hist.pushState(withMark(hist.state), ''); entry = true; }
        onCloseRequest(id);
        flush();
        return true;
      }
      if (marked(hist.state)) { ownBacks++; hist.back(); return true; } // Forward onto an entry no drawer owns: step over it
      return false;
    },
    get entryOpen() { return entry; },
    get openIds() { return [...open]; }
  };
}
