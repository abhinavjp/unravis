import type { LintEntry } from './copylint';
import { errorText, formatBytes, importerHelp, jobProgressText, reasonText } from './importCopy';

/**
 * Every string of the "This computer" panel (host-discovery spec, section 6). Plain words, no cause named, grade 8.5 or lower.
 * Strings are linted by discoverCopyEntries(). Wording that takes a value is a standalone function, not a member of the object,
 * so the lint test can call each one with a sample.
 */
export const DISCOVER_TEXT = {
  entryButton: 'This computer',
  entryHelp: 'Look on this computer for logs and data to import.',
  windowsOnly: 'Scanning is available on Windows only.',
  unavailable: 'Scanning is not available in this build.',

  heading: 'This computer',
  close: 'Close',
  intro: 'Unravis can look on this computer for logs and data it can import. It reads file names and sizes first. It opens nothing until you choose.',
  badgeElevated: 'Running as administrator',
  badgeStandard: 'Running as a standard user',
  scanButton: 'Scan this computer',
  scanAgain: 'Scan again',

  scanning: 'Looking on this computer',
  scanningNow: 'Checking this computer. This can take a minute.',
  scanCancel: 'Cancel scan',
  scanCancelling: 'Cancelling…',
  scanCancelled: 'The scan was cancelled.',
  scanFailed: 'The scan could not finish. Try again.',
  scanExpired: 'This scan is over an hour old. Scan again.',
  caseChanged: 'The case changed, so the import results were cleared.',

  nothingFound: 'No logs were found in the usual places. You can still import files by hand.',
  allDenied: 'Most sources need administrator rights. Close Unravis and start it with Run as administrator.',

  listHeading: 'Choose what to read',
  listNote: 'Large sources are never ticked for you. Tick only what you want.',
  tickSmall: 'Tick all except large',
  clearAll: 'Clear all',
  hostNote: 'Files from this computer use the host name below. Check it in the host list.',
  gapsHeading: 'Places that could not be used',

  groups: { web: 'Web server', windows: 'Windows', app: 'App logs', databases: 'Databases', live: 'Live' } as Record<string, string>,

  tagAdmin: 'Needs administrator',
  tagLarge: 'Large',
  tagExport: 'Export needed',
  exportFirst: 'This data is stored in a format Unravis does not read. Export it first:',
  liveRow: 'Use the recording switch below for this.',
  genericShows: 'A source Unravis can read.',
  exportShows: 'Data that needs an export before Unravis can read it.',
  genericHow: 'Export it as a text or CSV file, then import the file by hand.',
  showsLabel: 'Shows',

  gapReasons: {
    'needs-elevation': 'Run Unravis as administrator to read this.',
    'not-found': 'Nothing was found in the usual place.',
    'not-supported-os': 'This is not available on this version of Windows.',
    unreadable: 'Unravis could not open this.',
    'too-large': 'This is larger than Unravis will read.',
    'unsafe-path': 'Skipped: the path points somewhere Unravis does not follow.',
    'config-unparsed': 'The settings file for this could not be read.'
  } as Record<string, string>,
  gapUnknown: 'This was skipped.',

  windowHeading: 'Which time range?',
  windowChoices: { incident: 'Incident window', '1h': 'Last 1 hour', '6h': 'Last 6 hours', '24h': 'Last 24 hours', '7d': 'Last 7 days', custom: 'Custom' } as Record<string, string>,
  customFrom: 'From (UTC)',
  customTo: 'To (UTC)',
  customHelp: 'Use the form 2001-09-11 08:00.',
  fieldProblems: {
    empty: 'Enter the {field} time.',
    zone: 'Leave off the Z or offset. Times are in UTC.',
    seconds: 'Leave off the seconds. Use the form 2001-09-11 08:00.',
    date: 'That date or time does not exist. Check the day, month and hour.',
    format: 'Use the form 2001-09-11 08:00.'
  } as Record<string, string>,
  fieldNames: { from: 'start', to: 'end' } as Record<string, string>,
  windowProblems: {
    missing: 'Choose a time range.',
    order: 'The end must come after the start.',
    'too-long': 'The time range can be 31 days at most.'
  } as Record<string, string>,
  estimateNone: 'None of the ticked sources has entries in this time range.',

  noticeHeading: 'Before you import',
  noticeLabel: 'Notice',
  noticePointer: 'Read the notice at the top of this window first.',
  ack: 'I confirm what I import or record meets the conditions in the notice at the top of this window.',

  importButton: 'Import',
  importing: 'Importing…',
  importCancel: 'Cancel import',
  importCancelling: 'Cancelling…',
  importBlocked: {
    none: 'Tick at least one source.',
    ack: 'Tick the box to confirm the notice.',
    window: 'Choose a time range first.',
    tooMany: 'Choose 64 sources or fewer.',
    busy: 'An import is running.',
    case: 'Choose or start a case above first. Files are kept in a case.'
  } as Record<string, string>,
  entryLoading: 'Checking this computer…',
  retry: 'Try again',
  sessionLost: 'Open Unravis from the link it printed when it started, or restart it.',
  resultsHeading: 'Results',
  chooseAgain: 'Back to the list',
  statusQueued: 'Waiting to start',
  rowFailedGeneric: 'The server could not finish this import. Try again.',
  serverProblem: 'The server ran into a problem. Try again.',
  tones: { good: 'Done', neutral: 'Skipped', bad: 'Failed' } as Record<string, string>,

  liveHeading: 'Live recording',
  liveHelp: 'Records process names and numbers only. Stops after 72 hours.',
  liveIntervalLabel: 'How often, in seconds',
  liveStarting: 'Starting…',
  liveStopping: 'Stopping…',
  liveAdminNote: 'Some processes need administrator rights.',
  liveStopped: 'Recording stopped.',
  liveStopReasons: {
    cap: 'Recording stopped at the limit.',
    'case-closed': 'Recording stopped when the case was closed.',
    error: 'Recording stopped after an error. Start it again.'
  } as Record<string, string>
} as const;

