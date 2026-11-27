// The rules for "can this page still reach the Unravis app" (W-02, RV-UI-1 part 2). Plain TypeScript with no framework and no imports, so
// the request layer (api.ts, cases.ts) can report into it without pulling in the screen's reactive store, and without an import cycle.
// The screens read the reactive mirror in liveness.svelte.ts.
//
// Every answer from the app clears the count, every lost connection adds one. Three lost connections in a row, or one failed light check
// (liveness.svelte.ts), say the app has stopped. A page whose session the app refuses after that belongs to an earlier run (a new run has
// a new session), which Reload cannot fix. Only a lost connection (code `unreachable`) may be reported here: a read that ran out of time
// (`timeout`) is a slow app, not a stopped one, and a long import must never raise this alarm.

/** How many lost connections in a row say the app has stopped (one or two are a hiccup). */
export const STOP_AFTER_MISSES = 3;

export interface LivenessState {
  /** The app does not answer. Cleared by the next answer. */
  stopped: boolean;
  /** The app answers but refuses this page's session: it was started again. Stays until the page is closed or reloaded. */
  earlier: boolean;
  /** When the app last answered this page (ms since 1970), or 0 if it never did. */
  lastHeardAt: number;
}

const state: LivenessState = { stopped: false, earlier: false, lastHeardAt: 0 };
const listeners = new Set<() => void>();
let misses = 0;
let watching = false;

const changed = (): void => { for (const listener of [...listeners]) listener(); };

/** A copy of the state as it is now. */
export const livenessSnapshot = (): LivenessState => ({ ...state });

/** Calls `listener` after every change. Returns the function that stops it. */
export function onLivenessChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/** Tells the rules that the light check is running (so a refused session is read as an earlier run even before any stop). */
export function setLivenessWatching(on: boolean): void { watching = on; }

/** The app answered a request (any answer, even a refusal of the request itself): it is there. */
export function noteAnswered(): void {
  misses = 0;
  state.stopped = false;
  state.lastHeardAt = Date.now();
  changed();
}

/** A request never got an answer. Only an `unreachable` failure may be passed in; a time-limit failure is not one. */
export function noteUnreachable(): void {
  misses += 1;
  if (misses >= STOP_AFTER_MISSES) state.stopped = true;
  changed();
}

/** The light check could not reach the app: one is enough. */
export function noteCheckUnreachable(): void {
  state.stopped = true;
  changed();
}

/**
 * The app refused this page's session (401). After a stop, or while the light check runs, that means a newer run has started and this
 * page belongs to the one before. Before any of that (the start screen) the start error already says it, so nothing is added.
 */
export function noteRefused(): void {
  if (!state.stopped && !watching) return;
  misses = 0;
  state.stopped = false;
  state.earlier = true;
  changed();
}

/** For tests: back to a page that has heard nothing yet. */
export function resetLiveness(): void {
  misses = 0;
  watching = false;
  state.stopped = false;
  state.earlier = false;
  state.lastHeardAt = 0;
  changed();
}
