import type { LintEntry } from './copylint';

/**
 * Every sentence in the Import files popup, in one place so the copy lint and a reviewer can read them together.
 * Wording is taken from prototype/import-popup-WIREFRAME.html. A computer is a server Unravis has been told about;
 * a server name is the name a log file says wrote it (see the glossary in deck.ts).
 */

const filesWord = (n: number): string => (n === 1 ? ' file' : ' files');

export const POPUP_TEXT = {
  title: 'Import files',
  stepNames: ['Choose', 'Check', 'Result'],
  prog: {
    1: 'Step 1 of 3 · Choose files to import',
    2: 'Step 2 of 3 · Check files with the server before import',
    '3importing': 'Step 3 of 3 · Importing — do not close',
    '3result': 'Step 3 of 3 · Result'
  },

  dropTitle: 'Drop files or a folder here',
  dropSub: 'IIS web logs, log4net, SQL error logs, Windows event logs. Text and CSV only.',
  addFiles: 'Add files',
  addFolder: 'Add a folder',
  continueBtn: 'Continue',
  nothingSent: 'Nothing is sent until you press Import.',

  checking: (n: number): string => 'Checking ' + n + ' files with the server…',
  checkingSub: 'Nothing is imported yet.',

  fixTitleNeedsComputer: (n: number, name: string, kind: string): string => n + ' ' + kind + ' files come from ' + name + ', which is not on your computer list.',
  fixHelpNeedsComputer: 'Unravis will not guess which computer wrote a log. Add it once and every file from that server is accepted.',
  fixNoteIis: 'IIS logs are already UTC, no time zone needed.',
  fixAccept: (name: string, n: number): string => 'Add ' + name + ' as a computer and accept ' + n + ' files',
  fixLeaveOut: (n: number): string => 'Leave these ' + n + ' files out',
  fixAddAll: (n: number): string => 'Add all ' + n + ' servers',
  fixResolved: (name: string, n: number): string => 'Added ' + name + ', ' + n + ' files accepted',
  fixUndo: 'Undo',
  fixBringBack: 'Bring them back',
  fixLeftOut: (n: number, kind: string): string => n + ' ' + kind + ' files left out',
  fixNoComputerTitle: (n: number, kind: string): string => n + ' ' + kind + ' files do not say which computer wrote them.',
  fixNameLabel: 'Name of the computer',
  fixNameHint: 'Suggested from the folder name. Change it if wrong.',
  fixZoneLabel: 'Time zone of its clock',
  fixZoneHelp: 'SQL error logs use the computer\u2019s own clock, so the time zone changes where events land on the timeline.',
  fixUseName: (name: string, n: number): string => 'Use ' + name + ' and accept ' + n + ' files',

  notSupportedHead: (n: number): string => n + ' files are not supported',
  notSupportedReason: 'This file is not supported',
  removeFromList: 'Remove from list',
  remove: 'Remove',
  kindPickerPlaceholder: 'Tell us what this is',
  kinds: ['IIS web log', 'log4net', 'SQL error log', 'Windows event log', 'Hangfire', 'other'],

  ack: 'I understand this is read-only and nothing is changed on these computers',
  importBtn: (n: number): string => 'Import ' + n + filesWord(n),
  fixesNeeded: (n: number): string => n + (n === 1 ? ' fix' : ' fixes') + ' needed before you can import',

  discardTitle: (n: number): string => 'Discard ' + n + filesWord(n) + '?',
  discardBody: 'These files are still on the list. Discarding clears them and closes the popup.',
  discardKeep: 'Keep them',
  discardNow: 'Discard',

  impLine: (p: number, total: number): string => 'Importing ' + p + ' of ' + total + ' files',
  cancel: 'Cancel',

  resultHero: (n: number): string => n + ' files imported',
  resultImported: 'Imported',
  resultNotImported: 'Not imported',
  resultNoType: 'No file type chosen',
  computersLine: (n: number, names: string): string => 'Added ' + n + ' computers: ' + names + '.',
  seeComputers: 'See computers',
  openTimeline: 'Open timeline',
  importMore: 'Import more',

  badge: { ready: 'ready', found: 'found', needsChecking: 'needs checking' }
} as const;

/** Every fixed sentence in POPUP_TEXT plus one sample of each built sentence, for the copy lint. */
export function popupCopyEntries(): LintEntry[] {
  const entries: LintEntry[] = [];
  const walk = (prefix: string, value: unknown) => {
    if (typeof value === 'string') entries.push({ id: prefix, text: value });
    else if (Array.isArray(value)) value.forEach((inner, index) => walk(prefix + '.' + index, inner));
    else if (value && typeof value === 'object') for (const [key, inner] of Object.entries(value)) walk(prefix + '.' + key, inner);
  };
  walk('popup.text', POPUP_TEXT);
  const T = POPUP_TEXT;
  const samples: Record<string, string> = {
    checking: T.checking(13),
    fixTitleNeedsComputer: T.fixTitleNeedsComputer(8, 'iis_sandboxapi', 'IIS'),
    fixAccept: T.fixAccept('iis_sandboxapi', 8),
    fixLeaveOut: T.fixLeaveOut(8),
    fixAddAll: T.fixAddAll(2),
    fixResolved: T.fixResolved('iis_sandboxapi', 8),
    fixLeftOut: T.fixLeftOut(8, 'IIS'),
    fixNoComputerTitle: T.fixNoComputerTitle(3, 'SQL error log'),
    fixUseName: T.fixUseName('sql-prod-2', 3),
    notSupportedHead: T.notSupportedHead(2),
    importBtn: T.importBtn(13),
    importBtn1: T.importBtn(1),
    fixesNeeded: T.fixesNeeded(2),
    fixesNeeded1: T.fixesNeeded(1),
    discardTitle: T.discardTitle(13),
    discardTitle1: T.discardTitle(1),
    impLine: T.impLine(4, 13),
    resultHero: T.resultHero(13),
    computersLine: T.computersLine(2, 'iis_sandboxapi, sql-prod-2')
  };
  for (const [key, text] of Object.entries(samples)) entries.push({ id: 'popup.sample.' + key, text });
  return entries;
}