/* ------------------------------------------------------------------ wording keys */
// The server sends wording keys such as "discover.iis.shows". The key's middle part names the source. A name this table does not hold falls
// back to the importer's help line, then to a generic line, so a new detector never shows a raw key.

const SHOWS: Record<string, string> = {
  iis: 'Each web request IIS served: time, address, status and time taken.',
  httperr: 'Requests that HTTP.sys refused before IIS saw them.',
  freb: 'Detail for requests that failed or ran slow.',
  ancm: 'What an ASP.NET Core app wrote to its console.',
  log4net: 'Messages the app wrote through log4net.',
  nlog: 'Messages the app wrote through NLog.',
  serilog: 'Messages the app wrote through Serilog.',
  mel: 'Messages the app wrote to its log file.',
  events: 'Windows events for apps and services, such as pool restarts.',
  taskscheduler: 'When scheduled tasks started and stopped.',
  redis: 'The Redis server log.',
  mongo: 'The MongoDB server log, with slow operations.',
  sql: 'The SQL Server error log.',
  nginx: 'Each web request nginx served.',
  apache: 'Each web request Apache served.',
  perfmon: 'CPU, memory and other Windows counters over time.',
  dotnet: 'Counters saved from a .NET app.',
  live: 'CPU and memory for this computer and its main processes.'
};

const HOW: Record<string, string> = {
  appinsights: 'Run your App Insights query in the Azure portal, then export the result as CSV.',
  hangfire: 'Run the job history query on the Hangfire database, then export the result as CSV or JSON.',
  'sql-xe': 'Open the system_health session in SQL Server Management Studio. Export its events to CSV.',
  'sql-agent': 'Run the job history query in SQL Server Management Studio. Export the result as CSV.',
  sql: 'In SQL Server Management Studio, export the events to CSV.',
  blg: 'Convert it with: relog file.blg -f csv -o file.csv. Then import the CSV.',
  perfmon: 'Convert it with: relog file.blg -f csv -o file.csv. Then import the CSV.',
  nettrace: 'Use the dotnet-counters CSV or JSON file instead. A trace file is not read.',
  dotnet: 'Use the dotnet-counters CSV or JSON file instead. A trace file is not read.',
  etl: 'Convert it with: tracerpt file.etl -of CSV. Then import the CSV.'
};

