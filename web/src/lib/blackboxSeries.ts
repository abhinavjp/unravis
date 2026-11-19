/**
 * The Blackbox charts view, with no DOM: the shape check for POST /api/blackbox/{id}/series, plain names and units for every
 * measurement the recorder writes, value formats, gap breaks, per-line figures and the window presets. The recorder reads the
 * same Windows counters as Perfmon (PDH), so these charts show the same numbers Perfmon would, downsampled by the server.
 * A value that was not read is absent in the answer and stays absent here: a line breaks, it never drops to 0.
 */

export type SeriesUnit = 'pct' | 'bytes' | 'bytesPerSec' | 'ms' | 'sec' | 'perSec' | 'count';
/** [bucket start in ms, average, lowest, highest]. */
export type SeriesPoint = [number, number, number, number];

export interface SeriesLine { key: string; group: string; label: string; unit: string; points: SeriesPoint[] }
/** partial: samples whose reading covered only part of what it sums (some network adapters, for example). Absent from an older server. */
export interface SeriesGroupCover { group: string; read: number; missing: number; slow: number; busy: number; partial?: number }
export interface SeriesProc { name: string; peakCpuPct: number; cpu: SeriesPoint[]; workingSet: SeriesPoint[]; privateBytes: SeriesPoint[] }
export interface SeriesGap { from: string; to: string; seconds: number; cause: string }
export interface SeriesMarker { t: string; kind: string; detail?: string }
export interface SeriesEventId { channel: number; eventId: number; level: number; count: number }
export interface SeriesDump { name: string; size: number; mtime: string; target?: string }
export interface SeriesAnswer {
  start: string; end: string; bucketSeconds: number; intervalSeconds: number; samples: number; first?: string; last?: string;
  lines: SeriesLine[]; groups: SeriesGroupCover[]; processes: SeriesProc[]; eventIds: SeriesEventId[]; dumps: SeriesDump[];
  gaps: SeriesGap[]; markers: SeriesMarker[]; truncated?: boolean;
  read: { files: number; samples: number; torn: number; dropped: string[] | null; missing: string[] | null };
}
export interface SeriesRequest { start: string; end: string; points?: number }

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const isArr = Array.isArray;
const isPoint = (p: unknown): p is SeriesPoint => isArr(p) && p.length === 4 && p.every((x) => typeof x === 'number' && Number.isFinite(x));
const points = (v: unknown): SeriesPoint[] | null => (isArr(v) && v.every(isPoint) ? (v as SeriesPoint[]) : null);

/** The answer when it has the documented shape, else null (the view then says the answer could not be read, never draws a blank chart). */
export function readSeriesAnswer(v: unknown): SeriesAnswer | null {
  if (!isObj(v)) return null;
  if (typeof v.start !== 'string' || typeof v.end !== 'string' || typeof v.bucketSeconds !== 'number' || typeof v.samples !== 'number') return null;
  if (!isArr(v.lines) || !isArr(v.groups) || !isArr(v.gaps)) return null;
  for (const l of v.lines) {
    if (!isObj(l) || typeof l.key !== 'string' || typeof l.unit !== 'string' || !points(l.points)) return null;
  }
  for (const p of (isArr(v.processes) ? v.processes : [])) {
    if (!isObj(p) || typeof p.name !== 'string' || !points(p.cpu) || !points(p.workingSet) || !points(p.privateBytes)) return null;
  }
  const read = isObj(v.read) ? v.read : {};
  return {
    start: v.start, end: v.end, bucketSeconds: v.bucketSeconds, intervalSeconds: typeof v.intervalSeconds === 'number' ? v.intervalSeconds : 0,
    samples: v.samples, first: typeof v.first === 'string' ? v.first : undefined, last: typeof v.last === 'string' ? v.last : undefined,
    lines: v.lines as SeriesLine[], groups: v.groups as SeriesGroupCover[], processes: (isArr(v.processes) ? v.processes : []) as SeriesProc[],
    eventIds: (isArr(v.eventIds) ? v.eventIds : []) as SeriesEventId[], dumps: (isArr(v.dumps) ? v.dumps : []) as SeriesDump[],
    gaps: v.gaps as SeriesGap[], markers: (isArr(v.markers) ? v.markers : []) as SeriesMarker[], truncated: v.truncated === true,
    read: {
      files: typeof read.files === 'number' ? read.files : 0, samples: typeof read.samples === 'number' ? read.samples : 0,
      torn: typeof read.torn === 'number' ? read.torn : 0,
      dropped: isArr(read.dropped) ? (read.dropped as string[]) : null, missing: isArr(read.missing) ? (read.missing as string[]) : null
    }
  };
}

