import type { LintEntry } from './copylint';
import { DISCOVER_TEXT, locateReasonText, probeReasonText, unreadableText } from './discoverCopy';
import type { AddressProblem, DatabaseProblem, TagProblem } from '../wizardSources';

/**
 * Every fixed sentence of the sources step of the create wizard, and the small builders for the rest.
 * Pure: no DOM, no fetch. Rules: plain words; the scan covers this computer only and says so; another computer is reached by an address or by
 * files the person copied; nothing the person typed as a secret is ever put into a sentence here (only host names and names they chose).
 */

export const SOURCES_TEXT = {
  intro: 'Choose the logs and data to look at. You can skip this step and add data after the case is made.',
  noScanSupport: DISCOVER_TEXT.windowsOnly,

  thisPcHeading: 'On this computer',
  thisPcNote: 'The scan looks at this computer only. It reads file names and sizes. It opens nothing until the case is made.',
  scanStart: DISCOVER_TEXT.scanButton,
  scanAgain: DISCOVER_TEXT.scanAgain,
  scanBusy: DISCOVER_TEXT.scanningNow,
  scanCancel: DISCOVER_TEXT.scanCancel,
  scanCancelling: DISCOVER_TEXT.scanCancelling,
  scanLoading: DISCOVER_TEXT.entryLoading,
  scanNothing: DISCOVER_TEXT.nothingFound,
  listLabel: 'Found on this computer',
  gapsHeading: DISCOVER_TEXT.gapsHeading,

  chipReady: 'Ready',
  chipNotChecked: 'Not checked',
  chipSteps: 'Needs an export',
  chipAdmin: DISCOVER_TEXT.tagAdmin,
  chipLarge: DISCOVER_TEXT.tagLarge,
  unverifiedLead: 'Found, but Unravis cannot read it yet.',
  stepsLead: DISCOVER_TEXT.exportFirst,

  locateOpen: 'Where is it?',
  locateHelp: 'Type the full path to the log file, or to its folder, on this computer. Unravis checks it before you can add it.',
  locateLabel: 'Path on this computer',
  locateCheck: 'Check this place',
  locateChecking: 'Checking…',
  locateCancel: 'Cancel',
  locateExpired: DISCOVER_TEXT.scanExpired,

  tagsHeading: 'Name your sources',
  tagsIntro: 'Name each source by environment, portal and API. Leave any of them blank if you do not know it yet.',
  tagsEmpty: 'No sources are chosen yet.',
  tagsNote: 'Suggestions come from folder names. Pick one, or type your own.',
  envLabel: 'Environment',
  portalLabel: 'Portal',
  apiLabel: 'API',
  chipsSuggested: 'Suggested',
  chipsEnv: 'From the first step',
  tagBlank: 'Not named yet',
  removeSource: 'Remove',
  envUnknown: 'Pick one of the environments named on the first step. Or go back and add this one there.',
  tagSecret: 'This looks like a password or an address with a login. Type a plain name instead.',

  otherHeading: 'Another computer',
  otherHelp: 'The scan covers this computer only. To use data from another computer, connect to it by its address, or choose the files you copied from it.',
  addMongo: 'Connect to MongoDB',
  addSql: 'SQL Server by address',
  addRedis: 'Redis by address',
  addFiles: 'A file or export',
  close: 'Close',

  mongoHeading: 'MongoDB',
  mongoIntro: 'Use a read-only user. Unravis only reads. A password you type stays in memory on this computer and is not saved. A connection saved earlier can be picked below.',
  mongoSaved: 'Use a saved connection',
  mongoSavedNone: 'Type a new connection',
  mongoSavedHasPassword: '(password saved)',
  mongoSavedPasswordUsed: 'The saved password is used. It is not shown and never sent to this page.',
  mongoTls: 'Encryption and server trust',
  mongoTlsSystem: 'On: verify the certificate',
  mongoTlsCustom: 'On: use a certificate file',
  mongoTlsInsecure: 'On: skip verification (advanced)',
  mongoTlsUri: 'As set in the address',
  mongoTlsOff: 'Off',
  mongoTlsInsecureWarning: 'Certificate checks are off. Traffic stays encrypted, but the server is not verified. Use only on a network you trust.',
  mongoTlsOffWarning: 'Traffic is not encrypted. Use only on a trusted local network.',
  mongoCa: 'Certificate authority file (.pem or .crt)',
  mongoCaBad: 'Choose a PEM file with a CERTIFICATE block. Private keys are not accepted, and the file must be 256 KiB or smaller.',
  mongoCaNeeded: 'Choose the certificate file first.',
  mongoCollection: 'Audit log collection',
  mongoCollectionHint: 'The collection that holds the API audit log. The usual name is ApiAuditLog.',
  mongoUri: 'Connection address',
  mongoUriHint: 'For example: mongodb://host:27017. If you type the user name and password below, leave them out of the address.',
  mongoUser: 'User name (optional)',
  mongoPassword: 'Password (optional)',
  mongoAuth: 'Login database (optional)',
  mongoTest: 'Test and list databases',
  mongoTesting: 'Testing…',
  mongoNeedsAddress: 'Type the connection address first.',
  mongoNow: 'A connection shows MongoDB as it is now. Earlier hours come from its log files.',
  mongoListHeading: 'Databases found',
  mongoClassifyHelp: 'Choose what each database is. A database you do not choose is left out.',
  mongoAuditOptional: 'API audit logs are optional. You can make the case without any.',
  mongoNoDatabases: 'Connected, but no databases were listed.',
  mongoRemove: 'Remove this connection',
  mongoTestAgain: 'Test again',
  mongoFailed: 'Could not connect. Check the address, the user name and the password, then try again.',
  classLabel: 'What is it?',
  classUnset: 'Choose…',
  classAudit: 'API audit logs',
  classOther: 'Other data',
  classUnused: 'Do not use',

  addrLabel: 'Address',
  addrHint: 'A host name or an address, with a port if it is not the usual one. For SQL Server, 192.168.4.2,1655 works too.',
  addrCheck: 'Check and add',
  addrChecking: 'Checking…',
  addrFlagNote: 'It stays on your list, marked. Fix the address and check again, or remove it.',
  addrCheckAgain: 'Check again',

  sqlIntro: 'Fill this in the way you do in SQL Server Management Studio. The details are only kept on this computer for now. Nothing connects to the server yet.',
  sqlServer: 'Server name',
  sqlServerHint: 'For example 192.168.4.2,1655 (the port after a comma), or db1 if it uses the usual port.',
  sqlAuth: 'Authentication',
  sqlAuthSql: 'SQL Server Authentication',
  sqlAuthWindows: 'Windows Authentication',
  sqlUser: 'User name',
  sqlPassword: 'Password',
  sqlRemember: 'Remember password',
  sqlRememberHint: 'Off: the password is not kept at all. On: it is kept on this computer, sealed to your Windows account, and never shown again.',
  sqlDatabase: 'Database',
  sqlDatabaseHint: 'Leave empty to use the login’s default database.',
  sqlEncrypt: 'Encrypt',
  sqlEncryptMandatory: 'Mandatory',
  sqlEncryptOptional: 'Optional',
  sqlEncryptStrict: 'Strict',
  sqlTrust: 'Trust server certificate',
  sqlName: 'Connection name (optional)',
  sqlNameHint: 'A short name to find it by. Without one the server name is used.',
  sqlAdd: 'Add and keep details',
  sqlAdding: 'Adding…',
  sqlNeedServer: 'Type the server name first.',
  sqlNeedUser: 'Type the user name, or choose Windows Authentication.',
  sqlKept: 'Details kept on this computer.',
  sqlKeptWithPassword: 'Details kept on this computer, with the password.',
  sqlNotKept: 'The server was added, but its details could not be kept: ',
  sqlSavedHeading: 'Kept before',
  sqlUse: 'Use',
  sqlForget: 'Remove',
  sqlHasPassword: 'password kept',
  sqlNoPassword: 'no password kept',
  sqlWindowsLogin: 'Windows login',

  fileHeading: 'A file or export',
  fileIntro: 'Choose the kind of file you copied or exported from the other computer. You pick the file itself on the last step.',
  fileKind: 'Kind of file',
  fileName: 'A short name for it',
  fileNameHint: 'For example: Orders API log, March.',
  fileAdd: 'Add',
  fileAdded: 'Added to your sources.',

  limitFull: 'You can choose at most 200 sources.',
  alreadyChosen: 'That source is already chosen.'
} as const;

