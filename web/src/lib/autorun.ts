import { countEpochBuckets } from './time';
import type { Query } from './types';

/**
 * Scope changes (a range chip, a request type, a source, a filter) run the analysis by themselves. These helpers decide when that is
 * allowed and coalesce a burst of changes into one run. They are pure so the rules can be tested without a browser.
 */
export const AUTO_RUN_DELAY_MS = 300;
/** The landing view only runs by itself for a primary window up to this long; wider windows (a restored 7-day snapshot) wait for a click. */
export const LANDING_MAX_WINDOW_MS = 24 * 3_600_000;
const DAY_MS = 86_400_000;

type ScopeShape = Pick<Query, 'sourceIds' | 'start' | 'end' | 'baselineStart' | 'baselineEnd' | 'bucketSeconds'>;

/** The first reason the scope cannot be analysed, in the words the run button reports; null when it can run. Same checks, same order as a manual run. */
export function scopeProblem(query: ScopeShape, datesValid: boolean): string | null {
  if (!datesValid) return 'Complete every date and time with a valid local value before running a comparison.';
  if (!query.sourceIds.length) return 'Select at least one source before running a comparison.';
  const start = new Date(query.start).getTime(), end = new Date(query.end).getTime();
  const baselineStart = new Date(query.baselineStart).getTime(), baselineEnd = new Date(query.baselineEnd).getTime();
  if (!(start < end) || !(baselineStart < baselineEnd)) return 'Each window needs a start time earlier than its end time.';
  if (end - start > 7 * DAY_MS || baselineEnd - baselineStart > 7 * DAY_MS) return 'Each window can be at most 7 days.';
  if (baselineEnd > start) return 'The reference window must end at or before the current window starts.';
  if (!(Number.isInteger(query.bucketSeconds) && query.bucketSeconds >= 10 && query.bucketSeconds <= 86400)) return 'Bucket size must be a whole number between 10 and 86,400 seconds.';
  if (countEpochBuckets(start, end, query.bucketSeconds) > 120 || countEpochBuckets(baselineStart, baselineEnd, query.bucketSeconds) > 120) return 'This bucket size creates more than 120 time points. Increase the bucket size or shorten a window.';
  return null;
}

/** The short hint shown beside the run button while the scope is incomplete: the cause, not an error banner. */
export function scopeHint(query: ScopeShape, datesValid: boolean): string {
  if (!query.sourceIds.length) return 'Choose a source to run the analysis.';
  if (!datesValid) return 'Complete the dates to run the analysis.';
  // Same checks and order as scopeProblem, one short plain sentence each, so a scope that cannot run is never silently left un-run.
  const start = new Date(query.start).getTime(), end = new Date(query.end).getTime();
  const baselineStart = new Date(query.baselineStart).getTime(), baselineEnd = new Date(query.baselineEnd).getTime();
  if (!(start < end) || !(baselineStart < baselineEnd)) return 'Each window must start before it ends.';
  if (end - start > 7 * DAY_MS || baselineEnd - baselineStart > 7 * DAY_MS) return 'Each window can be at most 7 days.';
  if (baselineEnd > start) return 'The reference window must end before the current window starts.';
  if (!(Number.isInteger(query.bucketSeconds) && query.bucketSeconds >= 10 && query.bucketSeconds <= 86400)) return 'Bucket size must be 10 to 86,400 seconds.';
  if (countEpochBuckets(start, end, query.bucketSeconds) > 120 || countEpochBuckets(baselineStart, baselineEnd, query.bucketSeconds) > 120) return 'That bucket size makes over 120 time points; use a larger bucket or a shorter window.';
  return '';
}

/** Whether the landing view may run without a click: only a primary window of at most 24 hours. An unparseable or empty window never does. */
export function landingMayAutoRun(query: Pick<Query, 'start' | 'end'>): boolean {
  const span = new Date(query.end).getTime() - new Date(query.start).getTime();
  return Number.isFinite(span) && span > 0 && span <= LANDING_MAX_WINDOW_MS;
}

export interface AutoRunState {
  /** scopeProblem for the current scope; any problem blocks an automatic run. */
  problem: string | null;
  /** The fingerprint of the scope as it stands now. */
  signature: string;
  /** The fingerprint of the analysis that is on screen, if any. */
  shown: string | null;
  /** The fingerprint of the run in flight, if any. */
  inflight: string | null;
}

/** Run only when the scope is valid and is not already what is on screen or being fetched; re-selecting the active scope is a no-op. */
export function autoRunDecision(state: AutoRunState): 'run' | 'skip' {
  if (state.problem) return 'skip';
  if (state.inflight != null) return state.inflight === state.signature ? 'skip' : 'run';
  return state.shown === state.signature ? 'skip' : 'run';
}

export interface Timers { set: (fn: () => void, ms: number) => unknown; clear: (handle: unknown) => void }
const realTimers: Timers = { set: (fn, ms) => setTimeout(fn, ms), clear: handle => clearTimeout(handle as ReturnType<typeof setTimeout>) };

export interface Coalescer { schedule(): void; cancel(): void; readonly pending: boolean }

/** Collapses a burst of schedule() calls into one run after the quiet period; each call restarts the wait, so the last change wins. */
export function createCoalescer(run: () => void, delayMs = AUTO_RUN_DELAY_MS, timers: Timers = realTimers): Coalescer {
  let handle: unknown = null;
  let pending = false;
  const cancel = () => { if (pending) timers.clear(handle); handle = null; pending = false; };
  return {
    schedule() {
      cancel();
      pending = true;
      handle = timers.set(() => { handle = null; pending = false; run(); }, delayMs);
    },
    cancel,
    get pending() { return pending; }
  };
}