/* ------------------------------------------------------------------ names */

/** The group titles, in the order the picker lists them. "host" is the machine-wide base set every sample carries. */
export const GROUP_TITLES: Record<string, string> = {
  host: 'Machine', diskCpu: 'Disks and processor detail', memPressure: 'Memory pressure', vm: 'Virtual machine', tcp: 'Network (TCP)',
  http: 'HTTP.sys request queues', iis: 'IIS worker processes', aspnet: 'ASP.NET', clr: '.NET runtime', proc10: 'Watched programs',
  procIO: 'Watched programs: disk and memory', sqlHost: 'SQL Server', events: 'Windows events'
};
export const GROUP_ORDER = Object.keys(GROUP_TITLES);

/**
 * Plain names by the measurement's own field (the last part of the key). Where the field is a Perfmon counter, the counter's
 * Perfmon name is in brackets so the owner can find the same line in Perfmon.
 */
const FIELD_NAMES: Record<string, string> = {
  cpu: 'Processor busy (% Processor Time)', memAvail: 'Memory available (Available Bytes)', memCommit: 'Memory committed (Committed Bytes)',
  diskQueue: 'Disk queue (Current Disk Queue Length)', diskMs: 'Disk time per transfer (Avg. Disk sec/Transfer)',
  diskFreePct: 'Free space on the recordings drive', netInBps: 'Network received (Bytes Received/sec)', netOutBps: 'Network sent (Bytes Sent/sec)',
  currentQueueSize: 'Requests waiting in queue (CurrentQueueSize)', maxQueueItemAge: 'Oldest waiting request (MaxQueueItemAge)',
  rejectedRequests: 'Requests turned away (RejectedRequests)', arrivalRate: 'Requests arriving (ArrivalRate)',
  activeRequests: 'Requests running (Active Requests)', activeThreads: 'Threads busy (Active Threads Count)', pingLatencyMs: 'Health ping time (Ping Response Latency)',
  readMs: 'Read time (Avg. Disk sec/Read)', writeMs: 'Write time (Avg. Disk sec/Write)', readsPerSec: 'Reads (Disk Reads/sec)',
  writesPerSec: 'Writes (Disk Writes/sec)', splitIOPerSec: 'Split reads and writes (Split IO/Sec)', queueLength: 'Processor queue (Processor Queue Length)',
  privilegedPct: 'Windows itself (% Privileged Time)', dpcPct: 'Driver work (% DPC Time)', interruptPct: 'Hardware interrupts (% Interrupt Time)',
  utilityPct: 'Processor utility (% Processor Utility)', contextSwitchesPerSec: 'Context switches (Context Switches/sec)',
  batchRequestsPerSec: 'Batches (Batch Requests/sec)', userConnections: 'Connections (User Connections)', lockWaitsPerSec: 'Lock waits (Lock Waits/sec)',
  processesBlocked: 'Blocked sessions (Processes blocked)', memoryGrantsPending: 'Waiting for query memory (Memory Grants Pending)',
  pageLifeExpectancy: 'Page life expectancy', freeListStallsPerSec: 'Free list stalls (Free list stalls/sec)',
  compilationsPerSec: 'Compilations (SQL Compilations/sec)', reCompilationsPerSec: 'Recompilations (SQL Re-Compilations/sec)',
  deadlocksPerSec: 'Deadlocks (Number of Deadlocks/sec)', lockWaitTimeMs: 'Lock wait time (Lock Wait Time (ms))',
  logFlushWaitTimeMs: 'Log flush wait time (Log Flush Wait Time)', forwardedRecordsPerSec: 'Forwarded records (Forwarded Records/sec)',
  gen0Bytes: 'Gen 0 heap size', gen1Bytes: 'Gen 1 heap size', gen2Bytes: 'Gen 2 heap size', lohBytes: 'Large object heap size',
  pctTimeInGC: 'Time in garbage collection (% Time in GC)', allocBytesPerSec: 'Memory allocated (Allocated Bytes/sec)',
  contentionPerSec: 'Lock contention (Contention Rate / sec)', threads: 'Threads', exceptionsPerSec: 'Exceptions thrown (# of Exceps Thrown / sec)',
  cpuPct: 'Processor busy', workingSet: 'Memory in use (Working Set)', privateBytes: 'Private memory (Private Bytes)',
  ioReadBytesPerSec: 'Disk read (IO Read Bytes/sec)', ioWriteBytesPerSec: 'Disk write (IO Write Bytes/sec)', pageFileBytes: 'Page file use (Page File Bytes)',
  virtualBytes: 'Virtual memory (Virtual Bytes)', elapsedSec: 'Time since start',
  retransmitsPerSec: 'Retransmits (Segments Retransmitted/sec)', resetsPerSec: 'Resets (Connections Reset)', outboundQueue: 'Network send queue (Output Queue Length)',
  outboundQueueInterfaces: 'Network adapters counted', discardedInterfaces: 'Network adapters counted (packets dropped)',
  outErrorsInterfaces: 'Network adapters counted (send errors)', connFailuresPerSec: 'Failed connections (Connection Failures)',
  discardedPerSec: 'Packets dropped (Packets Received Discarded)', outErrorsPerSec: 'Send errors (Packets Outbound Errors)',
  requestsQueued: 'Requests queued (Requests Queued)', requestsCurrent: 'Requests now (Requests Current)', requestsRejected: 'Requests rejected (Requests Rejected)',
  requestWaitTimeMs: 'Request wait time (Request Wait Time)', applicationRestarts: 'App restarts (Application Restarts)',
  hardFaultsPerSec: 'Pages read from disk (Pages Input/sec)', poolPagedBytes: 'Kernel paged pool (Pool Paged Bytes)',
  poolNonPagedBytes: 'Kernel non-paged pool (Pool Nonpaged Bytes)', pageFileUsagePct: 'Page file used (% Usage)', pagesPerSec: 'Paging (Pages/sec)',
  pageFaultsPerSec: 'Page faults (Page Faults/sec)', standbyBytes: 'Standby memory (Standby Cache Bytes)', modifiedBytes: 'Modified memory (Modified Page List Bytes)',
  freeBytes: 'Free memory (Free & Zero Page List Bytes)', stealPct: 'Time taken by the host (steal)', balloonBytes: 'Memory taken back by the host (balloon)',
  lost: 'Events not read in time', unreadable: 'Events that could not be read'
};
const EVENT_LEVELS: Record<string, string> = { '1': 'Critical', '2': 'Error', '3': 'Warning', '4': 'Information' };

