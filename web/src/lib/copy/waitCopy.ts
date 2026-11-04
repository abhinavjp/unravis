import type { LintEntry } from './copylint';

/**
 * Words for reads that take a while: the timeout sentence, the "still reading" line, the Cancel and Try again controls, the
 * pointer from the 7-day Analysis refusal to the longer views, and the lane and daily notes for a read that hit its time limit.
 * Plain words, no jargon. Every string here passes the copy lint (tests/largeWindow.test.cjs).
 */
export const WAIT_TEXT = {
  /** Shown in a screen's error state when a read ran past the request timeout (not when the person cancelled). */
  timeout: 'This is taking longer than expected. Try a shorter window.',
  /** Replaces the plain "Reading..." line once a read has run for a while. */
  stillReading: 'Still reading. A large window can take a minute. You can cancel.',
  cancel: 'Cancel',
  cancelTitle: 'Stop reading. Nothing is changed.',
  retry: 'Try again',
  retryTitle: 'Read the same window again.',
  /** Added after the 7-day Analysis refusal. */
  longerPeriods: 'Use the Timeline or the Daily view for longer periods.',
  /** A lane that returned only part of its events because the read hit its time limit. */
  laneTimeout: 'Showing part of this lane: the read hit its time limit. Try a shorter window.',
  /** The daily and hourly view when a read hit its time limit and returned only part. */
  dailyTimeout: 'Showing part of the daily view: the read hit its time limit. Try a shorter span.',
  /** Said after Cancel in the daily and hourly view, with a Try again button beside it. */
  cancelledRead: 'Cancelled. Choose Try again to read the sources again.',
  /** A live audit lane over 24 hours is not read unless broad scans are allowed. */
  laneBroadScan: 'The live audit source would not read a window this long, so no API requests were read. This is not the same as no traffic. Choose a window of 24 hours or less.',
  /** Said once under the toolbar when a run finished and at least one lane was cut short. */
  lanesCutShort: 'Some lanes were not read in full. What was read is drawn; the rest is marked not read.',
  /** The daily view or step dating when the read ran out of time and only the oldest days were read. Used when the number planned is unknown. */
  daysPartialUnknown: 'Showing only some of the days; the read ran out of time. Shorten the window or Try again.'
} as const;

/** "Showing 12 of 30 days; the read ran out of time. Shorten the window or Try again." `planned` is 0 or less when the server did not say. */
export function partialDaysText(shown: number, planned: number | undefined): string {
  if (!(planned !== undefined && planned > 0 && shown >= 0 && shown <= planned)) return WAIT_TEXT.daysPartialUnknown;
  return `Showing ${shown} of ${planned} days; the read ran out of time. Shorten the window or Try again.`;
}

/** The Analysis run controls (the scope bar). */
export const RUN_TEXT = {
  run: 'Run analysis',
  running: 'Comparing…',
  cancelRun: 'Cancel run',
  cancelRunTitle: 'Stop this run. The results on screen stay as they are.',
  retryRun: 'Try again',
  retryRunTitle: 'Run the analysis again with the same settings.'
} as const;

/** The hour grid and its range choice. */
export const GRID_TEXT = {
  spanLabel: 'Span',
  gridLabelPrefix: 'Requests by hour, shaded by ',
  shadeBy: 'Shade squares by',
  day: 'Day',
  noSuchHour: 'No such hour on this day',
  lower: 'Lower',
  higher: 'Higher',
  fewRequests: 'Few requests',
  partialTag: 'partial'
} as const;

export const ROLLUP_RANGE_LABELS = {
  '1d': 'Last 24 hours',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  '60d': 'Last 60 days',
  '90d': 'Last 90 days',
  '120d': 'Last 120 days'
} as const;

export const waitCopyEntries = (): LintEntry[] => [
  ...Object.entries(WAIT_TEXT).map(([id, text]) => ({ id: 'wait.' + id, text })),
  { id: 'wait.partialDays', text: partialDaysText(12, 30) },
  ...Object.entries(RUN_TEXT).map(([id, text]) => ({ id: 'run.' + id, text })),
  ...Object.entries(GRID_TEXT).map(([id, text]) => ({ id: 'grid.' + id, text })),
  ...Object.entries(ROLLUP_RANGE_LABELS).map(([id, text]) => ({ id: 'range.' + id, text }))
];
