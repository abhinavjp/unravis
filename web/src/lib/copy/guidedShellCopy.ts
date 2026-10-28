/**
 * Copy for the guided shell and the Manual tools that guidedCopy.ts does not hold. One place, plain sentences, no dashes, no emoji.
 * Every key is a string or a function of one string or number, so the lint test in scn1.l5.test.cjs can read them all.
 */
export const SHELL_TEXT = {
  manualHeading: 'Manual tools',
  toolWaitingHint: 'Press Run the steps first. This tool shows what that run found.',
  sourcesLoading: 'Reading the sources this case holds.',
  sourcesFailed: 'The sources of this case could not be read. Reload the page and try again.',
  likelyNoTier: 'A suspect can be listed as Likely while no tier is dated. The data fits it, but Confidence stays None and no origin is named.',
  showRuledOut: 'Show the suspects set aside',
  showFewerSuspects: 'Show fewer suspects',
  suggestedAdded: (n: number) => (n === 1 ? 'Added 1 check.' : `Added ${n} checks.`)
} as const;
