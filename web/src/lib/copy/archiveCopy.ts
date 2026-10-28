import type { LintEntry } from './copylint';

/**
 * Every fixed sentence for archive, check, free and restore (P3), and the map from a server reason to a sentence.
 * The sentences M1 to M19 match internal/server/archive_messages.go. A server reason never shows the server's own words:
 * the page picks the sentence here, so a path or a key can never reach the screen through an error.
 * Pure: no DOM, no fetch.
 */

export const ARCHIVE_TEXT = {
  // The archive drawer (screen 127)
  archiveButton: 'Archive this case',
  archiveTitle: 'Archive this case',
  archiveIntro: 'Save the whole case in one file you can keep, copy or move. You do not need to close the case first.',
  folderLabel: 'Folder for the case file',
  folderHint: 'A drive path you can write to. Not a network drive, not a relative path, and not inside Unravis\u2019s own data folder.',
  folderPlaceholder: 'D:\\Unravis archive',
  caseSize: 'Case size',
  sealedWith: 'Sealed with',
  sealedMaster: 'The master key of this computer',
  sealedTyped: 'The passphrase you type below',
  sealedCase: 'The case key of this case',
  sealedCaseHint: 'The file opens with the case key, on this or any computer. Keep the key: without it the file cannot be opened.',
  passphraseLabel: 'Passphrase for this file',
  passphraseHint: 'At least 12 characters. Keep it: without it the file cannot be opened.',
  archiveSubmit: 'Archive the case',
  archiveCancel: 'Cancel',
  archiveClose: 'Close archive',
  // Running (screen 128)
  archiveRunning: 'Writing the case file',
  archiveChecking: 'Checking the case file',
  archiveStop: 'Cancel archiving',
  archiveStopping: 'Stopping\u2026',
  archiveDone: 'Archived.',
  progressLabel: 'Archive progress',
  // Check
  verifyButton: 'Check the case file',
  verifyRunning: 'Checking the case file',
  verifyStop: 'Cancel the check',
  verifyStopping: 'Stopping\u2026',
  verifyPassed: 'The case file passed the check.',
  verifyPassphraseLabel: 'Passphrase this file was made with',
  verifyStart: 'Start the check',

  // The archived case page (screen 122)
  archivedNote: 'This case is archived. It is read-only here.',
  freedNote: 'The working copy was freed. The case file is the only copy.',
  archivedWork: 'To read the analysis, check the sources or add files, restore the case first.',
  filesMade: 'Case files made earlier',
  caseFileLine: 'Case file',
  inFolder: 'in',
  keyEnding: 'Key ending',
  restoredLine: 'Restored as a new case',
  code: 'code',

  // The archived list (screen 129)
  listHeading: 'Archived cases',
  listToggle: 'Archived cases',
  listBack: 'Back to the working list',
  listEmpty: 'No archived cases.',
  listLoading: 'Loading the archived cases\u2026',
  listFailed: 'The archived cases could not be loaded.',
  listRetry: 'Try again',
  listMore: 'Show more archived cases',
  archivedOn: 'Archived on',
  chipArchived: 'Archived',
  chipKept: 'Working copy kept',
  chipFreed: 'Space freed',
  chipMissing: 'Missing',
  chipArchiving: 'Archiving',
  archivingNote: 'This case is being archived.',
  showProgress: 'Show progress',
  workingCopyMissing: 'Working copy missing',
  more: 'More',
  moreLabel: 'More for this case',
  restore: 'Restore',
  restoreAsNewMenu: 'Restore from the case file as a new case',
  freeMenu: 'Free the space',
  erase: 'Erase',
  eraseWarning: 'Erasing removes the record of this case from this computer. The case file is not touched. This cannot be undone.',
  eraseConfirmButton: 'Erase this case',
  eraseCancel: 'Keep this case',

  // Restore (screens 130 to 132)
  restoreInPlaceTitle: 'Restore this case',
  restoreInPlaceIntro: 'The working copy was kept, so this puts the case back in your working list, closed. Nothing is read from the case file.',
  restoreInPlaceSubmit: 'Restore this case',
  restoreInPlaceDone: 'The case is back in the working list. It is closed. Reopen it to add data.',
  restoreNewTitle: 'Restore as a new case',
  restoreNewIntro: 'This reads the case file and makes a new case from it. The archived case and the file are not changed.',
  restoreFile: 'Case file',
  restoreMade: 'Made',
  restoreKeyLabel: 'Passphrase this file was made with',
  restoreKeyHint: 'The passphrase you typed when the file was made.',
  restoreCaseKeyLabel: 'Case key this file was made with',
  restoreCaseKeyHint: 'The new case keeps this key.',
  caseKeyNeeded: 'Type the case key to restore this case. The new case keeps it.',
  restoreNewSubmit: 'Restore as a new case',
  restoreWorking: 'Restoring\u2026',
  restoreCancel: 'Cancel',
  restoreClose: 'Close',
  restoreOpenNew: 'Open the new case',

  // Free the space (screens 133 to 135)
  freeTitle: 'Free the space',
  freeIntro: 'Remove the working copy to get its space back. The case file stays. First the file is checked by restoring it to a temporary place.',
  freeStage1: '1. Check the case file',
  freeStage2: '2. Confirm',
  freeCheckRunning: 'Checking the case file by restoring it to a temporary place.',
  freeAfter: 'After this the working copy is gone. The only copy is:',
  freeKeyWarning: 'This file can only be opened with the key it was made with.',
  freeTypeCode: 'Type the case code to free the space',
  freeSubmit: 'Free the space',
  freeWorking: 'Freeing\u2026',
  freeKeep: 'Keep the working copy',
  freeDone: 'The space was freed. The case file is the only copy.',
  freeClose: 'Close',

  // Orphan files (health panel)
  orphanHeading: 'Case files left over',
  orphanIntro: 'These case files were finished but the archive was not completed. Unravis never deletes them. Keep, move or delete them yourself.',
  orphanNone: 'No case files left over.',
  orphanLoadFailed: 'The list of left-over case files could not be loaded.',
  orphanCleanup: 'Cleanup pending: a temporary folder from a check is still on disk. It is removed when Unravis starts again.',
  orphanFrom: 'for case',
  orphanMade: 'Made',

  // Sentences the server sends as failures (archive_messages.go). Kept word for word.
  m1: 'An import is still running. Let it finish or stop it first.',
  m2: 'This case changed in another tab. Reload.',
  m3: 'An archive is already running.',
  m4: 'The case is in use. Close its other tabs, then try again.',
  m6: 'Archived cases are in the Archived list on the start screen.',
  m8: 'The folder cannot be used. Choose a folder on this computer that you can write to.',
  m9: 'There is not enough free space for the archive.',
  m10: 'The archive file could not be checked. Nothing was changed. Try again.',
  m11: 'The case file did not pass the check. The working copy was kept.',
  m13: 'This file can only be opened with the key it was made with.',
  m13Fix: 'To get the case back, use Restore from a case file on the start screen. Type the master key or passphrase the file was made with.',
  m14: 'Archiving was stopped. The case is back as it was. Try again.',
  m15: 'Cancel the archive first.',
  m16: 'This case is too large to archive in one file.',
  m17: 'The check was stopped. Nothing was changed. Try again.',
  m18: 'A case file for this case was already made. Check the case page.',
  m19: 'This case is being freed. Try again in a moment.',
  a18: 'Another case file is being saved or restored.',
  notKept: 'This case is not archived, or its working copy is already gone.',
  checkFirst: 'Check the case file first.',
  typedPass: 'Type a passphrase of at least 12 characters for this file.',
  needsFile: 'The case file for this case is not in the folder it was saved to. Put it back there, or use Restore from a case file on the start screen and type where it is now.',
  confirmMismatch: 'The case code does not match. Nothing was freed.',
  noMasterKey: 'This needs the master key of this computer. Set a master key first.',
  noMasterKeyTitle: 'A master key is needed',
  noMasterKeyFree: 'Freeing the space needs the master key of this computer, and none is set up yet. Set a master key first. The working copy and the case file are not changed.',
  noMasterKeyRestore: 'Restoring as a new case needs the master key of this computer, and none is set up yet. Set a master key first. The archived case and the case file are not changed.',
  noMasterKeySetup: 'Set a master key',
  noMasterKeyLater: 'Not now',
  notFound: 'That case was not found. It may have been erased.',
  storeError: 'Something went wrong in the case store. Nothing was changed.',
  unreachable: 'The server could not be reached. Nothing was changed.',
  locked: 'The cases are locked. Unlock them first.',
  badFile: 'That file could not be opened with this key.',
  restoreFailed: 'The case could not be restored. Nothing was changed. Try again.',
  unavailable: 'Case files are not available right now.',
  generic: 'That did not work. Nothing was changed. Try again.'
} as const;

