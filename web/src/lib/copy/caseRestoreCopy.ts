import type { LintEntry } from './copylint';

/**
 * Words for opening a case that used a MongoDB source. The case keeps which saved connection and databases it used, never the login, so
 * opening it reconnects by itself when it can and otherwise asks for the sign-in here instead of showing the first-run connect screen.
 */
export const CASE_RESTORE_TEXT = {
  working: 'Reconnecting to the databases this case used…',
  workingLabel: 'Reconnecting this case to its databases',
  heading: 'Reconnect this case',
  signInButton: 'Sign in to reconnect',
  newButton: 'New connection',
  anotherButton: 'Use another connection',
  missingHeading: 'The saved connection is gone',
  missing: 'The saved connection this case used is no longer on this computer. Add it again to carry on.',
  failedHeading: 'The connection did not open',
  failed: 'The saved connection did not open. Sign in again, or pick another connection.',
  unreachableHeading: 'MongoDB could not be reached',
  unreachable: 'The server this case used did not answer in time. Check the network or VPN, then try again.',
  tryAgainButton: 'Try again',
  cancelButton: 'Cancel',
  partial: 'Some databases did not open. The others are ready.'
} as const;

/** "This case used MongoDB database orders. Sign in to reconnect." */
export function signInText(names: readonly string[]): string {
  const list = names.filter((name) => name.trim() !== '');
  if (list.length === 0) return 'This case used a MongoDB database. Sign in to reconnect.';
  if (list.length === 1) return `This case used MongoDB database ${list[0]}. Sign in to reconnect.`;
  return `This case used MongoDB databases ${list.slice(0, -1).join(', ')} and ${list[list.length - 1]}. Sign in to reconnect.`;
}

export const caseRestoreCopyEntries = (): LintEntry[] => [
  ...Object.entries(CASE_RESTORE_TEXT).map(([id, text]) => ({ id: 'caseRestore.' + id, text })),
  { id: 'caseRestore.sample.one', text: signInText(['orders']) },
  { id: 'caseRestore.sample.many', text: signInText(['orders', 'billing', 'audit']) },
  { id: 'caseRestore.sample.none', text: signInText([]) }
];
