import type { PageState, SettingsProblem, TargetsProblem } from '../blackbox';
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
  readOnly: 'Unravis reads your servers. It never changes them. On this PC it can start, stop, change or remove its own Blackbox, and Windows asks you first.',

  place: 'Place a Blackbox',
  addExisting: 'Add one by folder, share or address',
  scanPc: 'Scan this PC',
  scanning: 'Looking at this PC.',
  scanFailed: 'This PC could not be asked whether a Blackbox is installed. Press Scan this PC again. If it fails again, restart Unravis.',
  loading: 'Loading the Blackbox list.',
  loadFailed: 'The Blackbox list could not be loaded.',
  reload: 'Reload this page',
  retry: 'Try again',

  emptyTitle: 'No Blackbox yet',
  emptyText: 'A Blackbox records host measurements all the time. Without one, host steps in a case say "Cannot test yet". Place one on a server before you need it.',
  scanNothingNew: (name: string) => name + ' on this PC is already in the list. Nothing new was found.',
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
  diskFree: 'Recordings drive free',
  updates: 'Recorder updates',
  updateLine(u: { state: string; latest?: string; checked?: string; result?: string }): string {
    const v = u.latest ? ' ' + u.latest : '';
    switch (u.state) {
      case 'available': return 'Recorder' + v + ' is ready to install.';
      case 'held': return 'Recorder' + v + ' is on hold.';
      case 'downloading': return 'Downloading recorder' + v + '.';
      case 'installing': return 'Installing recorder' + v + '.';
      case 'skipped': return 'Recorder' + v + ' was skipped.';
      case 'failed': return 'The last update failed.';
      default: return u.checked || u.state === 'managed' || u.state === 'off' ? (u.result || 'Checked.') : 'Not checked yet.';
    }
  },
  intervalHelp: 'Unravis slows recording when the PC is busy, so the recorder stays light.',
  checkedAt: 'Checked',
  checkedFootnote: (at: string) => 'Checked ' + at + '.',
  host: 'Host',
  version: 'Version',
  noSample: 'none yet',
  noRecordingYet: 'No recording yet.',
  noGaps: 'none',
  openStatus: 'Open status',
  check: 'Check again',
  checking: 'Checking.',
  back: 'Back to the list',

  /* the nine states and what to do about them (wireframe screen 67) */
  stateNext: {
    running: 'Nothing to do. It is recording.',
    'running-late': 'No sample for more than three intervals. On the server, open Services, find UnravisBlackbox and restart it.',
    stopped: 'Start it again. On this PC press Start. On another server use Services.',
    'disk-low': 'Free space on that drive, or lower the size limit. Recording resumes by itself.',
    'clock-differs': 'Fix the clock on that server. Until then, events less than a second apart are not put in order.',
    'not-found': 'Check the folder, share or address, then check again.',
    unreachable: 'Check that the server is on and that Unravis can reach and read it, then check again.',
    incomplete: 'The recording has gaps. A gap of up to 2 minutes only warns. A longer gap makes the minutes inside it "Cannot test yet".',
    'not-placed': 'Place a Blackbox before you need it. Until then host steps say "Cannot test yet".'
  } as Record<PageState, string>,
  servicesStepsLate: 'On the server, open Services (services.msc). Find UnravisBlackbox. Choose Restart. Wait one minute. The state here changes to Running.',
  servicesSteps: 'On the server, open Services (services.msc). Find UnravisBlackbox. Choose Start, or Restart if it is late. Wait one minute. The state here changes to Running.',
  servicesTitle: 'Steps for the server',
  anotherServer: 'This is not the Blackbox installed on this PC, so Unravis does not offer Stop or Snapshot for it. Use the steps below.',
  unreachableUrl: 'Check that the server is on and that the port is open to this PC, then check again.',
  unreachableShare: 'Check the server name, the share name and that the account Unravis runs as may read the share, then check again.',
  stoppedThisPc: 'Press Start. The recordings are kept.',
  stoppedRemote: 'Start it from Services on that server. Unravis does not start another server.',
  failHeadline: 'Cannot reach this Blackbox',
  readKeyNote: 'You typed the read key when you added this Blackbox. It is saved here, sealed with Windows data protection, and never shown again. If the server makes a new key, press "Type a new read key".',
  readKeyNoteThisPc: 'Unravis made the read key when it installed this Blackbox and sealed it on this PC at once. It is shown only here, only when you press Show or Copy, and never put in a case file.',
  keyTitle: 'Read key',
  keyHereOnly: 'Shown here only for a Blackbox that is on this PC.',
  keyShow: 'Show',
  keyHide: 'Hide',
  keyCopy: 'Copy',
  keyWorking: 'Getting the read key.',
  keyCopied: 'Copied. Paste it where it is needed. Unravis does not keep a copy of it on this page.',
  keyShownAnnounce: 'Read key shown.',
  keyHiddenAgain: 'The key was hidden again after a short time.',
  keyFailed: 'The read key could not be shown. Press Show again. If it fails again, close and reopen this page.',
  keyCopyFailed: 'Copy did not work here. Press Show and copy it by hand.',
  keyHeld: 'The key keeper holds this read key and never shows it. Ask the person who looks after this PC.',
  readKeySaved: 'A read key is saved on this PC.',
  readKeyNone: 'This place needs no read key.',
  readKeyMissing: 'No read key is saved for this address. Press "Type a new read key".',
  keyFieldHelp: 'Paste the current key. It is saved sealed on this PC and never shown again.',
  changeKey: 'Type a new read key',
  newKey: 'New read key',
  saveKey: 'Save the new key',
  keySaved: 'The new key was tried and saved.',
  keyHiddenNow: 'Key is hidden.',
  keyHidesIn: 'It hides again in 30 seconds.',

  /* this-PC actions */
  start: 'Start',
  stop: 'Stop',
  snapshot: 'Snapshot now',
  updateTitle: 'A newer recorder is ready',
  updateText: 'This PC runs an older recorder than the one that came with Unravis. Updating swaps in the new one and starts it again. Windows asks for one administrator approval. Your recordings and settings are kept.',
  updateButton: 'Update the recorder',
  apply: 'Apply settings',
  remove: 'Remove',
  removeForget: 'Forget this place',
  working: 'Working. Windows may ask for administrator approval.',
  workingStart: 'Starting. Windows may ask for approval. If you do not see it, check the taskbar.',
  workingStop: 'Stopping. Windows may ask for approval. If you do not see it, check the taskbar.',
  workingSnapshot: 'Copying the recordings.',
  workingUpdate: 'Updating the recorder. Windows may ask for approval. If you do not see it, check the taskbar.',
  updatedRestarted: 'Updated. The recorder started again with the new program. Your recordings and settings were kept.',
  updatedStopped: 'Updated. The recorder was stopped, so it stays stopped until you press Start. Your recordings and settings were kept.',
  updateCurrent: 'Already up to date. Nothing was changed and the recorder was not restarted.',
  started: 'Started. Check again in a few seconds.',
  stopped: 'Stopped. The recordings are kept.',
  confirmStop: 'While stopped, nothing is recorded. A case that reads this Blackbox sees a gap from then on.',
  stopTitle: 'Stop recording?',
  stopYes: 'Stop recording',
  stopKeep: 'Keep recording',
  adminNote: 'Windows asks for administrator approval for service changes. If you choose No in that prompt, nothing changes.',

  applyTitle: 'Apply settings',
  applyIntro: 'These are the settings the service on this PC runs with now. Change what you need. Apply writes them and restarts the service. Recording pauses for a moment.',
  lockedField: 'Fixed after install. Remove it and place it again to change it.',
  applyLocked: 'Unravis finds the Blackbox by its port and address, so they cannot be changed here. To use others, remove it and place it again.',
  applyChangedLabels: {
    interval: 'Interval', processes: 'Process list', logs: 'IIS log catalogue', copyLogs: 'Log copies in a snapshot', targets: 'Watched sites and services',
    keepDays: 'Keep days', maxGB: 'Size limit', minFreePercent: 'Free disk', folder: 'Folder', logPaths: 'Log folders'
  } as Record<string, string>,
  applyChangedLine: (parts: string[]) => 'You changed: ' + parts.join('. ') + '.',
  applyFolderWarn: 'Old recordings stay in the old folder and are not moved. A case or reader must use the new folder.',
  applyIntervalWarn: 'Unravis can still slow recording when the PC is busy.',
  applyNoChange: 'Nothing is changed yet.',
  applyUnreadable: 'The settings of the service on this PC could not be read, so they cannot be changed here.',
  applied: 'Applied. The service restarted with the new settings.',
  appliedNoRestart: 'Applied. The service was stopped, so the new settings are used when it next starts.',
  samples: 'samples',
  // "4 files and 960 samples", never "4, 960", which reads as 4,960.
  filesAndSamples: (files: number, samples: number) => files + (files === 1 ? ' file' : ' files') + ' and ' + samples + (samples === 1 ? ' sample' : ' samples'),
  // The Gaps row. Files that could not be read to the end are what make a Blackbox Incomplete, so they show here too.
  // With the length of the longest gap it says how long, and whether the gap only warns (2 minutes or less) or makes its minutes "Cannot test yet".
  gapsText: (gaps: number, unread: number, longestSeconds = 0) => {
    const parts: string[] = [];
    if (gaps > 0) {
      let text = gaps + (gaps === 1 ? ' gap' : ' gaps');
      if (longestSeconds > 0) {
        const short = longestSeconds <= 120;
        const length = short ? longestSeconds + (longestSeconds === 1 ? ' second' : ' seconds') : Math.round(longestSeconds / 60) + ' minutes';
        text = (gaps === 1 ? '1 gap of ' + length : gaps + ' gaps, the longest is ' + length)
          + (short ? '. This is a warning only. Missing minutes are never counted as zero' : '. For those minutes the step says "Cannot test yet"');
      }
      parts.push(text);
    }
    if (unread > 0) parts.push(unread + (unread === 1 ? ' recording file was' : ' recording files were') + ' not read to the end');
    return parts.length ? parts.join('. ') : 'none';
  },
  goneFromList: 'That Blackbox is not in the list any more. It may have been removed in another window.',

  /* wizard */
  wizardTitle: 'Place a Blackbox',
  wizardLeave: 'Leave set-up',
  wizardNothingSaved: 'Nothing is saved until the last step.',
  partOf: (n: number) => 'Part ' + n + ' of 4',
  partNames: ['Name', 'Where', 'Settings', 'Review'],
  // The visible heading line, for example "Part 2 of 4: Where" (D-B-49).
  partHeading: (n: number, label: string) => 'Part ' + n + ' of 4: ' + label,
  done: 'Done',
  next: 'Next',
  nextWhere: 'Next: Where',
  nextSettings: 'Next: Settings',
  nextReview: 'Next: Review',
  backStep: 'Back',
  cancel: 'Cancel',
  // The Status, Remove, Apply and Snapshot pop-up (BL-131).
  popClose: 'Close',
  popCloseLabel: 'Close this Blackbox',
  popLeaveTitle: 'Leave without applying?',
  popLeaveBody: 'Your changes to the settings are not saved.',
  popKeepEditing: 'Keep editing',
  popLeave: 'Leave',
  popBusy: 'Please let the current action finish first.',

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
  recordProcesses: 'Process list: the top 10 by processor and top 5 by memory, plus any you watch',
  recordLogs: 'IIS log catalogue: names, sizes and times of the newest log files, never their content',
  recordCopyLogs: 'Log copies in a snapshot: copy the log files of the incident hour when a snapshot is taken',
  probeNote: 'The SQL Server probe is not part of this release.',
  processesNote: 'With the process list unticked, the web server process row in Step 5 says "Cannot test yet". Everything else works.',
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
  bindHelp: '127.0.0.1 means only this computer can ask it. To let Unravis on another PC read it, set the server address. Unravis does not add a firewall rule. Adding one is your own choice.',
  // Shown when the listen address is every address (D-B-50, D-C-20). Warn only; never refuse.
  bindWarn: 'This lets other computers on the network reach the status port. The read key is the only protection.',
  reviewBindWarn: 'This Blackbox listens on every address, so other computers can reach it. The read key is the only protection.',
  housekeepingNote: 'Old recordings are always removed at these limits. You can lower the limits but not turn them off.',
  settingProblems: {
    name: 'Go back and fix the name.',
    folder: 'Type a full folder path such as D:\\UnravisBlackbox. A drive root or a Windows system folder is not allowed.',
    interval: 'Choose 5, 15, 30 or 60 seconds.',
    keep: 'Keep for 1 to 365 days.',
    size: 'Use a size from 0.1 to 10000 GB.',
    free: 'Use 1 to 50 percent.',
    port: 'Use a port from 1 to 65535.',
    bind: 'Listens on needs an address such as 127.0.0.1, or a server name. Spaces and other symbols are not allowed.',
    logs: 'Each log folder must be a full path.',
    targets: 'Check the targets. Use up to 32 names, each 1 to 128 characters, with no \\ / : * ? " < > | and no name twice.'
  } as Record<SettingsProblem, string>,

  /* targets: the app pools and services picked in the Settings step (BBX-1-12) */
  targetsHeading: 'Websites and services to watch closely (optional)',
  // UX-8: the help says what the choice does, in the first words a first-time investigator reads:
  // a picked name is measured on its own as well as on the machine, and an empty list is the
  // machine alone. No jargon, no acronym.
  targetsHelp: 'Each watched name gets its own processor and memory record too, and none means the whole machine only.',
  targetsAppPools: 'App pools',
  targetsServices: 'Services',
  targetsNotRunning: 'not running',
  targetsNotInList: 'not in the detected list; running state unknown',
  targetsListUnreadable: 'list could not be read; running state unknown',
  targetsSourceNotFound: 'not detected on this PC; running state unknown',
  targetsKindLabel: 'Kind',
  targetsKindAppPool: 'App pool',
  targetsKindService: 'Service',
  targetsNameLabel: 'Target name',
  targetsManualNote: 'Type the name of an app pool or service, then press Add target.',
  targetsListNote: 'The list may be incomplete. Type a name below to add a target that is not listed.',
  // UX-6: one note under a group's legend, never the same note on every row. UX-8b: it fires when ANY
  // row's running state could not be read, so the sentence says "some of these" and not "these": it is
  // shown for a mixed group too, where some states were read. It states what the missed states mean for
  // the numbers. The filter never hides silently that names exist: the count line says how many are
  // shown, and the no-match line says how to see them all.
  targetsAllUnknown: 'Unravis could not read whether some of these are running. A target that is not running records nothing, so a missing figure may mean nothing was recorded.',
  targetsFilterLabel: 'Find a name',
  targetsFilterCount: (shown: number, total: number) => 'Showing ' + shown + ' of ' + total + ' names.',
  targetsFilterNone: (total: number) => 'No name matches. Clear the box to see all ' + total + ' names.',
  // What the detected lists say while the one read is in flight, or when it could not be read (UX-5).
  // A failed read is never shown as an empty healthy list; each message says what to do instead.
  targetsLoading: 'Looking for the app pools and services on this PC.',
  targetsLoadFailed: 'The app pools and services on this PC could not be read. Type the names below to watch them anyway.',
  targetsPoolsNotFound: 'No app pools were found on this PC. That is normal when IIS is not installed here.',
  targetsPoolsUnreadable: 'The app pools on this PC could not be read. Type an app pool name below to watch it.',
  targetsServicesNotFound: 'No services were found on this PC. Type a service name below to watch it.',
  targetsServicesUnreadable: 'The services on this PC could not be read. Type a service name below to watch it.',
  targetsAdd: 'Add target',
  targetsRemove: 'Remove target',
  // Why the Add target button is off: nothing is typed yet, or that name is already on the list.
  targetsAddNeedsName: 'Type a name first. Then Add target works.',
  targetsAddAlready: 'That name is already on the list.',
  // UX-7: the words the panel says out loud when a name goes on or off the list. They claim only what
  // the control actually did - these settings are not saved and nothing starts recording until the
  // Apply or Install step runs, so no running, recording or saving word belongs here.
  targetsAdded: (name: string) => name + ' added to the targets.',
  targetsRemoved: (name: string) => name + ' removed from the targets.',
  // Why a typed target was not added. The name stays in the box so it can be fixed (UX-4).
  targetProblems: {
    empty: 'Type the name of the app pool or service.',
    long: 'Use a name of 128 characters or fewer.',
    chars: 'A name cannot contain \\ / : * ? " < > | or a line break.',
    kind: 'Choose App pool or Service.',
    duplicate: 'That target is already in the list. Capital and small letters count as the same name.',
    many: 'The list holds up to 32 targets. Remove one before you add another.'
  } as Record<TargetsProblem, string>,

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
  reviewTargets: 'Watched closely',
  reviewTargetsNone: 'No app pool or service was picked. The Blackbox records the machine-wide counters for the whole server only.',
  // The Review row for the picked targets, for example "App pools: A, B. Services: C." An empty group is left out.
  reviewTargetsText: (appPools: string[], services: string[]) => {
    const parts: string[] = [];
    if (appPools.length) parts.push('App pools: ' + appPools.join(', '));
    if (services.length) parts.push('Services: ' + services.join(', '));
    return parts.length ? parts.join('. ') + '.' : 'none';
  },
  reviewProbe: 'Database probe',
  reviewProbeOff: 'Off (not in this release)',
  installButton: 'Install and start here',
  packButton: 'Download the pack',
  installing: 'Installing. Approve the Windows prompt when it appears.',
  building: 'Building the pack.',
  windowsAsks: 'Windows will ask: Do you want to allow this app to make changes to your device? Yes installs the service. No cancels, and nothing is changed.',
  packWill: (file: string, host: string) => 'The button builds ' + file + '. Copy it to ' + host + ' and unzip it. Then run install there as an administrator. Unravis does nothing on that server itself.',

  installedTitle: 'Installed on this PC',
  installedText: (name: string) => name + ' is installed and recording on this PC. Its read key was sealed on this PC the moment it was made. It is shown only if you press Show or Copy on its status screen.',
  installedKeyNote: 'Unravis made the read key and sealed it with Windows data protection. You only need it to let another PC read this Blackbox, and Show or Copy is on its status screen. Remove the Blackbox and install again if you ever need a new one.',
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
  addLooksGood: 'Looks good. Press Add to the list to keep it.',
  addNothingChanged: 'Nothing was changed on that server.',
  statusReadOnly: 'Unravis only reads here. Nothing was changed on that server.',
  hostResultTitle: (word: string) => 'Host reading: ' + word.toLowerCase(),
  duplicateTitle: 'That Blackbox is already in the list',
  duplicateText: (name: string) => 'It is listed as ' + name + '. It was not added a second time.',
  duplicateOpen: 'Open the one in the list',
  locationEmpty: 'Type where the Blackbox is.',
  keyNeeded: 'An address needs the read key. Type it, or use a folder or share instead.',

  /* remove (wireframe 68) */
  removeTitle: (name: string) => 'Remove ' + name,
  removeStops: 'Remove stops the Blackbox service on this PC. Then it removes the service. Windows asks you to approve first.',
  removeStopsLive: 'Recording ends now. Remove stops the Blackbox service on this PC and then removes it. Windows asks you to approve first.',
  removeChoiceLegend: 'What happens to the recordings',
  removeKeep: 'Keep the recordings',
  removeKeepText: 'The folder stays, so a later Blackbox can read it.',
  removeDelete: 'Delete the recordings too',
  removeDeleteText: 'Deletes only the files the Blackbox made. This cannot be undone.',
  removeTypeName: (name: string) => 'Type the name ' + name + ' to turn the button on',
  removeTypeLabel: 'Name',
  removeButton: 'Remove Blackbox',
  removeButtonDelete: 'Remove and delete recordings',
  removeNameMismatch: 'The name does not match yet.',
  removeFailedHint: 'Check Scan this PC to see what is left.',
  removing: 'Removing. Approve the Windows prompt when it appears.',
  removeCases: 'Cases that already read it keep what they imported. A case that has not read it yet loses the source and says "Cannot test yet".',
  removedTitle: 'Removed',
  removedKept: 'The service was removed. The recordings were kept.',
  removedDeleted: (n: number) => 'The service was removed and ' + n + (n === 1 ? ' file was' : ' files were') + ' deleted.',
  removeOther: 'Another server: run these steps there. Unravis never does.',
  removeOtherSteps: 'Run in an administrator command prompt on that server. The recordings in the folder stay.',
  removeOtherCommands: 'sc stop UnravisBlackbox\nsc delete UnravisBlackbox',
  removeOtherAfter: 'Afterwards use Forget this place, so Unravis stops asking for it. That also drops the sealed read key.',
  removeNotPlaced: 'No service with this name is installed on this PC now. Forget this place to clear it from the list. Nothing else is touched.',
  forgetText: 'Forget removes the saved place and its sealed read key from this PC. The Blackbox and its recordings are not touched.',
  forgotten: 'Removed from the list. The Blackbox and its recordings were not touched.',

  /* snapshot (wireframe 69) */
  snapshotTitle: (name: string) => 'Snapshot taken: ' + name,
  snapshotSaved: (dir: string, size: string) => 'Saved: ' + dir + ' (' + size + ').',
  snapshotRecordings: (n: number) => 'Recording files copied: ' + n + '. Copied as they are.',
  snapshotLogs: (n: number) => 'Log files copied: ' + n + '. They are copied only when that setting is ticked.',
  snapshotCopyPath: 'Copy the path',
  snapshotPathCopied: 'Path copied.',
  snapshotPathCopyFailed: 'Copy did not work here. Select the path above and copy it by hand.',
  snapshotNot: 'Not in it: passwords, request payloads, the read key.',

  /* use in a case (wireframe 35) */
  caseUseTitle: 'Use in a case',
  caseUseIntro: 'A case can take its host readings for Step 9 from one Blackbox. The case keeps only the name, never the address, the folder or the read key.',
  caseUseNoCase: 'Open a case first. Then this Blackbox can be used in it.',
  caseUseNotPlaced: 'This Blackbox is not placed yet, so a case cannot use it.',
  caseUseNotReadable: 'This Blackbox cannot be read right now, so a case cannot use it yet. Fix that first, then use it here.',
  caseUseClosed: 'This case is closed. Its Blackbox use cannot be changed. Reopen the case to change it.',
  caseUseButton: 'Use in this case',
  caseUseStop: 'Stop using it in this case',
  caseUseChecking: 'Looking at the case.',
  caseUseOn: 'This case uses this Blackbox for Step 9.',
  caseUseOther: (name: string) => 'This case already uses ' + name + '. A case reads one Blackbox. Stop using that one first.',
  caseUseAdded: 'Added. Step 9 reads this Blackbox the next time the case is investigated.',
  caseUseRemoved: 'Removed from this case. The Blackbox and its recordings are as they were.',
  caseUseNext: 'Try again. If it fails again, close and reopen this page.',
  caseUseFailed: 'The case could not be changed. Try again. If it fails again, close and reopen this page.',
  caseUseRetry: 'Look again',

  /* diagnostics (the timing log) */
  diagTitle: 'Diagnostics',
  diagIntro: 'Why is this Blackbox slow or late? This reads its timing log and says what it found.',
  diagRead: 'Read the timing log',
  diagReadAgain: 'Read again',
  diagCopy: 'Copy diagnostics',
  diagReading: 'Reading the log.',
  diagCopied: 'Copied. Paste it where you need it.',
  diagCopyFailed: 'Could not copy. Select the text below and copy it by hand.',
  diagFailed: 'The diagnostics could not be read.',
  diagLog: 'Newest log lines',
  diagLogLabel: 'Diagnostics log lines',
  diagNone: 'There is nothing to report yet. The recorder has not written a timing log.',

  /* host reading (Step 9) */
  hostTitle: 'Step 9: Host and network',
  hostQuestion: 'Was the web server or the SQL server short of CPU, memory or disk?',
  hostRead: 'Step 9: choose the windows to read',
  hostIncident: 'Incident window',
  hostWindowFrom: 'from',
  hostWindowTo: 'to',
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
  hostNutshell: 'We check the host against what this Blackbox recorded: CPU, memory, disk and network. A minute it did not record is never counted as zero.',
  hostGapTitle: 'Some minutes were not recorded',
  hostGapShort: (minutes: number) => minutes + (minutes === 1 ? ' minute has' : ' minutes have') + ' no sample, in gaps of 2 minutes or less. The check ran on the minutes that exist. Missing minutes are never counted as zero.',
  hostGapLong: (minutes: number) => minutes + (minutes === 1 ? ' minute has' : ' minutes have') + ' no sample, in gaps longer than 2 minutes. For those minutes this step says "Cannot test yet".',
  hostGapList: 'Gaps',
  hostGapMinutes: (m: number) => m + (m === 1 ? ' minute' : ' minutes'),
  hostReadNote: (files: number, samples: number) => files + (files === 1 ? ' file' : ' files') + ', ' + samples + ' samples read.',
  hostTorn: (n: number) => n + (n === 1 ? ' line was' : ' lines were') + ' cut off at the end of a file and skipped.',
  hostDropped: 'Some files could not be read to the end, so their minutes count as a gap.',
  hostNoEarlier: (at: string) => 'Recording started at ' + at + ', so there is no earlier time to compare with. Choose the reference window yourself.',
  hostWindowProblem: 'Each window needs an end after its start, and no window may be longer than 7 days.',
  hostWords: { Ready: 'Ready', 'Partly ready': 'Partly ready', 'Cannot test yet': 'Cannot test yet', Failed: 'Failed' } as Record<string, string>
};

