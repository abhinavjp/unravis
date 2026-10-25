import type { LintEntry } from './copylint';

/**
 * Every fixed sentence on the master key screens, and the small helpers that build the rest. Pure: no DOM, no fetch.
 * Rules: plain words; say what the key protects, that it is not sent anywhere, that it is asked once each launch, and that nobody can
 * recover it. The strength hint reads length and mix only and never claims the key is safe. No text here ever holds a key.
 */

/** The sentence for a key the server judged and refused (401 wrong-key). It is not used for a try that was never judged. */
export const UNLOCK_MESSAGE = 'That is not the master key for this PC.';

export const KEY_TEXT = {
  // The link on the start screen
  setupLink: 'Protect your cases with a master key',

  // Set up
  setupTitle: 'Protect your cases with a master key',
  setupIntro: 'A master key locks the cases kept on this computer. You type it once each time Unravis starts.',
  protects: 'Only someone who knows the key can open your cases.',
  notSent: 'The key is not sent anywhere. It stays on this computer.',
  noRecovery: 'Nobody can recover it. If you forget it, your cases cannot be opened again.',
  keyLabel: 'Master key',
  confirmLabel: 'Type the master key again',
  mismatch: 'The two keys do not match.',
  ackLabel: 'I understand that if I forget this key, my cases cannot be recovered.',
  submit: 'Set master key',
  submitting: 'Setting the key…',
  later: 'Not now',

  // After the key is set
  doneTitle: 'Master key set',
  doneText: 'Your cases are locked with it. You will type it each time Unravis starts.',
  rewrapping: 'Protecting your cases…',
  rewrapNone: 'Nothing was changed.',
  continue: 'Continue',
  retry: 'Try again',

  // Unlock
  unlockTitle: 'Unlock your cases',
  unlockIntro: 'Type the master key for this PC to see your cases. The key is not sent anywhere.',
  unlockLabel: 'Master key',
  unlockSubmit: 'Unlock',
  unlocking: 'Unlocking…',

  // Lock, in the header
  lock: 'Lock',
  lockLabel: 'Lock cases',
  lockFailed: 'The cases could not be locked. Try again.',
  home: 'Home',
  homeLabel: 'Back to the start screen'
} as const;

/** "Try again in 4 seconds." The wait is the only thing a throttled unlock adds to the one message. */
export function waitText(seconds: number): string {
  const whole = Math.max(1, Math.ceil(Number.isFinite(seconds) ? seconds : 1));
  if (whole >= 60) {
    const minutes = Math.ceil(whole / 60);
    return 'Try again in ' + minutes + (minutes === 1 ? ' minute.' : ' minutes.');
  }
  return 'Try again in ' + whole + (whole === 1 ? ' second.' : ' seconds.');
}

export interface UnlockFailure {
  kind: 'wrong-key' | 'throttled' | 'network' | 'not-set' | 'conflict' | 'locked' | 'damaged' | 'server' | 'other';
  retryAfterSeconds?: number;
}

/** The sentences for an unlock that was refused for a reason that is not a wrong key. None of them says the key was wrong. */
export const UNLOCK_FAILURE_TEXT = {
  network: 'Unravis could not be reached. Check that it is still running, then try again.',
  throttled: 'Too many tries.',
  notSet: 'This PC has no master key set now. Reload this page to start again.',
  conflict: 'The master key on this PC is not in the state Unravis expected. Reload this page, then try again.',
  locked: 'The cases are still locked. Type the master key and try again.',
  damaged: 'The master key file on this PC is damaged, so no key can open it. Nothing was changed.',
  server: 'Unravis ran into a problem and could not check the key. Try again.',
  other: 'The key could not be checked. Try again.'
} as const;

/**
 * The text under the unlock field. Only a refused key says "not the master key"; a try that was never judged (no answer, too many
 * tries, a server problem) says what happened instead, so the person is not sent to retype a key that may be right.
 */
