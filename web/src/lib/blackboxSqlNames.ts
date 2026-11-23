/**
 * Plain names and one-sentence meanings for the lines of the SQL service's samples ("SQL Server: deeper checks"), no DOM.
 * The server keys them "sqlDmv.<kind>[.<what>].<field>": see internal/server/blackbox_sqlseries.go. Only numbers are charted,
 * so a name here never holds query text, a login or a database name: a database or file is its numeric id, a wait type is the
 * server's own wait type name, a memory-grant pool is its numeric id.
 */

export const SQL_DMV_GROUP = 'sqlDmv';

interface Entry { name: string; meaning: string }
const own = <T>(rec: Record<string, T>, key: string): T | undefined => (Object.prototype.hasOwnProperty.call(rec, key) ? rec[key] : undefined);

/** Lines with no named thing in them, by "<kind>.<field>". */
const PLAIN: Record<string, Entry> = {
  'requests.tasks': { name: 'Requests running at once', meaning: 'How many requests were in the server at the same moment. Read every 15 seconds.' },
  'requests.blocked': { name: 'Requests held up by a lock', meaning: 'How many of those requests were held up by a lock that another session held.' },
  'requests.headBlockers': { name: 'Sessions holding up others (head blockers)', meaning: 'How many sessions held up others while nobody held them up. A chain of locks starts here.' },
  'requests.maxWaitMs': { name: 'Longest wait of a running request', meaning: 'The longest time one request had been in a wait at that moment.' },
  'requests.maxBlockMs': { name: 'Longest hold-up by a lock', meaning: 'The longest time one request had been held up by a lock at that moment.' },
  'schedulers.currentTasks': { name: 'Tasks on the schedulers', meaning: 'How much work the processor schedulers held. Online schedulers, added together.' },
  'schedulers.runnableTasks': { name: 'Tasks ready to run (runnable)', meaning: 'Work that could run but had to queue for a processor. A number above zero for long means little spare processor time.' },
  'tempdb.internalBytes': { name: 'Tempdb: used by queries (internal objects)', meaning: 'Space in tempdb that queries took for sorts and hashes. A total over open sessions, so it falls when sessions end.' },
  'tempdb.userBytes': { name: 'Tempdb: used by programs (user objects)', meaning: 'Space in tempdb for temporary tables and table variables. A total over open sessions.' },
  'fileStats.readLatencyMs': { name: 'Time per read, all database files (file stalls)', meaning: 'The average time one read from a database file took in this interval. All databases together.' },
  'fileStats.writeLatencyMs': { name: 'Time per write, all database files (file stalls)', meaning: 'The average time one write to a database file took in this interval. All databases together.' },
  'fileStats.reads': { name: 'Reads from database files', meaning: 'How many reads from database files happened in this interval. All databases together.' },
  'fileStats.writes': { name: 'Writes to database files', meaning: 'How many writes to database files happened in this interval. All databases together.' },
  'ag.databases': { name: 'Databases in availability groups', meaning: 'How many local databases belong to an availability group. Read only when the server has them on.' },
  'ag.notSynchronizing': { name: 'Availability databases not synchronizing', meaning: 'How many local databases got no changes from the other copy. Above zero is worth a look.' },
  'ag.synchronizing': { name: 'Availability databases synchronizing', meaning: 'How many got changes but had not caught up yet. Normal for a copy that commits later.' },
  'ag.synchronized': { name: 'Availability databases synchronized', meaning: 'How many were fully caught up with the other copy.' },
  'ag.other': { name: 'Availability databases reverting or starting', meaning: 'How many were in a state that is neither synchronizing nor synchronized.' }
};