/** Words for a refused call that the server did not explain in full. */
export const BB_REFUSAL: Record<string, string> = {
  rate: 'Too many tries in a minute. Wait a minute and try again.',
  offline: 'Unravis did not answer. If you restarted it, reload this page. If it is still starting, wait a moment and try again.',
  no_session: 'This page lost its link to Unravis, often after a restart. Reload this page. If that does not help, open Unravis from the link it printed.',
  other: 'Something went wrong and the Blackbox was left as it was. Press the button again. If it fails again, press Check again to see its state.',
  unavailable: 'Blackbox is not available in this build.',
  'no-helper': 'This build cannot change the Blackbox on this PC. Use the pack and set it up on the server by hand.',
  'busy-here': 'Another change to the Blackbox on this PC is still running. Try again when it has finished.',
  'list-damaged': 'The saved Blackbox list is damaged and was left as it is.',
  'list-unavailable': 'The saved Blackbox list could not be opened.',
  'key-not-saved': 'The Blackbox was installed, but Unravis could not save its read key. Remove it from this PC and install it again.',
  // The server refused the settings and did not say which field. Every such refusal is checked before
  // anything is written, so nothing was changed (UX-4).
  'invalid-config': 'These settings are not allowed. Nothing was saved. Check the folder, the limits and the target names, then try again.'
};

