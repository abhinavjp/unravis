import type { LintEntry } from './copylint';

/**
 * Words for the Investigation Timeline (WP-16a): the entry card on the case home page, the screen shell, and
 * the sentence shown for every refusal the tracing route can send. The screen itself is wired up in WP-16b.
 */
const CLOSED_REASON = 'This case is closed. You can read it, but nothing can be added.';

export const TRACING_TEXT = {
  heading: 'Investigation Timeline',
  entryNote: 'See the API audit, SQL and IIS on one timeline for your time window, next to a period to compare with.',
  entryButton: 'Open the Investigation Timeline',
  backToCase: 'Case home',
  readingWindow: 'Reading the window…',
  retry: 'Try again',
  cancel: 'Cancel',
  tracing: 'Tracing...',
  chartLabel: 'Timeline chart',
  emptyNoSources: 'No files have been added to this case yet. Add a source first.',
  closedView: 'This case is closed. You can look at the timeline but not change the case.',

  errBadWindow: 'The window or bucket size is not allowed.',
  errLocked: 'Unlock the case key to trace.',
  errUnavailable: 'The case store is unavailable right now.',
  errServer: 'The case could not be read right now.',
  errClosed: CLOSED_REASON
} as const;

export const tracingCopyEntries = (): LintEntry[] =>
  Object.entries(TRACING_TEXT).map(([id, text]) => ({ id: 'tracing.' + id, text }));
