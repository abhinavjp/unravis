/**
 * The Blackbox lanes of the Investigation timeline, with no DOM: the lane table (each lane is one figure the recorder
 * writes, named like the Windows counter it was read from), the series selectors, the turn from a case series answer
 * into a drawable lane, the "which lane moved first" clue, and the choice of lanes (at most 10, kept per browser).
 *
 * Nothing here names a cause. A figure that was not read is absent and stays absent: a line breaks, it never drops to 0.
 */
import { BBL_LANE_TEXT, BBL_TEXT } from '../copy/bbLanesCopy';
import type { SeriesItem } from '../resource';

export type LaneGroupId = 'host' | 'sql' | 'iis' | 'redis' | 'mem' | 'net' | 'log';
export type LaneUnit = 'pct' | 'ms' | 's' | 'perSec' | 'gb' | 'mb' | 'count';

export interface LaneDef {
  id: string;
  group: LaneGroupId;
  unit: LaneUnit;
  /** 'high': a high value is the worry. 'low': a low value is the worry. */
  worry: 'high' | 'low';
  /** 'value': the figure itself. 'count': how many events fell in the bucket. */
  mode: 'value' | 'count';
  /** Multiplies the stored number into the lane unit (bytes to GB, seconds to milliseconds). */
  factor: number;
  /** The recorder's own name for the figure, shown under the lane in the picker. */
  counter: string;
  /** The /series selector that reads it. Empty for a count lane: its figure is counted from the events. */
  sel: string;
  /** For a count lane: the event kind it counts. */
  eventKind?: string;
  /** In the starting set. */
  on?: true;
}

export const BB_SOURCE = 'blackbox-source';
export const MAX_LANES = 10;
const GB = 1 / (1024 * 1024 * 1024);
const MB = 1 / (1024 * 1024);

const counterSel = (object: string, counter: string, instance?: string): string =>
  'kind=counter;source=' + BB_SOURCE + ';attr.object=' + object + ';attr.counter=' + counter + (instance ? ';attr.instance=' + instance : '');
const metricSel = (metric: string): string => 'kind=metric;source=' + BB_SOURCE + ';attr.metric=' + metric;

const lane = (
  id: string, group: LaneGroupId, unit: LaneUnit, counter: string, sel: string,
  more: Partial<Pick<LaneDef, 'worry' | 'mode' | 'factor' | 'on' | 'eventKind'>> = {}
): LaneDef => ({ id, group, unit, counter, sel, worry: more.worry ?? 'high', mode: more.mode ?? 'value', factor: more.factor ?? 1, ...(more.on ? { on: true as const } : {}), ...(more.eventKind ? { eventKind: more.eventKind } : {}) });

