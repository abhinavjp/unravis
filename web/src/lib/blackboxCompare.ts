/**
 * Help and comparison helpers for the Blackbox charts view, with no DOM: one plain sentence for each counter ("What does this
 * mean?"), the quick questions that pick counters for you, the "Latest compared" position of each counter against its own range,
 * and a tiny shape line for the "Everything" table. Only the answer's own numbers are used.
 */
import type { SeriesLine, SeriesPoint, SeriesUnit } from './blackboxSeries';
import { lineFigures, own, seriesUnit } from './blackboxSeries';

/** What a counter tells you, by the field name the recorder uses (the last part of its key). Plain words, no cause named. */
const MEANING: Record<string, string> = {
  cpu: 'How much of the time the processors were busy. Near 100 % for a long time means the PC has no spare processor time.',
  memAvail: 'Memory that programs could use right away. A low number means the PC is short of memory.',
  memCommit: 'Memory the PC has promised to programs. If it nears the limit, programs may fail to get more.',
  diskQueue: 'Requests waiting for the disk. A number that stays above 2 or 3 per disk means the disk is not keeping up.',
  diskMs: 'How long the disk takes to answer one request. Under 10 ms is quick for a disk. Over 20 ms is slow.',
  diskFreePct: 'Free space on the drive that holds the recordings.',
  netInBps: 'Data arriving over the network.',
  netOutBps: 'Data leaving over the network.',
  queueLength: 'Work waiting for a processor. A number above 2 per processor that stays there means work is queuing.',
  privilegedPct: 'Time spent in Windows itself, drivers and the system, not in your programs.',
  dpcPct: 'Time spent on driver work that could not wait. A high number can point to a driver.',
  interruptPct: 'Time spent answering hardware.',
  utilityPct: 'Processor use measured against what the processors can do at their current speed. It can pass 100 %.',
  contextSwitchesPerSec: 'How often the processors switched from one thread to another.',
  readMs: 'How long the disk takes to answer one read.',
  writeMs: 'How long the disk takes to answer one write.',
  readsPerSec: 'Reads from the disk each second.',
  writesPerSec: 'Writes to the disk each second.',
  splitIOPerSec: 'Requests the disk had to split in two. A high number can mean a fragmented disk.',
  hardFaultsPerSec: 'Pages Windows had to read from disk because they were not in memory. High numbers mean memory is short.',
  pagesPerSec: 'Pages moved between memory and disk each second.',
  pageFileUsagePct: 'How much of the page file (memory spilled to disk) is in use.',
  pageFaultsPerSec: 'How often a program asked for a page that was not in its working memory. Many are normal.',
  poolPagedBytes: 'Memory Windows itself uses that can be moved to disk.',
  poolNonPagedBytes: 'Memory Windows itself uses that must stay in memory. A number that keeps growing can point to a leak.',
  standbyBytes: 'Cached data in memory that can be reused or given back at once.',
  modifiedBytes: 'Memory holding changes that are not yet saved to disk.',
  freeBytes: 'Memory that is not in use at all.',
  stealPct: 'Time the host of this virtual machine took the processor for something else.',
  balloonBytes: 'Memory the host of this virtual machine took back from it.',
  retransmitsPerSec: 'Network data that had to be sent again. A high number means a poor connection.',
  resetsPerSec: 'Network connections that were reset.',
  outboundQueue: 'Network data waiting to be sent.',
  connFailuresPerSec: 'Network connections that failed to open.',
  discardedPerSec: 'Network packets that arrived and were thrown away.',
  outErrorsPerSec: 'Network packets that could not be sent.',
  currentQueueSize: 'Web requests waiting in the HTTP queue. Above 0 for long means the site is not keeping up.',
  maxQueueItemAge: 'How long the oldest waiting web request has waited.',
  rejectedRequests: 'Web requests that the server turned away.',
  arrivalRate: 'New web requests arriving each second.',
  activeRequests: 'Web requests being worked on right now.',
  activeThreads: 'Threads busy in the web worker.',
  pingLatencyMs: 'How long the web worker took to answer a health ping.',
  requestsQueued: 'ASP.NET requests waiting to run.',
  requestsCurrent: 'ASP.NET requests in progress.',
  requestsRejected: 'ASP.NET requests turned away.',
  requestWaitTimeMs: 'How long the newest ASP.NET request waited.',
  applicationRestarts: 'How many times the web application started again.',
  gen0Bytes: 'Size of the youngest .NET memory area.',
  gen1Bytes: 'Size of the middle .NET memory area.',
  gen2Bytes: 'Size of the oldest .NET memory area. Growth here that never falls back can point to a leak.',
  lohBytes: 'Size of the area for large .NET objects.',
  pctTimeInGC: 'Share of time .NET spent cleaning up memory. High numbers slow the program down.',
  allocBytesPerSec: 'How fast the program asked for new memory.',
  contentionPerSec: 'How often threads waited for a lock held by another thread.',
  threads: 'Threads running.',
  exceptionsPerSec: 'Errors thrown inside .NET programs each second.',
  batchRequestsPerSec: 'SQL Server batches received each second. This is how busy the database is.',
  userConnections: 'People or programs connected to SQL Server.',
  lockWaitsPerSec: 'How often SQL Server had to wait for a lock.',
  processesBlocked: 'SQL sessions that are waiting on another session.',
  memoryGrantsPending: 'Queries waiting for memory. Above 0 means SQL Server is short of query memory.',
  pageLifeExpectancy: 'How long data stays in SQL Server memory. A falling number means memory pressure.',
  freeListStallsPerSec: 'How often SQL Server had to wait for a free memory page.',
  compilationsPerSec: 'Query plans SQL Server built each second.',
  reCompilationsPerSec: 'Query plans SQL Server had to build again.',
  deadlocksPerSec: 'Deadlocks: two sessions waiting for each other. Any number above 0 is worth a look.',
  lockWaitTimeMs: 'How long SQL Server waited for locks.',
  logFlushWaitTimeMs: 'How long SQL Server waited to save its log to disk.',
  forwardedRecordsPerSec: 'Rows SQL Server had to look up in a second place.',
  cpuPct: 'Processor use of this program.',
  workingSet: 'Memory this program holds in RAM right now.',
  privateBytes: 'Memory only this program uses.',
  ioReadBytesPerSec: 'Data this program read from disk each second.',
  ioWriteBytesPerSec: 'Data this program wrote to disk each second.',
  pageFileBytes: 'Page file space this program uses.',
  virtualBytes: 'Address space this program has reserved. It is not memory in use.',
  elapsedSec: 'How long this program has been running.',
  lost: 'Windows events the recorder knows it missed.',
  unreadable: 'Windows events the recorder found but could not read.'
};
const LEVEL_MEANING: Record<string, string> = {
  level1: 'Critical events Windows wrote in each period.',
  level2: 'Error events Windows wrote in each period.',
  level3: 'Warning events Windows wrote in each period.',
  level4: 'Information events Windows wrote in each period.'
};
const UNIT_MEANING: Record<SeriesUnit, string> = {
  pct: 'A reading in percent.', bytes: 'A size.', bytesPerSec: 'A speed in bytes per second.', ms: 'A time in milliseconds.',
  sec: 'A time in seconds.', perSec: 'A count for each second.', count: 'A count.'
};

