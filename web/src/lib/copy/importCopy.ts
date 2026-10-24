import type { CaseApiError, CaseInfo, ImportRecord, ImportReport, SuppliedProblem, SuppliedSpec } from '../cases';
import { formatTimeRange } from '../time';
import type { LintEntry } from './copylint';

/**
 * Every fixed sentence on the import screen, and the small helpers that build the rest.
 * They live here, not in the components, so the copy lint and the tests can read them (roadmap 6.6 and 8.14).
 * Rule: say what was read and what is missing. Never name a cause, and never call imported rows proof.
 */

/** The notice that is always on screen. Verbatim from the importer wave plan (UI card); do not reword it here. */
export const GATING_BANNER = 'Parsers are built and tested on synthetic files only. Importing real client exports needs: R2 preservation, R8 content review, R11 AI/MCP approval, R12 DBA dry run (SQL files), R13 Mongo access, R16 retention.';

/** The checkbox that must be ticked for each import. The banner above is what gets sent as ackText (see ackTextFor). */
export const ACK_LABEL = 'I confirm this file meets the conditions in the notice above.';

/** The only sentence shown after an erase (storage decision, "What the UI says, and only this"). */
export const ERASE_DONE = 'Case data deleted and its encryption key destroyed. Remaining fragments on disk are unreadable.';

/** The text sent as `ackText`: the exact notice the person acknowledged, so the case manifest records what they saw. */
export const ackTextFor = (): string => GATING_BANNER;