/** The lanes, in the order the picker lists them inside a group. Counter names match what the importer writes. */
export const LANES: LaneDef[] = [
  lane('cpu', 'host', 'pct', 'Processor\\% Processor Time (_Total)', counterSel('Processor', '% Processor Time', '_Total'), { on: true }),
  lane('mem', 'host', 'gb', 'Memory\\Available Bytes', counterSel('Memory', 'Available Bytes'), { worry: 'low', factor: GB, on: true }),
  lane('disk', 'host', 'ms', 'PhysicalDisk\\Avg. Disk sec/Transfer (_Total)', counterSel('PhysicalDisk', 'Avg. Disk sec/Transfer', '_Total'), { factor: 1000, on: true }),
  lane('dq', 'host', 'count', 'PhysicalDisk\\Current Disk Queue Length (_Total)', counterSel('PhysicalDisk', 'Current Disk Queue Length', '_Total')),
  lane('free', 'host', 'pct', 'LogicalDisk\\% Free Space (recordings-volume)', counterSel('LogicalDisk', '% Free Space', 'recordings-volume'), { worry: 'low' }),
  lane('blk', 'sql', 'count', 'SQLServer:General Statistics\\Processes blocked', counterSel('SQLServer:General Statistics', 'Processes blocked'), { on: true }),
  lane('lwt', 'sql', 'ms', 'SQLServer:Locks\\Lock Wait Time (ms) (_Total)', counterSel('SQLServer:Locks', 'Lock Wait Time (ms)', '_Total')),
  lane('dl', 'sql', 'perSec', 'SQLServer:Locks\\Number of Deadlocks/sec (_Total)', counterSel('SQLServer:Locks', 'Number of Deadlocks/sec', '_Total'), { on: true }),
  lane('gp', 'sql', 'count', 'SQLServer:Memory Manager\\Memory Grants Pending', counterSel('SQLServer:Memory Manager', 'Memory Grants Pending')),
  lane('ple', 'sql', 's', 'SQLServer:Buffer Manager\\Page life expectancy', counterSel('SQLServer:Buffer Manager', 'Page life expectancy'), { worry: 'low' }),
  lane('bat', 'sql', 'perSec', 'SQLServer:SQL Statistics\\Batch Requests/sec', counterSel('SQLServer:SQL Statistics', 'Batch Requests/sec')),
  lane('hq', 'iis', 'count', 'HTTP Service Request Queues\\CurrentQueueSize', counterSel('HTTP Service Request Queues', 'CurrentQueueSize'), { on: true }),
  lane('rej', 'iis', 'perSec', 'HTTP Service Request Queues\\RejectedRequests', counterSel('HTTP Service Request Queues', 'RejectedRequests')),
  lane('wr', 'iis', 'count', 'W3SVC_W3WP\\Active Requests', counterSel('W3SVC_W3WP', 'Active Requests'), { on: true }),
  lane('ping', 'iis', 'ms', 'Blackbox IIS\\Worker Ping Latency (ms)', counterSel('Blackbox IIS', 'Worker Ping Latency (ms)')),
  lane('aq', 'iis', 'count', 'ASP.NET\\Requests Queued', counterSel('ASP.NET', 'Requests Queued')),
  lane('rmem', 'redis', 'gb', 'used_memory (Redis INFO)', metricSel('used_memory'), { factor: GB, on: true }),
  lane('rcl', 'redis', 'count', 'blocked_clients (Redis INFO)', metricSel('blocked_clients')),
  lane('rops', 'redis', 'perSec', 'instantaneous_ops_per_sec (Redis INFO)', metricSel('instantaneous_ops_per_sec')),
  lane('pgs', 'mem', 'perSec', 'Memory\\Pages/sec', counterSel('Memory', 'Pages/sec')),
  lane('pp', 'mem', 'mb', 'Memory\\Pool Paged Bytes', counterSel('Memory', 'Pool Paged Bytes'), { factor: MB }),
  lane('tcpr', 'net', 'perSec', 'Blackbox TCP\\Retransmits/sec', counterSel('Blackbox TCP', 'Retransmits/sec')),
  lane('evt', 'log', 'count', 'Windows event log entries', '', { mode: 'count', eventKind: 'win-event' }),
  lane('dmp', 'log', 'count', 'Crash dump files', '', { mode: 'count', eventKind: 'dump-file' })
];

export const GROUPS: { id: LaneGroupId; name: string; open?: true }[] = [
  { id: 'host', name: BBL_TEXT.groupHost, open: true },
  { id: 'sql', name: BBL_TEXT.groupSql, open: true },
  { id: 'iis', name: BBL_TEXT.groupIis, open: true },
  { id: 'redis', name: BBL_TEXT.groupRedis, open: true },
  { id: 'mem', name: BBL_TEXT.groupMem },
  { id: 'net', name: BBL_TEXT.groupNet },
  { id: 'log', name: BBL_TEXT.groupLog }
];

export const laneById = (id: string): LaneDef | undefined => LANES.find((l) => l.id === id);
export const laneName = (id: string): string => BBL_LANE_TEXT[id]?.name ?? id;
export const laneWhat = (id: string): string => BBL_LANE_TEXT[id]?.what ?? '';

