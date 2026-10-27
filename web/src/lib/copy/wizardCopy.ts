import type { LintEntry } from './copylint';
import type { EnvDropped } from '../wizardEnvs';
import type { StepId, TitleProblem } from '../wizardFlow';

/**
 * Every fixed sentence of the create wizard shell (steps, name and environments, leaving), and the small helpers that build the rest.
 * Pure: no DOM, no fetch. Rules: plain words; say that nothing is saved until the last step; environments are the person's own words;
 * the scan reads this PC only (the sources step says so); no text here ever holds something the person typed except a name they chose.
 */

export const WIZARD_TEXT = {
  title: 'Create a new case',
  stepsLabel: 'Steps of the guide',
  stepsNote: 'Nothing is saved until the last step.',

  leave: 'Leave the guide',
  leaveAskTitle: 'Leave without creating a case?',
  leaveAskText: 'What you typed here will be thrown away. Nothing has been saved yet.',
  leaveYes: 'Leave and discard',
  leaveNo: 'Keep working',

  resumed: 'Your earlier answers are still here. Nothing has been saved yet.',
  startOver: 'Start over',

  back: 'Back',
  next: 'Next',
  nextBlocked: 'Give the case a name to go on.',

  nameHeading: 'Name your case',
  nameIntro: 'Pick a name you will recognise later, such as the problem or the week. A case can cover one or several environments.',
  titleLabel: 'Case name',
  titleHint: 'Use 2 to 80 characters. You can rename it later. Do not put passwords or customer names here.',

  envsLabel: 'Environments (optional)',
  envsHint: 'Name them any way you think about them, with commas between them. A name can be a stage, such as Production, or a client, such as Client A sandbox. One server can host several, so one case can hold them all. You can leave this blank and name them later.',
  envsPlaceholder: 'Client A production, Client A staging',
  envsListLabel: 'Environments in this case',
  envsEmpty: 'No environments named yet. You can name them later.',
  envsTag: 'Every source you add next is tagged with an environment, then a portal, then an API. Leave any of them blank if you do not know it yet.',

  sourcesHeading: 'Choose what to look at',
  sourcesSoon: 'The list of sources is not ready yet.',

  reviewHeading: 'Check and create',
  reviewSoon: 'The check and create step is not ready yet.',

  placeholder: 'The case will be created only on the last step.'
} as const;

/** Heading and short tab label for each step. */
export const STEP_TEXT: Readonly<Record<StepId, { label: string; heading: string }>> = {
  name: { label: 'Name', heading: WIZARD_TEXT.nameHeading },
  sources: { label: 'Sources', heading: WIZARD_TEXT.sourcesHeading },
  review: { label: 'Review', heading: WIZARD_TEXT.reviewHeading }
};

/** "Step 2 of 3: Sources". */
export const stepOfText = (number: number, total: number, label: string): string => 'Step ' + number + ' of ' + total + ': ' + label;

/** The label a screen reader hears on a step in the strip. */
export function stepButtonLabel(number: number, label: string, state: 'current' | 'done' | 'open' | 'locked'): string {
  const suffix = state === 'current' ? ', current step' : state === 'done' ? ', done' : state === 'locked' ? ', not available until the case has a name' : '';
  return 'Step ' + number + ', ' + label + suffix;
}

export const TITLE_PROBLEM_TEXT: Readonly<Record<TitleProblem, string>> = {
  empty: 'Type a name for the case.',
  'too-short': 'The name needs at least 2 characters.',
  'too-long': 'The name can have at most 80 characters.',
  'bad-characters': 'The name has a character that is not allowed. Remove it and try again.'
};

/** The line beside a blocked Next: the problem with the name when there is one worth naming, else the plain "give it a name". */
export const nextBlockedLine = (problem: TitleProblem | null): string => (problem === null || problem === 'empty' ? WIZARD_TEXT.nextBlocked : TITLE_PROBLEM_TEXT[problem]);

/** "12 of 80". */
export const titleCountText = (length: number, max: number): string => length + ' of ' + max;

/** "1 source chosen". */
export const chosenText = (count: number): string => count + (count === 1 ? ' source chosen' : ' sources chosen');

/** "3 environments" or "1 environment". */
export const envCountText = (count: number): string => count + (count === 1 ? ' environment' : ' environments');

const SHOWN = 3;

/** Up to three names, then "and 4 more". Names are the person's own words, shown as typed (cleaned). */
export function nameList(names: readonly string[]): string {
  if (names.length <= SHOWN) return names.join(', ');
  return names.slice(0, SHOWN).join(', ') + ' and ' + (names.length - SHOWN) + ' more';
}

/** One sentence for each kind of change the environments field went through. Empty when nothing was left out or changed. */
export function envNotes(dropped: EnvDropped): string[] {
  const notes: string[] = [];
  if (dropped.duplicates.length) notes.push('Left out, as the name is already in the list: ' + nameList(dropped.duplicates) + '.');
  if (dropped.overflow.length) notes.push('A case holds at most 32 environments. Left out: ' + nameList(dropped.overflow) + '.');
  if (dropped.shortened.length) notes.push('Shortened to 60 characters: ' + nameList(dropped.shortened) + '.');
  if (dropped.changed.length) notes.push('The sign › is not used in names, so it became a dash: ' + nameList(dropped.changed) + '.');
  return notes;
}

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const wizardLintEntries = (): LintEntry[] => [
  ...Object.entries(WIZARD_TEXT).map(([id, text]) => ({ id: 'wizard.' + id, text })),
  ...Object.entries(TITLE_PROBLEM_TEXT).map(([id, text]) => ({ id: 'wizard.title.' + id, text })),
  ...(Object.entries(STEP_TEXT) as [StepId, { label: string; heading: string }][]).map(([id, text]) => ({ id: 'wizard.step.' + id, text: text.heading })),
  { id: 'wizard.sample.stepOf', text: stepOfText(2, 3, 'Sources') },
  { id: 'wizard.sample.stepLabel', text: stepButtonLabel(3, 'Review', 'locked') },
  { id: 'wizard.sample.chosen', text: chosenText(2) },
  ...envNotes({ duplicates: ['A', 'B', 'C', 'D'], overflow: ['E'], shortened: ['F'], changed: ['G'] }).map((text, i) => ({ id: 'wizard.sample.note' + i, text }))
];
