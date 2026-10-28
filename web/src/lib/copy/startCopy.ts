import type { LintEntry } from './copylint';

/**
 * Every fixed sentence on the start screen, and the small helpers that build the rest. Pure: no DOM, no fetch.
 * Rules: plain words; three cards always shown; a locked screen names no case and shows no count.
 */

export const START_TEXT = {
  title: 'Start an investigation',
  intro: 'Open a case you already have, enter a case code, or create a new case.',
  statusLoading: 'Checking whether your cases are locked…',
  statusFailed: 'Unravis could not check whether your cases are locked. The cases below may be out of date.',

  existingTitle: 'Use an existing case',
  existingText: 'Open a case that is saved on this computer.',
  existingEmpty: 'No cases yet. Create a new case to begin.',
  existingLoading: 'Loading your cases…',
  existingFailed: 'Your cases could not be loaded.',
  retry: 'Try again',
  open: 'Open',
  closedLabel: 'Closed',
  codeLabel: 'Code',
  untrustedLabel: 'Legacy case from an older version',
  showMore: 'Show more cases',
  untitled: 'Untitled case',

  enterTitle: 'Enter an existing case',
  enterText: 'Type a case code to open a case on this computer, or restore one from a case file.',
  enterSoon: 'Entering a case code is not ready yet.',

  createTitle: 'Create a new one',
  createText: 'Start a new case and add the logs and data you want to look at.',
  createButton: 'Create a new case',

  analysisLink: 'Go to the analysis'
} as const;

/** "3 imports" or "1 import". */
export const importsText = (count: number): string => count + (count === 1 ? ' import' : ' imports');

/** "2 sources" or "1 source". */
export const sourcesText = (count: number): string => count + (count === 1 ? ' source' : ' sources');

/** "2 of 120 cases shown" style line under a capped list. */
export function shownText(shown: number, total: number): string {
  return shown + ' of ' + total + ' cases shown.';
}

/** The label a screen reader hears on a case row's button. */
export const openLabel = (title: string): string => 'Open case ' + title;

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const startLintEntries = (): LintEntry[] => [
  ...Object.entries(START_TEXT).map(([id, text]) => ({ id: 'start.' + id, text })),
  { id: 'start.sample.shown', text: shownText(50, 500) },
  { id: 'start.sample.imports', text: importsText(3) },
  { id: 'start.sample.sources', text: sourcesText(2) }
];
