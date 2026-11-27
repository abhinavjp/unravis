import { isUnreachableError } from './copy/unreachableCopy';
import { livenessSnapshot, noteAnswered, noteCheckUnreachable, noteRefused, onLivenessChange, setLivenessWatching, type LivenessState } from './livenessCore';
import { visiblePoll } from './visibility';

// Whether this page can still reach the Unravis app, as the screens see it (W-02, RV-UI-1 part 2). The rules and counters are in
// livenessCore.ts, which the request layer reports into; this file mirrors them into reactive state and runs the light check.

export { noteAnswered, noteRefused, noteUnreachable, resetLiveness, STOP_AFTER_MISSES } from './livenessCore';

/** How often the light check runs while the page can be seen. */
export const LIVENESS_CHECK_MS = 20_000;

/** The reactive state: `stopped` (no answer), `earlier` (answers, but refuses this page), `lastHeardAt`. */
export const liveness = $state<LivenessState>(livenessSnapshot());
onLivenessChange(() => { Object.assign(liveness, livenessSnapshot()); });

/**
 * What one failed light check says. A lost connection says stopped at once; a refused session says earlier run; an error answer from the
 * app (a 500) says the app is there; a time limit, a cancel or anything else is no evidence either way.
 */
function noteCheckFailed(cause: unknown): void {
  const carrier = (cause && typeof cause === 'object' ? cause : {}) as { code?: unknown; status?: unknown };
  if (isUnreachableError(cause)) noteCheckUnreachable();
  else if (carrier.code === 'no_session' || carrier.status === 401) noteRefused();
  else if (typeof carrier.status === 'number' && carrier.status >= 100) noteAnswered();
}

/**
 * Starts the light check: `probe` is a cheap read of the app (it goes through the shared request layer), run every 20 seconds while
 * the page is visible and never while it is hidden. It stops itself once the page is known to belong to an earlier run, because
 * nothing can bring it back. A read that ran out of time neither raises nor clears the alarm. Returns the function that stops it.
 */
export function startLivenessWatch(probe: () => Promise<unknown>): () => void {
  setLivenessWatching(true);
  const stop = visiblePoll(async () => {
    if (liveness.earlier) return false;
    try { await probe(); noteAnswered(); } catch (cause) { noteCheckFailed(cause); }
    return !liveness.earlier;
  }, LIVENESS_CHECK_MS);
  return () => { setLivenessWatching(false); stop(); };
}