/** The sentence for a server reason. An unknown reason gets the generic sentence, never the server's own words. */
export function archiveReasonText(reason: string, status = 0): string {
  switch (reason) {
    case 'import-running': return ARCHIVE_TEXT.m1;
    case 'case-changed': return ARCHIVE_TEXT.m2;
    case 'archive-running': return ARCHIVE_TEXT.m3;
    case 'case-in-use': return ARCHIVE_TEXT.m4;
    case 'case-archived': return ARCHIVE_TEXT.m6;
    case 'bad-folder': return ARCHIVE_TEXT.m8;
    case 'no-space': return ARCHIVE_TEXT.m9;
    case 'archive-failed': return ARCHIVE_TEXT.m10;
    case 'case-file-failed': return ARCHIVE_TEXT.m11;
    case 'old-key-only': return ARCHIVE_TEXT.m13 + ' ' + ARCHIVE_TEXT.m13Fix;
    case 'archive-stopped': return ARCHIVE_TEXT.m14;
    case 'cancel-archive-first': return ARCHIVE_TEXT.m15;
    case 'too-large': return ARCHIVE_TEXT.m16;
    case 'verify-stopped': return ARCHIVE_TEXT.m17;
    case 'case-already-made': return ARCHIVE_TEXT.m18;
    case 'case-freeing': return ARCHIVE_TEXT.m19;
    case 'busy': return ARCHIVE_TEXT.a18;
    case 'not-kept': return ARCHIVE_TEXT.notKept;
    case 'check-first': return ARCHIVE_TEXT.checkFirst;
    case 'bad-passphrase': return ARCHIVE_TEXT.typedPass;
    case 'file-missing': return ARCHIVE_TEXT.needsFile;
    case 'confirm-mismatch': return ARCHIVE_TEXT.confirmMismatch;
    case 'case-key-needed': return ARCHIVE_TEXT.caseKeyNeeded;
    case 'no-master-key': return ARCHIVE_TEXT.noMasterKey;
    case 'case-not-found': return ARCHIVE_TEXT.notFound;
    case 'store-error': return ARCHIVE_TEXT.storeError;
    case 'bad-case-file': return ARCHIVE_TEXT.badFile;
    case 'restore-failed': return ARCHIVE_TEXT.restoreFailed;
    case 'case-file-unavailable': return ARCHIVE_TEXT.unavailable;
    case 'unreachable': return ARCHIVE_TEXT.unreachable;
    case 'cases-locked': return ARCHIVE_TEXT.locked;
    default: return status === 423 ? ARCHIVE_TEXT.locked : status === 0 ? ARCHIVE_TEXT.unreachable : ARCHIVE_TEXT.generic;
  }
}