/** "diskCpu.C:.readMs" → { field: "readMs", where: "C:" }. "tcp.states.time-wait" → TCP state. */
function split(key: string): { group: string; where: string; field: string } {
  const parts = key.split('.');
  if (parts.length === 1) return { group: 'host', where: '', field: parts[0] };
  const group = parts[0];
  const field = parts[parts.length - 1];
  const where = parts.slice(1, -1).join('.');
  return { group, where, field };
}

/** The plain name of a measurement and, for a named thing (pool, instance, disk, program), whose it is. */
export function seriesName(key: string): { name: string; where: string; group: string } {
  const { group, where, field } = split(key);
  if (group === 'tcp' && where === 'states') return { name: 'Connections in state ' + field, where: '', group };
  if (group === 'events' && /^level\d$/.test(field)) return { name: (EVENT_LEVELS[field.slice(5)] ?? 'Level ' + field.slice(5)) + ' events', where: '', group };
  if (group === 'diskCpu' && where === 'cpu') return { name: FIELD_NAMES[field] ?? field, where: '', group };
  return { name: FIELD_NAMES[field] ?? field, where, group };
}
export const seriesTitle = (key: string): string => {
  const n = seriesName(key);
  return n.where ? n.name + ': ' + n.where : n.name;
};

/** The unit to draw. The server guesses from the field name; a few fields it cannot guess are fixed here. */
export function seriesUnit(key: string, serverUnit: string): SeriesUnit {
  const field = split(key).field;
  if (field === 'pctTimeInGC') return 'pct';
  if (field === 'maxQueueItemAge') return 'ms';
  if (field === 'pageLifeExpectancy' || field === 'elapsedSec') return 'sec';
  return (['pct', 'bytes', 'bytesPerSec', 'ms', 'sec', 'perSec', 'count'] as const).find((u) => u === serverUnit) ?? 'count';
}