/** The middle part of a wording key: "discover.iis.shows" gives "iis". Anything that does not fit gives an empty string. */
export function wordingName(key: string): string {
  const match = /^discover\.([a-z0-9-]+)\.(?:shows|how)$/.exec(key);
  return match ? match[1] : '';
}

const has = (table: Record<string, string>, key: string): boolean => Object.prototype.hasOwnProperty.call(table, key);

/**
 * What a source shows, in words. Falls back to the importer's help line, then to a generic line.
 * A source that needs an export never gets the line "A source Unravis can read", because its own steps say the opposite.
 */
export function showsText(key: string, importer: string, needsExport = false): string {
  const name = wordingName(key);
  if (name && has(SHOWS, name)) return SHOWS[name];
  return importerHelp(importer) || (needsExport ? DISCOVER_TEXT.exportShows : DISCOVER_TEXT.genericShows);
}

/** The exact step that makes a file Unravis can read. */
export function howText(key: string): string {
  const name = wordingName(key);
  return name && has(HOW, name) ? HOW[name] : DISCOVER_TEXT.genericHow;
}

/* ------------------------------------------------------------------ small wording functions */

export const privilegeBadge = (elevated: boolean): string => (elevated ? DISCOVER_TEXT.badgeElevated : DISCOVER_TEXT.badgeStandard);

const DETECTOR_NAMES: Record<string, string> = {
  iis: 'IIS', httperr: 'HTTP.sys errors', freb: 'failed request traces', ancm: 'ASP.NET Core logs', log4net: 'log4net', nlog: 'NLog', serilog: 'Serilog',
  mel: 'app log files', events: 'event logs', eventlog: 'event logs', taskscheduler: 'scheduled tasks', redis: 'Redis', mongo: 'MongoDB', sql: 'SQL Server',
  nginx: 'nginx', apache: 'Apache', perfmon: 'performance logs', dotnet: '.NET counters', appinsights: 'App Insights', hangfire: 'Hangfire', host: 'this computer'
};

const detectorName = (detector: string): string => (has(DETECTOR_NAMES, detector) ? DETECTOR_NAMES[detector] : detector);

/** One line of the scan progress: "Checking IIS", "Checked event logs". */
export function detectorLine(detector: string, state: string): string {
  const name = detectorName(detector);
  if (state === 'done') return 'Checked ' + name;
  if (state === 'waiting') return 'Next: ' + name;
  if (state === 'skipped') return 'Skipped ' + name;
  if (state === 'failed') return 'Could not check ' + name;
  return 'Checking ' + name;
}

export function foundText(sources: number, places: number): string {
  const found = sources === 1 ? 'Found 1 source.' : 'Found ' + sources + ' sources.';
  if (places === 0) return found;
  return found + ' ' + (places === 1 ? '1 place' : places + ' places') + ' could not be used.';
}

export const fileCountText = (files: number): string => (files === 1 ? '1 file' : files + ' files');

/** "updated 3 min ago". The age is whole minutes, or null when the newest entry has no time. */
export function updatedText(minutes: number | null): string {
  if (minutes === null) return '';
  if (minutes < 1) return 'updated just now';
  if (minutes < 60) return 'updated ' + minutes + ' min ago';
  if (minutes < 1440) return 'updated ' + Math.floor(minutes / 60) + ' h ago';
  const days = Math.floor(minutes / 1440);
  return 'updated ' + days + (days === 1 ? ' day ago' : ' days ago');
}

/**
 * The size line of one row: "14 files, 50 MB, updated 3 min ago. Strong match." A live series is not a set of files, so it has no
 * count, size or match line. A source that holds nothing to measure (an export step) shows only its match line.
 */
