import type { LintEntry } from './copylint';
import { waitText } from './masterKeyCopy';
import type { CaseKeyFailure } from '../caseKey';

/**
 * Every fixed sentence of the per-case key screens (7, 8, 9, 125, 126) and the small helpers that build the rest. Pure: no DOM, no fetch.
 * Rules: plain words; say that a case key is separate from the PC key; say that nobody can recover it; one sentence for a refused key
 * ("nothing was opened or changed"); never promise a wait the server does not give; no text here ever holds a key.
 */

export const CASE_KEY_TEXT = {
  // Create step (screens 8 and 9)
  createToggle: 'Protect this case with its own key',
  createHelp: 'Off by default. If you turn it on, only this case needs the key. The PC key is separate.',
  createKeyLabel: 'Case key',
  createKeyHelp: 'At least 12 characters. Unravis cannot recover it.',
  createRepeatLabel: 'Repeat the key',

  // Open a keyed case (screen 7)
  unlockTitle: 'This case has its own key',
  unlockIntro: 'The PC key is not used for this case.',
  unlockLabel: 'Case key',
  unlockHelp: 'At least 12 characters. This case only. The PC key does not open it.',
  unlockSubmit: 'Open case',
  unlocking: 'Opening…',
  unlockCancel: 'Cancel',
  unlockWrong: 'That key did not open this case. Nothing was opened or changed.',
  unlockNote: 'Case keys cannot be recovered. If it is lost, only erasing the case frees the space.',
  lockedTitle: '(locked)',
  lockedEyebrow: 'Case key needed',

  // Keys page (screens 125 and 126)
  pageTitle: 'Case key (optional)',
  pageTitleHas: 'Case key',
  sealedPc: 'Sealed with: PC key.',
  sealedWindows: 'Sealed with: your Windows sign-in.',
  sealedCase: 'Sealed with: Case key.',
  separate: 'A case key is separate from your Windows sign-in and from the PC key.',
  newLabel: 'New case key',
  newHelp: 'At least 12 characters. Unravis cannot recover it and never shows it again.',
  repeatLabel: 'Type it again',
  loseWarning: 'If you lose this key, the case and any archive made from it cannot be opened. There is no reset.',
  lockNote: 'The Lock button in the top bar locks all of Unravis. The PC key is then asked for again. This case asks for its own key too.',
  save: 'Save the case key',
  saving: 'Saving…',
  cancel: 'Cancel',
  saved: 'The case key is saved. This case is open now.',
  currentLabel: 'Current case key',
  currentHelp: 'Needed to change or remove it. Unravis never shows it.',
  changeOption: 'Change to a new case key',
  removeOption: 'Remove the case key',
  removeHelp: 'The case goes back to the PC key. The case is no longer asked for its own key.',
  oldFileNote: 'A case file you made earlier still uses the old key. Keep the old key to open it.',
  saveChange: 'Save the new key',
  removeConfirm: 'Remove the case key',
  currentWrong: 'The current key is not right: nothing changed. Try again.',
  changed: 'The case key is changed.',
  removed: 'The case key is removed. The case is sealed with the PC key now.',
  lockCase: 'Lock this case',
  locking: 'Locking…',
  homeLabel: 'Case key',
  homeAdd: 'Add a case key',
  homeManage: 'Change or remove the case key',

  // Why a call did not finish
  tooShort: 'The case key is too short. Use at least 12 characters.',
  blank: 'Use at least 8 characters that are not spaces or invisible marks.',
  tooLong: 'That key is too long. Use a shorter one.',
  invalidChars: 'The key has a character that cannot be saved. Remove it, or retype the key, then try again.',
  mismatch: 'The two keys are not the same. Type the case key again exactly.',
  inUse: 'The case is in use. Close its other tabs, then try again.',
  erasing: 'This case is being erased.',
  archiving: 'An archive of this case is running. Cancel it first.',
  gone: 'That case is not on this computer any more.',
  noCaseKey: 'This case has no case key.',
  hasCaseKey: 'This case already has a case key. Change or remove it instead.',
  unavailable: 'Case keys are not available in this run.',
  network: 'Unravis could not be reached. Check that it is still running, then try again.',
  appLocked: 'Unravis is locked. Unlock it with the PC key, then try again.',
  server: 'Unravis ran into a problem and could not check the key. Try again.',
  other: 'The case key could not be checked. Try again.',
  throttled: 'Too many tries.'
} as const;

export type CaseKeyContext = 'unlock' | 'current' | 'new';

/**
 * The sentence for a failed key call. A refused key says the key was not right and that nothing changed; a try that was never judged
 * (too many tries, no answer, a server problem) says what happened, so nobody retypes a key that may be right.
 */
export function caseKeyErrorText(failure: CaseKeyFailure, context: CaseKeyContext): string {
  switch (failure.kind) {
    case 'wrong-key': return context === 'unlock' ? CASE_KEY_TEXT.unlockWrong : CASE_KEY_TEXT.currentWrong;
    case 'throttled': return CASE_KEY_TEXT.throttled + ' ' + waitText(failure.retryAfterSeconds ?? 1);
    case 'network': return CASE_KEY_TEXT.network;
    case 'no-case-key': return CASE_KEY_TEXT.noCaseKey;
    case 'has-case-key': return CASE_KEY_TEXT.hasCaseKey;
    case 'in-use': return CASE_KEY_TEXT.inUse;
    case 'erasing': return CASE_KEY_TEXT.erasing;
    case 'archiving': return CASE_KEY_TEXT.archiving;
    case 'gone': return CASE_KEY_TEXT.gone;
    case 'too-short': return CASE_KEY_TEXT.tooShort;
    case 'blank': return CASE_KEY_TEXT.blank;
    case 'too-long': return CASE_KEY_TEXT.tooLong;
    case 'invalid-characters': return CASE_KEY_TEXT.invalidChars;
    case 'mismatch': return CASE_KEY_TEXT.mismatch;
    case 'unavailable': return CASE_KEY_TEXT.unavailable;
    case 'app-locked': return CASE_KEY_TEXT.appLocked;
    case 'server': return CASE_KEY_TEXT.server;
    default: return CASE_KEY_TEXT.other;
  }
}

/** The line under a new key field that the person has got wrong. */
export function newKeyProblemText(problem: 'too-short' | 'blank' | 'too-long' | 'invalid-characters' | 'mismatch'): string {
  switch (problem) {
    case 'too-short': return CASE_KEY_TEXT.tooShort;
    case 'blank': return CASE_KEY_TEXT.blank;
    case 'too-long': return CASE_KEY_TEXT.tooLong;
    case 'invalid-characters': return CASE_KEY_TEXT.invalidChars;
    default: return CASE_KEY_TEXT.mismatch;
  }
}

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const caseKeyLintEntries = (): LintEntry[] => [
  ...Object.entries(CASE_KEY_TEXT).map(([id, text]) => ({ id: 'casekey.' + id, text })),
  { id: 'casekey.sample.throttled', text: caseKeyErrorText({ kind: 'throttled', retryAfterSeconds: 8 }, 'unlock') }
];