export const UNIT_TITLES: Record<SeriesUnit, string> = {
  pct: 'Percent', bytes: 'Bytes', bytesPerSec: 'Bytes per second', ms: 'Milliseconds', sec: 'Seconds', perSec: 'Per second', count: 'Count'
};

/* ----------------------------------------------------------------- values */

const trim = (n: number, digits: number) => Number(n.toFixed(digits)).toLocaleString('en-US');
const scaled = (n: number, base: number, names: string[]) => {
  let i = 0;
  let v = Math.abs(n);
  while (v >= base && i < names.length - 1) { v /= base; i++; }
  const sign = n < 0 ? '-' : '';
  return sign + trim(v, v < 10 && i > 0 ? 2 : v < 100 && i > 0 ? 1 : i === 0 && v < 10 ? 2 : 0) + ' ' + names[i];
};
const plain = (v: number) => (Math.abs(v) >= 1000 ? trim(v, 0) : Math.abs(v) >= 10 ? trim(v, 1) : trim(v, 2));

/** One value as text, with its unit. */
export function formatValue(unit: SeriesUnit, v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return 'not read';
  switch (unit) {
    case 'pct': return plain(v) + ' %';
    case 'bytes': return scaled(v, 1024, ['B', 'KB', 'MB', 'GB', 'TB']);
    case 'bytesPerSec': return scaled(v, 1024, ['B/s', 'KB/s', 'MB/s', 'GB/s']);
    case 'ms': return Math.abs(v) >= 1000 ? trim(v / 1000, 2) + ' s' : plain(v) + ' ms';
    case 'sec': return Math.abs(v) >= 3600 ? trim(v / 3600, 1) + ' h' : Math.abs(v) >= 60 ? trim(v / 60, 1) + ' min' : plain(v) + ' s';
    case 'perSec': return plain(v) + ' /s';
    default: return plain(v);
  }
}

/** A short axis label (no long decimals). */
export function axisValue(unit: SeriesUnit, v: number): string {
  if (!Number.isFinite(v)) return '';
  if (unit === 'bytes' || unit === 'bytesPerSec' || unit === 'ms' || unit === 'sec') return formatValue(unit, v);
  const a = Math.abs(v);
  const s = a >= 1e9 ? trim(v / 1e9, 1) + 'G' : a >= 1e6 ? trim(v / 1e6, 1) + 'M' : a >= 1e4 ? trim(v / 1e3, 0) + 'k' : a >= 10 || a === 0 ? trim(v, 0) : trim(v, 2);
  return unit === 'pct' ? s + ' %' : unit === 'perSec' ? s + ' /s' : s;
}

/* ------------------------------------------------------------- lines */