export const IMPORT_TEXT = {
  title: 'Import files',
  intro: 'Bring in files exported from your servers. They are read on this computer only and kept in an encrypted case.',
  closeLabel: 'Close import',
  noticeLabel: 'Notice',

  caseHeading: 'Case',
  noCases: 'No cases yet. Create one to import files into it.',
  loadingCases: 'Loading cases…',
  newCaseLabel: 'New case title',
  newCasePlaceholder: 'For example: Slow week',
  createCase: 'Create case',
  creating: 'Creating…',
  useCase: 'Use this case',
  inUse: 'In use',
  closedBadge: 'Closed',
  closeCase: 'Close case',
  closeNote: 'A closed case is erased 30 days after it was closed.',
  closedCase: 'This case is closed. Choose or create another case to import files.',
  eraseCase: 'Erase case',
  eraseWarning: 'Erasing deletes every import in this case and destroys its encryption key. This cannot be undone.',
  eraseConfirmButton: 'Erase this case',
  erasing: 'Erasing…',
  eraseCancel: 'Keep this case',
  titleMissing: 'Give the case a title first.',

  fileHeading: 'File',
  fileLabel: 'Choose a file',
  fileHelp: 'Text and CSV exports only. Compressed files are refused, so extract them first.',
  pickerLabel: 'File type',
  pickerTie: 'More than one file type fits this file. Choose the right one.',
  pickerPlaceholder: 'Choose a file type',
  pickerRefused: 'The file type was not found. If you know it, choose it to add the settings the owner gave you.',
  hostLabel: 'Host',
  hostNone: 'The file names its own hosts',
  hostNote: 'Pick the host this file came from. A host that is not in the host list is refused.',
  noHosts: 'The host list is empty, so a file that names a host is refused as an unknown host.',
  unknownHostBadge: 'unknown host',

  previewHeading: 'Preview',
  previewing: 'Reading the file…',
  previewNote: 'Nothing is saved until you press Import file.',
  rowsRead: 'Rows read',
  rowsKept: 'Rows kept',
  rowsRejected: 'Rows rejected',
  timeCovered: 'Time covered',
  timeZoneRule: 'Time zone rule',
  noTimeCovered: 'No times were read',
  gapsHeading: 'Known gaps',
  noGaps: 'None recorded',
  reasonsHeading: 'Why rows were rejected',
  reasonsSample: 'These counts come from the first 100 rejected rows.',
  noRejected: 'No rows were rejected.',
  detailsHeading: 'Source details',
  detailLabels: { sourceVersion: 'Source version', threshold: 'Threshold', sampling: 'Row selection', timezoneRule: 'Time zone rule' },
  readOnlyNote: 'Imported rows are symptoms or correlations. They are never proof of a cause.',

  importButton: 'Import file',
  importing: 'Importing…',
  blocked: {
    closed: 'This case is closed, so nothing can be imported into it.',
    busy: 'Working…',
    'no-preview': 'Choose a file and let the preview finish first.',
    stale: 'The file or its settings changed. A new preview is loading.',
    'unknown-host': 'The host in this file does not match the host list or the host you picked, so it cannot be imported.',
    nothing: 'The preview kept no rows, so there is nothing to import.',
    settings: 'A setting you typed is not valid. Fix it first.',
    ack: 'Tick the box to confirm the notice first.'
  },

  historyHeading: 'Imports in this case',
  noImports: 'Nothing imported yet.',
  historyColumns: { file: 'File', type: 'Type', host: 'Host', rows: 'Rows', rejected: 'Rejected', status: 'Status' },
  noHost: 'Not set',

  suppliedHeading: 'Settings the owner told you',
  suppliedIntro: 'Fill in only what the server owner told you. A blank box is not sent. The file is read again when you leave a box.',
  suppliedNotSet: 'Not set',
  suppliedNeedsFix: 'needs fixing',
  suppliedHistory: 'Settings',
  statusLabels: { ok: 'Imported', quarantined: 'Refused', duplicate: 'Duplicate' } as Record<string, string>,

  fileEmpty: 'This file is empty.',
  fileCancelled: 'The file was not read. Choose it again.',

  hostsHeading: 'Host list',
  hostsIntro: 'Name each server once. A file that names a host that is not on this list is refused.',
  hostsOpen: 'Edit host list',
  hostsClose: 'Hide host list',
  hostsLoading: 'Loading the host list…',
  hostsNone: 'No hosts yet.',
  hostAdd: 'Add host',
  hostRemove: 'Remove',
  hostSave: 'Save host list',
  hostSaving: 'Saving…',
  hostSaved: 'The host list was saved.',
  hostFields: { label: 'Label', aliases: 'Other names, separated by commas', role: 'Role', tz: 'Time zone', cores: 'Logical cores', runtime: 'Runtime' },
  hostRoleNone: 'Not set',
  hostRoles: { iis: 'IIS web server', sql: 'SQL Server', mongo: 'MongoDB', app: 'Application server' } as Record<string, string>,
  hostTzNote: 'A zone name such as Europe/London. Files with no zone of their own use it.',
  hostCoresBad: 'Logical cores must be a whole number from 1 to 1024.',
  hostLabelMissing: 'Every host needs a label.',

  timelineHeading: 'Timeline',
  timelineNote: 'Lines up the audit window on screen with the events imported into this case. Each line is a clue to look at, not an answer.',
  timelineBuild: 'Build timeline',
  timelineBuilding: 'Building the timeline…',
  timelineNoSource: 'Choose at least one source first, so the audit window is set.',

  exportHeading: 'Export this case',
  exportNote: 'Builds a file of what this case holds, with private values hidden. You see all of it before anything is saved.',
  exportPreview: 'Preview export',
  exportPreviewing: 'Building the preview…',
  exportCancel: 'Cancel',
  exportEmpty: 'This case holds no events, so there is nothing to export.',
  exportMasking: 'What is hidden',
  exportHash: 'SHA-256 of the JSON file',
  exportSave: 'Save files',
  exportSaving: 'Saving…',
  exportSaveNote: 'Saving downloads two files: the case as JSON, and its events as a spreadsheet file.',
  exportMismatch: 'A file did not match the SHA-256 the server sent, so nothing was saved. Preview it again.',
  exportShowFile: 'Show the JSON file content',
  exportEventsHeading: 'Events in the file'
} as const;