export const KIND_LABELS: Readonly<Record<string, string>> = {
  discover: 'Found on this computer',
  'mongo-database': 'MongoDB database',
  'sql-server': 'SQL Server',
  redis: 'Redis',
  'iis-logs': 'IIS web server logs',
  'app-log': 'App log',
  'hangfire-csv': 'Hangfire jobs (CSV)',
  'redis-slowlog-csv': 'Redis slow log (CSV)',
  file: 'File or export'
};

/** Names for the file kinds in the choice list. */
export const FILE_KIND_OPTIONS: ReadonlyArray<{ kind: string; label: string }> = [
  { kind: 'iis-logs', label: 'IIS web server logs' },
  { kind: 'app-log', label: 'App log (log4net, NLog or similar)' },
  { kind: 'hangfire-csv', label: 'Hangfire jobs (CSV export)' },
  { kind: 'redis-slowlog-csv', label: 'Redis slow log (CSV export)' },
  { kind: 'file', label: 'Another file or export' }
];

const own = (table: Record<string, string>, key: string): boolean => Object.prototype.hasOwnProperty.call(table, key);

export const kindLabel = (kind: string): string => (own(KIND_LABELS as Record<string, string>, kind) ? KIND_LABELS[kind] : KIND_LABELS.file);

/** "Your sources (3)". */
export const yourSourcesText = (count: number): string => 'Your sources (' + count + ')';