/**
 * The points as chart data: [t, value], with a null where a real gap (one the server reports: a recorder stop, or a hole the
 * samples show at the recorder's own pace) falls between two points, so the line breaks only where the recorder had no reading.
 * Spacing alone is not used: the recorder slows down when the PC is busy, and that busy hour must still draw as a line.
 * On a log scale a value at or below 0 cannot be drawn; it becomes a break too and is counted.
 */
export function chartData(pts: SeriesPoint[], gaps: Array<{ fromMs: number; toMs: number }>, log: boolean, pick: 1 | 2 | 3 = 1): { data: Array<[number, number | null]>; hidden: number } {
  const out: Array<[number, number | null]> = [];
  let hidden = 0;
  let prev: number | null = null;
  for (const p of pts) {
    if (prev != null) {
      const a = prev;
      const g = gaps.find((x) => x.toMs > a && x.fromMs < p[0]);
      if (g) out.push([Math.max(a + 1, Math.min(p[0] - 1, Math.round((Math.max(a, g.fromMs) + Math.min(p[0], g.toMs)) / 2))), null]);
    }
    const v = p[pick];
    if (log && !(v > 0)) { hidden++; out.push([p[0], null]); } else out.push([p[0], v]);
    prev = p[0];
  }
  return { data: out, hidden };
}

/** The server's gaps as millisecond ranges, for chartData. A gap with a time that does not parse is left out. */
export function gapRanges(gaps: SeriesGap[]): Array<{ fromMs: number; toMs: number }> {
  return gaps.map((g) => ({ fromMs: Date.parse(g.from), toMs: Date.parse(g.to) })).filter((g) => Number.isFinite(g.fromMs) && Number.isFinite(g.toMs) && g.toMs > g.fromMs);
}

/** Last, average, lowest and highest over the shown points (Perfmon's bar under the chart). The average weighs every bucket the same. */
export function lineFigures(pts: SeriesPoint[], fromMs = -Infinity, toMs = Infinity): { last: number | null; avg: number | null; min: number | null; max: number | null; n: number } {
  let sum = 0, n = 0, min = Infinity, max = -Infinity, last: number | null = null;
  for (const p of pts) {
    if (p[0] < fromMs || p[0] > toMs) continue;
    sum += p[1]; n++;
    min = Math.min(min, p[2]); max = Math.max(max, p[3]);
    last = p[1];
  }
  return n ? { last, avg: sum / n, min, max, n } : { last: null, avg: null, min: null, max: null, n: 0 };
}

/** The lines the view starts with: the four Perfmon starts with for a slow server, in this order, when they were recorded. */
export const DEFAULT_KEYS = ['cpu', 'memAvail', 'diskMs', 'diskQueue'];
export function defaultSelection(lines: Pick<SeriesLine, 'key'>[]): string[] {
  const have = new Set(lines.map((l) => l.key));
  const picked = DEFAULT_KEYS.filter((k) => have.has(k));
  return picked.length ? picked : lines.slice(0, 1).map((l) => l.key);
}

/** The lines grouped for the picker, in GROUP_ORDER, each group sorted by its plain title. */
export function pickerGroups(lines: SeriesLine[]): Array<{ group: string; title: string; lines: SeriesLine[] }> {
  const by = new Map<string, SeriesLine[]>();
  for (const l of lines) {
    const g = l.group || 'host';
    if (!by.has(g)) by.set(g, []);
    by.get(g)!.push(l);
  }
  const order = [...GROUP_ORDER, ...[...by.keys()].filter((g) => !GROUP_ORDER.includes(g)).sort()];
  return order.filter((g) => by.has(g)).map((g) => ({
    group: g, title: GROUP_TITLES[g] ?? g,
    lines: by.get(g)!.slice().sort((a, b) => seriesTitle(a.key).localeCompare(seriesTitle(b.key)))
  }));
}