/** One refused setting, in the server's words made plain: what was refused, then how to fix it. Nothing was saved. */
export const refusedSetting = (field: SettingsProblem): string => 'The server did not accept this. ' + BB_TEXT.settingProblems[field] + ' Nothing was saved.';

/** "Recording every 60 seconds (set to 15; slowed to stay light)". One helper for every screen that shows the interval. */
export function intervalWords(set: number, effective?: number | null): string {
  const unit = (n: number) => n + (n === 1 ? ' second' : ' seconds');
  if (!effective || effective <= 0 || effective === set) return 'Recording every ' + unit(set);
  const word = effective > set ? 'slowed to stay light' : 'faster than set';
  return 'Recording every ' + unit(effective) + ' (set to ' + set + '; ' + word + ')';
}

/** How long ago, in plain words. An unreadable or future time gives ''. */
export function ageWords(value: string | undefined | null, now: number = Date.now()): string {
  if (!value) return '';
  const t = Date.parse(value);
  if (!Number.isFinite(t) || t > now + 60_000) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return 'less than a minute ago';
  const m = Math.round(s / 60);
  if (m < 60) return m + (m === 1 ? ' minute ago' : ' minutes ago');
  const h = Math.round(m / 60);
  if (h < 48) return h + (h === 1 ? ' hour ago' : ' hours ago');
  const d = Math.round(h / 24);
  return d + ' days ago';
}

