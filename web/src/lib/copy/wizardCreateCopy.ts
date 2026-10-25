import type { LintEntry } from './copylint';
import type { Failure, StepState } from '../wizardSubmit';
import { rowOutcomeText } from './discoverCopy';

/**
 * Every fixed sentence of the review step and the "creating your case" screen (TSK-19), and the small helpers that build the rest.
 * Pure: no DOM, no fetch. Rules: plain words; say what will be made before it is made; a blank name reads "not named"; a failure says which
 * source and how to try again; never a path, a connection string or a password.
 */

export const CREATE_TEXT = {
  intro: 'Check what will be made. Nothing is saved until you press the button at the bottom.',
  summaryLabel: 'What the case holds',
  sharedNote: 'A shared source has no portal named, so it serves all of them.',
  caseLabel: 'Case name',
  envsLabel: 'Environments',
  sourcesLabel: 'Sources',
  notNamed: 'not named',
  noEnvs: 'No environments named. You can name them later.',
  noSources: 'No sources chosen. The case will be made empty, and you can add data from inside it.',
  windowLabel: 'How far back to read the logs of this computer',
  windowHint: 'Each log is cut to this period before it is kept. You can add more later.',
  fileLegend: 'Files for the case',
  fileHint: 'Choose the file for each source below. It is read on this computer only.',
  fileNone: 'No file chosen yet.',
  duplicateTitle: 'A case with this name is already on this computer. That is allowed. The two stay apart and have different codes.',
  scanStale: 'The scan of this computer is more than an hour old. Go back to the Sources step and scan again.',
  scanBusy: 'There is no finished scan of this computer right now. If one is still running, let it end first. If it was stopped, go back to the Sources step and scan again.',
  noticeLabel: 'Notice',
  needAck: 'Tick the box above to confirm the notice.',
  needFiles: 'Choose a file for each file source, or go back and take it off the list.',
  needWindow: 'Choose how far back to read.',
  keepNote: 'If something cannot be read, the case is still kept. You will see which source failed, and you can try it again from Add more data.',

  create: 'Create case',
  createEmpty: 'Create empty case',
  creating: 'Creating…',

  kindOnly: 'Named only. Nothing is read from it.',
  kindScan: 'Read from this computer.',
  kindFile: 'Read from the file you choose.',
  kindAudit: 'An audit database. It is named and added to the analysis.',
  kindDatabaseOther: 'A database kept for reference. Nothing is read from it.',

  progressHeading: 'Creating your case',
  stepCase: 'Make the case',
  stepEnvs: 'Save the environment names',
  stepTags: 'Save the source names',
  stepImports: 'Read the sources',
  waiting: 'Waiting',
  working: 'Working',
  done: 'Done',
  skipped: 'Nothing to do',
  failed: 'Failed',
  working_status: 'Making your case. Please wait. Do not close this window.',
  caseFailed: 'The case could not be made, so nothing was saved. You can press the button to try again.',
  locked: 'The master key is locked. Unlock it, and the guide carries on from where it stopped. Nothing is made twice.',
  lockedButton: 'Continue',
  openCase: 'Open the case',
  openAnyway: 'Open the case anyway',
  retryHint: 'The case was kept. Open it and use Add more data to try the failed sources again.',
  allDone: 'Your case is ready. Opening it now.',
  tryAgain: 'Try again'
} as const;

const plural = (n: number, one: string, many: string): string => n + ' ' + (n === 1 ? one : many);

/** "2 environments, 3 portals, 1 API, 4 shared sources". A count of zero is still said, so the line never changes shape. */
export function summaryText(counts: { environments: number; portals: number; apis: number; shared: number }): string {
  return plural(counts.environments, 'environment', 'environments') + ', ' + plural(counts.portals, 'portal', 'portals') + ', ' + plural(counts.apis, 'API', 'APIs') + ', ' + plural(counts.shared, 'shared source', 'shared sources');
}

/** The tags of one source in words: "Production > Portal A > not named". */
export function tagLine(source: { env: string; portal: string; api: string }): string {
  return [source.env, source.portal, source.api].map((part) => part || CREATE_TEXT.notNamed).join(' > ');
}

/** The button that ends the review. */
export const createButtonText = (sources: number, busy: boolean): string => (busy ? CREATE_TEXT.creating : sources === 0 ? CREATE_TEXT.createEmpty : CREATE_TEXT.create);

/** "What happens to this source", from its kind and classification. */
export function whatHappens(source: { kind: string; classification: string }): string {
  if (source.kind === 'discover') return CREATE_TEXT.kindScan;
  if (['iis-logs', 'app-log', 'hangfire-csv', 'redis-slowlog-csv', 'file'].includes(source.kind)) return CREATE_TEXT.kindFile;
  if (source.kind === 'mongo-database') return source.classification === 'audit' ? CREATE_TEXT.kindAudit : CREATE_TEXT.kindDatabaseOther;
  return CREATE_TEXT.kindOnly;
}

/** "File chosen: u_ex010911.log". The name is the person's own file name. */
export const fileChosenText = (name: string): string => 'File chosen: ' + name;

export const chooseFileLabel = (label: string): string => 'Choose the file for ' + label;

