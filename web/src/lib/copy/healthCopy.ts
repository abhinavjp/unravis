import type { LintEntry } from './copylint';
import type { HealthStatus } from '../health';

/**
 * Words for the Health checks panel. The checks only read: they never change the database and never run anything a person typed.
 * A check runs only when the person presses "Run checks", never when a filter or a setting changes.
 */
export const HEALTH_TEXT = {
  heading: 'Health checks',
  intro: 'These checks look at a database server that your logs depend on, and say in plain words what looks healthy and what needs a look. They only read. They never change anything, and they run only when you press Run checks.',
  sqlHeading: 'SQL Server',
  sqlNone: 'No SQL Server is kept yet. Add one on the Sources step, then come back.',
  sqlPick: 'Kept SQL Server',
  sqlPassword: 'Password for this run',
  sqlPasswordHint: 'Only needed if no password was kept. It is used once and is not stored.',
  sqlDatabase: 'Database to check',
  sqlDatabaseAny: 'The login’s default database',
  mongoHeading: 'MongoDB',
  mongoNone: 'No MongoDB connection is kept yet. Connect one on the Sources step, then come back.',
  mongoPick: 'Kept MongoDB connection',
  mongoPassword: 'Password for this run',
  mongoDatabase: 'Database to check',
  mongoDatabaseChoose: 'Choose a database…',
  mongoCollection: 'Collection that holds the audit logs',
  test: 'Test and list databases',
  testing: 'Testing…',
  run: 'Run checks',
  running: 'Running checks…',
  connectedTo: 'Connected.',
  needPick: 'Choose a kept connection first.',
  needDatabase: 'Choose the database to check first.',
  noDatabases: 'Connected, but no databases could be listed. Type the name instead.',
  typeDatabase: 'Database name',
  busy: 'Another set of checks is running. Try again when it has finished.',
  limited: 'Too many tries in a short time. Wait a minute, then try again.',
  demo: 'Health checks are not available in the demo.',
  locked: 'The case files are locked. Unlock them first.',
  failed: 'The checks could not be run. Try again.',
  notSaved: 'The checks ran, but this result could not be kept in the case: ',
  resultHeading: 'Result',
  historyHeading: 'Earlier runs',
  historyNone: 'No checks have been run for this case yet.',
  historyOpen: 'Show',
  historyClose: 'Hide',
  clickNote: 'Nothing runs by itself. Press Run checks when you want a fresh look.',
  lookFirst: 'Look at these first',
  why: 'Why this matters',
  rowsHeading: 'Details',
  cumulative: 'Many of these numbers have been counted by the server since it last started, so a quiet day can still show an old problem.',
  moreRows: 'Showing the first rows only.'
} as const;

export const HEALTH_STATUS_LABEL: Record<HealthStatus, string> = {
  ok: 'Looks fine',
  note: 'Worth knowing',
  warn: 'Needs a look',
  bad: 'Needs attention',
  unavailable: 'Could not check'
};

export const HEALTH_STATUS_MARK: Record<HealthStatus, string> = { ok: '✓', note: 'i', warn: '!', bad: '✕', unavailable: '–' };

export const overallText = (status: HealthStatus, counts: Record<HealthStatus, number>): string => {
  const total = counts.ok + counts.note + counts.warn + counts.bad + counts.unavailable;
  const parts = [
    counts.bad ? `${counts.bad} need attention` : '',
    counts.warn ? `${counts.warn} need a look` : '',
    counts.note ? `${counts.note} worth knowing` : '',
    counts.ok ? `${counts.ok} look fine` : '',
    counts.unavailable ? `${counts.unavailable} could not be checked` : ''
  ].filter(Boolean);
  const lead = status === 'bad' ? 'Something needs attention.' : status === 'warn' ? 'Something needs a look.' : status === 'unavailable' ? 'No check could run.' : 'Nothing alarming turned up.';
  return `${lead} ${total} ${total === 1 ? 'check' : 'checks'}: ${parts.join(', ')}.`;
};

export const connectedText = (version: string, edition: string): string => {
  const what = [version && 'SQL Server ' + version, edition].filter(Boolean).join(' ');
  return what ? `${HEALTH_TEXT.connectedTo} ${what}.` : HEALTH_TEXT.connectedTo;
};

export const healthCopyEntries = (): LintEntry[] => [
  ...Object.entries(HEALTH_TEXT).map(([id, text]) => ({ id: 'health.' + id, text })),
  ...Object.entries(HEALTH_STATUS_LABEL).map(([id, text]) => ({ id: 'health.status.' + id, text })),
  { id: 'health.sample.overall', text: overallText('warn', { ok: 8, note: 1, warn: 2, bad: 0, unavailable: 1 }) },
  { id: 'health.sample.connected', text: connectedText('16.0.1000', 'Developer Edition') }
];
