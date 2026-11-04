import type { LintEntry } from './copylint';
import { EXTRA_KIND_IDS, FILE_KIND_IDS, isExtraKind, timeZoneRule } from '../fileKinds';
import type { CatalogKind, ExtraKind, PanelKind } from '../fileKinds';

/**
 * Every sentence of the "Import files" panel of the sources step, and of the IIS guide beside it (gap G1/G2, wireframe w37).
 * Pure: no DOM, no fetch. Rules: plain words; say what is read and what is not; never promise a time zone the file does not have.
 */

/** The name of each kind in the list and in the choice list. */
export const FILE_KIND_LABELS: Readonly<Record<CatalogKind, string>> = {
  'iis-w3c': 'IIS web log (W3C)',
  'iis-httperr': 'HTTP.sys error log (HTTPERR)',
  'iis-freb': 'IIS failed request trace',
  'sql-xe': 'SQL Server extended events',
  'sql-errorlog': 'SQL Server error log',
  'sql-querystore': 'SQL Server Query Store export',
  'win-events': 'Windows event log',
  'app-log': 'App log (log4net, NLog, Serilog or similar)'
};

/**
 * The three choices that are not importer names (git HEAD offered them in the kind list of the sources step). They are read by what the server
 * finds in the file; the person is not asked for a time zone for them.
 */
export const EXTRA_KIND_LABELS: Readonly<Record<ExtraKind, string>> = {
  'hangfire-csv': 'Hangfire jobs (CSV export)',
  'redis-slowlog-csv': 'Redis slow log (CSV export)',
  file: 'Another file or export'
};

export const fileKindLabel = (kind: string): string =>
  Object.prototype.hasOwnProperty.call(FILE_KIND_LABELS, kind) ? FILE_KIND_LABELS[kind as CatalogKind] : isExtraKind(kind) ? EXTRA_KIND_LABELS[kind] : '';

/** What Unravis reads from each kind. Shown once for each kind in the list, under the table. */
export const KIND_READS: Readonly<Record<CatalogKind, string>> = {
  'iis-w3c': 'IIS web log: request time (IIS writes W3C times in UTC), method, URL path, status and sub-status, Windows error code, time taken, bytes in and out, site and server name. Client IP and referer are kept. Cookies are kept only as hashes.',
  'iis-httperr': 'HTTP.sys error log: time (HTTP.sys writes it in UTC), client address, request line, status, the reason HTTP.sys refused or dropped the request, site number and queue name. Drawn as marks on the IIS lane.',
  'iis-freb': 'IIS failed request trace: the URL, the status and the time taken. It also names the IIS module that was running when the request failed or ran slow.',
  'sql-xe': 'SQL Server extended events: finished calls with their duration, CPU, reads, writes and result. Also errors, blocked-process reports and deadlock graphs. Each has its time.',
  'sql-errorlog': 'SQL Server error log: errors and their severity, failed logins, and disk and deadlock warnings. Each has its time.',
  'sql-querystore': 'SQL Server Query Store export: for each query and plan in each interval, the average duration, CPU and number of runs. It also shows plan changes.',
  'win-events': 'Windows event log: service starts and stops, crashes and warnings, each with its time.',
  'app-log': 'App log: the time, level and message of each line. Unravis reads the file to find out which log program wrote it.'
};

export const EXTRA_KIND_READS: Readonly<Record<ExtraKind, string>> = {
  'hangfire-csv': 'Hangfire recurring jobs export: for each job its id, queue, cron schedule, time zone and the last and next run times. Hangfire keeps them in its SQL Server tables, so an engineer exports those rows to CSV. Leave out the columns that name the job method and its arguments: a file that has them is set aside, as Unravis never reads them. Unravis reads the file to confirm what it is.',
  'redis-slowlog-csv': 'Redis slow log: the reply of the SLOWLOG GET command, saved to a file (for example from redis-cli). Each slow command has its time and how long it took in microseconds. Unravis reads the file to confirm what it is.',
  file: 'Another file or export: Unravis reads the file and works out what it is. If it cannot tell, the case says so for that file only.'
};

export const readsText = (kind: string): string =>
  Object.prototype.hasOwnProperty.call(KIND_READS, kind) ? KIND_READS[kind as CatalogKind] : isExtraKind(kind) ? EXTRA_KIND_READS[kind] : '';

