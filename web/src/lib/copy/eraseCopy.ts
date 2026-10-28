import type { LintEntry } from './copylint';

/**
 * Every fixed sentence of the Erase dialog (screens 138 and 139), the untrusted case page (123) and the Erased lines (124), and the
 * small helpers that build the rest. Pure: no DOM, no fetch. A line about an erased case holds an id and a time and nothing else.
 */

export const ERASE_TEXT = {
  open: 'Erase this case',
  title: 'Erase this case',
  removes: 'This removes the working copy, notes, suspects and checklist from this computer.',
  stops: 'An import or recording for this case stops.',
  archiveKept: 'A case file you saved is not touched.',
  final: 'This cannot be undone.',
  confirm: 'Erase this case',
  erasing: 'Erasing…',
  waiting: 'Erasing. This can take up to 30 seconds if the case is still open elsewhere.',
  cancel: 'Cancel',
  back: 'Back to the list',
  doneKept: 'Any case file you saved was not touched.',
  failed: 'The case could not be erased just now. Press Erase this case again to finish.',
  network: 'Unravis could not be reached. Check that it is still running, then try again.',
  mismatch: 'The code typed does not match this case.',

  // Untrusted case (screen 123)
  untrustedEyebrow: 'Legacy case from an older version',
  untrustedTitle: 'This case cannot be opened.',
  untrustedWhy: 'It was made by an older version that did not seal cases, so Unravis cannot tell whether it was changed.',
  untrustedSafe: 'Nothing was read and nothing was changed. If you have a case file you made yourself, restore that instead.',
  restore: 'Restore from a case file',

  // The list after an erase (screen 124)
  erasedSticker: 'Erased',
  onlyLine: 'Only this line is left.',
  clear: 'Clear',
  clearFailed: 'The line could not be cleared. Try again.'
} as const;

/** "Type the case code k7m2qx4a to erase". */
export const erasePromptText = (code: string): string => 'Type the case code ' + code + ' to erase';

/** The first sentence of the dialog: what goes, and where a saved case file stays. A path is shown only when the page knows it. */
export function eraseBody(archivePath?: string): string {
  const kept = archivePath ? 'The case file in ' + archivePath + ' is not touched.' : ERASE_TEXT.archiveKept;
  return [ERASE_TEXT.removes, ERASE_TEXT.stops, kept, ERASE_TEXT.final].join(' ');
}

/** After the erase: "k7m2qx4a is removed from this computer. Any case file you saved was not touched." */
export const eraseDoneText = (code: string): string => code + ' is removed from this computer. ' + ERASE_TEXT.doneKept;

/** "Erased case q2w7exak · erased 20 Oct 09:12 UTC. Only this line is left." `when` is the formatted time. */
export const erasedLineText = (code: string, when: string): string => 'Erased case ' + code + ' · erased ' + when + '. ' + ERASE_TEXT.onlyLine;

export const eraseLintEntries = (): LintEntry[] => [
  ...Object.entries(ERASE_TEXT).map(([id, text]) => ({ id: 'erase.' + id, text })),
  { id: 'erase.sample.prompt', text: erasePromptText('k7m2qx4a') },
  { id: 'erase.sample.body', text: eraseBody() },
  { id: 'erase.sample.bodyPath', text: eraseBody('D:\\Unravis archive') },
  { id: 'erase.sample.done', text: eraseDoneText('k7m2qx4a') },
  { id: 'erase.sample.line', text: erasedLineText('q2w7exak', '20 Oct 09:12 UTC') }
];