/**
 * The sentence for a job that ended without finishing. The same reason reads differently for the two jobs:
 * "check-failed" is M10 for an archive and M11 for a check; "stopped" is M14 and M17.
 */
export function jobFailureText(kind: 'archive' | 'verify', reason: string): string {
  switch (reason) {
    case 'check-failed': return kind === 'archive' ? ARCHIVE_TEXT.m10 : ARCHIVE_TEXT.m11;
    case 'stopped':
    case 'key-locked': return kind === 'archive' ? ARCHIVE_TEXT.m14 : ARCHIVE_TEXT.m17;
    default: return archiveReasonText(reason);
  }
}

/** "Key ending 7f3a". */
export const keyEndingText = (ending: string): string => (ending ? ARCHIVE_TEXT.keyEnding + ' ' + ending : '');

/** "Writing the case file, 42%". */
export const runningText = (phase: string, percent: number): string => (phase === 'check' ? ARCHIVE_TEXT.archiveChecking : ARCHIVE_TEXT.archiveRunning) + ', ' + percent + '%';

/** "Archived. The case file is <name> in <folder>. Key ending xxxx." */
export function archivedNotice(name: string, folder: string, ending: string): string {
  const base = ARCHIVE_TEXT.archiveDone + ' The case file is ' + name + (folder ? ' ' + ARCHIVE_TEXT.inFolder + ' ' + folder : '') + '.';
  return ending ? base + ' ' + keyEndingText(ending) + '.' : base;
}