/** What each refusal reason means, in plain words. Reasons come from the importer enum and the case routes. */
export const REASON_TEXT: Record<string, string> = {
  'field-count': 'A row had the wrong number of fields.',
  'bad-time': 'A time value could not be read.',
  'tz-unlabelled': 'A time had no time zone. Pick the host so its zone can be used.',
  'dst-ambiguous': 'A local time fell in a clock change and could mean two moments.',
  'dst-nonexistent': 'A local time fell in a clock change gap, so it never happened.',
  'bad-number': 'A number could not be read.',
  'line-too-long': 'A line was longer than the limit.',
  encoding: 'The text encoding could not be read.',
  'unknown-host': 'The host in this file does not match the host list or the host you picked. Check the host you picked, or add the host in the file to the list, then try again.',
  'unscrubbed-column': 'The file has a column that may hold private text, so it was refused. Export it again without that column.',
  'unsupported-format': 'This file format is not supported.',
  'archive-extract-first': 'This is a compressed file. Extract it first, then choose the extracted file.',
  'tz-mismatch': 'The time zone in the file does not match the host you picked.',
  'reject-fraction': 'Too many rows were rejected, so the whole file was refused.',
  limit: 'The file is over the size limit for its type.',
  'cap-exceeded': 'There is not enough room for this file. Erase a closed case and try again.',
  'ack-required': 'Tick the box to confirm the notice before importing.',
  'not-found': 'That case was not found.',
  'bad-case-id': 'That is not a valid case id.',
  unreachable: 'The server could not be reached. Try again.',
  unreadable: 'The server sent an answer that could not be read.',
  'cases-unavailable': 'Imported cases are not available in this session.',
  'case-not-found': 'That case was not found. It may have been erased.',
  'case-closed': 'This case is closed, so nothing can be imported into it.',
  'bad-title': 'The title is empty, too long, or holds a control character.',
  'confirm-mismatch': 'The code typed does not match this case.',
  'ledger-not-stripped': 'The case was erased, but your ledger still points at it. Erase it again to finish.',
  'unknown-importer': 'That file type is not available.',
  'importer-required': 'Choose a file type before importing.',
  'request-too-large': 'The upload is larger than the limit.',
  'not-multipart': 'The upload was not sent as a file.',
  'bad-form': 'The upload could not be read.',
  'file-missing': 'No file was sent.',
  'import-failed': 'The import could not be completed. Nothing was kept.',
  'duplicate-event': 'The file repeats an event this case already holds.',
  'invalid-event': 'A row of the file made an event that failed its checks, so the file was refused.',
  'store-error': 'The case could not be read or written. Try again.',
  timeout: 'The request took too long. Try again.',
  canceled: 'The request was cancelled.',
  'timeline-failed': 'The timeline could not be built. Try again.',
  'hosts-readonly': 'The host list cannot be saved in this session.',
  'hosts-error': 'The host list could not be read. Try again.',
  'hosts-not-saved': 'The host list could not be saved. The earlier list is unchanged.',
  'invalid-hosts': 'The host list was refused. Check for a repeated label or name, an unknown time zone, or a bad number.',
  'bad-supplied': 'A setting you typed is not one this file type accepts, or its value is not valid.',
  'ack-mismatch': 'The notice you confirmed is not the one this version shows. Reload the page and try again.',
  'ack-too-long': 'The confirmation text is too long.',
  'import-busy': 'Other imports are running. Try again in a moment.',
  'bad-bucket': 'The step size for the lines was not usable. Zoom out and try again.',
  'bad-selector': 'A line could not be asked for. Reload the page and try again.',
  'too-many-series': 'Too many lines were asked for at once. Reload the page and try again.',
  'too-many-buckets': 'This window is too long to draw in small steps. Zoom out and try again.',
  'bad-episode': 'A stall period could not be read. Reload the page and try again.'
};

const GENERIC_ERROR = 'The server refused this request. Try again.';

/** Plain words for a reason, or null when the reason is not one this screen knows. */
export function reasonText(reason: string): string | null {
  // The server names the limit that was hit ("limit-bytes", "limit-rows", ...); every one reads the same here.
  if (reason.startsWith('limit-')) return REASON_TEXT.limit;
  return Object.prototype.hasOwnProperty.call(REASON_TEXT, reason) ? REASON_TEXT[reason] : null;
}