export function rowMetaText(row: { kind: string; files: number; sizeBytes: number; confidence: number }, minutes: number | null): string {
  if (row.kind === 'live-series') return '';
  const match = confidenceText(row.confidence) + '.';
  if (row.files === 0 && row.sizeBytes === 0) return match;
  return fileCountText(row.files) + ', ' + formatBytes(row.sizeBytes) + (minutes !== null ? ', ' + updatedText(minutes) : '') + '. ' + match;
}

export function confidenceText(confidence: number): string {
  if (confidence >= 80) return 'Strong match';
  if (confidence >= 50) return 'Possible match';
  return 'Weak match';
}

export const gapReasonText = (reason: string): string => (has(DISCOVER_TEXT.gapReasons, reason) ? DISCOVER_TEXT.gapReasons[reason] : DISCOVER_TEXT.gapUnknown);

export const hostNameText = (label: string): string => 'Host name: ' + label;

export const windowChoiceLabel = (choice: string): string => (has(DISCOVER_TEXT.windowChoices, choice) ? DISCOVER_TEXT.windowChoices[choice] : choice);

export const windowProblemText = (problem: string): string => (has(DISCOVER_TEXT.windowProblems, problem) ? DISCOVER_TEXT.windowProblems[problem] : '');

/** The message under one custom time field: "Enter the start time.", "Leave off the seconds." */
export function timeFieldText(field: string, problem: string): string {
  if (!has(DISCOVER_TEXT.fieldProblems, problem)) return '';
  return DISCOVER_TEXT.fieldProblems[problem].replace('{field}', has(DISCOVER_TEXT.fieldNames, field) ? DISCOVER_TEXT.fieldNames[field] : field);
}

/** "About 1.2 GB will be read." */
export const estimateText = (bytes: number): string => (bytes <= 0 ? DISCOVER_TEXT.estimateNone : 'About ' + formatBytes(bytes) + ' will be read.');

export const importButtonText = (count: number): string => (count <= 0 ? 'Import' : 'Import ' + count + (count === 1 ? ' source' : ' sources'));

export const blockedText = (block: string): string => (has(DISCOVER_TEXT.importBlocked, block) ? DISCOVER_TEXT.importBlocked[block] : '');

const number = (n: number): string => n.toLocaleString('en-US');

/** The line for one source after its import: what was added, or why not. */
export function rowOutcomeText(outcome: { kind: string; rows?: number; reason?: string }): string {
  switch (outcome.kind) {
    case 'added': return 'Added ' + number(outcome.rows ?? 0) + (outcome.rows === 1 ? ' row' : ' rows');
    case 'duplicate': return 'Nothing new to add';
    case 'needs-admin': return 'Needs administrator';
    case 'cancelled': return 'Cancelled';
    case 'refused': return 'Refused. ' + (reasonText(outcome.reason ?? '') ?? 'The file was not accepted.');
    // A reason this screen does not know (a 500 with no name, for example) says the server could not finish, not that it refused.
    default: return reasonText(outcome.reason ?? '') ?? DISCOVER_TEXT.rowFailedGeneric;
  }
}

/** How a finished row reads at a glance: good (rows were added), neutral (nothing was added and nothing went wrong) or bad. */
export function rowOutcomeTone(outcome: { kind: string } | null): 'good' | 'neutral' | 'bad' | 'pending' {
  if (!outcome) return 'pending';
  switch (outcome.kind) {
    case 'added': return 'good';
    case 'duplicate':
    case 'cancelled': return 'neutral';
    default: return 'bad';
  }
}

/** The word that goes with a row's mark, so the result is never told by colour alone. */
export const toneLabel = (tone: string): string => (has(DISCOVER_TEXT.tones, tone) ? DISCOVER_TEXT.tones[tone] : '');

/** The line for a source still being read. Reuses the import screen's wording, so both screens say the same thing. */
export const rowProgressText = (state: string, percent: number | null): string => jobProgressText(state, percent);

export function finishedText(added: number, total: number): string {
  return 'Done. Added ' + added + ' of ' + total + (total === 1 ? ' source.' : ' sources.');
}

/** "Record CPU and memory every 15 seconds". With no interval (a recording found already running, whose interval the server does not say) it names no number. */
export const liveToggleLabel = (intervalSec: number | null): string => 'Record CPU and memory' + (intervalSec === null ? '' : ' every ' + intervalSec + ' seconds');

