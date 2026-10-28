/**
 * Copy for the guided shell and the Manual tools that guidedCopy.ts does not hold. One place, plain sentences, no dashes, no emoji.
 * Every key is a string or a function of one string or number, so the lint test in scn1.l5.test.cjs can read them all.
 */
export const SHELL_TEXT = {
  manualHeading: 'Manual tools',
  toolWaitingHint: 'Press Run the steps first. This tool shows what that run found.'
} as const;
