import type { CaseApiError, CaseInfo, ImportRecord, ImportReport } from '../cases';
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
    'unknown-host': 'This file names an unknown host, so it cannot be imported.',
    nothing: 'The preview kept no rows, so there is nothing to import.',
    ack: 'Tick the box to confirm the notice first.'
  },

  historyHeading: 'Imports in this case',
  noImports: 'Nothing imported yet.',
  historyColumns: { file: 'File', type: 'Type', host: 'Host', rows: 'Rows', rejected: 'Rejected', status: 'Status' },
  noHost: 'Not set',
  statusLabels: { ok: 'Imported', quarantined: 'Refused', duplicate: 'Duplicate' } as Record<string, string>,

  fileEmpty: 'This file is empty.',
  fileCancelled: 'The file was not read. Choose it again.'
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
  'unknown-host': 'This file names an unknown host. Add it to the host list, then try again.',
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
  unreachable: 'The server could not be reached. Try again.'
};

const GENERIC_ERROR = 'The server refused this request. Try again.';

/** Plain words for a reason, or null when the reason is not one this screen knows. */
export function reasonText(reason: string): string | null {
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
  wincounters: 'Windows performance counters',
  winevents: 'Windows event log',
  schedule: 'Job schedule',
  appinsights: 'App Insights query',
  'sql-xe': 'SQL Server extended events',
  'sql-errorlog': 'SQL Server error log',
  'sql-pdh': 'SQL Server performance counters'
};

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

/** The line shown after a successful import. */
export function importedText(record: Pick<ImportRecord, 'id' | 'rows' | 'rejected' | 'fileName'>): string {
  return 'Imported ' + record.rows + ' rows from ' + record.fileName + '. ' + record.rejected + ' rejected. This is import #' + record.id + '.';
}

export const statusLabel = (status: string): string => IMPORT_TEXT.statusLabels[status] ?? status;

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
    { id: 'import.sample.generic', text: errorText({ status: 500, reason: '', message: '' }) }
  ];
  const walk = (prefix: string, value: unknown) => {
    if (typeof value === 'string') entries.push({ id: prefix, text: value });
    else if (value && typeof value === 'object') for (const [key, inner] of Object.entries(value)) walk(prefix + '.' + key, inner);
  };
  walk('import.text', IMPORT_TEXT);
  walk('import.reason', REASON_TEXT);
  walk('import.importer', IMPORTER_LABELS);
  return entries;
}