/**
 * The sentence for a refused request. A duplicate names the earlier import. A refused column is named by its header only.
 * The server's own message is shown only for a missing session, because that text is written for the screen.
 */
export function errorText(error: Pick<CaseApiError, 'status' | 'reason' | 'column' | 'importId' | 'message'>): string {
  if (error.reason === 'no_session' || error.status === 401) return error.message;
  if (error.reason === 'duplicate') return error.importId ? 'This file was already imported (import #' + error.importId + ').' : 'This file was already imported.';
  const base = reasonText(error.reason) ?? GENERIC_ERROR;
  return error.column ? base + ' Column: ' + error.column + '.' : base;
}

/** True when a refusal means the file names a host the host list does not hold. */
export const isUnknownHost = (error: Pick<CaseApiError, 'reason'>): boolean => error.reason === 'unknown-host';

/** Friendly names for the importers in the plan. A name the screen does not know is shown as the server sent it. */
export const IMPORTER_LABELS: Record<string, string> = {
  'iis-w3c': 'IIS web log',
  'iis-httperr': 'HTTP.sys error log',
  'mongod-json': 'MongoDB server log',
  'win-counters': 'Windows performance counters',
  'win-events': 'Windows event log',
  'schedule-hangfire': 'Hangfire job schedule',
  'schedule-agent': 'SQL Server Agent job schedule',
  appinsights: 'App Insights query',
  'sql-xe': 'SQL Server extended events',
  'sql-errorlog': 'SQL Server error log',
  'sql-pdh': 'SQL Server performance counters',
  'log4net-text': 'log4net text log',
  'log4net-xml': 'log4net XML log',
  'serilog-clef': 'Serilog JSON log',
  'serilog-text': 'Serilog text log',
  'mel-console': 'ASP.NET Core console log',
  'mel-json': 'ASP.NET Core JSON log',
  'ancm-stdout': 'ASP.NET Core stdout log',
  'node-jsonl': 'Node Redis proxy log',
  'dotnet-counters': '.NET runtime counters',
  'win-procsnap': 'Process snapshots',
  'redis-slowlog': 'Redis slow command log',
  'redis-latency': 'Redis latency history',
  'redis-info': 'Redis INFO samples',
  'redis-log': 'Redis server log',
  'mongo-profile': 'MongoDB profiler export',
  'mongo-currentop': 'MongoDB current operations',
  'mongo-serverstatus': 'MongoDB serverStatus samples',
  'mongostat-json': 'mongostat samples',
  'linux-sadf': 'Linux sar samples',
  'linux-pidstat': 'Linux pidstat samples'
};

/** One short line per importer saying what file it reads. A name the screen does not know has no help line. */
export const IMPORTER_HELP: Record<string, string> = {
  'iis-w3c': 'The W3C log file that IIS writes for each site.',
  'iis-httperr': 'The error log that HTTP.sys writes for requests IIS never saw.',
  'mongod-json': 'The JSON log file that the MongoDB server writes.',
  'win-counters': 'A CSV file of Windows counters saved by typeperf or relog.',
  'win-events': 'A CSV export of Windows event log entries.',
  'schedule-hangfire': 'A JSON export of the Hangfire recurring jobs.',
  'schedule-agent': 'A CSV export of the SQL Server Agent jobs and their schedules.',
  appinsights: 'A CSV or JSON result of an App Insights query.',
  'sql-xe': 'An XML or CSV export of the system_health session.',
  'sql-errorlog': 'The SQL Server error log file.',
  'sql-pdh': 'A CSV file of SQL Server counters saved by typeperf or relog.',
  'log4net-text': 'A text log written by log4net, with its layout pattern.',
  'log4net-xml': 'An XML log written by log4net.',
  'serilog-clef': 'A JSON log written by Serilog, one event per line.',
  'serilog-text': 'A text log file written by Serilog.',
  'mel-console': 'Console output of an ASP.NET Core app, with time stamps.',
  'mel-json': 'JSON console output of an ASP.NET Core app, with time stamps.',
  'ancm-stdout': 'The stdout log file that IIS keeps for an ASP.NET Core app.',
  'node-jsonl': 'A JSON log written by the Node Redis proxy, one entry per line.',
  'dotnet-counters': 'A CSV or JSON file saved by dotnet-counters.',
  'win-procsnap': 'A CSV of process snapshots saved by the Get-Process steps.',
  'redis-slowlog': 'The output of SLOWLOG GET saved from redis-cli.',
  'redis-latency': 'The Redis latency log you saved with redis-cli.',
  'redis-info': 'INFO samples saved from redis-cli by the sample loop.',
  'redis-log': 'The log file that the Redis server writes.',
  'mongo-profile': 'The profiler collection saved by mongoexport.',
  'mongo-currentop': 'Current operation samples saved by the mongosh loop.',
  'mongo-serverstatus': 'serverStatus samples saved by the mongosh loop.',
  'mongostat-json': 'The JSON output of mongostat.',
  'linux-sadf': 'CPU and memory samples saved by sadf.',
  'linux-pidstat': 'Process CPU and memory samples saved by pidstat.'
};