/** The time zone column for a kind that needs no choice, or that needs one. */
export const ZONE_TEXT = {
  utc: 'UTC (as this type writes it)',
  local: 'Server local time',
  varies: 'Depends on the log program',
  none: 'None',
  asFile: 'As the file writes them'
} as const;

export const zoneText = (kind: string): string => {
  const rule = timeZoneRule(kind);
  return rule === null ? (isExtraKind(kind) ? ZONE_TEXT.asFile : ZONE_TEXT.none) : ZONE_TEXT[rule];
};

export const FILES_TEXT = {
  heading: 'Import files',
  intro: 'Choose a folder or files, or drag and drop files here. Unravis suggests what each one is. Nothing is read until the case is made.',
  dropTitle: 'Drag and drop files here',
  dropOr: 'or',
  dropLabel: 'Drop files here',
  chooseFiles: 'Choose files…',
  chooseFolder: 'Choose a folder…',
  filesInputLabel: 'Choose files to import',
  folderInputLabel: 'Choose a folder to import',

  listHeading: 'The files',
  listHeadingFolder: 'Files in this folder',
  readsHeading: 'What will be read',
  listHelp: 'Unravis suggests a type for each one. Change it if it is wrong, or choose “Don’t use this file”.',
  colFile: 'File',
  colSize: 'Size',
  colKind: 'What it is',
  colZone: 'Time zone of its times',
  dontUse: 'Don’t use this file',
  notRecognised: 'Not recognised',
  notRecognisedLeft: 'Not recognised, so it is left out.',
  compressed: 'Compressed files are refused. Extract it first.',
  empty: 'This file is empty, so it is left out.',
  noZone: '–',
  colComputer: 'Computer and time zone',
  dropReading: 'Reading the folder…',
  dropTruncated: 'The folder holds more than 1000 files. Only the first 1000 were listed. Drop a smaller folder to see the rest.',
  dropTooDeep: 'Some folders are more than 12 levels down. They were not opened. Drop the inner folder to see them.',
  dropNoFiles: 'No files were found in what you dropped.',
  dropNoEntries: 'This browser did not let the page read the folder. Use “Choose a folder…” instead.',

  clear: 'Clear',
  removeRow: 'Remove',
  addNone: 'Add files',
  folderNote: 'The folder may hold many files. Only the ones you keep are added.',
  added: 'Added to your sources. You pick nothing more for these files on the last step.',
  held: 'Chosen. It is read when the case is made.',
  full: 'You can choose at most 200 sources. The rest were not added.',
  supportedTail: '. Text and CSV exports only. Extract compressed files first.'
} as const;

export const looksLikeText = (kind: string): string => 'Looks like: ' + fileKindLabel(kind) + '.';
export const suggestedText = (kind: string): string => 'Suggested: ' + fileKindLabel(kind) + '.';
export const chosenKindText = (kind: string): string => 'Chosen by you: ' + fileKindLabel(kind) + '.';
export const removeRowLabel = (name: string): string => 'Remove ' + name + ' from the list';
export const kindSelectLabel = (name: string): string => 'What ' + name + ' is';
export const addFilesText = (count: number): string => (count > 0 ? 'Add ' + count + (count === 1 ? ' file' : ' files') : FILES_TEXT.addNone);
export const zoneWarnText = (count: number): string =>
  count + (count === 1 ? ' file writes' : ' files write') + ' server local time. Until you say which time zone, the timeline cannot line ' + (count === 1 ? 'it' : 'them') + ' up with other sources.';
export const hostWarnText = (count: number): string =>
  count + (count === 1 ? ' file needs' : ' files need') + ' a computer named. Pick one in the list, or add it on the last step. The case cannot be made until each has one.';
export const dropUnreadableText = (count: number): string => (count === 1 ? '1 item could not be read and was left out.' : count + ' items could not be read and were left out.');
export const addedText = (count: number): string => (count === 1 ? '1 file was added to your sources.' : count + ' files were added to your sources.');
export const skippedText = (count: number): string => (count === 1 ? '1 file was left out.' : count + ' files were left out.');

