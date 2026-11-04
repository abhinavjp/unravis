import type { LintEntry } from './copylint';
import type { CollectionProblem, ReadyView, ReadyWhy } from '../auditGap';
import { DATABASE_CAP } from '../auditGap';

/** The words added to Add data > MongoDB on an existing case: the readiness chip, the collection-name check and the links. Pure. */
export const AUDIT_GAP_TEXT = {
  readyLabel: 'Readiness',
  collectionOk: 'The name has a valid shape.',
  collectionNote: 'One exact collection name. The first read of the database confirms the documents.',
  permissionLink: 'Check permission',
  permissionHelp: 'Step 8: MongoDB checks what the login may read and gives the script for the owner of the server to run.',
  warningLead: 'The server said:',
  cutNote: 'Only the first ' + DATABASE_CAP + ' databases are listed. If yours is missing, use a login that can see fewer databases.'
} as const;

export const COLLECTION_PROBLEM_TEXT: Readonly<Record<Exclude<CollectionProblem, ''>, string>> = {
  empty: 'Type the name of the collection that holds the audit documents.',
  'too-long': 'A collection name is at most 120 characters.',
  'bad-char': 'A collection name has no spaces and none of the characters " $ / and backslash.',
  system: 'Names that start with system. belong to MongoDB. Type the audit collection instead.',
  pattern: 'Type one exact collection name. A pattern with * or ? is not read.'
};

const READY_HINT: Readonly<Record<ReadyWhy, string>> = {
  testing: 'Testing the connection.',
  'not-tested': 'Test the connection to list its databases.',
  'no-database': 'Choose a database.',
  'no-use': 'Tick what the database is for.',
  collection: 'The audit collection name needs fixing.',
  'test-failed': 'The connection test failed. The reason is above.',
  'add-failed': 'The last add failed. The reason is below.',
  'connected-not-tagged': 'Connected, but the case was not told yet. Tag it to the case again.',
  'app-only': 'It is noted on the case only. The timeline does not read application data.',
  'added-audit': 'Added. The timeline and the guided steps read it.',
  'added-app': 'Noted on the case. Nothing was connected.',
  ready: 'Ready to add. The timeline and the guided steps will read it.'
};

export const readyHintText = (view: ReadyView): string => READY_HINT[view.why];

export const auditGapCopyEntries = (): LintEntry[] => [
  ...Object.entries(AUDIT_GAP_TEXT).map(([id, text]) => ({ id: 'auditGap.' + id, text })),
  ...Object.entries(COLLECTION_PROBLEM_TEXT).map(([id, text]) => ({ id: 'auditGap.collection.' + id, text })),
  ...Object.entries(READY_HINT).map(([id, text]) => ({ id: 'auditGap.ready.' + id, text }))
];
