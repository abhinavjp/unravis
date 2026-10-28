import type { LintEntry } from './copylint';

/**
 * Words for the guided analysis, the manual tools, the checklist, the after-the-fix comparison and the report export. Plain sentences. The
 * status words are fixed: Ready, Partly ready, Cannot test yet, Failed. Nothing here names a cause; every sentence says what the data shows.
 */

export const GUIDED_TEXT = {
  heading: 'Guided analysis',
  intro: 'Eleven steps, cheapest first. Each step reads the data you already have and says what it shows and what it could not test.',
  introManual: 'Pick one tool and run it on its own. Each tool reads the same data and the same windows as the steps.',
  railLabel: 'Guided analysis steps',
  railHint: 'Each step has a status. Waiting means it has not run yet.',
  modeHeading: 'Way of working',
  caseActions: 'Case actions',
  toManual: 'Switch to Manual tools',
  toGuided: 'Switch to Guided analysis',
  modeKept: 'Your notes, checklist and saved views are kept.',
  modeGuidedNow: 'You are using Guided analysis.',
  modeManualNow: 'You are using Manual tools.',
  panelsLabel: 'Parts of the analysis',
  tabSteps: 'Steps',
  tabTools: 'Tools',
  openGuided: 'Open guided analysis',
  openGuidedNote: 'Walk from the question to the suspects in eleven steps. Or pick one tool at a time. Keep a checklist and save a report.',
  closeGuided: 'Back to the case',
  nutshell: 'In a nutshell',

  run: 'Run the steps',
  rerun: 'Run again',
  running: 'Reading the data. This can take a little while.',
  runFailed: 'The steps could not run.',
  retry: 'Retry reading',
  cancel: 'Cancel reading',
  wait: 'Wait',
  noSources: 'This case has no source yet, so there is nothing to read. Add data first.',
  addData: 'Add data',
  backToCase: 'Back to the case',
  outOfDate: 'Out of date, rerun',
  outOfDateNote: 'The windows, sources or lists changed after this ran. The numbers below are from the earlier run.',
  readNote: (calls: string, rows: string) => `This run read ${calls} audit calls and ${rows} IIS rows.`,
  truncated: 'The read stopped at its limit. Narrow the window to read everything.',
  nextStep: 'Next step',
  previousStep: 'Previous step',
  jumpedAhead: 'This step shows the last run. Run the steps first to fill it in.',
  needRun: 'Nothing has run yet. Choose the windows in Step 0, then run the steps.',

  /* Step 0 */
  frameLead: 'Slow compared to what? Pick a normal period to compare with. Then Unravis looks for the minute things changed.',
  incidentWindow: 'Incident window',
  referenceWindow: 'Reference window (normal)',
  from: 'From',
  to: 'To',
  utcNote: 'Times are UTC. Times from your own clock or from a ticket are not converted: enter them as UTC.',
  referenceNote: 'The normal period to compare with. It must not share time with the incident.',
  quickPick: 'Quick pick',
  lastHour: 'Last hour',
  today: 'Today',
  yesterday: 'Yesterday',
  quickNote: 'A quick pick sets the incident window and a normal period of the same length just before it.',
  focus: 'Focus on one API (optional)',
  allApis: 'All APIs',
  focusNote: 'An API is the URL path of one endpoint. Steps 3 and 4 show only that API. The rest still read every API.',
  poolLimit: 'SqlClient pool limit (optional)',
  poolNote: 'If you know the pool limit, enter it. Step 4 then says whether the calls in flight fill it.',
  onset: 'Onset t0',
  onsetFound: (when: string) => `Unravis found the minute the slow end first stayed above twice the reference: ${when} UTC.`,
  onsetNone: 'Unravis found no such minute. That is a finding, not an error.',
  onsetCannot: 'Unravis cannot date the onset from this data.',
  onsetNotRun: 'The onset is found when the steps run.',
  useSuggested: 'Use suggested',
  useMine: 'Use my time',
  carryOn: 'Carry on without an onset',
  carryOnNote: 'The next steps run without an onset.',
  widen: 'Widen the incident window',
  myOnset: 'My onset (UTC)',
  myOnsetNote: 'Your time is shown beside the suggested one. The steps still use the minute Unravis found.',
  windowsKept: 'These windows are kept in one place. Changing them marks any step already run as out of date.',
  scope: (calls: string, incident: string, reference: string) => `Scope: ${calls} calls. The incident window is ${incident}. The reference window is ${reference}.`,
  fixWindows: 'Fix the windows to run the steps.',
  changeList: 'Changes you know about (optional)',
  changeNote: 'Releases or changes with a time in UTC. Step 5 sets them beside the onset.',
  addChange: 'Add a change',
  changeLabel: 'What changed',
  changeAt: 'When (UTC)',
  removeChange: 'Remove',
  noChanges: 'No change listed.',
  changesEntered: 'I have listed every change I know of',
  changesEnteredNote: 'Leave this off if you have not looked yet. An empty list entered on purpose reads "No change listed".',
  changesFull: 'The change list holds at most 50 changes.',
  changesUnticked: 'These changes are used only when the box beside Add a change is ticked.',

  /* Step 1 */
  trustLead: 'Check that the data can be trusted before reading what it says.',
  sourcesHeading: 'Sources',
  sourceCol: 'Source',
  stateCol: 'State',
  usedCol: 'Used for',
  gapsHeading: 'Gaps in the data',
  noGaps: 'No minute is missing.',
  gapShort: 'Short gap: the step runs on the minutes that exist.',
  gapLong: 'Long gap: these minutes are Cannot test yet.',
  notEnoughTitle: 'Not enough data to continue',
  notEnoughLead: 'One of the windows holds no call. Possible reasons are listed below.',
  notEnoughReasons: [
    'The window is outside the time the source covers.',
    'The source is missing, or it stopped before the window.',
    'The window or the API you chose has no calls.'
  ],
  changeWindow: 'Change the window',

  /* Step 2 */
  shapeLead: 'Which APIs moved, and by how much? A change counts only when it passes both marks.',
  byApi: 'By API',
  apiCol: 'API',
  callsCol: 'Calls',
  p95Col: 'P95',
  refP95Col: 'Reference P95',
  changeCol: 'Change',
  shapeRule: (pct: number, ms: number) => `The rule: a change counts when it is at least ${pct} % and at least ${ms} ms.`,
  nothingMoved: (pct: number, ms: number) => `Nothing moved: no API changed by ${pct} % and ${ms} ms. The slow end looks the same as normal, as far as these calls show.`,
  retriesLine: (inc: string, ref: string) => `Calls sent again by the same client within seconds: ${inc} in the incident, ${ref} in the reference.`,
  retriesUnknown: 'Repeated calls could not be counted: the audit has no client id.',
  shapeKind: (kind: string) => `Shape of the slowdown: ${kind}.`,
  showAll: 'Show every API',
  showLess: 'Show fewer',
  byApiFocusNote: 'This table lists every API, so you can see whether the API you chose is the only one that moved.',

  /* Step 3 */
  splitLead: 'Where did the time of a call go? Each call is split into audit set-up, action time, payload record and outside time.',
  partCol: 'Part',
  normalCol: 'Normal',
  incidentCol: 'Incident',
  shareCol: 'Share of the extra time',
  barNormal: 'Normal',
  barIncident: 'Incident',
  barLabel: 'Bars of the average time of a call, on one scale.',
  untestedParts: 'Not drawn: parts that could not be tested.',
  addAuditLogs: 'Add the audit logs',
  openNearby: 'Open nearby activity',
  slowestCalls: 'Slowest calls in the incident window',
  showSlowCalls: 'Show the slowest calls',
  startedCol: 'Started (UTC)',
  timeCol: 'Time',

  /* Step 4 */
  loadLead: 'How many calls were open at once? More calls in flight can mean the system was busier, or that each call stayed longer.',
  measureCol: 'Measure',
  p95Label: 'P95 by minute (ms)',
  inFlightLabel: 'Calls in flight by minute',

  /* Step 5 */
  changesLead: 'Did something change just before the onset? Only changes you list are compared.',
  changesNone: 'No change listed. Add the releases or changes you know of in Step 0.',
  changesNoneEntered: 'You said there was no change to list.',
  changeListButton: 'Change the list',
  changeTimeCol: 'When',
  changeWhatCol: 'What',
  changeRelCol: 'Against the onset',

  /* Steps 6 to 9 */
  groupLead6: 'Checks on the application tier: queues, threads and connection pools.',
  groupLead7: 'Checks on SQL Server: blocking, waits and capacity.',
  groupLead8: 'Checks on MongoDB: slow operations, queues and the replica set.',
  groupLead9: 'Checks on the hosts: processor, memory, disk and network.',
  findings: 'What the data shows',
  reasonsHeading: 'What could not be tested',
  suspectsHere: 'Suspects checked in this step',
  noSuspectsHere: 'No suspect belongs to this step.',
  addSource: 'Add a source',
  placeBlackbox: 'Place a Blackbox',

  /* Step 10 */
  originLead: 'What moved first, and which suspects fit the data. Unravis does not name a culprit. It says what fits and what could not be tested.',
  moved: 'What moved first',
  tierCol: 'Tier',
  firstMovedCol: 'First moved',
  whatMovedCol: 'What moved',
  roleCol: 'Role',
  colours: 'How to read the colours',
  red: 'Red: most likely suspect',
  orange: 'Orange: likely suspect',
  green: 'Green: ruled out by the data we have',
  grey: 'Grey: not sure. It is not a pass.',
  suspectsHeading: 'Suspects, most likely first',
  noRanked: 'No suspect is red or orange. See the grey ones: they could not be tested.',
  notTested: 'Could not be tested (grey, never a pass)',
  setAside: (n: number) => `Set aside: ${n} green ${n === 1 ? 'suspect' : 'suspects'}`,
  whyLabel: 'Why',
  evidenceLabel: 'Evidence',
  overturn: 'What would overturn it',
  cheapest: 'Cheapest check',
  observed: 'Observed',
  inferred: 'Inferred',
  needsLabel: 'Needs',
  addCheck: 'Add its check to the checklist',
  checkAdded: 'On the checklist',
  noClearOrigin: 'No clear origin',
  noClearOriginNote: 'Two or more tiers moved within the same minute, or the clocks differ by more than the order can be told. The table lists them together, and none is called first.',
  notEnoughOrigin: 'Not enough data to date any tier. Add the sources listed under How sure are we.',
  howSure: 'How sure are we',
  sureCol: 'Item',
  sourcesUsed: 'Sources used',
  sourcesMissing: 'Sources missing',
  suspectsNotTested: 'Suspects not tested',
  oneCheck: 'The one check that would overturn the answer',
  noneWord: 'None',
  openChecklist: 'Open the checklist',
  confidenceWord: (word: string) => `Confidence: ${word}`,

  /* Nearby activity */
  nearbyHeading: 'Nearby activity',
  nearbyLead: 'What each source recorded around one moment. It shows what overlapped in time. It does not say why.',
  around: 'Around',
  before: 'Minutes before',
  after: 'Minutes after',
  picks: 'Quick windows',
  nearbyRun: 'Show activity',
  nearbyNoCase: 'Open a case with a source first.',
  nearbyScale: 'Bar colours: red striped over 2 seconds, orange 1 to 2 seconds, green under 1 second. These are not the suspect colours.',
  nearbyEmpty: 'Nothing was recorded in this window.',
  nearbyLanes: 'Sources',
  laneApi: 'API calls',
  laneSql: 'SQL Server',
  laneMongo: 'MongoDB',
  laneHost: 'Host',
  laneNone: 'Cannot test yet: this case has no source of this kind.',
  nearbyBad: 'Pick a moment in UTC and a span of 0 to 120 minutes.',
  nearbyMore: (n: number) => `Showing the first ${n} calls of the window. Narrow the window to see the rest.`,
  nearbyFailed: 'The activity could not be read.',
  nearbyBandFast: 'Under 1 second',
  nearbyBandSlow: '1 to 2 seconds',
  nearbyBandVerySlow: 'Over 2 seconds',
  nearbyBandUnknown: 'No time',

  /* Checklist */
  checklistHeading: 'Checklist',
  checklistLead: 'Keep four lists: Need to do, Working on, What we learned and Ruled out.',
  done: 'Done',
  open: 'Open',
  addItem: '+ Add item',
  addSuggested: 'Add suggested checks from Step 10',
  noSuggested: 'Step 10 has no check that is not on the list already.',
  itemText: 'What is the item?',
  itemSave: 'Add',
  itemMove: 'Move to',
  itemDelete: 'Delete',
  itemEmpty: 'Nothing here yet.',
  itemSuspect: (id: string) => `Suspect ${id}`,
  itemTooLong: 'An item holds up to 300 characters.',
  checklistFull: 'The checklist holds up to 500 items. Delete one to add another.',
  closedNote: 'This case is closed. You can read the checklist but not change it.',

  /* After the fix */
  fixHeading: 'After the fix',
  fixLead: 'Compare the same hours on another day with the incident day.',
  fixDay: 'The later day (UTC)',
  fixNote: 'Pick hours on a day after the fix. Unravis compares them with the incident day.',
  fixRun: 'Compare',
  fixAdd: 'Add this day to the case',
  fixAdded: 'The later day is part of this comparison. Recording it writes one line under What we learned.',
  measureFix: 'Measure',
  earlierDay: 'Earlier day',
  laterDay: 'Later day',
  changeVsEarlier: 'Change vs the earlier day',
  record: 'Record result',
  notNow: 'Not now',
  recorded: 'Recorded under What we learned.',
  pickAlike: 'Pick a more alike day',
  oneRoute: 'Compare one route only',
  recordWarning: 'Record with this warning',
  fixNeedsRun: 'Run the steps first. The comparison needs the incident day.',
  fixDayFirst: 'Pick the later day first.',
  fixRunning: 'Reading the later day.',
  fixFailed: 'The later day could not be compared.',
  fixRoute: 'Compare one route only (optional)',
  fixWarnings: 'Warnings',
  fixClosed: 'This case is closed, so the result cannot be recorded. You can still compare.',

  /* Manual tools */
  toolsHeading: 'Manual tools',
  toolsLead: 'The same facts as the steps, one tool at a time, in any order.',
  openTool: 'Open',
  backToTools: 'Back to tools',
  runGuided: 'Run Guided analysis instead',
  savedViewsCount: (n: number) => `Saved views (${n})`,
  sourcesLabel: 'Sources',
  toolNeedsHint: 'Nothing is shown rather than a guess.',
  onlyMatching: 'Only APIs matching',
  minCalls: 'Minimum calls',
  runTool: 'Run',
  saveView: 'Save this view',
  addNote: 'Add a note to the checklist',
  noteAdded: 'Added to the checklist.',
  noApiRows: (n: number) => `No API had ${n.toLocaleString('en-US')} or more calls. Lower Minimum calls to see more.`,
  noRecords: 'This window holds no audit records, so there is nothing to list. Cannot test yet.',
  slowCallsLead: 'The longest calls that started in the incident window.',
  toolFailed: 'The tool could not run.',

  /* Saved views */
  viewsHeading: 'Saved views',
  viewName: 'Name',
  viewNameNote: 'Up to 60 characters. Not a secret: it is shown in lists and in exports.',
  viewSaves: 'Saves: the windows, the API and the tool filters. Not saved: results.',
  viewSave: 'Save the view',
  viewCancel: 'Cancel',
  viewOpen: 'Open',
  viewDelete: 'Delete',
  viewToolCol: 'Tool',
  viewWindowsCol: 'Windows (UTC)',
  viewSavedCol: 'Saved',
  viewActionsCol: 'Actions',
  viewsEmpty: 'No view is saved yet.',
  viewNameMissing: 'Give the view a name.',
  viewsFull: 'This case holds 200 saved views. Delete one to save another.',
  viewsClosed: 'This case is closed. You can open views but not change them.',

  /* Missing source */
  missingTitle: 'This tool cannot run yet',
  missingLead: (need: string) => `This tool needs ${need}, and this case has none. Nothing is shown rather than a guess.`,
  addByLocation: 'Add by location',

  /* Export */
  exportHeading: 'Export',
  exportLead: 'Build a report from what is on screen. The report is made on this computer and held in this window until you save it.',
  includeFigures: 'Figures and charts',
  includeSuspects: 'Suspects with their cheapest checks',
  includeChecklist: 'Checklist',
  includeSources: 'Sources and what could not be tested',
  includeNearby: 'Nearby activity for chosen calls',
  includeNearbyNeeds: 'Pick calls first: this needs calls chosen in Nearby activity.',
  preview: 'Preview',
  downloadHtml: 'Download report (HTML)',
  downloadHtmlWarn: 'Download report (HTML) with these warnings',
  downloadCsv: 'Download data (CSV)',
  exportPrivacy: 'No request payloads, no user ids, no connection details, no passwords. Dates in UTC.',
  exportBuilding: 'Building the report.',
  exportReady: (name: string, bytes: string) => `${name} is ready, ${bytes}.`,
  exportSaved: (hash: string) => `Saved. SHA-256 of the saved file: ${hash}`,
  exportMismatch: 'The report did not match its checksum, so it was not saved. Try again.',
  exportEmpty: 'Nothing has been analysed yet. The report will say so and list the case sources only.',
  exportWarningsHeading: 'This report has warnings',
  exportFailed: 'The report could not be built.',
  exportOptions: 'Include',
  exportStale: 'The windows or lists changed after the last run. The report holds the earlier run, as it is on screen.',
  exportClose: 'Close preview',
  backToImport: 'Back to the import'
} as const;