export const importerHelp = (name: string): string => (Object.prototype.hasOwnProperty.call(IMPORTER_HELP, name) ? IMPORTER_HELP[name] : '');

export const importerLabel = (name: string): string => (Object.prototype.hasOwnProperty.call(IMPORTER_LABELS, name) ? IMPORTER_LABELS[name] : name);

/** "12.4 MB" style size with binary units named plainly. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return (unit === 0 || value >= 100 ? Math.round(value) : Math.round(value * 10) / 10) + ' ' + units[unit];
}

/** The sentence for a file that is too big or empty, before anything is sent. */
export function sizeProblemText(problem: { kind: 'empty' } | { kind: 'too-big'; cap: number }, bytes: number): string {
  return problem.kind === 'empty' ? IMPORT_TEXT.fileEmpty : 'This file is ' + formatBytes(bytes) + '. The limit for this file type is ' + formatBytes(problem.cap) + '.';
}

/** "Looks like: IIS web log" */
export const detectedText = (importer: string): string => 'Looks like: ' + importerLabel(importer) + '.';

/** One line for a case in the list. */
export function caseStats(info: Pick<CaseInfo, 'imports' | 'bytes'>): string {
  return info.imports + (info.imports === 1 ? ' import, ' : ' imports, ') + formatBytes(info.bytes);
}

/** The prompt for the typed erase confirmation. The code is the first 8 characters of the case id. */
export const erasePrompt = (code: string): string => 'Type ' + code + ' to confirm.';

/** First and last time the file held, in UTC, or a plain line when none were read. */
export function coverageText(report: Pick<ImportReport, 'first' | 'last'>): string {
  if (!report.first || !report.last) return IMPORT_TEXT.noTimeCovered;
  return formatTimeRange(report.first, report.last, { zone: 'UTC', year: true });
}

/** The facts under "Source details" that the report holds, with labels. Empty facts are left out, never guessed. */
export function qualityDetails(quality: ImportReport['quality'] | undefined): { label: string; value: string }[] {
  if (!quality) return [];
  const keys = ['sourceVersion', 'threshold', 'sampling', 'timezoneRule'] as const;
  return keys.flatMap((key) => (quality[key] ? [{ label: IMPORT_TEXT.detailLabels[key], value: String(quality[key]) }] : []));
}

/* ------------------------------------------------------------------ settings the owner told you (card U3) */

/** Plain names for the supplied keys. A key the screen does not know is shown as the server sent it. */
export const SUPPLIED_LABELS: Record<string, string> = {
  slowms: 'Slow operation limit (ms)',
  'app-request-min-ms': 'Shortest request to count (ms)',
  pattern: 'log4net layout pattern',
  'log-time': 'Log times are in',
  'slowlog-log-slower-than': 'Slow command limit (microseconds)',
  'slowlog-max-len': 'Slow log length (entries)',
  'exported-at': 'Time the slow log was read',
  'latency-monitor-threshold': 'Latency monitor limit (ms)',
  year: 'Year the log was written',
  date: 'Day the samples were taken',
  pid: 'Process id',
  'date-order': 'Order of day and month in dates'
};