/** One plain sentence for a line. The same counter has the same name in Perfmon (see the bracket in its title). */
export function meaningOf(key: string, serverUnit: string): string {
  const parts = key.split('.');
  const field = parts[parts.length - 1];
  if (parts[0] === 'tcp' && parts[1] === 'states') return 'How many network connections are in the "' + field + '" state.';
  if (parts[0] === 'events' && own(LEVEL_MEANING, field)) return own(LEVEL_MEANING, field)!;
  return own(MEANING, field) ?? UNIT_MEANING[seriesUnit(key, serverUnit)];
}

/* ------------------------------------------------------------ quick questions */

export interface QuickQuestion { id: string; label: string; hint: string; fields: string[] }
export const QUICK_QUESTIONS: QuickQuestion[] = [
  { id: 'cpu', label: 'Is the processor busy?', hint: 'Shows processor use and the queue of work waiting for a processor.', fields: ['cpu', 'queueLength'] },
  { id: 'mem', label: 'Is memory running out?', hint: 'Shows free memory, committed memory and pages read from disk.', fields: ['memAvail', 'memCommit', 'hardFaultsPerSec'] },
  { id: 'disk', label: 'Is the disk slow?', hint: 'Shows how long the disk takes to answer and how many requests wait.', fields: ['diskMs', 'diskQueue', 'readMs', 'writeMs'] },
  { id: 'net', label: 'Is the network struggling?', hint: 'Shows data in and out, and data that had to be sent again.', fields: ['netInBps', 'netOutBps', 'retransmitsPerSec'] },
  { id: 'web', label: 'Are web requests waiting?', hint: 'Shows requests waiting or running in the web server, when it was recorded.', fields: ['currentQueueSize', 'activeRequests', 'requestsQueued'] },
  { id: 'events', label: 'Did Windows report problems?', hint: 'Shows how many critical, error and warning events Windows wrote.', fields: ['level1', 'level2', 'level3'] }
];
export const QUICK_MAX = 6;