export const SAVED_VIEW_ERRORS: Readonly<Record<string, string>> = {
  'case-closed': 'This case is closed, so its views cannot be changed.',
  'plan-full': GUIDED_TEXT.viewsFull,
  'plan-too-large': 'The view holds too much to save.',
  'plan-invalid': 'That view could not be saved. Check the name and try again.',
  'view-not-found': 'That view is gone. The list has been refreshed.',
  'plan-unavailable': 'Saving is off in the demo.'
};

export const CHECKLIST_ERRORS: Readonly<Record<string, string>> = {
  'case-closed': GUIDED_TEXT.closedNote,
  'plan-full': GUIDED_TEXT.checklistFull,
  'plan-too-large': GUIDED_TEXT.itemTooLong,
  'plan-invalid': 'That item could not be saved. Check the text and try again.',
  'item-not-found': 'That item is gone. The list has been refreshed.',
  'plan-unavailable': 'The checklist is off in the demo.'
};

/** The sentence for a refusal, or a plain fallback. The server's own message is used when no sentence is known. */
export function refusalText(reason: string, table: Readonly<Record<string, string>>, fallback: string): string {
  return table[reason] ?? fallback;
}

/** Fixed sentences and a few sample sentences of the templates, for the copy lint. */
export function guidedLintEntries(): LintEntry[] {
  const entries: LintEntry[] = [];
  const add = (id: string, value: unknown): void => {
    if (typeof value === 'string') entries.push({ id, text: value });
    else if (Array.isArray(value)) value.forEach((v, i) => add(`${id}.${i}`, v));
  };
  for (const [key, value] of Object.entries(GUIDED_TEXT)) {
    if (typeof value === 'function') continue;
    add(`guided.${key}`, value);
  }
  add('guided.readNote', GUIDED_TEXT.readNote('12,000', '3,400'));
  add('guided.onsetFound', GUIDED_TEXT.onsetFound('26 Oct 10:42'));
  add('guided.scope', GUIDED_TEXT.scope('73,648', '30 minutes', '60 minutes'));
  add('guided.shapeRule', GUIDED_TEXT.shapeRule(30, 100));
  add('guided.nothingMoved', GUIDED_TEXT.nothingMoved(30, 100));
  add('guided.retriesLine', GUIDED_TEXT.retriesLine('41', '12'));
  add('guided.setAside', GUIDED_TEXT.setAside(3));
  add('guided.itemSuspect', GUIDED_TEXT.itemSuspect('S3'));
  add('guided.noApiRows', GUIDED_TEXT.noApiRows(20));
  add('guided.missingLead', GUIDED_TEXT.missingLead('a Blackbox source'));
  add('guided.exportReady', GUIDED_TEXT.exportReady('report.html', '12 KB'));
  add('guided.nearbyMore', GUIDED_TEXT.nearbyMore(100));
  for (const [key, value] of Object.entries(SAVED_VIEW_ERRORS)) add(`views.${key}`, value);
  for (const [key, value] of Object.entries(CHECKLIST_ERRORS)) add(`checklist.${key}`, value);
  return entries;
}