const UNREACHABLE_NEXT: Record<string, string> = {
  key: 'The read key was refused. Press "Type a new read key" and paste the current one.',
  wait: 'Too many wrong keys were tried. Wait a few minutes, then press Check again.',
  busy: 'The recorder was busy. Wait a minute, then press Check again.',
  'newer-format': 'This Blackbox is newer than this Unravis. Update Unravis.'
};

/**
 * The next step for one state. It follows the server's reason for Unreachable, tells a paused recorder from one that
 * is only low on space, and gives this PC the buttons it really has.
 */
export function stateNextFor(info: { state: PageState; tone?: string; reason?: string }, thisPc: boolean, kind?: 'folder' | 'share' | 'url' | null): string {
  const T = BB_TEXT;
  switch (info.state) {
    case 'unreachable':
      return (info.reason && UNREACHABLE_NEXT[info.reason]) || (kind === 'url' ? T.unreachableUrl : kind === 'share' ? T.unreachableShare : T.stateNext.unreachable);
    case 'stopped':
      return thisPc ? T.stoppedThisPc : T.stoppedRemote;
    case 'running-late':
      return thisPc ? 'No sample for more than three intervals. Press Stop, then Start.' : T.stateNext['running-late'];
    case 'disk-low':
      return info.tone === 'bad'
        ? 'Free space on that drive. Recording resumes by itself.'
        : 'Free space on that drive, or lower the size limit, before it pauses.';
    case 'incomplete':
      return 'Some recording files could not be read to the end. Press Check again. If it stays, copy the files again.';
    default:
      return T.stateNext[info.state];
  }
}