/** The label of the tick box of a found source. */
export const addFindingLabel = (display: string): string => 'Add ' + display;

/** Why a found file source cannot be added yet, in words (the scan's own reason). */
export const notReadableText = (key: string): string => unreadableText(key);

/** The answer to a path the person typed, success: "Found 3 files. You can add it now." */
export function locateOkText(files: number): string {
  return (files === 1 ? 'Found 1 file.' : 'Found ' + files + ' files.') + ' It is ready to add.';
}

export const locateFailText = (reason: string): string => locateReasonText(reason);

/** The line under the tag fields when a tag cannot be used. */
export function tagProblemText(problem: TagProblem): string {
  return problem === 'secret' ? SOURCES_TEXT.tagSecret : SOURCES_TEXT.envUnknown;
}

const ADDRESS_PROBLEMS: Record<AddressProblem, string> = {
  empty: 'Type a host name or an address.',
  secret: 'Type only the host name or address. Leave out any user name, password or web address.',
  'bad-host': 'That is not a host name or an address Unravis can use.',
  'bad-port': 'The port must be a number from 1 to 65535.'
};

export const addressProblemText = (problem: AddressProblem): string => ADDRESS_PROBLEMS[problem];

/** "Reached db1:1433." */
export const reachedText = (address: string): string => 'Reached ' + address + '.';

/** "Could not reach db1:1433. There was no answer in time. ..." The reason is one of the fixed probe reasons. */
export const unreachedText = (address: string, reason: string): string => 'Could not reach ' + address + '. ' + probeReasonText(reason);

const DATABASE_PROBLEMS: Record<DatabaseProblem, string> = {
  'not-tested': 'Test the connection first. That database was not in the list.',
  'needs-classification': 'Choose what the database is.',
  'bad-name': 'This database name is too long, or looks like a password, so it cannot be named as a source. Leave it as not used.',
  full: SOURCES_TEXT.limitFull
};

export const databaseProblemText = (problem: DatabaseProblem): string => DATABASE_PROBLEMS[problem];

/** "Connected. Found 3 databases." */
export function testedText(count: number): string {
  return count === 0 ? SOURCES_TEXT.mongoNoDatabases : 'Connected. Found ' + count + (count === 1 ? ' database.' : ' databases.');
}

