/**
 * Add data > MongoDB on an existing case (AUDIT-DB GAPS packet): the pure rules behind the readiness chip, the collection-name check and the
 * database list. No DOM, no fetch.
 *
 * The words of the chip are the readiness words used everywhere else: Ready, Partly ready, Cannot test yet, Failed.
 * The collection check looks at the NAME only. The server takes one exact collection name and checks the documents itself when it reads, so a
 * pattern with a wildcard is refused here instead of being accepted and finding nothing.
 */

export type ReadyWord = 'Ready' | 'Partly ready' | 'Cannot test yet' | 'Failed';
export type ReadyClass = 'ready' | 'partly' | 'cannot' | 'failed';
export type ReadyWhy =
  | 'testing' | 'not-tested' | 'no-database' | 'no-use' | 'collection' | 'test-failed' | 'add-failed'
  | 'connected-not-tagged' | 'app-only' | 'added-audit' | 'added-app' | 'ready';

export interface ReadyView { word: ReadyWord; cls: ReadyClass; why: ReadyWhy }

/** The longest name the collection box takes. */
export const COLLECTION_MAX = 120;
/** The most database names the list keeps. */
export const DATABASE_CAP = 200;

export type CollectionProblem = '' | 'empty' | 'too-long' | 'bad-char' | 'system' | 'pattern';

/** What is wrong with the shape of a collection name, or ''. Mirrors what MongoDB allows and what the server lets into a command. */
export function collectionProblem(raw: string): CollectionProblem {
  const name = raw.trim();
  if (name === '') return 'empty';
  if (name.length > COLLECTION_MAX) return 'too-long';
  if (/[*?]/.test(name)) return 'pattern';
  if (/[\u0000"$/\\\s]/.test(name)) return 'bad-char';
  if (name.toLowerCase().startsWith('system.')) return 'system';
  return '';
}

export interface ReadyInput {
  testing: boolean;
  tested: boolean;
  testProblem: boolean;
  database: string;
  /** What the ticks come to: 'unused' when nothing is ticked. */
  chosen: 'audit' | 'other' | 'unused';
  collectionProblem: CollectionProblem;
  connected: boolean;
  tagged: boolean;
  /** An add that failed and has not been tried again. */
  addProblem: boolean;
}

const view = (word: ReadyWord, why: ReadyWhy): ReadyView => ({
  word, why, cls: word === 'Ready' ? 'ready' : word === 'Partly ready' ? 'partly' : word === 'Failed' ? 'failed' : 'cannot'
});

/** Where this add stands, in the four readiness words. */
export function auditReady(i: ReadyInput): ReadyView {
  if (i.tagged) return i.chosen === 'audit' ? view('Ready', 'added-audit') : view('Partly ready', 'added-app');
  if (i.connected) return view('Partly ready', 'connected-not-tagged');
  if (i.addProblem) return view('Failed', 'add-failed');
  if (i.testing) return view('Cannot test yet', 'testing');
  if (i.testProblem) return view('Failed', 'test-failed');
  if (!i.tested) return view('Cannot test yet', 'not-tested');
  if (i.database === '') return view('Cannot test yet', 'no-database');
  if (i.chosen === 'unused') return view('Cannot test yet', 'no-use');
  if (i.chosen === 'audit' && i.collectionProblem !== '') return view('Cannot test yet', 'collection');
  return i.chosen === 'audit' ? view('Ready', 'ready') : view('Partly ready', 'app-only');
}

export interface DatabaseList { list: string[]; /** True when the server sent more names than are kept. */ cut: boolean }

/** The database names a test returned: text only, not empty, no more than DATABASE_CAP. */
export function cleanDatabases(raw: unknown): DatabaseList {
  const names = Array.isArray(raw) ? raw.filter((d): d is string => typeof d === 'string' && d.length > 0) : [];
  return { list: names.slice(0, DATABASE_CAP), cut: names.length > DATABASE_CAP };
}

/**
 * The password to send with a test. A typed one wins. With the field empty, the password of the connection that was just tested is used again,
 * but only for the same saved connection, so a test of another connection never carries it over.
 */
export function passwordForTest(typed: string, held: { savedConnectionId?: string; password: string } | null, savedId: string): string {
  if (typed !== '') return typed;
  return held && held.savedConnectionId === savedId ? held.password : '';
}