/** The lanes everyone starts with, in order. */
export const defaultLaneIds = (): string[] => LANES.filter((l) => l.on).map((l) => l.id);

/* ---------------------------------------------------------------- the choice of lanes */

/** Adds a lane to the bottom. Refused (the same list) when 10 are shown or the id is not a lane. */
export function addLane(ids: readonly string[], id: string): string[] {
  if (ids.includes(id) || ids.length >= MAX_LANES || !laneById(id)) return [...ids];
  return [...ids, id];
}

export const removeLane = (ids: readonly string[], id: string): string[] => ids.filter((x) => x !== id);

/** The saved list when it is a list of known lane ids with no repeat and at most 10, else the starting lanes. */
export function readLaneChoice(raw: unknown): string[] {
  if (!Array.isArray(raw)) return defaultLaneIds();
  const seen = new Set<string>();
  const out: string[] = [];
  for (const v of raw) {
    if (typeof v !== 'string' || !laneById(v) || seen.has(v)) continue;
    seen.add(v);
    out.push(v);
  }
  return out.length > MAX_LANES ? out.slice(0, MAX_LANES) : out;
}

/** Whether a lane matches a search, by name, meaning or counter, case-insensitively. An empty search matches all. */
export function laneMatches(l: LaneDef, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (laneName(l.id) + ' ' + laneWhat(l.id) + ' ' + l.counter).toLowerCase().includes(q);
}

/* ---------------------------------------------------------------- a lane's figures */

export interface LanePoint { t: number; v: number }
export interface LaneView {
  def: LaneDef;
  /** One point per bucket that held a reading. A bucket with none is not here (a gap, never zero). */
  points: LanePoint[];
  /** The top of the scale: the highest value shown. Never below a tiny floor, so a flat lane draws. */
  top: number;
  /** The lowest value shown (the worry value of a low-is-bad lane). */
  low: number;
}

/**
 * The drawable lane from the server's answer for its selector. A high-is-bad lane shows the highest value of each
 * bucket, a low-is-bad lane the lowest, so a short spike is never averaged away. A count lane has no series: it counts
 * the events of its kind in each bucket, over the buckets that fall in the recorded stretches (outside a gap).
 * An item that is undefined (nothing read) gives no points.
 */
export function laneView(def: LaneDef, item: SeriesItem | undefined, events?: { times: readonly number[]; fromMs: number; toMs: number; bucketMs: number; gaps: readonly GapSpan[] }): LaneView {
  const points: LanePoint[] = [];
  if (def.mode === 'count') {
    if (events && events.bucketMs > 0) {
      const counts = new Map<number, number>();
      for (const t of events.times) {
        if (t < events.fromMs || t > events.toMs) continue;
        const slot = events.fromMs + Math.floor((t - events.fromMs) / events.bucketMs) * events.bucketMs;
        counts.set(slot, (counts.get(slot) ?? 0) + 1);
      }
      // Every bucket outside a gap has a reading of 0 or more; a bucket inside a gap has none.
      for (let t = events.fromMs; t < events.toMs; t += events.bucketMs) {
        if (events.gaps.some((g) => t >= g.from && t < g.to)) continue;
        points.push({ t, v: counts.get(t) ?? 0 });
      }
    }
  } else {
    for (const p of item?.points ?? []) {
      if (p.count <= 0) continue;
      const v = (def.worry === 'low' ? p.min : p.max) * def.factor;
      if (Number.isFinite(v)) points.push({ t: p.t, v });
    }
  }
  points.sort((a, b) => a.t - b.t);
  const values = points.map((p) => p.v);
  return { def, points, top: Math.max(1e-9, ...values), low: values.length ? Math.min(...values) : 0 };
}

