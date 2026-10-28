import type { LintEntry } from './copylint';

/**
 * Words for the SQL Server and MongoDB sources (P7): the connections page, grant scripts, the profiler guide, steps 7 and 8,
 * Add data, Add another day, source combinations and nothing read. The readiness words are fixed: Ready, Partly ready,
 * Cannot test yet, Failed. A connection test says only Works, Failed or Not tested.
 */
/** The label of the short summary box on every screen. */
export const NUTSHELL = 'In a nutshell';

export const CONN_TEXT = {
  heading: 'Saved connections',
  intro: 'One list of every saved connection, SQL Server and MongoDB. Cases pick from it. Each row says what its last test found and whether a password is saved.',
  addSql: '+ SQL Server',
  addMongo: '+ MongoDB',
  mongoHere: 'A MongoDB connection is set up in the Connections window, which + MongoDB opens. Save it there, come back and press Refresh to see it in this list.',
  mongoNowhere: 'A MongoDB connection cannot be added from this page here.',
  refresh: 'Refresh',
  noneHeading: 'No saved connections yet',
  noneBody: 'A saved connection tells Unravis how to reach one SQL Server or MongoDB, once. Every case can then use it. Passwords are sealed on this PC with Windows data protection, only if you ask.',
  test: 'Test',
  testing: 'Testing…',
  edit: 'Edit',
  forget: 'Forget',
  forgetAsk: 'Forget this connection? Its saved password goes too. Cases keep the data they already read. No server is touched.',
  forgetYes: 'Forget it',
  forgetNo: 'Keep it',
  pwSaved: 'password saved',
  pwNone: 'no password saved',
  pwNotNeeded: 'no password needed',
  wordsNote: 'Works, Failed and Not tested describe one connection test only. They are not the readiness words used for sources (Ready, Partly ready, Cannot test yet, Failed).',
  origin: 'Passwords are sealed on this PC with Windows data protection, only when you tick Remember password, and are never sent back to this screen. Up to 20 SQL connections. Forget removes the saved connection and its password. It does not touch any server.',
  countSql: 'SQL Server connections saved:',
  countMongo: 'MongoDB connections listed (no limit):',
  full: 'You have 20 of 20 saved SQL Server connections. Forget one before you save another.',
  demo: 'Saved connections are not available here. Real ones are saved on your own computer.',
  loadFailed: 'The saved connections could not be read. Try again.',
  retry: 'Try again',
  testNeedsPassword: 'This connection has no saved password. Edit it and type one to test.',
  mongoTestHint: 'Test a MongoDB connection from the Connections drawer.',
  sqlFormHeading: 'SQL Server connection',
  save: 'Save connection',
  saving: 'Saving…',
  cancel: 'Cancel',
  saved: 'Saved.',
  keepPasswordNote: 'A password is saved. Leave the password empty to keep it.'
} as const;

/**
 * Words for the key keeper (P14). On a PC set up for everyone, saved passwords and read keys live only in the Unravis Key Keeper
 * service, which does the checks that need them. Unravis itself never gets them back.
 */