export function unlockMessage(failure: UnlockFailure): string {
  switch (failure.kind) {
    case 'wrong-key': return UNLOCK_MESSAGE;
    case 'throttled': return UNLOCK_FAILURE_TEXT.throttled + ' ' + waitText(failure.retryAfterSeconds ?? 1);
    case 'network': return UNLOCK_FAILURE_TEXT.network;
    case 'not-set': return UNLOCK_FAILURE_TEXT.notSet;
    case 'conflict': return UNLOCK_FAILURE_TEXT.conflict;
    case 'locked': return UNLOCK_FAILURE_TEXT.locked;
    case 'damaged': return UNLOCK_FAILURE_TEXT.damaged;
    case 'server': return UNLOCK_FAILURE_TEXT.server;
    default: return UNLOCK_FAILURE_TEXT.other;
  }
}

/** What the strength hint can say: how long the key is and how many kinds of character it uses. It says nothing about safety. */
export function strengthText(length: number, kinds: number, minimum: number): string {
  if (length === 0) return 'Use at least ' + minimum + ' characters.';
  if (length < minimum) {
    const more = minimum - length;
    return more === 1 ? 'Add 1 more character. At least ' + minimum + ' are needed.' : 'Add ' + more + ' more characters. At least ' + minimum + ' are needed.';
  }
  if (kinds >= 3 && length >= 16) return 'Long, with a mix of characters. This check looks at length and mix only.';
  return 'Long enough. A longer key with letters, numbers and symbols is harder to guess.';
}

/** The line under the key field when it has 12 characters but too few of them show anything (spaces, invisible marks). */
export function blankHint(minimum: number): string {
  return 'Use at least ' + minimum + ' characters that are not spaces or invisible marks.';
}

export type SetupFailureKind = 'too-short' | 'too-long' | 'blank' | 'invalid-characters' | 'mismatch' | 'acknowledgement-required' | 'already-set' | 'damaged' | 'throttled' | 'incomplete' | 'network' | 'other';

/** The text for a refused set-up. It never repeats the key. */
export function setupErrorText(kind: SetupFailureKind, retryAfterSeconds?: number): string {
  switch (kind) {
    case 'too-short': return 'The master key needs at least 12 characters.';
    case 'too-long': return 'That key is too long. Use a shorter one.';
    case 'blank': return blankHint(8);
    case 'invalid-characters': return 'The key has a character that cannot be saved. Remove it, or retype the key, then try again.';
    case 'damaged': return UNLOCK_FAILURE_TEXT.damaged;
    case 'mismatch': return KEY_TEXT.mismatch;
    case 'acknowledgement-required': return 'Tick the box to confirm you understand.';
    case 'already-set': return 'A master key is already set on this PC. Unlock to use it.';
    case 'throttled': return 'Too many tries. ' + waitText(retryAfterSeconds ?? 1);
    case 'incomplete': return 'The key was saved but could not be used yet. Type it on the unlock screen.';
    case 'network': return 'Unravis could not be reached. Check that it is still running, then try again.';
    default: return 'The master key could not be set. Try again.';
  }
}

/** The screen for a master key file that no key can open (damaged, empty or replaced). */
export const DAMAGED_TEXT = {
  title: 'The master key file is damaged',
  what: 'The file that holds the master key for this PC cannot be read. It may be damaged, empty or replaced. No master key can open it.',
  unchanged: 'Nothing was changed. Your cases and the file are still where they were.',
  dpapi: 'Cases that were saved before you set a master key are not affected.',
  option: 'If you cannot put the file back from a backup, you can start over with a new master key.',
  keptAside: 'The damaged file is kept aside, not deleted. If it turns out to be fine, it can be put back under its old name.',
  warning: 'Cases protected by the old master key will stay unreadable after a new one is set.',
  ackLabel: 'I understand that cases protected by the old master key will stay unreadable.',
  reset: 'Start over with a new master key',
  resetting: 'Setting the file aside…',
  doneTitle: 'The old file was set aside',
  doneKept: 'The damaged file was kept aside as',
  doneNext: 'Next, set a new master key for this PC.',
  continue: 'Set a new master key'
} as const;

