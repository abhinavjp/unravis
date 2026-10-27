import type { PageState, SettingsProblem } from '../blackbox';
import type { LintEntry } from './copylint';

/**
 * Every word of the Blackbox panel. Plain sentences: say what happened, then what to do. The state labels and the
 * readiness words come from the server (fixed A36 and A5 vocabulary); this file holds the rest.
 * `blackboxCopyEntries()` lists the texts for the copy lint (web/tests/blackbox.test.cjs).
 */

export const BB_TEXT = {
  title: 'Blackbox',
  intro: 'A recorder you place on a server. It is not part of any case.',
  close: 'Close the Blackbox panel',
  nutshellLabel: 'In a nutshell',
  nutshell: 'Think of the black box in an aeroplane. It records quietly all the time. When something goes wrong, you open the recording and look. Unravis reads what it has recorded. It never changes your server.',
  readOnly: 'Unravis reads your servers. It never changes them. On this PC it changes its own Blackbox, and only when you press a button. Install adds the service. Start and Stop turn it on and off. Apply settings restarts it. Remove uninstalls it. Delete the recordings too also deletes the recordings folder. Windows asks for administrator approval first. Snapshot copies the recordings into one folder. Nothing is done to another server.',

  place: 'Place a Blackbox',
  addExisting: 'Add one by folder, share or address',
  scanPc: 'Scan this PC',
  scanning: 'Looking at this PC.',
  scanFailed: 'This PC could not be asked whether a Blackbox is installed.',
  loading: 'Loading the Blackbox list.',
  loadFailed: 'The Blackbox list could not be loaded.',
  retry: 'Try again',

  emptyTitle: 'No Blackbox yet',
  emptyText: 'A Blackbox records host measurements all the time. Without one, host steps in a case say Cannot test yet. Place one on a server before you need it.',
  thisPcNote: 'This PC has no Blackbox. Place one here, or scan again after you install one.',
  thisPcFound: 'A Blackbox is installed on this PC but is not in the list yet.',
  thisPcFoundAdd: 'Add it to the list',
  thisPcUnreadable: 'A Blackbox is installed on this PC, but its settings file could not be read.',

  rowThisPc: 'this PC',
  rowReachedBy: 'Reached by',
  kindFolder: 'Folder',
  kindShare: 'Network share',
  kindUrl: 'Address and port',
  lastSample: 'Last sample',
  samplesEvery: 'Samples every',
  recorded: 'Recorded',
  gaps: 'Gaps',
  files: 'Files',
  diskFree: 'Disk free',
  host: 'Host',
  version: 'Version',
  noSample: 'none yet',
  noGaps: 'none',
  openStatus: 'Open status',
  check: 'Check again',
  checking: 'Checking.',
  back: 'Back to the list',

  /* the nine states and what to do about them (wireframe screen 67) */
  stateNext: {
    running: 'Nothing to do. It is recording.',
    'running-late': 'No sample for more than three intervals. On the server, open Services, find UnravisBlackbox and restart it.',
    stopped: 'On this PC, press Start. On another server, start it from Services.',
    'disk-low': 'Free space on that drive, or lower the size limit. Recording resumes by itself.',
    'clock-differs': 'Fix the clock on that server. Until then, events less than a second apart are not put in order.',
    'not-found': 'Check the folder, share or address, then check again.',
    unreachable: 'Check the share permissions or the firewall port, then check again.',
    incomplete: 'The recording has gaps. A gap of up to 2 minutes only warns. A longer gap makes the minutes inside it Cannot test yet.',
    'not-placed': 'Place a Blackbox before you need it. Until then host steps say Cannot test yet.'
  } as Record<PageState, string>,
  servicesSteps: 'On the server, open Services (services.msc). Find UnravisBlackbox. Choose Start, or Restart if it is late. Wait one minute. The state here changes to Running.',
  servicesTitle: 'Steps for the server',
  anotherServer: 'This Blackbox is on another server, so Unravis does not offer Stop or Snapshot for it. Use the steps below.',
  readKeyNote: 'The read key was shown once on that server when the Blackbox was installed. It is saved here, sealed with Windows data protection, and never shown again.',
  readKeySaved: 'A read key is saved on this PC.',
  readKeyNone: 'This place needs no read key.',
  changeKey: 'Type a new read key',
  newKey: 'New read key',
  saveKey: 'Save the new key',
  keySaved: 'The new key was tried and saved.',

  /* this-PC actions */
  start: 'Start',
  stop: 'Stop',
  snapshot: 'Snapshot now',
  apply: 'Apply settings',
  remove: 'Remove',
  removeForget: 'Forget this place',
  working: 'Working. Windows may ask for administrator approval.',
  started: 'Started. Check again in a few seconds.',
  stopped: 'Stopped. The recordings are kept.',
  confirmStop: 'While stopped, nothing is recorded. A case that reads this Blackbox sees a gap from then on.',
  adminNote: 'Windows asks for administrator approval for service changes. Choosing No changes nothing.',

  applyTitle: 'Apply settings',
  applyIntro: 'These are the settings the service on this PC runs with now. Change what you need. Apply writes them and restarts the service. Recording pauses for a moment.',
  applyUnreadable: 'The settings of the service on this PC could not be read, so they cannot be changed here.',
  applied: 'Applied. The service restarted with the new settings.',
  appliedNoRestart: 'Applied. The service was stopped, so the new settings are used when it next starts.',
  samples: 'samples',

  /* wizard */
  wizardTitle: 'Place a Blackbox',
  wizardLeave: 'Leave set-up',
  wizardNothingSaved: 'Nothing is saved until the last step.',
  partOf: (n: number) => 'Part ' + n + ' of 4',
  partNames: ['Name', 'Where', 'Settings', 'Review'],
  done: 'Done',
  next: 'Next',
  nextWhere: 'Next: Where',
  nextSettings: 'Next: Settings',
  nextReview: 'Next: Review',
  backStep: 'Back',
  cancel: 'Cancel',

  nameHeading: 'Name it',
  nameLabel: 'Name',
  nameHelp: 'Used in file names, for example unravis-blackbox-sql-prod-01.zip. Letters, numbers and dashes. Keep it the same as the server name so cases find it easily.',
  nameProblems: {
    empty: 'Type a name.',
    long: 'Use 63 characters or fewer.',
    chars: 'Use letters, numbers and dashes only, starting with a letter or number.'
  } as Record<string, string>,

  whereHeading: 'Where should it run?',
  wherePc: 'On this PC',
  wherePcText: 'Unravis installs it as a Windows service here. It starts at boot and keeps recording. Windows asks for administrator approval.',
  wherePack: 'On another server (pack)',
  wherePackText: 'Unravis builds a zip with the program, blackbox.json and a README. Copy it to the server and run it as a service.',
  wherePackNote: 'A pack contains no passwords and no data. It only holds the recorder and its settings.',
  wherePcUnavailable: 'This build cannot install on this PC. Build a pack instead.',
  wherePackUnavailable: 'This build cannot build a pack.',
  whereAlready: 'A Blackbox is already installed on this PC. Open it from the list, or remove it first.',

  settingsHeading: 'Settings',
  sampleEvery: 'Sample every',
  seconds: 'seconds',
  whatToRecord: 'What to record',
  recordCounters: 'Processor, memory, disk, network',
  recordCountersNote: 'Always on.',
  recordProcesses: 'Process list (top 20 by CPU)',
  recordLogs: 'Logs list: names, sizes and times of IIS and application logs, never their content',
  recordCopyLogs: 'Copy log files of the incident hour when a snapshot is taken',
  probeNote: 'The SQL Server probe is not part of this release.',
  processesNote: 'Switched off, the web server process row in Step 5 says Cannot test yet. Everything else works.',
  keepHeading: 'Keep',
  keepDays: 'Keep for (days)',
  keepDaysHelp: 'Older files are deleted automatically.',
  sizeLimit: 'Size limit (GB)',
  sizeLimitHelp: 'The oldest files go first.',
  keepFree: 'Keep free disk (percent)',
  keepFreeHelp: 'Recording pauses if free space falls below this.',
  folder: 'Folder',
  folderHelp: 'A folder of its own. Recordings are a manifest plus text files.',
  port: 'Status port',
  portHelp: 'The recorder answers on this port.',
  bind: 'Listens on',
  bindHelp: '127.0.0.1 means only this computer can ask it. To let Unravis on another PC read it, set the server address and add a firewall rule for the port.',
  housekeepingNote: 'Housekeeping always runs. You can lower the limits but not turn them off.',
  settingProblems: {
    name: 'Go back and fix the name.',
    folder: 'Type a full folder path such as D:\\UnravisBlackbox. A drive root is not allowed.',
    interval: 'Choose 5, 15, 30 or 60 seconds.',
    keep: 'Keep for 1 to 365 days.',
    size: 'Use a size from 0.1 to 10000 GB.',
    free: 'Use 1 to 50 percent.',
    port: 'Use a port from 1 to 65535.',
    bind: 'Type an address such as 127.0.0.1.',
    logs: 'Each log folder must be a full path.'
  } as Record<SettingsProblem, string>,

  reviewHeading: 'Review',
  reviewName: 'Name',
  reviewRuns: 'Runs',
  reviewRunsPc: 'On this PC as a Windows service named UnravisBlackbox',
  reviewRunsPack: 'On another server, from a pack you copy there',
  reviewSamples: 'Samples',
  reviewKeeps: 'Keeps',
  reviewKeepsText: (days: number, gb: number, free: number) => days + ' days, up to ' + gb + ' GB, ' + free + ' percent of the disk kept free',
  logPaths: 'Log folders to list (one per line)',
  reviewFolder: 'Folder',
  reviewPort: 'Port',
  reviewBind: 'Listens on',
  reviewProbe: 'Database probe',
  reviewProbeOff: 'Off (not in this release)',
  installButton: 'Install and start here',
  packButton: 'Download the pack',
  installing: 'Installing. Approve the Windows prompt when it appears.',
  building: 'Building the pack.',
  windowsAsks: 'Windows will ask: Do you want to allow this app to make changes to your device? Yes installs the service. No cancels, and nothing is changed.',
  packWill: (file: string, host: string) => 'The button builds ' + file + '. Copy it to ' + host + ' and unzip it. Then run install there as an administrator. Unravis does nothing on that server itself.',

  installedTitle: 'Installed on this PC',
  installedText: (name: string) => name + ' is installed and recording on this PC. Its read key was sealed on this PC the moment it was made. It is never shown.',
  installedKeyNote: 'Unravis made the read key and sealed it with Windows data protection. You do not need to copy it. Remove the Blackbox and install again if you ever need a new one.',
  installFailedTitle: 'Install did not finish',
  installUndone: 'The install stopped and was undone. Nothing is left half done.',
  installFixAndRetry: 'Fix it, then try again.',
  changeSettings: 'Change settings',

  /* pack ready (wireframe screen 64) */
  packReadyTitle: (file: string) => 'Pack ready: ' + file,
  packInside: 'Inside: the recorder program, blackbox.json, README. No passwords, no data.',
  packSteps: [
    'Copy the zip to the server.',
    'Unzip it.',
    'Open a command prompt as an administrator in that folder.',
    'Run: unravis-blackbox.exe install. The service UnravisBlackbox starts at boot. It makes the read key and prints it once: copy it there.',
    'Check it: on that server open http://localhost:7788 in a browser. It should say Running.',
    'Back here, give a case the folder, the share or the address, and type the read key. Unravis cannot scan another server for it.'
  ],
  packKeyNote: 'The read key is not in the zip. It is made on the server at install and shown once on that server.',
  packNotSeen: 'Not seen yet: Unravis has not found a Blackbox named {name}. That is expected until the service is running.',
  packBindNote: 'The status port listens on this server only, until you opt in. For another PC to read it by address, set the listen address in blackbox.json and run apply. Then open the port in the firewall for that PC only.',
  packUninstall: 'To uninstall it later, stop the UnravisBlackbox service on that server and delete it. The Remove screen has the steps.',
  packDone: 'Back to the list',
  packAgain: 'Download again',

  /* add by folder, share or address (wireframes 35 and 36) */
  addTitle: 'Add a Blackbox',
  addWhere: 'Where is it?',
  addPlace: 'Folder, network share, or address and port',
  addPlaceHelp: 'A folder such as D:\\UnravisBlackbox, a share such as \\\\web-c\\UnravisBlackbox, or an address such as http://web-a:7788. Unravis reads it as the account it runs as, which needs read access only. It does not scan the network.',
  addName: 'Name (optional)',
  addNameHelp: 'Leave it empty to use the name the Blackbox gives itself.',
  addKey: 'Read key (for an address)',
  addKeyHelp: 'Made at install and shown once on that Blackbox. It is saved sealed on this PC with Windows data protection, never shown again, and never put in a case file.',
  addKeyShow: 'Show what I type',
  addKeyHide: 'Hide what I type',
  addHttpNote: 'The address is plain http inside your network. The read key is its only protection.',
  addLookAgain: 'Look again',
  addButton: 'Add to the list',
  adding: 'Looking at the place.',
  addedTitle: 'Added',
  addFound: (name: string) => 'Found ' + name + ' and saved it.',
  addFailTitle: 'Nothing could be read there',
  addFailLikely: 'The most likely reasons:',
  addFailReasons: [
    'Wrong path. No share has that name. Check the server name and the share name.',
    'No access. The share exists but the account Unravis runs as may not read it. Give that account read access.',
    'No Blackbox there. The folder has no manifest, or the service is not running.',
    'Firewall. For an address, the port may be blocked.',
    'Key refused. The address answered but the read key was not accepted. Type the new key and look again.'
  ],
  addNothingChanged: 'Nothing was changed on that server.',
  duplicateTitle: 'That Blackbox is already in the list',
  duplicateText: (name: string) => 'It is listed as ' + name + '. It was not added a second time.',
  duplicateOpen: 'Open the one in the list',
  locationEmpty: 'Type where the Blackbox is.',
  keyNeeded: 'An address needs the read key. Type it, or use a folder or share instead.',

  /* remove (wireframe 68) */
  removeTitle: (name: string) => 'Remove ' + name,
  removeThisPc: 'Remove stops the UnravisBlackbox service on this PC and uninstalls it. Windows asks for administrator approval first. Cases that already read it keep what they imported.',
  removeKeep: 'Keep the recordings',
  removeKeepText: 'The folder stays, so a later Blackbox can read it.',
  removeDelete: 'Delete the recordings too',
  removeDeleteText: 'Deletes only the files the Blackbox made. This cannot be undone.',
  removeTypeName: (name: string) => 'Type the name ' + name + ' to turn the button on',
  removeTypeLabel: 'Name',
  removeButton: 'Remove Blackbox',
  removing: 'Removing. Approve the Windows prompt when it appears.',
  removeCasesNote: 'A case that has not read this Blackbox yet loses the source. Its host steps say Cannot test yet.',
  removedTitle: 'Removed',
  removedKept: 'The service was removed. The recordings were kept.',
  removedDeleted: (n: number) => 'The service was removed and ' + n + (n === 1 ? ' file was' : ' files were') + ' deleted.',
  removeOther: 'Another server: run these steps there. Unravis never does.',
  removeOtherSteps: 'Run in an administrator command prompt on that server. The recordings in the folder stay.',
  removeOtherCommands: 'sc stop UnravisBlackbox\nsc delete UnravisBlackbox',
  removeOtherAfter: 'Afterwards use Forget this place, so Unravis stops asking for it. That also drops the sealed read key.',
  forgetText: 'Forget removes the saved place and its sealed read key from this PC. The Blackbox and its recordings are not touched.',
  forgotten: 'Removed from the list. The Blackbox and its recordings were not touched.',

  /* snapshot (wireframe 69) */
  snapshotTitle: (name: string) => 'Snapshot taken: ' + name,
  snapshotSaved: (dir: string, size: string) => 'Saved: ' + dir + ' (' + size + ').',
  snapshotRecordings: (n: number) => 'Recording files copied: ' + n + '. Copied as they are.',
  snapshotLogs: (n: number) => 'Log files copied: ' + n + '. They are copied only when that setting is ticked.',
  snapshotNot: 'Not in it: passwords, request payloads, the read key.',

  /* host reading (Step 9) */
  hostTitle: 'Step 9: Host and network',
  hostQuestion: 'Was the web server or the SQL server short of CPU, memory or disk?',
  hostRead: 'Read for a case window',
  hostIncident: 'Incident window',
  hostReference: 'Reference window',
  hostStart: 'Start',
  hostEnd: 'End',
  hostRun: 'Read the host for these windows',
  hostRunning: 'Reading the recordings.',
  hostChecks: 'Checks',
  hostMinutes: (covered: number) => covered + ' minutes were read.',
  hostOnset: 'The host first moved at',
  hostTopProcess: 'Busiest process near the start',
  hostNutshellLabel: 'In a nutshell',
  hostGapTitle: 'Some minutes were not recorded',
  hostGapShort: (minutes: number) => minutes + (minutes === 1 ? ' minute has' : ' minutes have') + ' no sample, in gaps of 2 minutes or less. The check ran on the minutes that exist. Missing minutes are never counted as zero.',
  hostGapLong: (minutes: number) => minutes + (minutes === 1 ? ' minute has' : ' minutes have') + ' no sample, in gaps longer than 2 minutes. For those minutes this step says Cannot test yet.',
  hostGapList: 'Gaps',
  hostGapMinutes: (m: number) => m + (m === 1 ? ' minute' : ' minutes'),
  hostReadNote: (files: number, samples: number) => files + (files === 1 ? ' file' : ' files') + ', ' + samples + ' samples read.',
  hostTorn: (n: number) => n + (n === 1 ? ' line was' : ' lines were') + ' cut off at the end of a file and skipped.',
  hostDropped: 'Some files could not be read to the end, so their minutes count as a gap.',
  hostWindowProblem: 'Each window needs an end after its start, and no window may be longer than 7 days.',
  hostWords: { Ready: 'Ready', 'Partly ready': 'Partly ready', 'Cannot test yet': 'Cannot test yet', Failed: 'Failed' } as Record<string, string>
};