export const KEEPER_TEXT = {
  held: 'Password held by the key keeper',
  heldDown: 'Password held by the key keeper, which cannot be used until it runs again',
  sourcesStopped: 'Held sources are unticked until the key keeper runs again. Sources whose password you typed still work.',
  origin: 'On this PC, saved passwords and Blackbox read keys are kept by the Unravis Key Keeper service. Unravis asks it to run a check by name and never gets a password back. Up to 20 SQL connections. Forget removes the saved connection and its password. It does not touch any server.',
  stoppedHeading: 'The key keeper is not running',
  stoppedBody: 'Saved passwords and read keys cannot be used until the Unravis Key Keeper service runs again. Start the service, or ask the person who looks after this PC. Connections that need no password still work.',
  impostorHeading: 'The key keeper could not be trusted',
  impostorBody: 'Something other than the Unravis Key Keeper service answered, so nothing was sent to it. Ask the person who looks after this PC.',
  retry: 'Check again',
  checking: 'Checking…',
  migrateHeading: 'Move your saved connections to the key keeper',
  migrateBody: 'This PC now keeps saved passwords in one place for everyone who uses Unravis on it. Your saved connections from before are still in your own folder. Move them so the key keeper holds their passwords.',
  move: 'Move them',
  moving: 'Moving…',
  notNow: 'Not now',
  notNowNote: 'Your old saved connections stay where they are until you move them.',
  notNowUndo: 'Show the move again',
  moveAgain: 'Move them again',
  movedHeading: 'Your saved connections are in the key keeper',
  movedBody: 'The old copies are still in your own folder. Remove them once you have checked that the connections work here.',
  removeOld: 'Remove old copies',
  removeAsk: 'Remove the old copies from your own folder? The key keeper keeps its copies. No server is touched.',
  removeYes: 'Yes, remove',
  removeNo: 'Keep them',
  removed: 'The old copies were removed.',
  failed: 'That did not work. Your old files were left unchanged. Try again.'
} as const;

/** The note on the moved card when some old records were already saved in the key keeper by someone else on this PC. */
export const keeperDuplicatesText = (n: number): string =>
  (n === 1 ? '1 of your old saved connections was' : n + ' of your old saved connections were') +
  ' already saved in the key keeper by someone else on this PC, so theirs was kept and yours was not moved. Its saved password may not be yours. Removing the old copies removes yours too.';

/** The counts line of the move card, for example "2 SQL Server connections, 1 MongoDB link and 1 Blackbox read key wait to be moved." */
export const keeperPendingText = (sql: number, mongo: number, blackbox: number): string => {
  const parts: string[] = [];
  if (sql > 0) parts.push(sql + (sql === 1 ? ' SQL Server connection' : ' SQL Server connections'));
  if (mongo > 0) parts.push(mongo + (mongo === 1 ? ' MongoDB link' : ' MongoDB links'));
  if (blackbox > 0) parts.push(blackbox + (blackbox === 1 ? ' Blackbox read key' : ' Blackbox read keys'));
  if (parts.length === 0) return 'Nothing waits to be moved.';
  const list = parts.length === 1 ? parts[0] : parts.slice(0, -1).join(', ') + ' and ' + parts[parts.length - 1];
  const total = sql + mongo + blackbox;
  return list + (total === 1 ? ' waits' : ' wait') + ' to be moved.';
};

export const GRANT_TEXT = {
  heading: 'Permissions to give the login',
  notRun: 'Unravis never runs this script. It only shows it, so the owner of the server can run it.',
  readOnly: 'Read-only permissions. Copy and run it yourself.',
  runAs: 'Run as sysadmin in SSMS',
  copy: 'Copy script',
  copied: 'Copied. Paste it into SSMS.',
  copyFailed: 'The script could not be copied. Select it and copy it by hand.',
  login: 'Login to give the permissions to',
  loginHint: 'The name of the SQL login, or the Windows account Unravis runs as.',
  database: 'Audit database',
  databaseHint: 'The database that holds the API audit tables.',
  version: 'SQL Server version',
  v2022: 'SQL Server 2022 or later',
  v2019: 'SQL Server 2019 or earlier',
  loading: 'Writing the script…',
  loadFailed: 'The script could not be written. Check the names and try again.',
  mongoHeading: 'Roles to give the MongoDB user',
  mongoRunAs: 'Run in mongosh as a user that can grant roles',
  mongoLogin: 'User to give the roles to',
  mongoDatabase: 'Audit database'
} as const;

export const versionNote = (version: '2019' | '2022'): string =>
  version === '2019'
    ? 'Script version: SQL Server 2019 or earlier. Do not run the 2022 script here. It names permissions this version does not have.'
    : 'Script version: SQL Server 2022 or later. Do not run the 2019 script here. It uses older permission names.';

