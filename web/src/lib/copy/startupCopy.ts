import type { LintEntry } from './copylint';

/**
 * What the page says when it cannot start, and the rule that picks the sentence. Pure: no DOM, no fetch.
 * Rule: say what the person can do next. Never name a cause the page did not observe.
 */

export const STARTUP_TITLE = 'Explorer unavailable';

export const STARTUP_TEXT = {
  noSession: 'This tab has no session. Open Unravis again from the launch link the app printed, or restart the app to get a new one.',
  sessionRefused: 'This session is no longer valid. The app may have been restarted. Open the newest launch link.',
  unreachable: 'Can’t reach the Unravis app. Check it is still running, then retry.',
  /** Used when nothing more specific is known and the error carries no text of its own. */
  fallback: 'The request could not be completed.'
} as const;

export type StartupFailureKind = 'noSession' | 'sessionRefused' | 'unreachable' | 'other';

export interface StartupFailure {
  kind: StartupFailureKind;
  message: string;
  /** Retry helps only when the server may answer differently the next time. A missing or refused session needs a new launch link instead. */
  canRetry: boolean;
}

const NETWORK_TEXT = /failed to fetch|networkerror|network request failed|load failed|network error|fetch failed/i;

function statusOf(cause: unknown): number | undefined {
  const value = cause && typeof cause === 'object' ? (cause as { status?: unknown }).status : undefined;
  return typeof value === 'number' ? value : undefined;
}

/** A browser reports an unreachable server as a TypeError from fetch (the text differs by browser). An aborted request is not one. */
function isNetworkError(cause: unknown): boolean {
  if (!(cause instanceof Error) || cause.name === 'AbortError') return false;
  if (statusOf(cause) !== undefined) return false;
  return cause.name === 'TypeError' || NETWORK_TEXT.test(cause.message);
}

/**
 * Picks the startup message. `hadToken` is whether this tab held a session token when the request began: a 401 clears the token,
 * so it must be read before the call, not after. A token that was absent decides the message even if the call never left the page.
 */
export function classifyStartupFailure(input: { hadToken: boolean; cause: unknown }): StartupFailure {
  const { hadToken, cause } = input;
  const status = statusOf(cause);
  const code = cause && typeof cause === 'object' ? (cause as { code?: unknown }).code : undefined;
  if (!hadToken && (code === 'no_session' || status === 401 || status === 403)) return { kind: 'noSession', message: STARTUP_TEXT.noSession, canRetry: false };
  if (hadToken && (status === 401 || status === 403 || code === 'no_session')) return { kind: 'sessionRefused', message: STARTUP_TEXT.sessionRefused, canRetry: false };
  if (isNetworkError(cause)) return { kind: 'unreachable', message: STARTUP_TEXT.unreachable, canRetry: true };
  const own = cause instanceof Error ? cause.message.trim() : '';
  return { kind: 'other', message: own || STARTUP_TEXT.fallback, canRetry: true };
}

/** The heading over a failed comparison. A filter or window the page or the server rejected says so; any other failure keeps the plain label. */
export const COMPARISON_NOT_RUN = 'Comparison not run';
export const COMPARISON_NOT_RUN_FILTER = 'Comparison not run: fix the filter first';
export function comparisonLabel(error: string, filterTexts: readonly (string | null | undefined)[]): string {
  return error && filterTexts.some(text => text && text === error) ? COMPARISON_NOT_RUN_FILTER : COMPARISON_NOT_RUN;
}

/** Every fixed sentence above, for the copy lint. */
export const startupLintEntries = (): LintEntry[] => [
  ...Object.entries(STARTUP_TEXT).map(([id, text]) => ({ id: 'startup.' + id, text })),
  { id: 'startup.comparisonFilter', text: COMPARISON_NOT_RUN_FILTER }
];
