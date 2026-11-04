import { WAIT_TEXT } from './copy/waitCopy';

/**
 * A time limit for reads. A read that is still running after this long is stopped and reported in plain words, so a
 * stalled server never leaves a screen on "Reading..." for ever. Job polls and downloads get no limit.
 */
export const READ_TIMEOUT_MS = 150_000;

/** Code carried by the error a timed-out read throws (`ApiError.code`, `CaseApiError.reason`). */
export const TIMEOUT_CODE = 'timeout';

export interface GuardedSignal {
  /** The signal to give to `fetch`: the caller's signal, the time limit, or both. */
  signal: AbortSignal | undefined;
  /** True when the time limit ended the read and the caller did not cancel it. */
  timedOut: () => boolean;
  /** Releases the timer and listener. Call it once the whole answer has been read. */
  done: () => void;
}

/**
 * Combines the caller's signal with a time limit. Uses AbortSignal.timeout and AbortSignal.any when the browser has them;
 * otherwise an AbortController with a timer. `timeoutMs` null, undefined or not positive means no limit.
 */
export function guardSignal(signal: AbortSignal | undefined, timeoutMs: number | null | undefined): GuardedSignal {
  if (!(typeof timeoutMs === 'number' && timeoutMs > 0)) return { signal, timedOut: () => false, done: () => {} };
  if (typeof AbortSignal !== 'undefined' && typeof AbortSignal.timeout === 'function' && typeof AbortSignal.any === 'function') {
    const limit = AbortSignal.timeout(timeoutMs);
    return {
      signal: signal ? AbortSignal.any([signal, limit]) : limit,
      timedOut: () => limit.aborted && !signal?.aborted,
      done: () => {}
    };
  }
  const controller = new AbortController();
  let expired = false;
  const timer = setTimeout(() => { expired = true; controller.abort(); }, timeoutMs);
  const onCaller = () => controller.abort();
  if (signal) {
    if (signal.aborted) controller.abort();
    else signal.addEventListener('abort', onCaller, { once: true });
  }
  return {
    signal: controller.signal,
    timedOut: () => expired && !signal?.aborted,
    done: () => { clearTimeout(timer); signal?.removeEventListener('abort', onCaller); }
  };
}

/** True for the error `fetch` throws when a signal aborts (a cancel or a time limit). */
export const isAbortLike = (error: unknown): boolean =>
  typeof error === 'object' && error !== null && ((error as { name?: unknown }).name === 'AbortError' || (error as { name?: unknown }).name === 'TimeoutError');

/** True for the error a timed-out read throws, from either request layer. */
export function isTimeoutError(error: unknown): boolean {
  if (!(error instanceof Error)) return false;
  const carrier = error as Error & { code?: unknown; reason?: unknown };
  return carrier.code === TIMEOUT_CODE || carrier.reason === TIMEOUT_CODE;
}

/** The sentence for a read that ran past its time limit. */
export const timeoutMessage = (): string => WAIT_TEXT.timeout;