export const GUIDE_TEXT = {
  heading: 'Switched on?',
  unravisCannot: 'Unravis cannot switch this on. You run it.',
  why: 'Why it matters',
  cost: 'What it costs',
  steps: 'Steps',
  check: 'Check that it is on',
  switchOn: 'Switch it on (you run it)',
  switchOff: 'Switch it off again',
  changes: 'This changes the server setting. Unravis never runs it.',
  loadFailed: 'The guide could not be written. Try again.'
} as const;

export const STATE_NOTE: Record<string, string> = {
  Ready: 'Every check ran and answered.',
  'Partly ready': 'Some checks ran. The rest say what to fix.',
  'Cannot test yet': 'No check could run yet. Each one says what is missing.',
  Failed: 'The checks could not read the server.'
};

export const STEP_TEXT = {
  sqlHeading: 'Step 7: SQL Server',
  mongoHeading: 'Step 8: MongoDB',
  sqlLead: 'Did a session block others, or did a query turn slow? Each check names the permission it needs.',
  mongoLead: 'Did the database itself slow down? The profiler holds the slow operations.',
  noSql: 'No SQL Server has been read for this case yet. Step 7 says Cannot test yet until one is added.',
  noMongo: 'No MongoDB server has been read for this case yet. Step 8 says Cannot test yet until one is added.',
  noMongoWhy: 'The audit logs may come from MongoDB, but this step needs the server itself: its profiler and its health.',
  addSql: 'Add a SQL Server',
  addMongo: 'Add a MongoDB server',
  answersHeading: 'What the checks answered',
  noAnswers: 'No question was answered. A missing answer is never a pass.',
  answerYes: 'Seen in this window',
  answerNo: 'Not seen in this window',
  onsetLabel: 'First sign at',
  checksHeading: 'Checks',
  fixHeading: 'To fix',
  stateCol: 'State',
  checkCol: 'Check',
  daysHeading: 'Days read',
  readAt: 'Read',
  loadFailed: 'The days that were read could not be loaded. Try again.',
  loading: 'Loading…',
  retry: 'Try again',
  show: 'Show',
  shown: 'Shown',
  conclusion: 'Only checks that ran count. A check that could not run says so and is never treated as a pass.'
} as const;

export const RUN_TEXT = {
  heading: 'Read from a server',
  pick: 'Saved connection',
  pickChoose: 'Choose a connection…',
  pickNone: 'No saved connection is available. Save one first.',
  password: 'Password for this run',
  passwordHint: 'Only needed when no password is saved. It is used once and not kept.',
  database: 'Audit database',
  databaseHint: 'Type the name of the database that holds the audit tables.',
  collection: 'Collection',
  incidentStart: 'Incident starts (UTC)',
  incidentEnd: 'Incident ends (UTC)',
  referenceStart: 'Reference starts (UTC)',
  referenceEnd: 'Reference ends (UTC)',
  timesHint: 'Times are UTC, written as 2026-10-26 10:30. The reference window is the normal time to compare with.',
  run: 'Read this day',
  running: 'Reading…',
  needPick: 'Choose a saved connection first.',
  needDatabase: 'Type the audit database first.',
  badTime: 'A time could not be read. Write each one as year-month-day, then hour:minute, like 2026-10-26 10:30.',
  signedOut: 'This page is no longer signed in to Unravis, so nothing was read. Reload the page, or open Unravis from the link it printed when it started, then try again.',
  needIncident: 'Give the incident a start and an end, with the end after the start.',
  needReference: 'Give the reference a start and an end, with the end after the start.',
  busy: 'Another read is running. Try again when it has finished.',
  limited: 'Too many tries in a short time. Wait a minute, then try again.',
  locked: 'The case files are locked. Unlock them first.',
  demo: 'Reading a server is not available right now. Try again in a moment.',
  closed: 'The case is closed, so nothing was read. Reopen the case to read from a server.',
  failed: 'The read could not be made. Try again.',
  notSaved: 'The read worked, but this day could not be kept in the case: ',
  done: 'Read and added to the case.',
  overlap: 'You already added a day for this database that overlaps these times. Use the same start and end to check that day again, or pick times that do not overlap. Nothing was read.'
} as const;

