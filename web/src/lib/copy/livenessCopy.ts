import type { LintEntry } from './copylint';

/**
 * The words of the two banners that say the Unravis app is gone (W-02, RV-UI-1 part 2): it stopped, or it was started again and this
 * page belongs to the run before. Pure: no DOM, no fetch. Each sentence says what happened and what to do next, and names no cause
 * the page did not observe (it cannot know whether the app was closed, updated or crashed).
 */
export const LIVENESS_TEXT = {
  stoppedTitle: 'Unravis has stopped.',
  stoppedBody: 'This page can no longer reach it, so nothing here will update. It may have been closed or updated.',
  /** Said only when this page heard from the app at some point; `time` is the clock time on this PC. */
  lastHeard: (time: string): string => `Last heard from it at ${time}.`,
  saved: 'Cases and files you saved stay on this PC.',
  /** The bold lead of the instruction line, then the rest of it. */
  stoppedStep: 'Start Unravis again',
  stoppedStepRest: ' from the Start menu or its tray icon, then press Reload.',
  /** After an update the app comes back on a new address and opens its own page; this old page cannot follow it (the update bar's lost-app note is hidden while the banner shows). */
  newPage: 'A new Unravis page may have opened: use that one.',
  reload: 'Reload this page',
  earlierTitle: 'Unravis was restarted.',
  earlierBody: 'This page was opened before that, so it can no longer talk to Unravis.',
  earlierStep: 'Open Unravis again from the tray icon',
  earlierStepRest: ' and use the new page. You can close this one.'
} as const;

/** The clock time on this PC, as hours and minutes, for "Last heard from it at 14:32." Empty when the clock cannot be read. */
export function heardAtText(ms: number): string {
  if (!ms) return '';
  try { return new Date(ms).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }); } catch { return ''; }
}

/** Every fixed sentence above (as it reads on screen), for the copy lint. */
export const livenessLintEntries = (): LintEntry[] => [
  { id: 'liveness.stopped.title', text: LIVENESS_TEXT.stoppedTitle },
  { id: 'liveness.stopped.body', text: LIVENESS_TEXT.stoppedBody },
  { id: 'liveness.stopped.lastHeard', text: LIVENESS_TEXT.lastHeard('14:32') },
  { id: 'liveness.stopped.saved', text: LIVENESS_TEXT.saved },
  { id: 'liveness.stopped.step', text: LIVENESS_TEXT.stoppedStep + LIVENESS_TEXT.stoppedStepRest },
  { id: 'liveness.stopped.newPage', text: LIVENESS_TEXT.newPage },
  { id: 'liveness.earlier.title', text: LIVENESS_TEXT.earlierTitle },
  { id: 'liveness.earlier.body', text: LIVENESS_TEXT.earlierBody },
  { id: 'liveness.earlier.step', text: LIVENESS_TEXT.earlierStep + LIVENESS_TEXT.earlierStepRest }
];