/** "Recording since 08:00, 1,440 samples." `since` is a time already formatted for the screen. */
export function liveStatusText(since: string, samples: number): string {
  return 'Recording' + (since ? ' since ' + since : '') + ', ' + number(samples) + (samples === 1 ? ' sample.' : ' samples.');
}

export function liveStopText(reason: string, samples: number): string {
  const base = has(DISCOVER_TEXT.liveStopReasons, reason) ? DISCOVER_TEXT.liveStopReasons[reason] : DISCOVER_TEXT.liveStopped;
  return base + ' ' + number(samples) + (samples === 1 ? ' sample was kept.' : ' samples were kept.');
}

/** What a problem means, in words. `unavailable` is the build without scanning. */
export function problemText(problem: string, error?: { status: number; reason: string; message: string }): string {
  switch (problem) {
    case 'unavailable': return DISCOVER_TEXT.unavailable;
    case 'busy': return 'A scan is already running. Try again in a moment.';
    case 'import-busy': return 'Another import is running. Try again in a moment.';
    case 'sampler-busy': return 'Recording is already on for another case. Stop it there first.';
    case 'expired': return DISCOVER_TEXT.scanExpired;
    case 'unreachable': return errorText({ status: 0, reason: 'unreachable', message: '' });
    case 'session': return error?.message || DISCOVER_TEXT.sessionLost;
    default:
      // A 5xx with no reason this screen knows is the server failing, not refusing the request.
      if (error && error.status >= 500 && reasonText(error.reason) === null) return DISCOVER_TEXT.serverProblem;
      return errorText(error ?? { status: 0, reason: '', message: '' });
  }
}

/* ------------------------------------------------------------------ lint */