export const ADD_TEXT = {
  heading: 'Add data',
  lead: 'Add a source after the case was created. New data is read on its own and does not touch what is there.',
  sqlTitle: 'SQL Server',
  sqlBody: 'Query Store, sessions, jobs, error log.',
  mongoTitle: 'MongoDB',
  mongoBody: 'This reads the server. It needs the clusterMonitor role. It also needs read on the database.',
  blackboxTitle: 'Blackbox',
  blackboxBody: 'Machine counters.',
  filesTitle: 'Files',
  filesBody: 'IIS logs, CSV, audit exports (.log, .csv, .json, .zip).',
  dayTitle: 'Another day',
  dayBody: 'More hours of a source the case already has.',
  addSql: 'Add SQL source',
  addMongo: 'Add Mongo source',
  addBlackbox: 'Add a Blackbox',
  addFiles: 'Add files',
  addDay: 'Add another day',
  connReady: (n: number): string => (n === 0 ? 'No saved connection yet' : n === 1 ? '1 saved connection ready' : n + ' saved connections ready'),
  held: (n: number): string => (n === 0 ? 'None yet' : n === 1 ? '1 read in this case' : n + ' read in this case'),
  byDay: (can: number, all: number): string => can + ' of ' + all + ' sources can be read by day',
  outOfDate: 'A step that ran before the new data arrived is marked Out of date, rerun. It does not change by itself.',
  readsOnly: 'Reading does not change any server.',
  back: 'Back to the case'
} as const;

export const DAY_TEXT = {
  heading: 'Add another day',
  lead: 'Read more hours of a source the case already holds, for example the day after a fix. It is read separately and added to the case. Nothing already read is replaced.',
  holds: 'What the case already holds',
  source: 'Source',
  inCase: 'Already in the case (UTC)',
  state: 'State',
  notByDay: 'Not by day',
  profilerWhy: 'The profiler keeps only the latest entries. An older day cannot be read.',
  nothingHeld: 'The case holds no SQL Server day yet. Read one first, then add another.',
  onlyByDay: 'Only sources that can be read by day are offered.',
  newRange: 'New range',
  prefill: 'The times start as the same hours on the next day. Change them if you need other hours.',
  sameHours: 'Same hours as the incident, a later day. Times are UTC.',
  expects: 'SQL Server checks Query Store, and the blocked-process report if it was switched on that day.',
  emptyDay: 'The source cannot read a day that has no records yet. It then says so, and nothing is guessed.',
  rerun: 'When it finishes, steps that used this source are marked Out of date, rerun.',
  pickSource: 'Source to read again',
  running: 'Reading…',
  cancel: 'Cancel',
  checkAgain: 'A check of the same day again is allowed. A day that overlaps another without being the same is not.',
  added: 'Day added to the case.'
} as const;

export const COMBO_TEXT = {
  heading: 'Source combinations',
  lead: 'A case does not need every source. This is what each combination can answer, so you know what to add next. What a source cannot answer is Cannot test yet, never a pass.',
  colHolds: 'What the case holds',
  colAnswers: 'Steps it can answer',
  colMissing: 'Steps that say Cannot test yet',
  today: 'This case today',
  todayNone: 'No SQL Server or MongoDB server has been read for this case.',
  next: 'Add the next source that unlocks the most.',
  rows: [
    { holds: 'Mongo audit logs only', answers: '0, 1 (partly), 2, 3 (A, C and D), 4, 6, 10 (from what exists)', missing: '3 (O needs IIS), 5, 7, 8, 9' },
    { holds: '+ IIS logs', answers: 'adds O in step 3, and queueing in 4', missing: '5, 7, 8, 9' },
    { holds: '+ SQL Server', answers: 'adds 7, and the jobs and error log lanes of 5', missing: '8, 9' },
    { holds: '+ MongoDB server (profiler, clusterMonitor)', answers: 'adds 8: slow operations first, connections with clusterMonitor', missing: '9, the host lanes of 5' },
    { holds: '+ Blackbox (web server), and one on the SQL host', answers: 'adds 9 and the host lane of 5, and SQL host counters', missing: 'none from missing sources' }
  ],
  note: 'Application logs add lines to Step 5 only. A Query Store export is for SQL you cannot connect to. Neither is needed to start.'
} as const;