/** The reasons an add can fail that fit what was typed: a path for a folder or share, an address for a URL. */
export function addFailReasonsFor(kind: 'folder' | 'share' | 'url' | null): string[] {
  const r = BB_TEXT.addFailReasons;
  if (!kind) return r;
  return kind === 'url' ? [r[2], r[3], r[4]] : [r[0], r[1], r[2]];
}

const STATE_IDS = Object.keys(BB_TEXT.stateNext) as PageState[];

/** One changed setting as a short phrase: "Interval 15 to 30 seconds", "Folder D:\a to E:\b". Lists are named, not dumped. */
export function applyChangePart(c: { field: string; from: string; to: string }): string {
  const label = BB_TEXT.applyChangedLabels[c.field] ?? c.field;
  if (c.field === 'targets' || c.field === 'logPaths') return label + ' changed';
  const unit = c.field === 'interval' ? ' seconds' : c.field === 'maxGB' ? ' GB' : c.field === 'minFreePercent' ? ' percent' : c.field === 'keepDays' ? ' days' : '';
  const shown = (v: string) => (v === '' ? 'none' : v);
  return label + ' ' + shown(c.from) + ' to ' + shown(c.to) + unit;
}

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
      else if (key === 'partHeading') add(key, fn(2, 'Where'));
      else if (key === 'packReadyTitle' || key === 'removeTitle' || key === 'removeTypeName' || key === 'snapshotTitle' || key === 'duplicateText' || key === 'addFound' || key === 'installedText' || key === 'caseUseOther') add(key, fn('web-a'));
      else if (key === 'snapshotSaved') add(key, fn('D:\\UnravisBlackbox\\snapshots\\web-a-2026-10-20-0905', '46 MB'));
      else if (key === 'removedDeleted' || key === 'snapshotRecordings' || key === 'snapshotLogs' || key === 'hostTorn' || key === 'hostGapShort' || key === 'hostGapLong' || key === 'hostGapMinutes' || key === 'hostMinutes') { add(key + '.1', fn(1)); add(key + '.2', fn(3)); }
      else if (key === 'hostNoEarlier') add(key, fn('2026-11-17 09:05 UTC'));
      else if (key === 'hostReadNote') add(key, fn(2, 240));
      else if (key === 'targetsFilterCount') { add(key + '.1', fn(1, 200)); add(key + '.2', fn(10, 200)); }
      else if (key === 'targetsFilterNone') add(key, fn(200));
      // A real service name, so the linted sample reads as the page does (UX-7).
      else if (key === 'targetsAdded' || key === 'targetsRemoved') add(key, fn('W3SVC'));
      else if (key === 'reviewKeepsText') add(key, fn(7, 2, 10));
      else if (key === 'reviewTargetsText') { add(key + '.both', fn(['PoolA', 'PoolB'], ['SvcOne'])); add(key + '.pools', fn(['PoolA'], [])); add(key + '.services', fn([], ['SvcOne'])); }
      else if (key === 'updateLine') { for (const st of ['idle', 'managed', 'available', 'held', 'skipped', 'downloading', 'installing', 'failed', 'off']) add(key + '.' + st, fn({ state: st, latest: 'v1.2.3' })); add(key + '.idle.checked', fn({ state: 'idle', checked: '2026-11-21T10:00:00Z', result: 'Could not reach the update feed.' })); }
      else if (key === 'filesAndSamples') { add(key + '.1', fn(1, 1)); add(key + '.2', fn(4, 960)); }
      else if (key === 'gapsText') {
        add(key + '.none', fn(0, 0)); add(key + '.gaps', fn(2, 0)); add(key + '.unread', fn(0, 1)); add(key + '.both', fn(1, 3));
        add(key + '.short', fn(1, 0, 90)); add(key + '.shorts', fn(2, 0, 100)); add(key + '.long', fn(1, 0, 300)); add(key + '.longs', fn(3, 1, 1800));
      }
    }
  }
  add('applyChangedLine', T.applyChangedLine([applyChangePart({ field: 'interval', from: '15', to: '30' }), applyChangePart({ field: 'folder', from: 'D:\a', to: 'E:\b' }), applyChangePart({ field: 'targets', from: '', to: 'x' })]));
  Object.entries(T.applyChangedLabels).forEach(([k, v]) => add('applyChangedLabels.' + k, v));
  for (const id of STATE_IDS) add('stateNext.' + id, T.stateNext[id]);
  add('interval.same', intervalWords(15, 15)); add('interval.slow', intervalWords(15, 60)); add('interval.fast', intervalWords(30, 15)); add('interval.one', intervalWords(1));
  for (const age of [0, 30, 120, 7200, 400000]) add('age.' + age, ageWords(new Date(Date.now() - age * 1000).toISOString()));
  for (const reason of Object.keys(UNREACHABLE_NEXT)) add('unreachableNext.' + reason, stateNextFor({ state: 'unreachable', reason }, true));
  for (const id of STATE_IDS) for (const pc of [true, false]) for (const tone of ['bad', 'warn']) add('next.' + id + '.' + pc + '.' + tone, stateNextFor({ state: id, tone }, pc));
  for (const [k, v] of Object.entries(T.nameProblems)) add('nameProblems.' + k, v);
  for (const [k, v] of Object.entries(T.settingProblems)) add('settingProblems.' + k, v);
  for (const [k, v] of Object.entries(T.targetProblems)) add('targetProblems.' + k, v);
  for (const [k, v] of Object.entries(BB_REFUSAL)) add('refusal.' + k, v);
  return out;
}
