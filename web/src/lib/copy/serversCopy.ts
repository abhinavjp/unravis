import type { LintEntry } from './copylint';
import { FAILURE_TEXT, FOLDER_KINDS, caseRangeProblem, clockOffsetText, machineChoice, rangeLabel, jobStateLabel, type RangeChoice } from '../servers';

/**
 * Every word of the Servers panel that is not an answer from the backend. Plain sentences. A server's own error and next-step
 * sentences are shown as it gives them. The words "transport", "minion" and "allowlist" never appear here: a machine is a machine,
 * and "Salt master" is the only place Salt is named.
 * `serversCopyEntries()` lists the texts for the copy lint (web/tests/servers.test.cjs).
 */

export const SV_TEXT = {
  title: 'Servers',
  pageTitle: 'Get data from a server',
  purpose: 'Get Blackbox recordings and logs from a server onto your PC. You do not need to copy files by hand.',
  purposeDetail: 'You pick the server. Unravis connects, shows what is there, and copies the parts you choose. It only reads; it never changes anything on the server.',
  promiseHeading: 'What Unravis will and will not do',
  routeLinux: 'Through a Salt master on a Linux server',
  routeWindows: 'Through a Salt master on a Windows server',
  routeLinuxHelp: 'Connect to the Linux server that runs the Salt master, then choose the machines it manages.',
  routeWindowsHelp: 'The same, when your Salt master runs on Windows.',
  routeDirect: 'Connect directly to one server',
  routeUnavailable: 'Direct connections are not available here yet.',
  folderSummary: (n: number) => n + (n === 1 ? ' folder on the list' : ' folders on the list'),
  machineColumn: 'Machine',
  osColumn: 'Operating system',
  statusColumn: 'Right now',
  chosen: (n: number) => n + ' chosen',
  connectMachines: (n: number) => 'Connect to ' + n + (n === 1 ? ' machine' : ' machines'),
  intro: 'Get Blackbox recordings and logs from a remote server onto this PC.',
  close: 'Close the Servers panel',
  closeButton: 'Close',
  readOnly: 'Unravis only reads. It never changes, restarts or deletes anything on a server.',
  promises: [
    'It never accepts or removes a machine on the Salt master.',
    'Passwords and keys stay on this PC, sealed.',
    'Secrets in the copied logs are hidden before they reach your case.'
  ],

  stepsLabel: 'Steps',
  steps: ['Connect', 'Trust the key', 'Choose machines', 'Reach them', 'See what they have', 'Copy to your PC', 'In your timeline'],

  /* saved servers */
  savedHeading: 'Servers you saved',
  savedLoading: 'Loading your servers.',
  savedFailed: 'The list of servers could not be loaded.',
  savedEmpty: 'No server yet. Add the first one below.',
  savedUse: 'Use this server',
  savedForget: 'Forget',
  savedForgetAsk: 'Forget this server? Its saved password or key is removed from this PC. Files you already copied stay.',
  savedForgetYes: 'Yes, forget it',
  savedForgetNo: 'Keep it',
  savedRunning: 'A copy is running now.',
  savedNoSecret: 'No password or key is saved for this server.',
  savedTrusted: 'Trusted',
  savedNotTrusted: 'Not trusted yet',
  savedMachines: (n: number) => n === 1 ? '1 machine chosen' : n + ' machines chosen',
  retry: 'Try again',

  /* connect */
  connectTitle: 'Where do you want to get data from?',
  connectLead: 'Choose the server that runs the Salt master.',
  fieldType: 'Type of server',
  typeLinux: 'Linux',
  typeWindows: 'Windows',
  fieldName: 'Name',
  fieldNameHelp: 'A name for the list. Leave it empty to use the address.',
  fieldHost: 'Address',
  fieldHostHelp: 'The name or IP address of the server that runs the Salt master.',
  fieldPort: 'Port',
  fieldPortHelp: 'Most servers use 22.',
  fieldUser: 'User name',
  fieldUserHelp: 'The user name you use to sign in to this server.',
  helpLabel: (label: string) => 'Help with ' + label,
  statusUnknown: 'Status unknown',
  unknownNote: 'Check whether this machine is online before choosing it.',
  pickHelp: 'Not sure you have access? Check first.',
  trustGood: 'What good looks like: the fingerprint matches the one your admin gave you.',
  trustReason: 'Compare the fingerprint before continuing.',
  fieldAuth: 'How you sign in',
  authPassword: 'Password (like PuTTY)',
  authKey: 'Key file (optional)',
  fieldPassword: 'Password',
  fieldPasswordHelp: 'It is sent once, sealed on this PC, and never shown again.',
  fieldKeyFile: 'Key file',
  fieldKeyFileHelp: 'The private key file your admin gave you. It is read in this page, sent once and sealed on this PC.',
  fieldPassphrase: 'Key passphrase',
  fieldPassphraseHelp: 'Only if the key has one. Leave it empty if it has none.',
  connectGo: 'Connect and check the key',
  connecting: 'Saving this server.',
  secretNeeded: 'This server has no saved password or key. Add it to go on.',
  secretSave: 'Save it and go on',
  secretKeepLocal: 'Leave this empty if the secret is already saved.',

  /* trust */
  trustTitle: 'Is this really your server?',
  trustLead: 'Unravis asked the server for its key. It did not send your password. Check the fingerprint below with your server admin. PuTTY asks the same question the first time.',
  trustFetching: 'Asking the server for its key.',
  trustFingerprint: 'Fingerprint the server shows',
  trustKeyType: 'Key type',
  trustLater: 'If the fingerprint ever changes later, Unravis stops and tells you. It will not sign in.',
  trustPick: 'The server showed more than one key. Choose the one your admin gave you.',
  trustCompared: 'I compared this with the fingerprint from my server admin',
  trustGo: 'Trust it and list the machines',
  trustAgain: 'Ask the server again',
  trustHelp: 'Want to check the fingerprint yourself?',
  trustHelpSub: 'Run this yourself, or send it to your IT admin. Every line only reads.',
  trustCommands: 'Commands that only read',
  trustAdmin: 'Message for your server admin',
  trustChangedTitle: 'The key of this server changed',
  trustChangedLead: 'Unravis did not sign in. The server shows a different key than the one you trusted. This can be a new install, or someone else answering at this address. Ask your admin before you go on.',
  trustPinned: 'The key you trusted',
  trustPresented: 'The key it shows now',
  trustReplaceCompared: 'I asked my server admin and the new key is right',
  trustReplace: 'Trust the new key',
  trustChangedKeep: 'Keep the old key and stop',

  /* pick */
  pickTitle: 'Which machines do you want data from?',
  pickLead: 'These are the machines the Salt master manages. You can only choose machines that are accepted and on.',
  pickLoading: 'Asking the Salt master.',
  pickEmpty: 'The Salt master did not list any machine.',
  pickCount: (accepted: number, online: number, offline: number, notAccepted: number) =>
    accepted + ' accepted, ' + online + ' online, ' + offline + ' off, ' + notAccepted + ' not accepted or refused.',
  pickSudo: 'Run the commands on the Salt master with sudo',
  pickSudoHelp: 'Tick this if your user needs sudo on a Linux master.',
  pickLimit: 'You can choose up to 20 machines.',
  pickGo: 'Reach these machines',
  pickNone: 'Choose at least one machine.',
  pickCommands: 'Commands to check the list yourself',
  pickAdmin: 'Message for your server admin',

  /* reach */
  reachTitle: 'Connecting to your machines',
  reachLead: 'Unravis can only read folders you allow. Add the folders here, then check each machine.',
  foldersHeading: 'Folders Unravis may read',
  foldersEmpty: 'No folder yet.',
  foldersAdd: 'Add folder',
  foldersPath: 'Full path of the folder',
  foldersRemove: 'Remove',
  foldersSuggest: 'Try',
  foldersSaving: 'Saving the folders.',
  foldersSaved: 'Folders saved.',
  reachCheck: 'Check this machine',
  reachChecking: 'Checking',
  reachNotChecked: 'Not checked yet. Use Check this machine.',
  reachRecheck: 'Check again',
  reachNoFolders: 'Add at least one folder before you check.',
  reachOkLine: 'Ready to read.',
  reachPartLine: 'Some folders could not be read.',
  reachNoneLine: 'No folder could be read.',
  reachNoAnswer: 'No answer',
  reachCommands: 'Commands to check access yourself',
  reachAdmin: 'Message for your server admin',
  reachGo: 'See what they have',
  reachNeed: 'Check at least one machine, and it needs one folder Unravis can read.',

  /* has */
  hasTitle: 'What these machines have',
  hasLead: 'Unravis reads the file list first. Nothing is copied yet.',
  hasCaseHeading: 'Time range from this case',
  hasCaseIncident: 'Incident window',
  hasCaseComparison: 'Comparison period',
  hasCaseChange: 'Change this in the Investigation timeline',
  hasCaseOutside: 'Open a case to use its time windows here.',
  hasCaseSettings: 'These come from the case. This page only reads them.',
  hasRangeAll: 'Set every row to',
  hasRange: 'Time range',
  hasPlanning: 'Reading the file list.',
  hasEmpty: 'No files in this range.',
  hasFiles: (n: number, size: string) => (n === 1 ? '1 file, ' : n + ' files, ') + size,
  hasSkipped: (n: number) => n === 1 ? '1 file left out.' : n + ' files left out.',
  hasOldest: 'Oldest',
  hasNewest: 'Newest',
  hasTotals: (items: number, files: number, size: string) =>
    (items === 1 ? '1 item' : items + ' items') + ', ' + (files === 1 ? '1 file' : files + ' files') + ', ' + size + '.',
  hasLimit: 'One copy can hold up to 4 GB and 2000 files. A larger copy is refused, with the reason.',
  hasGo: (items: number) => items === 1 ? 'Copy 1 item to my PC' : 'Copy ' + items + ' items to my PC',
  hasNothing: 'Tick at least one item that has files.',
  hasReplan: 'Read again',
  hasSeen: (files: number, machines: number) => 'Unravis read the file list first: ' + (files === 1 ? '1 file' : files + ' files') + ' on ' + (machines === 1 ? '1 machine' : machines + ' machines') + '. Nothing is copied yet.',
  hasWhyHeading: 'What is this for?',
  hasForCase: 'For a case',
  hasNotForCase: 'Not for a case',
  hasNotForCaseNote: 'Choose a time range for each item below.',
  hasWindow: 'Investigation window',
  hasReference: 'Reference range',
  hasPulledFor: 'Pulled for each item',
  hasPulledForValue: 'Set per item in the tables below',
  hasReadOnly: 'Set once in the Investigation timeline, shown here read only.',
  hasChosen: (size: string) => size + ' chosen',
  hasWhat: 'What',
  hasHowMuch: 'How much',
  hasFound: 'Found',
  hasSize: 'Size',
  hasNotFound: 'Not found on this machine',
  hasFileCount: (n: number) => n === 1 ? '1 file' : n + ' files',

  /* pull */
  pullTitle: 'Copying to your PC',
  pullLead: 'Files are copied in pieces and checked against the server. Secrets are hidden before anything reaches a case.',
  pullHeading: (pct: number, size: string) => pct + '% of ' + size,
  pullColMachine: 'Machine',
  pullColWhat: 'What',
  pullColSize: 'Size',
  pullColProgress: 'Progress',
  pullColStatus: 'Status',
  pullDoneStatus: 'Done, hashes match',
  pullDoneUnchecked: 'Done',
  pullStoppedStatus: 'Stopped',
  pullFailedStatus: 'Failed',
  pullCopyingStatus: 'Copying',
  pullDropHeading: 'The connection dropped. Nothing is lost.',
  pullDropBody: (done: string, total: string) => done + ' of ' + total + ' is already on your PC. Resume continues from the exact byte it stopped at, so nothing is copied twice.',
  pullChooseAgain: 'Choose again',
  pullQueued: 'Waiting its turn',
  pullStop: 'Stop copying',
  pullStopping: 'Stopping.',
  pullResume: 'Resume',
  pullResumeHelp: 'Starts again at the byte where it stopped.',
  pullFile: (done: number, total: number) => 'File ' + done + ' of ' + total,
  pullCopied: (size: string) => size + ' copied',
  pullNext: 'See what was copied',
  pullAttached: 'A copy was already running on this server. Here it is.',

  /* land */
  landTitle: 'Done. It is on your PC and ready for the timeline',
  landLead: 'The copies are saved on this PC. The originals on the server were not touched.',
  landEmpty: 'Nothing was copied.',
  landVerified: (n: number) => n === 1 ? '1 file matches the server.' : n + ' files match the server.',
  landNotChecked: (n: number) => n === 1 ? '1 file could not be compared with the server.' : n + ' files could not be compared with the server.',
  landChanged: (n: number) => n === 1 ? '1 file was still growing while it was copied.' : n + ' files were still growing while they were copied.',
  landHidden: (n: number) => n === 1 ? '1 line with a secret was hidden.' : n + ' lines with secrets were hidden.',
  landDropped: (n: number) => n === 1 ? '1 line was left out.' : n + ' lines were left out.',
  landBlackboxPanel: 'Blackbox view',
  landBlackboxPanelSub: 'Each machine is its own saved recording set',
  landLogsPanelSub: 'Save any file to your PC',
  landNoBlackbox: 'No Blackbox recordings were copied.',
  landNoLogs: 'No logs were copied.',
  landSource: 'Timeline source',
  landHashes: 'Hashes match.',
  landSets: (files: number, size: string) => (files === 1 ? '1 recording, ' : files + ' recordings, ') + size,
  landBlackbox: 'Saved in the Blackbox view as',
  landOpenBlackbox: 'Open the Blackbox view',
  landBlackboxHelp: 'Open the set there and use it in a case. The timeline can then pick it as a source.',
  landLogs: 'Logs',
  landDownload: 'Save a copy of this file',
  landFilesSummary: (n: number) => (n === 1 ? 'Save the file (1)' : 'Save a file (' + n + ' files)'),
  landDownloading: 'Saving.',
  landAnother: 'Get more from this server',
  landDone: 'Back to the list of servers',
  landFailedHeading: 'Stopped by a problem',
  landStoppedHeading: 'Stopped',

  /* general */
  back: 'Back',
  next: 'Next',
  copy: 'Copy',
  copied: 'Copied',
  copyFailed: 'Copy failed. Select the text and copy it yourself.',
  working: 'Working.',
  fixTrust: 'Go to the key check',
  fixConnect: 'Go to the sign-in',
  fixFolders: 'Go to the folders',
  fixMachines: 'Go to the machines'
};