/** One short line per supplied key saying where the owner finds the value. */
export const SUPPLIED_HINTS: Record<string, string> = {
  slowms: 'The slowms value from the MongoDB profiler settings. Slower operations were logged.',
  'app-request-min-ms': 'The shortest request time, in milliseconds, that counts as an app request. Blank uses 1000.',
  pattern: 'The pattern line from the log4net config file.',
  'log-time': 'Choose UTC if the app wrote its times in UTC and the file does not say so.',
  'slowlog-log-slower-than': 'The slowlog-log-slower-than value from CONFIG GET.',
  'slowlog-max-len': 'The slowlog-max-len value from CONFIG GET.',
  'exported-at': 'When you ran SLOWLOG GET, for example 2001-02-03T04:05:06Z.',
  'latency-monitor-threshold': 'The latency-monitor-threshold value from CONFIG GET.',
  year: 'Redis log lines have no year. Type the year they were written.',
  date: 'mongostat rows hold a time of day only. Type the day, for example 2001-02-03.',
  pid: 'The process id that dotnet-counters was watching.',
  'date-order': 'Dates like 05/03/2001 are ambiguous. Choose which comes first. Blank reads them from the file.'
};

/** Words shown for the fixed values of a key. A value with no entry is shown as sent. */
export const SUPPLIED_VALUE_LABELS: Record<string, Record<string, string>> = {
  'log-time': { utc: 'UTC' },
  'date-order': { mdy: 'Month first (05/03 is May 3)', dmy: 'Day first (05/03 is 5 March)' }
};

const has = (table: object, key: string): boolean => Object.prototype.hasOwnProperty.call(table, key);

export const suppliedLabel = (key: string): string => (has(SUPPLIED_LABELS, key) ? SUPPLIED_LABELS[key] : key);
export const suppliedHint = (key: string): string => (has(SUPPLIED_HINTS, key) ? SUPPLIED_HINTS[key] : '');
export const suppliedValueLabel = (key: string, value: string): string => (has(SUPPLIED_VALUE_LABELS, key) && has(SUPPLIED_VALUE_LABELS[key], value) ? SUPPLIED_VALUE_LABELS[key][value] : value);

const plainNumber = (n: number): string => String(n);

/** What a number or text key accepts, as one short line under the box. Words for a fixed set are the options themselves. */
export function suppliedRangeText(spec: SuppliedSpec): string {
  if (spec.kind === 'number') return (spec.integer ? 'A whole number' : 'A number') + ' from ' + plainNumber(spec.min) + ' to ' + plainNumber(spec.max) + '.';
  if (spec.kind === 'text') return 'Up to ' + spec.maxLen + ' bytes of plain text.';
  return '';
}

/** The sentence under a box whose value the screen will not send. */
export function suppliedProblemText(problem: SuppliedProblem, spec: SuppliedSpec): string {
  switch (problem) {
    case 'not-a-number': return 'Type a plain number, such as 250.';
    case 'not-whole': return 'Use a whole number, with no decimal point.';
    case 'too-low': return 'The smallest value is ' + (spec.kind === 'number' ? plainNumber(spec.min) : '0') + '.';
    case 'too-high': return 'The largest value is ' + (spec.kind === 'number' ? plainNumber(spec.max) : '0') + '.';
    case 'not-in-set': return 'Choose one of the listed values.';
    case 'too-long': return 'This is too long. The limit is ' + (spec.kind === 'text' ? spec.maxLen : 0) + ' bytes.';
    default: return 'Remove line breaks and other hidden characters.';
  }
}

/** The end of the group's summary line: how many settings are set, or that one needs fixing. */
export const suppliedSummaryNote = (set: number, problems: number): string =>
  problems > 0 ? ' (' + IMPORT_TEXT.suppliedNeedsFix + ')' : set > 0 ? ' (' + set + ' set)' : '';

/** The settings kept with an import, as label and value pairs in key order, for the imported-file list. */
export function suppliedPairs(supplied: Record<string, string> | undefined): { key: string; label: string; value: string }[] {
  return Object.keys(supplied ?? {}).sort().map((key) => ({ key, label: suppliedLabel(key), value: suppliedValueLabel(key, (supplied ?? {})[key]) }));
}

