import type { LintEntry } from './copylint';

/**
 * The one sentence for "the page could not reach the Unravis app" (the app was closed, updated or crashed, or the network
 * to it dropped). Every screen that has no wording of its own shows this instead of the browser's "Failed to fetch".
 * Pure: no DOM, no fetch. Rule: say what happened and what the person can do next; never name a cause the page did not observe.
 */
export const UNREACHABLE_MESSAGE = 'Unravis could not be reached. It may have been closed or updated. Start Unravis again, then reload this page.';

/** The code the shared request layer puts on that error (`ApiError.code`; `CaseApiError.reason` carries the same word). */
export const UNREACHABLE_CODE = 'unreachable';

/** True for an error that says "the app could not be reached": the shared error from the request layer, or a case-route error with that reason. */
export function isUnreachableError(cause: unknown): boolean {
  if (!cause || typeof cause !== 'object') return false;
  const carrier = cause as { code?: unknown; reason?: unknown };
  return carrier.code === UNREACHABLE_CODE || carrier.reason === UNREACHABLE_CODE;
}

/** Every fixed sentence above, for the copy lint. */
export const unreachableLintEntries = (): LintEntry[] => [{ id: 'unreachable.message', text: UNREACHABLE_MESSAGE }];