/** "Supported: IIS web log (W3C) · HTTP.sys error log (HTTPERR) · …". */
export const supportedText = (): string => 'Supported: ' + FILE_KIND_IDS.filter((id) => id !== 'app-log').map((id) => FILE_KIND_LABELS[id]).join(' · ') + ' · ' + FILE_KIND_LABELS['app-log'] + FILES_TEXT.supportedTail;

/* ------------------------------------------------------------------ the IIS guide */

/** A piece of a guide line: plain text, or a name to be shown as code (a field or a folder). */
export type GuideSeg = string | { code: string };

export interface GuideItem { id: string; lead: string; parts: readonly GuideSeg[] }

export const IIS_GUIDE: { heading: string; items: readonly GuideItem[] } = {
  heading: 'Before you add IIS logs: what has to be on',
  items: [
    {
      id: 'w3c',
      lead: 'Web log (W3C):',
      parts: [
        'In IIS Manager, open Logging, choose the W3C format and tick at least ',
        { code: 'date, time, cs-method, cs-uri-stem, sc-status, sc-substatus, sc-win32-status, time-taken' },
        '. ', { code: 'time-taken' }, ' gives the durations. Without it the lane shows counts only. Nice to have: ',
        { code: 's-sitename, sc-bytes, cs-bytes, c-ip, cs(Referer), cs(Cookie)' }, '. IIS writes W3C times in UTC.'
      ]
    },
    {
      id: 'httperr',
      lead: 'HTTP.sys error log:',
      parts: ['It is on by default in ', { code: 'C:\\Windows\\System32\\LogFiles\\HTTPERR' }, '. Reading it needs administrator rights. It writes UTC. The file does not say which computer wrote it, so you pick the computer when you add it.']
    },
    {
      id: 'freb',
      lead: 'Failed request traces:',
      parts: ['Switch on Failed Request Tracing for the site and add a rule (by status or by time taken). The files are named ', { code: 'fr######.xml' }, '.']
    },
    {
      id: 'reading',
      lead: 'Reading:',
      parts: [
        'The Windows account that runs Unravis needs read access to the log folder (by default ', { code: '%SystemDrive%\\inetpub\\logs\\LogFiles\\W3SVC<id>' },
        '). On the server itself Unravis can find the logs for you. For another server, copy the files and add them here.'
      ]
    }
  ]
};

/* ------------------------------------------------------------------ the type cards (wireframe w37) */

/** One card of "Add files: logs and exports". The words are the wireframe's. A card preselects its kind for the files chosen in it. */
export interface FileCard {
  id: 'iis' | 'app' | 'querystore' | 'audit';
  kind: PanelKind;
  title: string;
  text: string;
  chip: string;
  button: string;
  /** An honest line the wireframe does not have, where Unravis cannot promise what the card says. */
  note?: string;
}

export const FILE_CARDS: readonly FileCard[] = [
  {
    id: 'iis', kind: 'iis-w3c', title: 'IIS logs (W3C)',
    text: 'Gives time-taken per request. Used for O in Step 3. Usually in C:\\inetpub\\logs\\LogFiles. Loose .log files.',
    chip: 'needs time-taken field', button: 'Add IIS logs'
  },
  {
    id: 'app', kind: 'app-log', title: 'Application logs',
    text: 'Errors and warnings near onset. Used by Step 5 only.',
    chip: 'text or JSON lines', button: 'Add app logs'
  },
  {
    id: 'querystore', kind: 'sql-querystore', title: 'Query Store export',
    text: 'For SQL you cannot connect to. A CSV from the sys.query_store views.',
    chip: 'csv', button: 'Add export'
  },
  {
    id: 'audit', kind: 'file', title: 'Audit export',
    text: 'For a Mongo you cannot connect to. A JSON or CSV export of the audit documents (for example from mongoexport). Gives Steps 2 to 4 their calls.',
    chip: 'json or csv', button: 'Add audit export',
    note: 'Unravis reads the file when the case is made. If it is not a format it reads, that file is reported on its own and the rest carry on.'
  }
];

export const CARDS_TEXT = {
  heading: 'Add files by type',
  lead: 'For servers Unravis cannot reach: bring IIS logs, application logs, a Query Store export and an audit export. Each file type has a short guide below.',
  footer: 'No step needs application logs or a Query Store export to start. IIS logs and audit exports feed the main steps (2 to 4).',
  unsure: 'Not sure what a file is? Add any file here and Unravis suggests what it is.',
  otherFiles: 'Other files',
  none: 'No files chosen yet'
} as const;