const PULL_KIND_SAMPLE: RangeChoice[] = ['case', 'window', '24', '72', '168'];

/** The texts the copy lint reads. Functions are called with sample values. */
export function serversCopyEntries(): LintEntry[] {
  const out: LintEntry[] = [];
  const add = (id: string, text: string) => { out.push({ id: 'servers.' + id, text }); };
  for (const [key, value] of Object.entries(SV_TEXT)) {
    if (typeof value === 'string') add(key, value);
    else if (Array.isArray(value)) value.forEach((v, i) => add(key + '.' + i, v));
    else if (typeof value === 'function') {
      const fn = value as (...args: any[]) => string;
      if (key === 'landFilesSummary') { add(key + '.1', fn(1)); add(key + '.2', fn(6)); }
      else if (key === 'savedMachines') { add(key + '.1', fn(1)); add(key + '.2', fn(3)); }
      else if (key === 'folderSummary' || key === 'chosen' || key === 'connectMachines') { add(key + '.1', fn(1)); add(key + '.2', fn(3)); }
      else if (key === 'helpLabel') add(key, fn('Address'));
      else if (key === 'pickCount') add(key, fn(5, 3, 1, 2));
      else if (key === 'hasFiles') { add(key + '.1', fn(1, '2 MB')); add(key + '.2', fn(12, '48 MB')); }
      else if (key === 'hasSkipped' || key === 'landVerified' || key === 'landNotChecked' || key === 'landChanged' || key === 'landHidden' || key === 'landDropped') { add(key + '.1', fn(1)); add(key + '.2', fn(4)); }
      else if (key === 'hasTotals') { add(key + '.1', fn(1, 1, '2 MB')); add(key + '.2', fn(3, 40, '2.1 GB')); }
      else if (key === 'hasGo') { add(key + '.1', fn(1)); add(key + '.2', fn(3)); }
      else if (key === 'hasSeen') add(key, fn(1200, 3));
      else if (key === 'hasChosen') add(key, fn('2 MB'));
      else if (key === 'hasFileCount') { add(key + '.1', fn(1)); add(key + '.2', fn(4)); }
      else if (key === 'pullHeading') add(key, fn(40, '2 GB'));
      else if (key === 'pullDropBody') add(key, fn('1 GB', '2 GB'));
      else if (key === 'landSets') { add(key + '.1', fn(1, '2 MB')); add(key + '.2', fn(30, '2 GB')); }
      else if (key === 'pullFile') add(key, fn(2, 5));
      else if (key === 'pullCopied') add(key, fn('12.0 MB'));
    }
  }
  for (const [key, value] of Object.entries(FAILURE_TEXT)) add('failure.' + key, value);
  for (const info of FOLDER_KINDS) { add('folder.' + info.kind + '.label', info.label); add('folder.' + info.kind + '.hint', info.hint); }
  for (const choice of PULL_KIND_SAMPLE) add('range.' + choice, rangeLabel(choice));
  for (const state of ['none', 'default-shaped', 'too-old'] as const) add('rangeProblem.' + state, caseRangeProblem({ state }));
  add('rangeProblem.too-long.window', caseRangeProblem({ state: 'too-long', which: 'window' }));
  add('rangeProblem.too-long.comparison', caseRangeProblem({ state: 'too-long', which: 'comparison' }));
  for (const sample of [{ state: 'accepted', online: true }, { state: 'accepted', online: false }, { state: 'accepted' }, { state: 'pending' }, { state: 'denied' }]) {
    const c = machineChoice(sample);
    add('machine.' + sample.state + '.' + String(sample.online) + '.label', c.label);
    add('machine.' + sample.state + '.' + String(sample.online) + '.note', c.note);
  }
  for (const s of ['running', 'done', 'stopped', 'failed'] as const) add('job.' + s, jobStateLabel(s));
  add('clock.ahead', clockOffsetText(40e9));
  add('clock.same', clockOffsetText(0));
  return out;
}

