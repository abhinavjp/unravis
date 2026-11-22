// One idea of "the page is hidden" for everything that runs on a timer (BL-429, spec 07 D20).
//
// A hidden tab or a minimised window has nobody looking at it, so a poll that only feeds the screen has no reason to run: it costs a
// request, a wake-up and a repaint for nothing. Browsers slow hidden timers down by themselves, but not for a page that is playing audio, holds a
// connection or an IndexedDB lock, and a WebView2 window behaves differently again, so the page stops its own timers.
//
// Two kinds of timer, two helpers:
//   visiblePoll   a poll that only feeds the screen (status line, clocks, download progress). Stops while hidden; one run on show if it is
//                 due. Nothing is sent while hidden.
//   pacedTimeout  a wait for work the person started (an import, an archive, a scan). The job runs on the server whether or not the page
//   pacedSleep    is shown, and a batch of files goes on to its next file when the answer comes, so these slow down (HIDDEN_JOB_POLL_MS)
//                 and wake at once on show instead of stopping.
//
// Not touched here: the page-presence ping (updateState.svelte.ts). It already sends nothing while hidden, which is what the silent updater
// reads as "nobody is using the app" (internal/server/updater_auto.go, presenceWindow); stopping its timer while hidden only removes a
// wake-up that did nothing.

/** How often a job that the person started is asked about while the page is hidden (visible: the caller's own interval, one to two seconds). */
export const HIDDEN_JOB_POLL_MS = 5000;

type Listener = () => void;

/** True when the page cannot be seen (hidden tab, minimised window). Anything other than "visible" counts, as the presence ping does. */
export function isHidden(): boolean {
  return typeof document !== 'undefined' && document.visibilityState !== 'visible';
}

/** Runs `listener` on every visibilitychange. Returns the function that stops it. */
export function onVisibilityChange(listener: Listener): () => void {
  if (typeof document === 'undefined') return () => {};
  document.addEventListener('visibilitychange', listener);
  return () => document.removeEventListener('visibilitychange', listener);
}

/**
 * Calls `start` now when the page is visible and on every change to visible; calls `stop` on every change to hidden.
 * Returns the unsubscribe function, which also calls `stop`.
 */
export function onVisible(start: () => void, stop: () => void): () => void {
  let running = false;
  const go = () => { if (!running) { running = true; start(); } };
  const halt = () => { if (running) { running = false; stop(); } };
  const sync = () => { if (isHidden()) halt(); else go(); };
  const off = onVisibilityChange(sync);
  sync();
  return () => { off(); halt(); };
}

export interface VisiblePollOptions {
  /** Run once at once (when visible) instead of after the first interval. Default false. */
  immediate?: boolean;
  /** A clock, for tests. */
  now?: () => number;
}

/**
 * Runs `run` every `intervalMs` while the page is visible, and not at all while it is hidden.
 *  - The next run is timed from the end of the last one, so a slow answer never piles up requests.
 *  - On show, a run that is due (a full interval has passed since the last one) happens at once; otherwise the wait goes on for what is left.
 *  - `run` may return false (or a promise of false) to end the poll for good.
 * Returns the function that stops it.
 */
export function visiblePoll(run: () => void | boolean | Promise<void | boolean>, intervalMs: number, options: VisiblePollOptions = {}): () => void {
  const now = options.now ?? Date.now;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let last = options.immediate ? -Infinity : now();
  let alive = true;
  let busy = false;

  const clear = () => { if (timer !== undefined) { clearTimeout(timer); timer = undefined; } };
  const arm = () => {
    clear();
    if (!alive || busy || isHidden()) return;
    timer = setTimeout(fire, Math.max(0, last + intervalMs - now()));
  };
  const fire = async () => {
    timer = undefined;
    if (!alive || busy || isHidden()) return;
    busy = true;
    last = now();
    let again: void | boolean = true;
    try { again = await run(); } catch { /* the caller's own run handles its errors; a thrown one must not end the poll */ }
    busy = false;
    if (again === false) { alive = false; clear(); off(); return; }
    arm();
  };
  const off = onVisibilityChange(() => { if (isHidden()) clear(); else arm(); });
  arm();
  return () => { alive = false; clear(); off(); };
}

/**
 * setTimeout for a wait that goes on while hidden, but slower: `ms` while visible, at least `hiddenMs` while hidden. When the page is
 * shown again while the wait was slowed, `fn` runs at once. Returns the function that cancels it.
 */
export function pacedTimeout(fn: () => void, ms: number, hiddenMs: number = HIDDEN_JOB_POLL_MS): () => void {
  let slowed = isHidden();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let done = false;
  const finish = () => { if (done) return; done = true; if (timer !== undefined) clearTimeout(timer); off(); fn(); };
  const off = onVisibilityChange(() => { if (isHidden()) slowed = true; else if (slowed) finish(); });
  timer = setTimeout(finish, slowed ? Math.max(ms, hiddenMs) : ms);
  return () => { if (done) return; done = true; if (timer !== undefined) clearTimeout(timer); off(); };
}

/** A promise that settles after `ms` (see pacedTimeout) and rejects with an AbortError when `signal` aborts. */
export function pacedSleep(ms: number, signal?: AbortSignal, hiddenMs: number = HIDDEN_JOB_POLL_MS): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException('Aborted', 'AbortError')); return; }
    const onAbort = () => { cancel(); reject(new DOMException('Aborted', 'AbortError')); };
    const cancel = pacedTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve(); }, ms, hiddenMs);
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

let marking = false;
/**
 * Keeps `data-page-hidden` on the root element in step with the page, so the stylesheet can pause every running CSS animation
 * while nobody sees it. Safe to call twice.
 */
export function markPageVisibility(): void {
  if (marking || typeof document === 'undefined') return;
  marking = true;
  const apply = () => { document.documentElement.toggleAttribute('data-page-hidden', isHidden()); };
  onVisibilityChange(apply);
  apply();
}
