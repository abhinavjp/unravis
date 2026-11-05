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

/** What the server's source-stage codes mean here: a problem with the collection or the version of one database, not a sign-in problem. */
const SOURCE_FAILED_TEXT: Readonly<Record<string, string>> = {
  collection_missing: 'This database has no collection with the audit log name the case read. Open Add more data, pick the right collection, then try the database again.',
  permission_denied: 'The saved sign-in cannot read the audit collection of this database. Ask for read access, or pick another collection in Add more data.',
  unsupported_version: 'The MongoDB server version of this database is not supported. Unravis reads MongoDB 3.4 and 4.4 or newer.'
};

/**
 * The sentence that goes under "The connection did not open" when a database the case read did not open. The server's code on the first
 * source-stage cause picks it: a collection the person has to pick again, a sign-in without access, or a version this app cannot read.
 * Any other cause returns '', which keeps the standard wording of the failed strip.
 */
export function restoreFailureNote(causes: readonly unknown[]): string {
  for (const cause of causes) {
    const record = cause !== null && typeof cause === 'object' ? (cause as { stage?: unknown; code?: unknown }) : null;
    if (!record || record.stage !== 'source') continue;
    const text = SOURCE_FAILED_TEXT[String(record.code ?? '')];
    if (text) return text;
  }
  return '';
}

export const caseRestoreCopyEntries = (): LintEntry[] => [
  ...Object.entries(CASE_RESTORE_TEXT).map(([id, text]) => ({ id: 'caseRestore.' + id, text })),
  ...Object.entries(SOURCE_FAILED_TEXT).map(([id, text]) => ({ id: 'caseRestore.sourceFailed.' + id, text })),
  { id: 'caseRestore.sample.one', text: signInText(['orders']) },
  { id: 'caseRestore.sample.many', text: signInText(['orders', 'billing', 'audit']) },
  { id: 'caseRestore.sample.none', text: signInText([]) }
];