/** Every string and every function's sample output, for the copy lint test. */
export function discoverCopyEntries(): LintEntry[] {
  const entries: LintEntry[] = [
    { id: 'discover.privilege.on', text: privilegeBadge(true) },
    { id: 'discover.privilege.off', text: privilegeBadge(false) },
    { id: 'discover.sample.checking', text: detectorLine('iis', 'running') },
    { id: 'discover.sample.checked', text: detectorLine('events', 'done') },
    { id: 'discover.sample.next', text: detectorLine('sql', 'waiting') },
    { id: 'discover.sample.skipped', text: detectorLine('redis', 'skipped') },
    { id: 'discover.sample.failed', text: detectorLine('mongo', 'failed') },
    { id: 'discover.sample.found', text: foundText(4, 2) },
    { id: 'discover.sample.found.one', text: foundText(1, 1) },
    { id: 'discover.sample.files', text: fileCountText(14) },
    { id: 'discover.sample.updated.now', text: updatedText(0) },
    { id: 'discover.sample.updated.min', text: updatedText(3) },
    { id: 'discover.sample.updated.hour', text: updatedText(180) },
    { id: 'discover.sample.updated.day', text: updatedText(3000) },
    { id: 'discover.sample.updated.one-day', text: updatedText(1440) },
    { id: 'discover.sample.meta.files', text: rowMetaText({ kind: 'log-set', files: 14, sizeBytes: 52428800, confidence: 90 }, 5) },
    { id: 'discover.sample.meta.export', text: rowMetaText({ kind: 'export-needed', files: 0, sizeBytes: 0, confidence: 60 }, null) },
    { id: 'discover.sample.shows.export', text: showsText('', '', true) },
    { id: 'discover.sample.field.empty', text: timeFieldText('from', 'empty') },
    { id: 'discover.sample.field.empty.to', text: timeFieldText('to', 'empty') },
    { id: 'discover.sample.problem.server', text: problemText('other', { status: 500, reason: '', message: '' }) },
    { id: 'discover.sample.row.failed.generic', text: rowOutcomeText({ kind: 'failed', reason: 'server' }) },
    { id: 'discover.sample.tone.good', text: toneLabel('good') },
    { id: 'discover.sample.tone.neutral', text: toneLabel('neutral') },
    { id: 'discover.sample.tone.bad', text: toneLabel('bad') },
    { id: 'discover.sample.confidence.strong', text: confidenceText(90) },
    { id: 'discover.sample.confidence.possible', text: confidenceText(60) },
    { id: 'discover.sample.confidence.weak', text: confidenceText(10) },
    { id: 'discover.sample.host', text: hostNameText('local') },
    { id: 'discover.sample.estimate', text: estimateText(1288490188) },
    { id: 'discover.sample.estimate.none', text: estimateText(0) },
    { id: 'discover.sample.import.one', text: importButtonText(1) },
    { id: 'discover.sample.import.many', text: importButtonText(3) },
    { id: 'discover.sample.row.added', text: rowOutcomeText({ kind: 'added', rows: 41200 }) },
    { id: 'discover.sample.row.added.one', text: rowOutcomeText({ kind: 'added', rows: 1 }) },
    { id: 'discover.sample.import.none', text: importButtonText(0) },
    { id: 'discover.sample.row.duplicate', text: rowOutcomeText({ kind: 'duplicate' }) },
    { id: 'discover.sample.row.admin', text: rowOutcomeText({ kind: 'needs-admin' }) },
    { id: 'discover.sample.row.cancelled', text: rowOutcomeText({ kind: 'cancelled' }) },
    { id: 'discover.sample.row.refused', text: rowOutcomeText({ kind: 'refused', reason: 'unsupported-format' }) },
    { id: 'discover.sample.row.refused.unknown', text: rowOutcomeText({ kind: 'refused', reason: 'a-reason-nobody-named' }) },
    { id: 'discover.sample.row.failed', text: rowOutcomeText({ kind: 'failed', reason: 'unreachable' }) },
    { id: 'discover.sample.row.progress', text: rowProgressText('running', 40) },
    { id: 'discover.sample.finished', text: finishedText(3, 4) },
    { id: 'discover.sample.finished.one', text: finishedText(1, 1) },
    { id: 'discover.sample.live.toggle', text: liveToggleLabel(15) },
    { id: 'discover.sample.live.toggle.bare', text: liveToggleLabel(null) },
    { id: 'discover.sample.live.status', text: liveStatusText('08:00 UTC', 1440) },
    { id: 'discover.sample.live.status.bare', text: liveStatusText('', 1) },
    { id: 'discover.sample.live.stop.cap', text: liveStopText('cap', 17280) },
    { id: 'discover.sample.live.stop.closed', text: liveStopText('case-closed', 1) },
    { id: 'discover.sample.live.stop.error', text: liveStopText('error', 0) },
    { id: 'discover.sample.live.stop.other', text: liveStopText('', 5) },
    { id: 'discover.sample.shows.fallback', text: showsText('discover.nothing.shows', 'iis-w3c') },
    { id: 'discover.sample.shows.generic', text: showsText('', '') },
    { id: 'discover.sample.how.generic', text: howText('') }
  ];
  for (const problem of ['unavailable', 'busy', 'import-busy', 'sampler-busy', 'expired', 'unreachable', 'session', 'other']) entries.push({ id: 'discover.problem.' + problem, text: problemText(problem) });
  for (const name of Object.keys(SHOWS)) entries.push({ id: 'discover.shows.' + name, text: showsText('discover.' + name + '.shows', '') });
  for (const name of Object.keys(HOW)) entries.push({ id: 'discover.how.' + name, text: howText('discover.' + name + '.how') });
  for (const reason of Object.keys(DISCOVER_TEXT.gapReasons)) entries.push({ id: 'discover.gap.' + reason, text: gapReasonText(reason) });
  for (const id of Object.keys(DETECTOR_NAMES)) entries.push({ id: 'discover.detector.' + id, text: detectorLine(id, 'running') });
  const walk = (prefix: string, value: unknown) => {
    if (typeof value === 'string') entries.push({ id: prefix, text: value });
    else if (value && typeof value === 'object') for (const [key, inner] of Object.entries(value)) walk(prefix + '.' + key, inner);
  };
  walk('discover.text', DISCOVER_TEXT);
  return entries;
}