export type ResetFailureKind = 'acknowledgement-required' | 'not-damaged' | 'failed' | 'unavailable' | 'network' | 'other';

/** The text for a start-over that did not work. */
export function resetErrorText(kind: ResetFailureKind): string {
  switch (kind) {
    case 'acknowledgement-required': return 'Tick the box to confirm you understand.';
    case 'not-damaged': return 'The master key file is no longer damaged. Reload this page to carry on.';
    case 'failed': return 'The file could not be set aside. Nothing was changed. Try again.';
    case 'unavailable': return 'The master key cannot be changed right now. Try again in a moment.';
    case 'network': return 'Unravis could not be reached. Check that it is still running, then try again.';
    default: return 'The master key could not be started over. Nothing was changed. Try again.';
  }
}

const RESET_KINDS: ResetFailureKind[] = ['acknowledgement-required', 'not-damaged', 'failed', 'unavailable', 'network', 'other'];

/** The offer to protect cases made before the key existed, with a count. */
export function rewrapOffer(count: number): string {
  return count === 1
    ? '1 case is still protected the older way. Protect it with the master key too?'
    : count + ' cases are still protected the older way. Protect them with the master key too?';
}

export function rewrapAction(count: number): string {
  return 'Protect ' + (count === 1 ? '1 case' : count + ' cases');
}

/** What the re-wrap did, in counts only. */
export function rewrapDone(done: number, failed: number): string {
  const first = done === 1 ? '1 case is now protected with the master key.' : done + ' cases are now protected with the master key.';
  if (failed === 0) return first;
  return first + ' ' + (failed === 1 ? '1 case could not be changed.' : failed + ' cases could not be changed.') + ' You can try again.';
}

const SETUP_KINDS: SetupFailureKind[] = ['too-short', 'too-long', 'blank', 'invalid-characters', 'damaged', 'mismatch', 'acknowledgement-required', 'already-set', 'throttled', 'incomplete', 'network', 'other'];

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export function masterKeyLintEntries(): LintEntry[] {
  return [
    { id: 'masterkey.unlockMessage', text: UNLOCK_MESSAGE },
    ...Object.entries(KEY_TEXT).map(([id, text]) => ({ id: 'masterkey.' + id, text })),
    ...Object.entries(UNLOCK_FAILURE_TEXT).map(([id, text]) => ({ id: 'masterkey.unlockFailure.' + id, text })),
    { id: 'masterkey.sample.wait', text: unlockMessage({ kind: 'throttled', retryAfterSeconds: 4 }) },
    { id: 'masterkey.sample.waitMinute', text: waitText(60) },
    { id: 'masterkey.sample.strengthEmpty', text: strengthText(0, 0, 12) },
    { id: 'masterkey.sample.strengthShort', text: strengthText(7, 2, 12) },
    { id: 'masterkey.sample.strengthLong', text: strengthText(13, 1, 12) },
    { id: 'masterkey.sample.strengthMixed', text: strengthText(20, 4, 12) },
    ...SETUP_KINDS.map((kind) => ({ id: 'masterkey.sample.setup.' + kind, text: setupErrorText(kind, 4) })),
    ...Object.entries(DAMAGED_TEXT).map(([id, text]) => ({ id: 'masterkey.damaged.' + id, text })),
    ...RESET_KINDS.map((kind) => ({ id: 'masterkey.sample.reset.' + kind, text: resetErrorText(kind) })),
    { id: 'masterkey.sample.blankHint', text: blankHint(8) },
    { id: 'masterkey.sample.rewrapOne', text: rewrapOffer(1) },
    { id: 'masterkey.sample.rewrapMany', text: rewrapOffer(5) },
    { id: 'masterkey.sample.rewrapActionOne', text: rewrapAction(1) },
    { id: 'masterkey.sample.rewrapActionMany', text: rewrapAction(5) },
    { id: 'masterkey.sample.rewrapDone', text: rewrapDone(4, 1) },
    { id: 'masterkey.sample.rewrapDoneAll', text: rewrapDone(1, 0) }
  ];
}