/** The line shown after a successful import. */
export function importedText(record: Pick<ImportRecord, 'id' | 'rows' | 'rejected' | 'fileName'>): string {
  return 'Imported ' + record.rows + ' rows from ' + record.fileName + '. ' + record.rejected + ' rejected. This is import #' + record.id + '.';
}

export const statusLabel = (status: string): string => IMPORT_TEXT.statusLabels[status] ?? status;

/** The line shown after both case export files were saved. */
export const caseExportSavedText = (jsonSha256: string): string => 'Saved two files. SHA-256 of the JSON file: ' + jsonSha256;

/** The word for a host role, or the role as sent when it is not one the screen knows. */
export const hostRoleLabel = (role: string): string => (Object.prototype.hasOwnProperty.call(IMPORT_TEXT.hostRoles, role) ? IMPORT_TEXT.hostRoles[role] : role);

/**
 * Every fixed sentence, plus one sample of each built sentence, for the copy lint. A new string added above must reach this list
 * (the test checks that every string in IMPORT_TEXT and REASON_TEXT is linted).
 */
// GATING_BANNER and ERASE_DONE are mandated word for word (plan card and storage decision), so tests check their exact text instead of linting reading grade.
export function importCopyEntries(): LintEntry[] {
  const entries: LintEntry[] = [
    { id: 'import.ack', text: ACK_LABEL },
    { id: 'import.sample.detected', text: detectedText('iis-w3c') },
    { id: 'import.sample.caseStats', text: caseStats({ imports: 2, bytes: 4096 }) },
    { id: 'import.sample.erasePrompt', text: erasePrompt('abcd2345') },
    { id: 'import.sample.size', text: sizeProblemText({ kind: 'too-big', cap: 64 * 1024 * 1024 }, 200 * 1024 * 1024) },
    { id: 'import.sample.imported', text: importedText({ id: 3, rows: 120, rejected: 2, fileName: 'web-a.log' }) },
    { id: 'import.sample.duplicate', text: errorText({ status: 409, reason: 'duplicate', importId: 3, message: '' }) },
    { id: 'import.sample.column', text: errorText({ status: 422, reason: 'unscrubbed-column', column: 'sql_text', message: '' }) },
    { id: 'import.sample.generic', text: errorText({ status: 500, reason: '', message: '' }) },
    { id: 'import.sample.limit', text: errorText({ status: 413, reason: 'limit-bytes', message: '' }) },
    { id: 'import.sample.caseExportSaved', text: caseExportSavedText('0'.repeat(64)) }
  ];
  const walk = (prefix: string, value: unknown) => {
    if (typeof value === 'string') entries.push({ id: prefix, text: value });
    else if (value && typeof value === 'object') for (const [key, inner] of Object.entries(value)) walk(prefix + '.' + key, inner);
  };
  walk('import.text', IMPORT_TEXT);
  walk('import.reason', REASON_TEXT);
  walk('import.importer', IMPORTER_LABELS);
  walk('import.help', IMPORTER_HELP);
  walk('import.supplied.label', SUPPLIED_LABELS);
  walk('import.supplied.hint', SUPPLIED_HINTS);
  walk('import.supplied.value', SUPPLIED_VALUE_LABELS);
  const number: SuppliedSpec = { key: 'x', kind: 'number', min: 0, max: 600000, integer: true };
  const text: SuppliedSpec = { key: 'y', kind: 'text', maxLen: 64 };
  entries.push({ id: 'import.sample.supplied.range', text: suppliedRangeText(number) }, { id: 'import.sample.supplied.text', text: suppliedRangeText(text) });
  for (const problem of ['not-a-number', 'not-whole', 'too-low', 'too-high', 'not-in-set', 'too-long', 'bad-characters'] as const) {
    entries.push({ id: 'import.sample.supplied.' + problem, text: suppliedProblemText(problem, problem === 'too-long' ? text : number) });
  }
  return entries;
}
