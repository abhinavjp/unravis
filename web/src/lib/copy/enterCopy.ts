import type { LintEntry } from './copylint';
import { waitText } from './masterKeyCopy';

/**
 * Every fixed sentence of the "Enter an existing case" card, and the small helpers that build the rest. Pure: no DOM, no fetch.
 * Rules: plain words; one message for every case code that does not open, one message for every case file that does not open; say that
 * restoring always makes a new case; no text here ever holds a master key, a path or a code that was typed.
 */

/** The one sentence for a code that is malformed, unknown, erased, or one the server could not answer. The screen never says which. */
export const CODE_NOT_FOUND = 'That code does not match a case on this computer.';

/** The one sentence for a case file that does not open: a wrong master key, a wrong path, a damaged file, or anything else about the file. */
export const RESTORE_FAILED = 'That file could not be opened with that master key. Check the path and the master key, then try again.';

export const ENTER_TEXT = {
  // The code
  codeLabel: 'Case code',
  codeHint: 'A case code has 26 letters and numbers. Spaces and dashes are fine.',
  codeButton: 'Open case',
  codeChecking: 'Looking for the case…',

  // The case file
  restoreToggle: 'Restore from a case file',
  restoreIntro: 'A case file is one encrypted file with a copy of a case. Restoring it always makes a new case. It never changes a case you already have.',
  pathLabel: 'Path of the case file',
  pathHint: 'The full path of a file that ends in .unravis-case. In File Explorer, select the file and press Ctrl+Shift+C to copy its path.',
  keyLabel: 'Master key of the PC it was saved on',
  keyHint: 'Type the master key that was used when the case file was made. It is not kept after you press the button.',
  restoreButton: 'Restore as a new case',
  restoring: 'Restoring…',

  // Before the button can be pressed
  pathEmpty: 'Type the path of the case file.',
  pathName: 'The file name must end in .unravis-case.',
  pathShape: 'Use the full path on this computer, such as C:\\Cases\\mycase.unravis-case. Network paths are not accepted.',
  pathTooLong: 'That path is too long. Move the file to a folder with a shorter path, then try again.',
  keyEmpty: 'Type the master key the case file was saved with.',

  // After the server answered
  needKey: 'Restoring needs a master key on this PC too. Set one up first.',
  locked: 'Your cases are locked. Unlock them, then try again.',
  damaged: 'The master key file on this PC is damaged, so cases cannot be opened or restored. Open the unlock screen to start over.',
  busy: 'Another case file is being saved or restored. Try again in a moment.',
  network: 'Unravis could not be reached. Check that it is still running, then try again.',
  badRequest: 'Unravis could not read the path or the master key as typed. Check the path, which can be at most 520 bytes long, then try again.',
  other: 'The case file could not be restored. Try again.',
  throttled: 'Too many tries.'
} as const;

/** The text for a throttled restore: the same first sentence for everyone, then how long to wait. */
export const throttledText = (seconds: number): string => ENTER_TEXT.throttled + ' ' + waitText(seconds);

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const enterLintEntries = (): LintEntry[] => [
  { id: 'enter.codeNotFound', text: CODE_NOT_FOUND },
  { id: 'enter.restoreFailed', text: RESTORE_FAILED },
  ...Object.entries(ENTER_TEXT).map(([id, text]) => ({ id: 'enter.' + id, text })),
  { id: 'enter.sample.throttled', text: throttledText(4) },
  { id: 'enter.sample.throttledMinute', text: throttledText(60) }
];