export const NOTHING_TEXT = {
  heading: 'Nothing could be read',
  lead: 'The case is not broken. Every source answered, but the window held nothing to read.',
  reasonSql: 'The SQL Server answered, but no check could read anything for these hours.',
  reasonMongo: 'The MongoDB server answered, but its profiler held nothing for these hours.',
  hours: 'The hours may be wrong, or that hour is before the system was used.',
  change: 'Change the window, or add the day that holds data.',
  addDay: 'Add another day',
  addData: 'Add data',
  tableHeading: 'What this case can answer',
  colQuestion: 'Question',
  colNeeds: 'Needs',
  colCan: 'Can answer',
  qSlower: 'Did calls get slower, and when?',
  needAudit: 'Audit logs',
  cannotNoRecords: 'Cannot test yet: no records in the window',
  qBlocking: 'Is SQL blocking or busy?',
  needSql: 'SQL Server and the audit calls',
  cannotNoWindow: 'Cannot test yet: no incident window to look at'
} as const;

export const HUB_TEXT = {
  heading: 'SQL Server and MongoDB',
  intro: 'Saved connections, the SQL Server and MongoDB steps, and what to add next.',
  label: 'SQL Server and MongoDB sections',
  connections: 'Connections',
  sql: 'Step 7: SQL Server',
  mongo: 'Step 8: MongoDB',
  add: 'Add data',
  day: 'Add another day',
  combos: 'Combinations',
  readOnlyWhy: 'This case cannot take new data right now: it is closed, or it changed somewhere else. Reopen or refresh it first. What it already holds can still be read here.'
} as const;

const sample = (id: string, text: string): LintEntry => ({ id: 'sources.' + id, text });

export const sourcesCopyEntries = (): LintEntry[] => [
  ...Object.entries(CONN_TEXT).map(([id, text]) => sample('conn.' + id, text)),
  ...Object.entries(GRANT_TEXT).map(([id, text]) => sample('grant.' + id, text)),
  sample('grant.version2019', versionNote('2019')),
  sample('grant.version2022', versionNote('2022')),
  ...Object.entries(GUIDE_TEXT).map(([id, text]) => sample('guide.' + id, text)),
  ...Object.entries(STATE_NOTE).map(([id, text]) => sample('state.' + id, text)),
  ...Object.entries(STEP_TEXT).map(([id, text]) => sample('step.' + id, text)),
  ...Object.entries(RUN_TEXT).map(([id, text]) => sample('run.' + id, text)),
  ...Object.entries(ADD_TEXT).filter(([, v]) => typeof v === 'string').map(([id, text]) => sample('add.' + id, text as string)),
  sample('add.connReady', ADD_TEXT.connReady(3)), sample('add.held', ADD_TEXT.held(2)), sample('add.byDay', ADD_TEXT.byDay(1, 3)),
  ...Object.entries(DAY_TEXT).map(([id, text]) => sample('day.' + id, text)),
  sample('combo.lead', COMBO_TEXT.lead), sample('combo.note', COMBO_TEXT.note), sample('combo.today', COMBO_TEXT.todayNone),
  ...Object.entries(HUB_TEXT).map(([id, text]) => sample('hub.' + id, text)),
  ...Object.entries(KEEPER_TEXT).map(([id, text]) => sample('keeper.' + id, text)),
  sample('keeper.pending', keeperPendingText(2, 1, 1)), sample('keeper.duplicates', keeperDuplicatesText(2)), sample('keeper.duplicatesOne', keeperDuplicatesText(1)), sample('keeper.pendingOne', keeperPendingText(1, 0, 0)),
  ...Object.entries(NOTHING_TEXT).map(([id, text]) => sample('nothing.' + id, text))
];