/** One lane value in words, with its unit. A missing value is "not recorded", never 0. */
export function formatLaneValue(unit: LaneUnit, v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return BBL_TEXT.notRecorded;
  switch (unit) {
    case 'pct': return v.toFixed(0) + ' %';
    case 'gb': return v.toFixed(2) + ' GB';
    case 'mb': return v.toFixed(0) + ' MB';
    case 'ms': return v.toFixed(0) + ' ms';
    case 's': return v.toFixed(0) + ' s';
    case 'perSec': return (v % 1 ? v.toFixed(1) : v.toFixed(0)) + ' per second';
    default: return v % 1 ? v.toFixed(1) : v.toFixed(0);
  }
}

/** The scale line under a lane name. */
export function scaleLine(view: LaneView): string {
  if (view.points.length === 0) return BBL_TEXT.noData;
  const top = formatLaneValue(view.def.unit, view.top).replace(' per second', '/s');
  if (view.def.worry === 'low') return BBL_TEXT.scaleLow(formatLaneValue(view.def.unit, view.low).replace(' per second', '/s'), top);
  return BBL_TEXT.scale(top);
}

/* ---------------------------------------------------------------- gaps and events */

export interface GapSpan { from: number; to: number }

/** The stretches with no recording, as [from, to] in milliseconds, merged and clipped to the window. */
export function gapSpans(events: readonly { start: number; end: number }[], fromMs: number, toMs: number): GapSpan[] {
  const spans = events
    .map((e) => ({ from: Math.max(fromMs, e.start), to: Math.min(toMs, e.end) }))
    .filter((s) => s.to > s.from)
    .sort((a, b) => a.from - b.from);
  const out: GapSpan[] = [];
  for (const s of spans) {
    const last = out[out.length - 1];
    if (last && s.from <= last.to) last.to = Math.max(last.to, s.to);
    else out.push({ ...s });
  }
  return out;
}

const EVENT_TEXT: Record<string, string> = {
  'recorder-start': BBL_TEXT.evRecorderStart,
  'recording-gap': BBL_TEXT.evGap,
  'win-event': BBL_TEXT.evWin,
  'dump-file': BBL_TEXT.evDump,
  deadlock: BBL_TEXT.evDeadlock,
  'non-yielding': BBL_TEXT.evNonYield,
  'io-stall': BBL_TEXT.evIoStall,
  start: BBL_TEXT.evSqlStart,
  stop: BBL_TEXT.evSqlStop,
  error: BBL_TEXT.evSqlError
};

/** The event kinds the recorder writes that the timeline marks. */
export const EVENT_KINDS = Object.keys(EVENT_TEXT);

/** The sentence for an event kind, or '' for a kind the timeline does not mark. */
export const eventText = (kind: string): string => EVENT_TEXT[kind] ?? '';

/* ---------------------------------------------------------------- which lane moved first */

export interface FirstMove { id: string; t: number }

/**
 * The lanes that left their own normal range, earliest first. Normal is the first third of the window: its mean and
 * spread. A lane has moved at the first later reading that is more than 4 spreads and 10 % of the lane's range away.
 * A lane with fewer than 6 readings in that first third has no normal, so it is not judged. This is a clue to read
 * the chart with, not a finding.
 */
export function firstToChange(views: readonly LaneView[], fromMs: number, toMs: number): FirstMove[] {
  const edge = fromMs + (toMs - fromMs) / 3;
  const out: FirstMove[] = [];
  for (const view of views) {
    const base = view.points.filter((p) => p.t < edge).map((p) => p.v);
    if (base.length < 6) continue;
    const mean = base.reduce((a, b) => a + b, 0) / base.length;
    const sd = Math.sqrt(base.reduce((a, b) => a + (b - mean) ** 2, 0) / base.length) || view.top * 0.05;
    const hit = view.points.find((p) => p.t >= edge && Math.abs(p.v - mean) > 4 * sd + view.top * 0.1);
    if (hit) out.push({ id: view.def.id, t: hit.t });
  }
  return out.sort((a, b) => a.t - b.t);
}