export const cardInputLabel = (card: FileCard): string => 'Choose ' + card.title + ' files';
export const cardCountText = (count: number): string => (count === 0 ? CARDS_TEXT.none : count === 1 ? '1 file chosen' : count + ' files chosen');
export const cardGroupHeading = (title: string, count: number): string => title + ': ' + (count === 1 ? '1 file' : count + ' files');

/** The short guides of the other two cards (the IIS one is IIS_GUIDE). Wireframe w37. */
export const CARD_GUIDES: ReadonlyArray<{ id: string; heading: string; steps: readonly string[] }> = [
  {
    id: 'app',
    heading: 'Guide: application logs',
    steps: [
      'Find the folder your app writes logs to (the path is in its logging settings).',
      'Copy the files for the incident hour.',
      'Plain text or JSON lines both work. Each line needs a time.'
    ]
  },
  {
    id: 'querystore',
    heading: 'Guide: Query Store export',
    steps: [
      'In SSMS, open the database, then Query Store, then Regressed Queries.',
      'Export the result grid to CSV.',
      'Choose that file here.'
    ]
  }
];

const segText = (parts: readonly GuideSeg[]): string => parts.map((part) => (typeof part === 'string' ? part : part.code)).join('');

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const fileKindsCopyEntries = (): LintEntry[] => [
  ...Object.entries(FILES_TEXT).map(([id, text]) => ({ id: 'files.' + id, text })),
  ...Object.entries(ZONE_TEXT).map(([id, text]) => ({ id: 'files.zone.' + id, text })),
  ...FILE_KIND_IDS.map((id) => ({ id: 'files.kind.' + id, text: FILE_KIND_LABELS[id] })),
  ...FILE_KIND_IDS.map((id) => ({ id: 'files.reads.' + id, text: KIND_READS[id] })),
  ...EXTRA_KIND_IDS.map((id) => ({ id: 'files.extra.' + id, text: EXTRA_KIND_LABELS[id] })),
  ...EXTRA_KIND_IDS.map((id) => ({ id: 'files.extraReads.' + id, text: EXTRA_KIND_READS[id] })),
  { id: 'files.sample.looks', text: looksLikeText('iis-w3c') },
  { id: 'files.sample.suggested', text: suggestedText('sql-xe') },
  { id: 'files.sample.chosen', text: chosenKindText('iis-httperr') },
  { id: 'files.sample.select', text: kindSelectLabel('u_ex261020.log') },
  { id: 'files.sample.add', text: addFilesText(3) },
  { id: 'files.sample.add1', text: addFilesText(1) },
  { id: 'files.sample.zone', text: zoneWarnText(2) },
  { id: 'files.sample.zone1', text: zoneWarnText(1) },
  { id: 'files.sample.host', text: hostWarnText(2) },
  { id: 'files.sample.host1', text: hostWarnText(1) },
  { id: 'files.sample.unreadable', text: dropUnreadableText(1) },
  { id: 'files.sample.unreadable2', text: dropUnreadableText(3) },
  { id: 'files.sample.added', text: addedText(3) },
  { id: 'files.sample.skipped', text: skippedText(2) },
  { id: 'files.sample.supported', text: supportedText() },
  { id: 'guide.heading', text: IIS_GUIDE.heading },
  ...IIS_GUIDE.items.map((item) => ({ id: 'guide.' + item.id, text: item.lead + ' ' + segText(item.parts) })),
  ...FILE_CARDS.map((card) => ({ id: 'cards.' + card.id, text: [card.title, card.text, card.chip, card.button, card.note ?? ''].join(' ') })),
  ...Object.entries(CARDS_TEXT).map(([key, text]) => ({ id: 'cards.text.' + key, text })),
  { id: 'cards.sample.input', text: cardInputLabel(FILE_CARDS[0]) },
  { id: 'cards.sample.count', text: cardCountText(2) },
  { id: 'cards.sample.group', text: cardGroupHeading('IIS logs (W3C)', 2) },
  ...CARD_GUIDES.map((guide) => ({ id: 'cards.guide.' + guide.id, text: guide.heading + ' ' + guide.steps.join(' ') }))
];