/** Words for a refused call that the server did not explain in full. */
export const BB_REFUSAL: Record<string, string> = {
  rate: 'Too many tries in a minute. Wait a minute and try again.',
  offline: 'Unravis could not be reached. Check that it is still running.',
  no_session: 'Open Unravis from the link it printed when it started, or restart it.',
  unavailable: 'Blackbox is not available in this build.',
  'no-helper': 'This build cannot change the Blackbox on this PC. Use the pack and set it up on the server by hand.',
  'busy-here': 'Another change to the Blackbox on this PC is still running. Try again when it has finished.',
  'list-damaged': 'The saved Blackbox list is damaged and was left as it is.',
  'list-unavailable': 'The saved Blackbox list could not be opened.',
  'key-not-saved': 'The Blackbox was installed, but Unravis could not save its read key. Remove it from this PC and install it again.'
};

const STATE_IDS = Object.keys(BB_TEXT.stateNext) as PageState[];

/** The texts the copy lint reads. Functions are called with sample values. */
export function blackboxCopyEntries(): LintEntry[] {
  const out: LintEntry[] = [];
  const add = (id: string, text: string) => { out.push({ id: 'blackbox.' + id, text }); };
  const T = BB_TEXT;
  for (const [key, value] of Object.entries(T)) {
    if (typeof value === 'string') add(key, value);
    else if (Array.isArray(value)) value.forEach((v, i) => add(key + '.' + i, v));
    else if (typeof value === 'function') {
      const fn = value as (...args: any[]) => string;
      if (key === 'packWill') add(key, fn('unravis-blackbox-web-a.zip', 'web-a'));
      else if (key === 'partOf') add(key, fn(2));
      else if (key === 'packReadyTitle' || key === 'removeTitle' || key === 'removeTypeName' || key === 'snapshotTitle' || key === 'duplicateText' || key === 'addFound' || key === 'installedText') add(key, fn('web-a'));
      else if (key === 'snapshotSaved') add(key, fn('D:\\UnravisBlackbox\\snapshots\\web-a-2026-10-20-0905', '46 MB'));
      else if (key === 'removedDeleted' || key === 'snapshotRecordings' || key === 'snapshotLogs' || key === 'hostTorn' || key === 'hostGapShort' || key === 'hostGapLong' || key === 'hostGapMinutes' || key === 'hostMinutes') { add(key + '.1', fn(1)); add(key + '.2', fn(3)); }
      else if (key === 'hostReadNote') add(key, fn(2, 240));
      else if (key === 'reviewKeepsText') add(key, fn(7, 2, 10));
    }
  }
  for (const id of STATE_IDS) add('stateNext.' + id, T.stateNext[id]);
  for (const [k, v] of Object.entries(T.nameProblems)) add('nameProblems.' + k, v);
  for (const [k, v] of Object.entries(T.settingProblems)) add('settingProblems.' + k, v);
  for (const [k, v] of Object.entries(BB_REFUSAL)) add('refusal.' + k, v);
  return out;
}