/** "A new case was made: <title>, code <code>. It is closed. Reopen it to add data." */
export const restoredNewNotice = (title: string, code: string): string => 'A new case was made: ' + title + ', code ' + code + '. It is closed. Reopen it to add data.';

/** "Archived on 12 Oct 2026 · 9 imports · 4 sources" is built by the row; this is the counts part. */
export const archiveCounts = (imports: number, sources: number): string => imports + (imports === 1 ? ' import' : ' imports') + ' \u00b7 ' + sources + (sources === 1 ? ' source' : ' sources');

/** "n of n archived cases shown." */
export const archivedShownText = (shown: number, total: number): string => shown + ' of ' + total + ' archived cases shown.';

/** "Archived cases (3)". */
export const archivedToggleText = (total: number): string => ARCHIVE_TEXT.listToggle + ' (' + total + ')';

/** "Case files made earlier: 2". */
export const filesMadeText = (count: number): string => ARCHIVE_TEXT.filesMade + ': ' + count;

/** The line for a story entry. The detail of an archive is the file name; the key ending joins it when known. */
export function historyLine(kind: string, detail: string, ending: string): { title: string; detail: string } | null {
  switch (kind) {
    case 'archived': return { title: 'Case archived', detail: [detail, keyEndingText(ending)].filter(Boolean).join(' \u00b7 ') };
    case 'restored': return { title: 'Case restored in place', detail: '' };
    case 'freed': return { title: 'Space freed', detail: '' };
    case 'restored-from-file': return { title: 'Case restored from a case file', detail: '' };
    case 'restored-from-case': return { title: 'A new case was restored from this one', detail: detail ? ARCHIVE_TEXT.code + ' ' + detail : '' };
    default: return null;
  }
}

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const archiveLintEntries = (): LintEntry[] => [
  ...Object.entries(ARCHIVE_TEXT).map(([id, text]) => ({ id: 'archive.' + id, text })),
  { id: 'archive.sample.running', text: runningText('write', 42) },
  { id: 'archive.sample.notice', text: archivedNotice('q3-slowdown.unravis-case', 'D:\\Archive', '7f3a') },
  { id: 'archive.sample.restored', text: restoredNewNotice('Q3 month-end slowdown', 'p4w9c6xe') },
  { id: 'archive.sample.counts', text: archiveCounts(9, 4) },
  { id: 'archive.sample.shown', text: archivedShownText(3, 3) },
  { id: 'archive.sample.toggle', text: archivedToggleText(3) },
  { id: 'archive.sample.filesMade', text: filesMadeText(2) }
];