/** Lines about a named thing, by "<kind>.<field>"; the thing is shown after the name. */
const NAMED: Record<string, Entry> = {
  'requestsByWait.tasks': { name: 'Requests in wait type', meaning: 'How many requests were in this wait type at that moment. SQL Server names each kind of wait.' },
  'waits.deltaMs': { name: 'Wait time added (wait stats)', meaning: 'Time requests spent in this wait type since the last reading. Added over all requests, so it can exceed the time that passed.' },
  'waits.deltaTasks': { name: 'Waits ended (wait stats)', meaning: 'How many waits of this wait type ended since the last reading.' },
  'semaphores.grantees': { name: 'Queries holding a memory grant', meaning: 'How many queries held memory for sorts and hashes from this pool and semaphore.' },
  'semaphores.waiters': { name: 'Queries queued for a memory grant', meaning: 'How many queries sat in the queue for memory from this pool and semaphore. Above zero for long means little memory for queries.' },
  'semaphores.forced': { name: 'Memory grants forced', meaning: 'How many grants the server forced with less memory than asked since the last reading.' },
  'semaphores.timeouts': { name: 'Memory grant queues that timed out', meaning: 'How many queries gave up their place in the memory queue since the last reading.' }
};

/** Per-database file lines: "fileStats.db12.readLatencyMs". */
const DB_FIELD: Record<string, Entry> = {
  readLatencyMs: { name: 'Time per read (file stalls)', meaning: 'The average time one read from this database took in this interval. The database is shown by its number.' },
  writeLatencyMs: { name: 'Time per write (file stalls)', meaning: 'The average time one write to this database took in this interval. The database is shown by its number.' }
};

export interface SqlDmvName { name: string; where: string; meaning: string }

/** The name, the thing it is about and the meaning of a sqlDmv key, or null when the key is not one of this view's shapes. */
export function sqlDmvLine(key: string): SqlDmvName | null {
  const p = key.split('.');
  if (p[0] !== SQL_DMV_GROUP || p.length < 3) return null;
  const kind = p[1];
  const field = p[p.length - 1];
  if (p.length === 3) {
    const e = own(PLAIN, kind + '.' + field);
    return e ? { name: e.name, where: '', meaning: e.meaning } : null;
  }
  if (p.length === 4 && kind === 'fileStats') {
    const m = /^db(\d+)$/.exec(p[2]);
    const e = own(DB_FIELD, field);
    return m && e ? { name: e.name, where: 'database ' + m[1], meaning: e.meaning } : null;
  }
  if (p.length === 4) {
    const e = own(NAMED, kind + '.' + field);
    if (!e) return null;
    if (kind === 'semaphores') {
      const m = /^pool(\d+)sem(\d+)$/.exec(p[2]);
      return m ? { name: e.name, where: 'pool ' + m[1] + ', semaphore ' + m[2], meaning: e.meaning } : null;
    }
    return { name: e.name, where: p[2], meaning: e.meaning };
  }
  return null;
}

/** What went wrong with the SQL part of an answer, in plain words, or null when nothing needs saying. */
export interface SeriesSql {
  files: number; samples: number; missing: number; dropped: number; corrupt: number; torn: number; truncated: number;
  locked?: boolean; unsupported?: boolean; limited?: boolean; newer?: boolean; failed?: boolean;
}

export function sqlNote(s: SeriesSql | undefined): string | null {
  if (!s) return null;
  if (s.locked) return 'the folder is locked to administrators, so this PC cannot read it. Open this Blackbox by its address (the http:// address of the recorder) to see them';
  if (s.newer) return 'written by a newer version than this Unravis understands. Update Unravis to read them';
  if (s.unsupported) return 'this way of reaching the Blackbox cannot read them. Open it by its address instead';
  if (s.limited) return 'the range holds more than one read can take in, so the start of it is missing. Pick a shorter range';
  if (s.failed) return 'the read stopped before it was done, so some points are missing. Try again, or pick a shorter range';
  if (s.dropped > 0 && s.files === 0) return 'could not be read from the Blackbox. Try again';
  if (s.dropped > 0) return 'part of the range could not be read, so some points are missing';
  if (s.corrupt > 0 || s.torn > 1) return 'some lines in the recording were damaged and were left out';
  if (s.truncated > 0) return s.truncated === 1 ? 'one reading was cut at the service\'s row limit, so its totals are low' : s.truncated + ' readings were cut at the service\'s row limit, so their totals are low';
  return null;
}