/** "Chosen: 1 as API audit logs, 2 as other data." */
export function classSummaryText(audit: number, other: number): string {
  if (audit === 0 && other === 0) return 'None of these is chosen yet.';
  const parts: string[] = [];
  if (audit > 0) parts.push(audit + ' as API audit logs');
  if (other > 0) parts.push(other + ' as other data');
  return 'Chosen: ' + parts.join(', ') + '.';
}

/** Notice after the first step's environment list changed. */
export const envsClearedText = (count: number): string =>
  count + (count === 1 ? ' source was' : ' sources were') + ' set back to "not named yet", as its environment is not on the list from the first step.';

/** Notice after a new scan no longer offers some chosen sources. */
export const scanDroppedText = (count: number): string =>
  count + (count === 1 ? ' source was' : ' sources were') + ' taken off your list, as the new scan could not read ' + (count === 1 ? 'it.' : 'them.');

/** The tick box description for a source that needs administrator rights while Unravis runs as a standard user. */
export const adminNote = 'Reading this may need administrator rights.';

/** A message from the server, made safe: the typed address, user name and password are removed, and it is cut short. Falls back to a fixed line. */
export function safeServerLine(message: string, secrets: readonly string[]): string {
  let text = String(message ?? '');
  for (const secret of secrets) if (secret.length >= 3) text = text.split(secret).join('[hidden]');
  text = text.replace(/[a-z][a-z0-9+.-]*:\/\/\S+/gi, '[address hidden]').replace(/\s+/g, ' ').trim();
  if (text === '') return SOURCES_TEXT.mongoFailed;
  return [...text].slice(0, 240).join('');
}

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const wizardSourcesCopyEntries = (): LintEntry[] => [
  ...Object.entries(SOURCES_TEXT).map(([id, text]) => ({ id: 'wizard.sources.' + id, text })),
  ...Object.entries(KIND_LABELS).map(([id, text]) => ({ id: 'wizard.sources.kind.' + id, text })),
  ...FILE_KIND_OPTIONS.map((option) => ({ id: 'wizard.sources.fileKind.' + option.kind, text: option.label })),
  ...(Object.keys(ADDRESS_PROBLEMS) as AddressProblem[]).map((id) => ({ id: 'wizard.sources.address.' + id, text: ADDRESS_PROBLEMS[id] })),
  ...(Object.keys(DATABASE_PROBLEMS) as DatabaseProblem[]).map((id) => ({ id: 'wizard.sources.database.' + id, text: DATABASE_PROBLEMS[id] })),
  { id: 'wizard.sources.sample.yours', text: yourSourcesText(3) },
  { id: 'wizard.sources.sample.add', text: addFindingLabel('W3SVC2 (u_ex010911.log and 13 more)') },
  { id: 'wizard.sources.sample.notReadable', text: notReadableText('discover.wrongformat.shows') },
  { id: 'wizard.sources.sample.locateOk', text: locateOkText(3) },
  { id: 'wizard.sources.sample.locateOk1', text: locateOkText(1) },
  { id: 'wizard.sources.sample.locateFail', text: locateFailText('no-log-files') },
  { id: 'wizard.sources.sample.reached', text: reachedText('db1:1433') },
  { id: 'wizard.sources.sample.unreached', text: unreachedText('db1:1433', 'timed-out') },
  { id: 'wizard.sources.sample.unreached2', text: unreachedText('db1:1433', 'refused') },
  { id: 'wizard.sources.sample.tested', text: testedText(3) },
  { id: 'wizard.sources.sample.tested1', text: testedText(1) },
  { id: 'wizard.sources.sample.classes', text: classSummaryText(1, 2) },
  { id: 'wizard.sources.sample.classes0', text: classSummaryText(0, 0) },
  { id: 'wizard.sources.sample.envs', text: envsClearedText(2) },
  { id: 'wizard.sources.sample.envs1', text: envsClearedText(1) },
  { id: 'wizard.sources.sample.dropped', text: scanDroppedText(2) },
  { id: 'wizard.sources.sample.dropped1', text: scanDroppedText(1) },
  { id: 'wizard.sources.sample.admin', text: adminNote },
  { id: 'wizard.sources.sample.tagSecret', text: tagProblemText('secret') },
  { id: 'wizard.sources.sample.tagEnv', text: tagProblemText('unknown-env') }
];