const REASONS: Readonly<Record<string, string>> = {
  'no-file': 'No file was chosen for it.',
  'no-window': 'No period was chosen to read.',
  missing: 'No period was chosen to read.',
  'no-scan': 'The scan of this computer was not ready, so nothing was read. Scan again, then add it from Add more data.',
  order: 'The period to read ends before it starts.',
  'too-long': 'The period to read is too long.',
  'not-started': 'The server did not start reading it.',
  unreachable: 'The server could not be reached.',
  'rate-limited': 'The server is asking for a short pause after too many connection tests. Wait a minute, then add it from Add more data.',
  'bad-request': 'The server did not accept the details. Check the connection, then add it from Add more data.',
  'bad-tag': 'A name was empty, too long, or looked like a password.',
  'tag-cap-exceeded': 'The case already holds as many names as it can.',
  'case-closed': 'The case is closed.',
  'case-not-found': 'The case could not be found.',
  'bad-classification': 'The database was not marked as audit logs, other data or not used.',
  'database-not-tested': 'The connection to the database was not tested a moment ago, or it could not be reached. Test it again.',
  'connection-lost': 'The connection to the database is no longer held. Test it again.',
  'needs-type': 'The file could be more than one kind. Add it from Add more data and choose the kind.',
  'unknown-type': 'The file is not a kind this app can read.',
  'unknown-host': 'This computer was not confirmed for reading.',
  locked: 'The master key was locked.'
};

export const reasonLine = (reason: string): string => REASONS[reason] ?? 'The server could not finish it.';

/** One line for something that did not go through, naming the source. */
export function failureText(failure: Failure, outcomeText?: string): string {
  const what = failure.step === 'case' ? '' : failure.step === 'environments' ? 'The environment names were not saved. ' : failure.step === 'tag' ? failure.label + ': its names were not saved. ' : failure.step === 'audit-source' ? failure.label + ': it was not added to the analysis. ' : failure.label + ': ';
  if (failure.step === 'case') return CREATE_TEXT.caseFailed;
  return what + (failure.step === 'import' && outcomeText ? outcomeText : reasonLine(failure.reason));
}

/** The words for one step's state. */
export function stateText(state: Pick<StepState, 'status'>): string {
  switch (state.status) {
    case 'running': return CREATE_TEXT.working;
    case 'done': return CREATE_TEXT.done;
    case 'failed': return CREATE_TEXT.failed;
    case 'skipped': return CREATE_TEXT.skipped;
    default: return CREATE_TEXT.waiting;
  }
}

/** The line at the end of a run: how many sources were read and what to do about the rest. */
export function resultText(imported: number, total: number, failures: number): string {
  if (failures === 0) return CREATE_TEXT.allDone;
  return 'Your case was kept. ' + imported + ' of ' + total + (total === 1 ? ' source was read. ' : ' sources were read. ') + plural(failures, 'thing needs', 'things need') + ' another try. ' + CREATE_TEXT.retryHint;
}

/** One import row while it is being read: "Reading, 40 percent". */
export const readingText = (percent: number | null): string => (percent === null ? 'Reading' : 'Reading, ' + percent + ' percent');

/** The words for one import row: its outcome when it has one, else where it is. */
export function rowText(row: { status: string; percent: number | null; outcome: { kind: string; rows?: number; reason?: string } | null; reason: string }): string {
  if (row.outcome) return rowOutcomeText(row.outcome);
  if (row.status === 'running') return readingText(row.percent);
  if (row.status === 'failed' || row.status === 'skipped') return reasonLine(row.reason);
  return CREATE_TEXT.waiting;
}

/** Every fixed sentence and a sample of each built one, for the copy lint. */
export const wizardCreateCopyEntries = (): LintEntry[] => [
  ...Object.entries(CREATE_TEXT).map(([id, text]) => ({ id: 'wizard.create.' + id, text })),
  ...Object.entries(REASONS).map(([id, text]) => ({ id: 'wizard.create.reason.' + id, text })),
  { id: 'wizard.create.sample.summary', text: summaryText({ environments: 2, portals: 3, apis: 1, shared: 4 }) },
  { id: 'wizard.create.sample.summary0', text: summaryText({ environments: 0, portals: 0, apis: 0, shared: 0 }) },
  { id: 'wizard.create.sample.result', text: resultText(2, 3, 1) },
  { id: 'wizard.create.sample.result1', text: resultText(0, 1, 2) },
  { id: 'wizard.create.sample.fail.tag', text: failureText({ step: 'tag', key: 'k', label: 'W3SVC1', reason: 'bad-tag' }) },
  { id: 'wizard.create.sample.fail.envs', text: failureText({ step: 'environments', key: '', label: '', reason: 'tag-cap-exceeded' }) },
  { id: 'wizard.create.sample.fail.audit', text: failureText({ step: 'audit-source', key: 'k', label: 'orders', reason: 'unreachable' }) },
  { id: 'wizard.create.sample.fail.import', text: failureText({ step: 'import', key: 'k', label: 'W3SVC1', reason: 'no-file' }) },
  { id: 'wizard.create.sample.fail.case', text: failureText({ step: 'case', key: '', label: '', reason: 'unreachable' }) },
  { id: 'wizard.create.sample.reading', text: readingText(40) },
  { id: 'wizard.create.sample.reading0', text: readingText(null) }
];
