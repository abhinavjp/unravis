/**
 * One readiness vocabulary (gap G14). A source, a step card and a wizard chip all say one of these four words, and nothing else:
 * Ready, Partly ready, Cannot test yet, Failed. This file maps the states of the places that kept their own words onto them.
 * Pure: no DOM, no fetch. The data model is unchanged; only the word shown is chosen here.
 */

export const READINESS_WORDS = ['Ready', 'Partly ready', 'Cannot test yet', 'Failed'] as const;
export type ReadinessWord = typeof READINESS_WORDS[number];

export const isReadinessWord = (value: unknown): value is ReadinessWord => READINESS_WORDS.includes(value as ReadinessWord);

/** A word the page does not know is shown as Cannot test yet: it is never read as a pass. */
export const toReadinessWord = (value: unknown): ReadinessWord => (isReadinessWord(value) ? value : 'Cannot test yet');

/** The scan states of a found source in the create wizard (see entryState in wizardSources.ts). */
export type FoundState = 'ready' | 'unverified' | 'steps';

/**
 * A verified source is Ready. One Unravis found but could not read yet, and one that needs an export first, both Cannot test yet:
 * no check ran on either. The row's own sentence says which of the two it is.
 */
export function foundReadiness(state: FoundState): ReadinessWord {
  return state === 'ready' ? 'Ready' : 'Cannot test yet';
}