/** The keys a quick question picks, from what was recorded: at most QUICK_MAX, in the order of its fields. Empty when none were recorded. */
export function quickKeys(q: QuickQuestion, lines: Pick<SeriesLine, 'key'>[]): string[] {
  const out: string[] = [];
  for (const f of q.fields) {
    for (const l of lines) {
      const field = l.key.split('.').pop();
      if (field === f && !out.includes(l.key)) out.push(l.key);
    }
  }
  return out.slice(0, QUICK_MAX);
}

/* ------------------------------------------------------------ latest compared */

export type Position = 'high' | 'usual' | 'low' | 'flat';
export interface Compared { last: number; avg: number; min: number; max: number; at: number; position: Position }

/**
 * Where the latest value sits between this counter's own lowest and highest in the range (0 = lowest, 1 = highest).
 * "Near its highest" is the top fifth, "near its lowest" the bottom fifth. A counter that never moved is "flat".
 */
export function compareLatest(pts: SeriesPoint[], fromMs = -Infinity, toMs = Infinity): Compared | null {
  const f = lineFigures(pts, fromMs, toMs);
  if (f.n === 0 || f.last == null || f.avg == null || f.min == null || f.max == null) return null;
  const range = f.max - f.min;
  const at = range > 0 ? Math.min(1, Math.max(0, (f.last - f.min) / range)) : 0.5;
  const position: Position = range <= 0 ? 'flat' : at >= 0.8 ? 'high' : at <= 0.2 ? 'low' : 'usual';
  return { last: f.last, avg: f.avg, min: f.min, max: f.max, at, position };
}

export const POSITION_WORDS: Record<Position, string> = {
  high: 'Near its highest', usual: 'In its usual range', low: 'Near its lowest', flat: 'Did not change'
};

/* ------------------------------------------------------------ shape line */

/** A small shape of the averages as an SVG path in a 0..w by 0..h box, at most `n` points. Empty when there is nothing to draw. */
export function shapePath(pts: SeriesPoint[], w = 80, h = 18, n = 30): string {
  if (pts.length < 2) return '';
  const step = Math.max(1, Math.ceil(pts.length / n));
  const v: number[] = [];
  for (let i = 0; i < pts.length; i += step) v.push(pts[i][1]);
  let lo = Infinity, hi = -Infinity;
  for (const x of v) { lo = Math.min(lo, x); hi = Math.max(hi, x); }
  const span = hi - lo;
  return v.map((x, i) => {
    const px = v.length === 1 ? 0 : (i / (v.length - 1)) * w;
    const py = span > 0 ? h - ((x - lo) / span) * (h - 2) - 1 : h / 2;
    return (i ? 'L' : 'M') + px.toFixed(1) + ' ' + py.toFixed(1);
  }).join('');
}