/** The units of the picked lines, in first-picked order: one chart strip per unit, so no two scales share an axis. */
export function unitStrips(keys: string[], lines: SeriesLine[]): Array<{ unit: SeriesUnit; keys: string[] }> {
  const byKey = new Map(lines.map((l) => [l.key, l]));
  const strips: Array<{ unit: SeriesUnit; keys: string[] }> = [];
  for (const k of keys) {
    const l = byKey.get(k);
    if (!l) continue;
    const u = seriesUnit(l.key, l.unit);
    let s = strips.find((x) => x.unit === u);
    if (!s) { s = { unit: u, keys: [] }; strips.push(s); }
    s.keys.push(k);
  }
  return strips;
}

/** A group with samples but no readings in the window: why, in plain words. Null when it was read at least once or never asked for. */
export function coverNote(c: SeriesGroupCover): string | null {
  if (c.read > 0) {
    const p = c.partial ?? 0;
    if (p <= 0) return null;
    return p === c.read ? 'every reading covered only part of what it adds up (for example some network adapters), so totals are low'
      : p + ' of ' + c.read + ' readings covered only part of what they add up (for example some network adapters), so those totals are low';
  }
  if (c.slow > 0) return 'skipped: the reading took too long, so the recorder left it out to stay light';
  if (c.busy > 0) return 'skipped: the last reading was still running';
  if (c.missing > 0) return 'could not be read on this server';
  return null;
}

/* ------------------------------------------------------------- windows */

export type WindowPreset = '15m' | '1h' | '6h' | '24h' | '7d';
export const PRESET_MS: Record<WindowPreset, number> = { '15m': 15 * 60_000, '1h': 3_600_000, '6h': 6 * 3_600_000, '24h': 24 * 3_600_000, '7d': 7 * 24 * 3_600_000 };
export const PRESET_LABELS: Record<WindowPreset, string> = { '15m': 'Last 15 min', '1h': 'Last hour', '6h': 'Last 6 hours', '24h': 'Last 24 hours', '7d': 'Last 7 days' };
/** The server reads at most 7 days at once. */
export const MAX_SPAN_MS = PRESET_MS['7d'];

/**
 * A preset window ending at the last recorded sample (not now: a stopped Blackbox still shows its last hours), never starting
 * before the first sample. Null when nothing was recorded.
 */
export function presetWindow(preset: WindowPreset, firstSample: string | undefined, lastSample: string | undefined): { startMs: number; endMs: number } | null {
  const last = Date.parse(lastSample ?? '');
  if (!Number.isFinite(last)) return null;
  const first = Date.parse(firstSample ?? '');
  const endMs = last + 1000;
  let startMs = endMs - PRESET_MS[preset];
  if (Number.isFinite(first) && startMs < first) startMs = first;
  if (endMs - startMs < 60_000) startMs = endMs - 60_000;
  return { startMs, endMs };
}

/** The smallest preset that holds the whole recording, so the first view shows all of a short one. */
export function firstPreset(firstSample: string | undefined, lastSample: string | undefined): WindowPreset {
  const span = Date.parse(lastSample ?? '') - Date.parse(firstSample ?? '');
  if (!Number.isFinite(span)) return '1h';
  return (Object.keys(PRESET_MS) as WindowPreset[]).find((p) => PRESET_MS[p] >= span) ?? '7d';
}

/**
 * The request for a window: the bucket is a whole number of recorder intervals (so each point averages the same number of
 * readings and the line does not wobble), the window is stretched back to a whole number of buckets, and at most `max` points.
 */
export function alignedRequest(startMs: number, endMs: number, intervalSeconds: number, max = 600): { startMs: number; endMs: number; points: number; bucketSeconds: number } {
  const iv = Math.max(1, Math.round(intervalSeconds || 15));
  const spanS = Math.max(1, (endMs - startMs) / 1000);
  const bucketSeconds = iv * Math.max(1, Math.ceil(spanS / max / iv));
  const step = bucketSeconds * 1000;
  const alignedEnd = Math.ceil(endMs / step) * step;
  const points = Math.min(max, Math.floor(MAX_SPAN_MS / step), Math.max(1, Math.ceil((alignedEnd - startMs) / step)));
  return { startMs: alignedEnd - points * step, endMs: alignedEnd, points, bucketSeconds };
}
