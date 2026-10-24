/**
 * The resource panel's logic, with no DOM: the shape checks for the two case routes it reads (/resource and /series), the selectors it asks
 * for, the zoom window and bucket arithmetic, the strips it draws and the chips it shows. Everything here is pure so a test can drive it.
 *
 * What the panel never does: say why something happened. It shows what was measured, where, and which rule rows the server fired.
 */
import type { Palette } from './charts';
import { chartTime } from './chartTime';

// ---------------------------------------------------------------------------------------------------------------------------------
// Fixed words. Every string the panel shows that is not data lives here, so the copy lint can check all of it.

export const resourceCopy = {
  heading: 'Resource counters',
  intro: 'What each host measured around the stalls: CPU, memory, queues and logs. Open a host to see the lines.',
  loading: 'Reading the resource counters.',
  loadingLines: 'Reading the lines for this window.',
  retry: 'Try again',
  open: 'Show lines',
  close: 'Hide lines',
  openLabel: 'Show the resource lines for',
  closeLabel: 'Hide the resource lines for',
  hostLabel: 'Host',
  noSummary: 'No counters were read for this host in this window.',
  coverageLabel: 'Data present',
  coverageNone: 'No data',
  noData: 'No resource lines were found for this host in this window.',
  noProcesses: 'No per-process lines are available for this host.',
  processesHeading: 'Busiest processes',
  processMetricLabel: 'Process measure',
  otherProcesses: 'All other processes',
  apiStrip: 'API P95',
  partial: 'Some data was not read in time, so the lines may be incomplete.',
  gapsHeading: 'Gaps',
  markersHeading: 'Start and stop markers',
  noMarkers: 'No start or stop markers.',
  episodesHeading: 'Stall periods',
  noEpisodes: 'No stall periods in this window.',
  episodeOpen: 'Show rows',
  chipsHeading: 'Rules that fired',
  chipsNone: 'No rule fired in this window.',
  chipHintOpen: 'Open to see the wording and the evidence.',
  drawerHeading: 'Rule rows',
  drawerNoRows: 'No rule rows at this time.',
  drawerClose: 'Close the rule rows',
  wordingLabel: 'Wording',
  thresholdsLabel: 'Limits used',
  evidenceLabel: 'Evidence',
  noEvidence: 'No evidence ids.',
  windowLabel: 'Window',
  whenLabel: 'During',
  anyTime: 'This rule is not tied to one stall period.',
  zoomIn: 'Zoom in',
  zoomOut: 'Zoom out',
  earlier: 'Earlier',
  later: 'Later',
  resetZoom: 'Whole period',
  brushHint: 'Drag across the lines to zoom to that time. Click a time to see the rule rows around it.',
  windowNote: 'Lines read in steps of',
  emptyWindow: 'No resource data in this window.',
  chartLabel: 'Resource lines for'
} as const;

/** Names for the rule ids the server sends. A hint says in plain words what the rule looks at; it never says why. */
export const RULE_HINTS: Record<string, string> = {
  RC1: 'The processes with the highest CPU or memory in this window.',
  RC2: 'A memory number that rose steadily in this window.',
  RC3: 'Host CPU, free memory or disk time went past a set limit during a stall.',
  RC4: 'Garbage collection time or pauses were high during a stall.',
  RC5: 'Queue counters or log lines showed work lined up during a stall.',
  RC6: 'Redis events overlapped a stall.',
  RC7: 'Warning and error log entries per minute, next to calm periods.',
  RC8: 'A restart or a start-up came shortly before a stall.',
  RC9: 'Mongo calls that were slow or still running during a stall.'
};
export const FALLBACK_HINT = 'A rule result for this host. Open it to see the evidence.';
export const ruleHint = (id: string): string => (Object.prototype.hasOwnProperty.call(RULE_HINTS, id) ? RULE_HINTS[id] : FALLBACK_HINT);

/** Plain names for catalogue metric ids, for the coverage dots. An id the panel does not know is shown as sent. */
export const METRIC_NAMES: Record<string, string> = {
  'host.cpu': 'Host CPU',
  'host.mem.available': 'Free memory',
  'host.mem.committed': 'Committed memory',
  'host.pages': 'Memory pages per second',
  'host.disk.sec_per_transfer': 'Disk time per transfer',
  'host.disk.queue': 'Disk queue',
  'host.net.bytes': 'Network bytes',
  'proc.cpu': 'Process CPU',
  'proc.ws': 'Process working set',
  'proc.private': 'Process private bytes',
  'proc.handles': 'Process handles',
  'proc.threads': 'Process threads',
  'clr.gen2': 'Gen 2 collections',
  'clr.gc_time': 'Time in garbage collection',
  'aspnet.queued': 'ASP.NET requests queued',
  'aspnet.current': 'ASP.NET requests current',
  'aspnet.wait_ms': 'ASP.NET request wait',
  'iis.wp.active': 'IIS active requests',
  'net.tp.queue': 'Thread pool queue',
  'net.tp.threads': 'Thread pool threads',
  'net.gc_pause': 'Garbage collection pause',
  'kestrel.queued': 'Kestrel queue',
  'redis.mem': 'Redis memory',
  'redis.clients': 'Redis clients',
  'redis.blocked': 'Redis blocked clients',
  'redis.ops': 'Redis operations per second',
  'mongo.queue': 'Mongo queue',
  'mongo.conns': 'Mongo connections',
  'mongo.wt.dirty': 'Mongo dirty cache'
};
export const metricName = (id: string): string => (Object.prototype.hasOwnProperty.call(METRIC_NAMES, id) ? METRIC_NAMES[id] : id);

/** The process measures the toggle offers, in plan order. */
export const PROC_METRICS = [
  { id: 'proc.cpu', label: 'CPU' },
  { id: 'proc.ws', label: 'Working set' },
  { id: 'proc.private', label: 'Private bytes' },
  { id: 'proc.handles', label: 'Handles' },
  { id: 'proc.threads', label: 'Threads' }
] as const;
export type ProcMetricId = (typeof PROC_METRICS)[number]['id'];
export const TOP_PROCESSES = 5;

// ---------------------------------------------------------------------------------------------------------------------------------
// Escaping. Chart tooltips are HTML strings, so every name and value goes through this.

export const escapeHTML = (value: string): string =>
  String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char] ?? char);

// ---------------------------------------------------------------------------------------------------------------------------------
// Response shapes and their checks. A check returns null for an answer that is not the documented shape; the caller turns that into
// "the server sent an answer that could not be read". Arrays are never null in the documented shape, so a null array is refused.

export interface CoverageItem { metric: string; fraction: number }
/** One process the server ranked for a host. sel maps a process measure id to the opaque /series selector that reads it. */
export interface ProcessRef { name: string; sel: Partial<Record<ProcMetricId, string>> }
export interface ResourceHost { host: string; summary: string[]; coverage: CoverageItem[]; processes: ProcessRef[] }
export interface EpisodeSpan { start: number; end: number }
export interface RuleRow {
  id: string; host: string; title: string; wording: string; label: string; thresholds: string[]; evidence: string[]; episode: EpisodeSpan | null;
}
export interface Marker { kind: string; edge: string; host: string; t: number; text: string }
export interface ResourceView {
  fromMs: number; toMs: number; partial: boolean; hosts: ResourceHost[]; rules: RuleRow[]; gaps: string[]; markers: Marker[]; episodes: EpisodeSpan[];
}

const isObject = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const isText = (value: unknown): value is string => typeof value === 'string';
const isNumber = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const stringList = (value: unknown): string[] | null => (Array.isArray(value) && value.every(isText) ? (value as string[]) : null);
const timeMs = (value: unknown): number | null => {
  if (!isText(value)) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
};

function parseSpan(value: unknown): EpisodeSpan | null {
  if (!isObject(value)) return null;
  const start = timeMs(value.start), end = timeMs(value.end);
  return start != null && end != null && end >= start ? { start, end } : null;
}

/** thresholds may arrive as a list of sentences, one sentence, or a small object of name and value; each becomes plain lines. */
function parseThresholds(value: unknown): string[] | null {
  if (value == null) return [];
  if (isText(value)) return value ? [value] : [];
  if (Array.isArray(value)) return value.every(isText) ? (value as string[]) : null;
  if (isObject(value)) return Object.entries(value).map(([key, item]) => key + ': ' + (isText(item) || isNumber(item) ? String(item) : JSON.stringify(item)));
  return null;
}

function parseHost(value: unknown): ResourceHost | null {
  if (!isObject(value) || !isText(value.host)) return null;
  const summary = stringList(value.summary);
  if (!summary || !Array.isArray(value.coverage)) return null;
  const coverage: CoverageItem[] = [];
  for (const item of value.coverage) {
    if (!isObject(item) || !isText(item.metric) || !isNumber(item.fraction)) return null;
    coverage.push({ metric: item.metric, fraction: Math.min(1, Math.max(0, item.fraction)) });
  }
  // processes is the one field this panel adds to the contract. It is optional, and an entry that is not usable is dropped, not fatal.
  const processes: ProcessRef[] = [];
  if (Array.isArray(value.processes)) {
    for (const item of value.processes) {
      if (!isObject(item) || !isText(item.name) || !isObject(item.sel)) continue;
      const sel: Partial<Record<ProcMetricId, string>> = {};
      for (const metric of PROC_METRICS) { const text = item.sel[metric.id]; if (isText(text) && text) sel[metric.id] = text; }
      if (Object.keys(sel).length > 0) processes.push({ name: item.name, sel });
    }
  }
  return { host: value.host, summary, coverage, processes };
}

function parseRule(value: unknown): RuleRow | null {
  if (!isObject(value) || !isText(value.id) || !isText(value.host) || !isText(value.title) || !isText(value.wording) || !isText(value.label)) return null;
  const evidence = stringList(value.evidence);
  const thresholds = parseThresholds(value.thresholds);
  if (!evidence || !thresholds) return null;
  let episode: EpisodeSpan | null = null;
  if (value.episode != null) { episode = parseSpan(value.episode); if (!episode) return null; }
  return { id: value.id, host: value.host, title: value.title, wording: value.wording, label: value.label, thresholds, evidence, episode };
}

function parseMarker(value: unknown): Marker | null {
  if (!isObject(value) || !isText(value.kind) || !isText(value.edge) || !isText(value.host) || !isText(value.text)) return null;
  const t = timeMs(value.t);
  return t == null ? null : { kind: value.kind, edge: value.edge, host: value.host, t, text: value.text };
}

function parseList<T>(value: unknown, parse: (item: unknown) => T | null): T[] | null {
  if (!Array.isArray(value)) return null;
  const out: T[] = [];
  for (const item of value) { const parsed = parse(item); if (parsed === null) return null; out.push(parsed); }
  return out;
}

/** GET /api/cases/{id}/resource. Null when the answer is not the documented shape. */
export function parseResource(value: unknown): ResourceView | null {
  if (!isObject(value) || typeof value.partial !== 'boolean') return null;
  const fromMs = timeMs(value.from), toMs = timeMs(value.to);
  const gaps = stringList(value.gaps);
  const hosts = parseList(value.hosts, parseHost), rules = parseList(value.rules, parseRule);
  const markers = parseList(value.markers, parseMarker), episodes = parseList(value.episodes, parseSpan);
  if (fromMs == null || toMs == null || !gaps || !hosts || !rules || !markers || !episodes) return null;
  return { fromMs, toMs, partial: value.partial, hosts, rules, gaps, markers, episodes };
}

export interface SeriesPoint { t: number; min: number; mean: number; max: number; count: number }
export interface SeriesItem { sel: string; unit: string; unitMixed: boolean; count: number; points: SeriesPoint[] }
export interface SeriesView { fromMs: number; toMs: number; bucketMs: number; buckets: number; partial: boolean; scanned: number; series: SeriesItem[] }

function parsePoint(value: unknown): SeriesPoint | null {
  if (!isObject(value) || !isNumber(value.t) || !isNumber(value.min) || !isNumber(value.mean) || !isNumber(value.max) || !isNumber(value.count)) return null;
  return { t: value.t, min: value.min, mean: value.mean, max: value.max, count: value.count };
}
function parseSeriesItem(value: unknown): SeriesItem | null {
  if (!isObject(value) || !isText(value.sel) || !isNumber(value.count)) return null;
  // A series with nothing in it may arrive with null points; that is the same as none.
  const points = value.points == null ? [] : parseList(value.points, parsePoint);
  if (!points) return null;
  return { sel: value.sel, unit: isText(value.unit) ? value.unit : '', unitMixed: value.unitMixed === true, count: value.count, points };
}

/** GET /api/cases/{id}/series. Null when the answer is not the documented shape. */
export function parseSeries(value: unknown): SeriesView | null {
  if (!isObject(value) || typeof value.partial !== 'boolean' || !isNumber(value.bucketMs) || !isNumber(value.buckets)) return null;
  const fromMs = timeMs(value.from), toMs = timeMs(value.to);
  const series = parseList(value.series, parseSeriesItem);
  if (fromMs == null || toMs == null || !series) return null;
  return { fromMs, toMs, bucketMs: value.bucketMs, buckets: value.buckets, partial: value.partial, scanned: isNumber(value.scanned) ? value.scanned : 0, series };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The view window and the bucket.

export const TARGET_BUCKETS = 600;
export const MAX_BUCKETS = 2000;
export const MAX_SELECTORS = 50;

/** The bucket for a view: span ÷ 600, whole milliseconds, never under the least the server allows (and never so small that 2000 buckets are passed). */
export function bucketFor(spanMs: number, leastMs = 1): number {
  const span = Math.max(1, Math.floor(spanMs));
  return Math.max(1, Math.ceil(span / TARGET_BUCKETS), Math.ceil(span / MAX_BUCKETS), Math.ceil(leastMs) || 1);
}

/** The least bucket a refusal names ("Use a bucket of at least 87 ms."), or null when the message does not name one. */
export function leastBucketFrom(message: string): number | null {
  const found = /at least (\d+) ms/i.exec(message);
  return found ? Number(found[1]) : null;
}

export interface ViewWindow { fromMs: number; toMs: number }
/** The window the lines show: a window this panel set itself, else the zoom view when it is zoomed, else the queried window. */
export function viewWindow(input: {
  local: ViewWindow | null; zoomed: boolean; slots: ReadonlyArray<{ start: number; end: number }>; query: { start: string; end: string };
}): ViewWindow | null {
  if (input.local) return input.local;
  if (input.zoomed && input.slots.length > 0) return { fromMs: input.slots[0].start, toMs: input.slots[input.slots.length - 1].end };
  const fromMs = Date.parse(input.query.start), toMs = Date.parse(input.query.end);
  return Number.isFinite(fromMs) && Number.isFinite(toMs) && toMs > fromMs ? { fromMs, toMs } : null;
}

/** The most stall periods one /resource request may carry (the server refuses more with bad-episode). */
export const MAX_EPISODES = 200;

/**
 * The stall periods the Patterns report found (start and end as RFC 3339 text), as sorted spans in milliseconds. A period with no readable
 * start or end, or an end before its start, is dropped; two periods with the same start and end are one.
 */
export function stallSpans(list: ReadonlyArray<{ start: string; end: string }> | null | undefined): EpisodeSpan[] {
  const seen = new Set<string>();
  const out: EpisodeSpan[] = [];
  for (const item of list ?? []) {
    const start = Date.parse(item?.start), end = Date.parse(item?.end);
    if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) continue;
    const key = start + '/' + end;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ start, end });
  }
  return out.sort((a, b) => a.start - b.start || a.end - b.end);
}

/**
 * The stall periods to send with a /resource read for this view: the ones that touch the view, at most `cap`. Over the cap the longest are kept
 * (the earlier one wins a tie), and the result is sorted by start again. Nothing in, nothing sent.
 */
export function episodesForRequest(spans: ReadonlyArray<EpisodeSpan>, view: ViewWindow | null, cap = MAX_EPISODES): EpisodeSpan[] {
  const inView = view ? spans.filter(span => span.end >= view.fromMs && span.start <= view.toMs) : [...spans];
  const kept = inView.length > cap
    ? [...inView].sort((a, b) => (b.end - b.start) - (a.end - a.start) || a.start - b.start || a.end - b.end).slice(0, Math.max(0, cap))
    : inView;
  return [...kept].sort((a, b) => a.start - b.start || a.end - b.end);
}

/** A short text that is the same exactly when two stall lists are the same, so a refetch happens only when the list changes. */
export const spansKey = (spans: ReadonlyArray<EpisodeSpan>): string => spans.map(span => span.start + '-' + span.end).join(',');

/** The slot indexes that cover a dragged span, for the zoom store's index-range setter. Null when no slot is touched. */
export function slotRange(slots: ReadonlyArray<{ start: number; end: number }>, fromMs: number, toMs: number): [number, number] | null {
  const low = Math.min(fromMs, toMs), high = Math.max(fromMs, toMs);
  let first = -1, last = -1;
  for (let i = 0; i < slots.length; i++) {
    if (slots[i].end > low && slots[i].start < high) { if (first < 0) first = i; last = i; }
  }
  return first < 0 ? null : [first, last];
}

/** A dragged span is a zoom only when it is wide enough to mean it; anything smaller was a click. */
export const isBrush = (pixels: number): boolean => Math.abs(pixels) >= 6;

/** The window for the zoom buttons when the panel keeps its own: halve or double around the middle, or step by half a window. Never wider than the queried window. */
export function stepWindow(current: ViewWindow, whole: ViewWindow, action: 'in' | 'out' | 'earlier' | 'later'): ViewWindow {
  const span = current.toMs - current.fromMs, middle = current.fromMs + span / 2;
  let from = current.fromMs, to = current.toMs;
  if (action === 'in') { from = middle - span / 4; to = middle + span / 4; }
  else if (action === 'out') { from = middle - span; to = middle + span; }
  else { const shift = (action === 'later' ? 1 : -1) * span / 2; from += shift; to += shift; }
  const width = Math.min(to - from, whole.toMs - whole.fromMs);
  if (from < whole.fromMs) { from = whole.fromMs; to = from + width; }
  if (to > whole.toMs) { to = whole.toMs; from = to - width; }
  return { fromMs: Math.round(from), toMs: Math.round(to) };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Selectors. A selector is ";"-separated terms: kind, optional source and host, up to four attr.<name> terms, and field.

export interface Cand { kind: string; attrs: Array<[string, string]>; mul: number }
export interface LineDef { id: string; name: string; cands: Cand[]; /** first: the first candidate with data. rate: add every candidate, per minute. */ combine: 'first' | 'rate' }
export interface StripDef { id: string; title: string; unit: string; max?: number; lines: LineDef[] }

const counter = (object: string | string[], name: string, mul = 1, instance?: string): Cand[] =>
  (Array.isArray(object) ? object : [object]).map(item => ({ kind: 'counter', attrs: [['object', item], ...(instance ? [['instance', instance] as [string, string]] : []), ['counter', name]], mul }));
const metric = (name: string, mul = 1): Cand => ({ kind: 'metric', attrs: [['counter', name]], mul });
const named = (name: string, mul = 1): Cand => ({ kind: 'metric', attrs: [['metric', name]], mul });
const GIB = 1 << 30;

/** The catalogue of lines a host strip can show. Candidates are different spellings of one measure; the panel uses the first that has data. */
export const STRIP_DEFS: StripDef[] = [
  { id: 'host-cpu', title: 'Host CPU', unit: '%', max: 100, lines: [
    { id: 'host.cpu', name: 'Host CPU', combine: 'first', cands: counter(['Processor', 'Processor Information'], '% Processor Time', 1, '_Total') }] },
  { id: 'host-mem', title: 'Free memory', unit: 'GiB', lines: [
    { id: 'host.mem.available', name: 'Free memory', combine: 'first', cands: [
      ...counter('Memory', 'Available MBytes', (1 << 20) / GIB), ...counter('Memory', 'Available KBytes', (1 << 10) / GIB), ...counter('Memory', 'Available Bytes', 1 / GIB)] }] },
  { id: 'host-disk', title: 'Disk time per transfer', unit: 'ms', lines: [
    { id: 'host.disk.sec_per_transfer', name: 'Disk time per transfer', combine: 'first', cands: counter(['PhysicalDisk', 'LogicalDisk'], 'Avg. Disk sec/Transfer', 1000, '_Total') }] },
  { id: 'runtime-gc', title: 'Time in garbage collection', unit: '%', lines: [
    { id: 'clr.gc_time', name: 'Time in garbage collection', combine: 'first', cands: [...counter('.NET CLR Memory', '% Time in GC'), metric('% Time in GC since last GC'), metric('% Time in GC')] }] },
  { id: 'runtime-queues', title: 'Queues', unit: 'items', lines: [
    { id: 'net.tp.queue', name: 'Thread pool queue', combine: 'first', cands: [metric('ThreadPool Queue Length'), metric('dotnet.thread_pool.queue.length')] },
    { id: 'aspnet.queued', name: 'ASP.NET requests queued', combine: 'first', cands: counter(['ASP.NET', 'ASP.NET v4.0.30319'], 'Requests Queued') },
    { id: 'kestrel.queued', name: 'Kestrel queue', combine: 'first', cands: [metric('Request Queue Length'), metric('Connection Queue Length')] }] },
  { id: 'redis', title: 'Redis', unit: 'clients', lines: [
    { id: 'redis.clients', name: 'Connected clients', combine: 'first', cands: [named('connected_clients')] },
    { id: 'redis.blocked', name: 'Blocked clients', combine: 'first', cands: [named('blocked_clients')] }] },
  { id: 'mongo', title: 'Mongo', unit: 'count', lines: [
    { id: 'mongo.queue', name: 'Operations queued', combine: 'first', cands: [named('globalLock.currentQueue.total')] },
    { id: 'mongo.conns', name: 'Connections', combine: 'first', cands: [named('connections.current'), named('conn')] }] },
  { id: 'log-rate', title: 'Log entries per minute, warning and above', unit: 'per min', lines: [
    { id: 'log.rate', name: 'Warning and above', combine: 'rate', cands: ['Warning', 'Warn', 'WARN', 'Error', 'ERROR', 'Critical', 'Fatal', 'FATAL'].map(level => ({ kind: 'log-rate', attrs: [['level', level]] as Array<[string, string]>, mul: 1 })) }] }
];

/** The selector text for one candidate on one host, or null when a part of it could not be written (a ";" or "=" in a name, or a part over 64 characters). */
export function selectorFor(host: string, cand: Cand): string | null {
  const parts = [cand.kind, host, ...cand.attrs.flat()];
  if (parts.some(part => /[;=]/.test(part) || part.length > 64 || part.length === 0)) return null;
  return ['kind=' + cand.kind, 'host=' + host, ...cand.attrs.map(([key, value]) => 'attr.' + key + '=' + value), 'field=value'].join(';');
}

/** Every selector the host strips need, each once, in a stable order. */
export function hostSelectors(host: string): string[] {
  const seen = new Set<string>();
  for (const strip of STRIP_DEFS) for (const line of strip.lines) for (const cand of line.cands) { const sel = selectorFor(host, cand); if (sel) seen.add(sel); }
  return [...seen];
}

/** The process selectors for one measure, in the server's order, at most five. */
export function processSelectors(processes: ProcessRef[], metricId: ProcMetricId): Array<{ name: string; sel: string }> {
  const out: Array<{ name: string; sel: string }> = [];
  for (const item of processes) { const sel = item.sel[metricId]; if (sel) out.push({ name: item.name, sel }); if (out.length >= TOP_PROCESSES) break; }
  return out;
}

/** Split selectors into requests the server accepts (at most 50 each). */
export function chunkSelectors<T>(items: T[], size = MAX_SELECTORS): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}

export interface SeriesQuery { fromMs: number; toMs: number; bucketMs: number; sels: string[] }
export type SeriesFetcher = (request: SeriesQuery, signal?: AbortSignal) => Promise<SeriesView>;
export interface SeriesRead { items: Map<string, SeriesItem>; bucketMs: number; partial: boolean }

/**
 * Read the lines for one view window: bucket = span ÷ 600, selectors in requests of at most 50. When the server names a larger least bucket
 * ("Use a bucket of at least N ms.") the read is repeated once with it. The fetcher is passed in, so nothing here touches the network.
 */
export async function readSeries(fetcher: SeriesFetcher, view: ViewWindow, sels: string[], signal?: AbortSignal): Promise<SeriesRead> {
  const items = new Map<string, SeriesItem>();
  let partial = false;
  let bucketMs = bucketFor(view.toMs - view.fromMs);
  for (const chunk of chunkSelectors(sels)) {
    let answer: SeriesView;
    try {
      answer = await fetcher({ fromMs: view.fromMs, toMs: view.toMs, bucketMs, sels: chunk }, signal);
    } catch (error) {
      const least = error instanceof Error ? leastBucketFrom(error.message) : null;
      if (least == null || least <= bucketMs || (error as { reason?: string }).reason !== 'too-many-buckets') throw error;
      bucketMs = bucketFor(view.toMs - view.fromMs, least);
      answer = await fetcher({ fromMs: view.fromMs, toMs: view.toMs, bucketMs, sels: chunk }, signal);
    }
    if (answer.partial) partial = true;
    bucketMs = answer.bucketMs || bucketMs;
    for (const item of answer.series) items.set(item.sel, item);
  }
  return { items, bucketMs, partial };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Strips: the lines the chart draws.

export type Point = [number, number | null];
export interface Line { id: string; name: string; points: Point[]; band?: boolean }
export interface Strip { id: string; title: string; unit: string; max?: number; lines: Line[] }

/** Series points as [time, value] with a null where buckets are missing, so a gap in the data is a gap in the line. */
export function toPoints(points: SeriesPoint[], bucketMs: number, mul = 1, pick: 'mean' | 'max' = 'mean'): Point[] {
  const out: Point[] = [];
  let previous: number | null = null;
  for (const point of points) {
    if (previous != null && point.t - previous > bucketMs * 1.5) out.push([previous + bucketMs, null]);
    out.push([point.t, round(point[pick] * mul)]);
    previous = point.t;
  }
  return out;
}
const round = (value: number): number => Math.round(value * 1000) / 1000;

function lineFor(def: LineDef, host: string, items: Map<string, SeriesItem>, bucketMs: number): Line | null {
  if (def.combine === 'first') {
    for (const cand of def.cands) {
      const sel = selectorFor(host, cand);
      const item = sel ? items.get(sel) : undefined;
      if (item && item.points.length > 0) return { id: def.id, name: def.name, points: toPoints(item.points, bucketMs, cand.mul) };
    }
    return null;
  }
  // rate: entries per minute, summed over every level that has data. mean × count is the entries in a bucket's events.
  const totals = new Map<number, number>();
  for (const cand of def.cands) {
    const sel = selectorFor(host, cand);
    const item = sel ? items.get(sel) : undefined;
    if (item) for (const point of item.points) totals.set(point.t, (totals.get(point.t) ?? 0) + point.mean * point.count);
  }
  if (totals.size === 0) return null;
  const minutes = Math.max(bucketMs, 1) / 60000;
  const points = [...totals.entries()].sort((a, b) => a[0] - b[0]).map(([t, sum]) => ({ t, min: 0, mean: sum / minutes, max: 0, count: 1 }));
  return { id: def.id, name: def.name, points: toPoints(points, bucketMs) };
}

/** The host's strips that have data, in catalogue order. A strip with no line is left out. */
export function buildStrips(host: string, items: Map<string, SeriesItem>, bucketMs: number): Strip[] {
  const out: Strip[] = [];
  for (const def of STRIP_DEFS) {
    const lines = def.lines.map(line => lineFor(line, host, items, bucketMs)).filter((line): line is Line => line != null);
    if (lines.length > 0) out.push({ id: def.id, title: def.title, unit: def.unit, max: def.max, lines });
  }
  return out;
}

/** The API P95 strip, from windows the app already holds. Windows without a P95, or outside the view, are left out. */
export function apiStrip(windows: ReadonlyArray<{ at: string; p95Ms: number | null }>, view: ViewWindow, bucketMs: number): Strip | null {
  const points = windows
    .map(item => ({ t: Date.parse(item.at), value: item.p95Ms }))
    .filter(item => Number.isFinite(item.t) && item.value != null && item.t >= view.fromMs && item.t <= view.toMs)
    .sort((a, b) => a.t - b.t)
    .map(item => ({ t: item.t, min: item.value as number, mean: item.value as number, max: item.value as number, count: 1 }));
  if (points.length === 0) return null;
  // The windows sit one load interval apart, which is wider than a /series bucket, so the gap test uses their own spacing.
  const steps = points.slice(1).map((point, index) => point.t - points[index].t).filter(step => step > 0).sort((a, b) => a - b);
  const spacing = steps.length > 0 ? steps[Math.floor(steps.length / 2)] : bucketMs;
  return { id: 'api-p95', title: resourceCopy.apiStrip, unit: 'ms', lines: [{ id: 'api.p95', name: resourceCopy.apiStrip, points: toPoints(points, Math.max(bucketMs, spacing)) }] };
}

const processUnit = (metricId: ProcMetricId): { unit: string; mul: number } =>
  metricId === 'proc.ws' || metricId === 'proc.private' ? { unit: 'MiB', mul: 1 / (1 << 20) } : metricId === 'proc.cpu' ? { unit: '%', mul: 1 } : { unit: 'count', mul: 1 };

/**
 * The process strip: one line per ranked process. For CPU in percent of the host, with a host CPU line present, "all other processes" is the host
 * line minus the listed ones, drawn as a grey band; without host CPU, or when the lines are not in percent of the host, there is no band and
 * the strip says only what the listed processes did.
 */
export function processStrip(metricId: ProcMetricId, refs: Array<{ name: string; sel: string }>, items: Map<string, SeriesItem>, bucketMs: number, hostCpu: Line | null): Strip | null {
  const { unit, mul } = processUnit(metricId);
  const lines: Line[] = [];
  const found: SeriesItem[] = [];
  for (const ref of refs) {
    const item = items.get(ref.sel);
    if (item && item.points.length > 0) { lines.push({ id: ref.sel, name: ref.name, points: toPoints(item.points, bucketMs, mul) }); found.push(item); }
  }
  if (lines.length === 0) return null;
  let max: number | undefined;
  if (metricId === 'proc.cpu' && hostCpu && found.every(item => item.unit === 'percent-host' && !item.unitMixed)) {
    const listed = new Map<number, number>();
    for (const item of found) for (const point of item.points) listed.set(point.t, (listed.get(point.t) ?? 0) + point.mean);
    const other: Point[] = [];
    let previous: number | null = null;
    for (const [t, value] of hostCpu.points) {
      if (value == null) continue;
      if (previous != null && t - previous > bucketMs * 1.5) other.push([previous + bucketMs, null]);
      other.push([t, round(Math.max(0, value - (listed.get(t) ?? 0)))]);
      previous = t;
    }
    if (other.length > 0) lines.push({ id: 'other', name: resourceCopy.otherProcesses, points: other, band: true });
    max = 100;
  }
  return { id: 'processes', title: resourceCopy.processesHeading, unit: unit === '%' ? '% of host' : unit, max, lines };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Rule rows: chips, and the rows behind a click.

export interface Chip { key: string; id: string; host: string; text: string; label: string; wording: string; hint: string; aria: string }

/** One chip per fired rule. The wording is the server's, unchanged; the hint is this panel's. */
export function chipsFor(rules: RuleRow[], host: string | null): Chip[] {
  return rules.filter(rule => host == null || rule.host === host).map((rule, index) => ({
    key: rule.id + '|' + rule.host + '|' + index, id: rule.id, host: rule.host, text: rule.title, label: rule.label, wording: rule.wording, hint: ruleHint(rule.id),
    aria: rule.title + '. ' + rule.label + '. ' + ruleHint(rule.id) + ' ' + resourceCopy.chipHintOpen
  }));
}

/** The rules whose stall period overlaps [t, t + width], for a click on a time. Rules not tied to a period are left out. */
export function rulesAt(rules: RuleRow[], t: number, width: number, host: string | null): RuleRow[] {
  return rules.filter(rule => (host == null || rule.host === host) && rule.episode != null && rule.episode.end >= t && rule.episode.start <= t + Math.max(width, 0));
}

/** The rules of one stall period (a click on its band or in the list). */
export function rulesOfEpisode(rules: RuleRow[], span: EpisodeSpan, host: string | null): RuleRow[] {
  return rules.filter(rule => (host == null || rule.host === host) && rule.episode != null && rule.episode.start === span.start && rule.episode.end === span.end);
}

/** The stall period a time falls in, if any. */
export const episodeAt = (episodes: EpisodeSpan[], t: number): EpisodeSpan | null => episodes.find(span => t >= span.start && t <= span.end) ?? null;

export const rangeText = (start: number, end: number, zone: string): string => chartTime(start, zone, { seconds: false }) + ' to ' + chartTime(end, zone, { seconds: false });

/** Coverage dots: a fraction becomes one of four fills and a plain sentence. */
export function coverageDot(item: CoverageItem): { level: 'full' | 'most' | 'some' | 'none'; text: string } {
  const percent = Math.round(item.fraction * 100);
  const level = item.fraction >= 0.9 ? 'full' : item.fraction >= 0.5 ? 'most' : item.fraction > 0 ? 'some' : 'none';
  return { level, text: metricName(item.metric) + ': ' + (percent === 0 ? resourceCopy.coverageNone : percent + '% of the window') };
}

/** A bucket in plain units: "250 ms", "30 s", "2.5 min". */
export function bucketText(ms: number): string {
  if (ms < 1000) return Math.round(ms) + ' ms';
  if (ms < 60000) return Math.round(ms / 100) / 10 + ' s';
  return Math.round(ms / 6000) / 10 + ' min';
}

/** The strips in the order the panel shows them: the API line, host CPU and memory, the processes, then runtime, Redis, Mongo and logs. */
export function orderStrips(api: Strip | null, host: Strip[], processes: Strip | null): Strip[] {
  const front = host.filter(strip => strip.id === 'host-cpu' || strip.id === 'host-mem' || strip.id === 'host-disk');
  const back = host.filter(strip => !front.includes(strip));
  return [...(api ? [api] : []), ...front, ...(processes ? [processes] : []), ...back];
}

/** One sentence for the chart's accessible name. */
export function stripsSummary(host: string, strips: Strip[], episodes: EpisodeSpan[], markers: Marker[], view: ViewWindow, zone: string): string {
  const titles = strips.map(strip => strip.title).join(', ');
  return resourceCopy.chartLabel + ' ' + host + ', ' + rangeText(view.fromMs, view.toMs, zone) + '. Lines: ' + (titles || 'none') + '. ' +
    episodes.length + (episodes.length === 1 ? ' stall period' : ' stall periods') + ', ' + markers.length + (markers.length === 1 ? ' marker.' : ' markers.');
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The ECharts option: one grid per strip, one shared time axis, bands for stall periods and rules for markers on every grid.

export const ROW_HEIGHT = 74;
const TOP = 26, GAP = 26, BOTTOM = 34;
export const chartHeight = (rows: number): number => TOP + rows * ROW_HEIGHT + Math.max(0, rows - 1) * GAP + BOTTOM;
export const GRID_LEFT = 58, GRID_RIGHT = 14;

const withAlpha = (color: string, alpha: number): string =>
  /^#[0-9a-f]{6}$/i.test(color) ? color + Math.round(alpha * 255).toString(16).padStart(2, '0') : color;

export interface OptionInput {
  strips: Strip[]; view: ViewWindow; episodes: EpisodeSpan[]; markers: Marker[]; zone: string; pal: Palette;
}

export function stripsOption(input: OptionInput) {
  const { strips, view, episodes, markers, zone, pal } = input;
  const grids: unknown[] = [], xAxes: unknown[] = [], yAxes: unknown[] = [], titles: unknown[] = [], series: unknown[] = [];
  const last = strips.length - 1;
  strips.forEach((strip, i) => {
    const top = TOP + i * (ROW_HEIGHT + GAP);
    grids.push({ left: GRID_LEFT, right: GRID_RIGHT, top, height: ROW_HEIGHT });
    xAxes.push({
      type: 'time', gridIndex: i, min: view.fromMs, max: view.toMs, axisLine: { lineStyle: { color: pal.axisLine } }, axisTick: { show: i === last },
      splitLine: { show: true, lineStyle: { color: pal.grid } },
      axisLabel: { show: i === last, color: pal.text2, fontSize: 10, hideOverlap: true, formatter: (value: number) => chartTime(value, zone, { seconds: false, zone: false }) },
      axisPointer: { show: true, lineStyle: { color: pal.text2, type: 'dashed' } }
    });
    yAxes.push({
      type: 'value', gridIndex: i, min: 0, max: strip.max, splitNumber: 2, scale: false,
      axisLabel: { color: pal.text2, fontSize: 10 }, splitLine: { lineStyle: { color: pal.grid } }, axisLine: { show: false }
    });
    titles.push({ text: strip.title + (strip.unit ? ' (' + strip.unit + ')' : ''), left: GRID_LEFT, top: top - 17, textStyle: { color: pal.text2, fontSize: 11, fontWeight: 600 } });
    strip.lines.forEach((line, k) => {
      const color = line.band ? pal.axisLine : pal.series[k % pal.series.length];
      series.push({
        type: 'line', name: line.name, xAxisIndex: i, yAxisIndex: i, data: line.points, connectNulls: false, showSymbol: line.points.length < 3, symbolSize: 5,
        z: line.band ? 1 : 3, lineStyle: line.band ? { opacity: 0 } : { color, width: 1.6 }, itemStyle: { color },
        ...(line.band ? { areaStyle: { color: withAlpha(pal.text2, 0.18) }, emphasis: { disabled: true } } : {})
      });
    });
    // A series with no data carries the bands and the rules for its grid.
    series.push({
      type: 'line', name: '', xAxisIndex: i, yAxisIndex: i, data: [], silent: true, tooltip: { show: false },
      markArea: {
        silent: true, animation: false, itemStyle: { color: withAlpha(pal.warn, 0.14), borderWidth: 0 },
        data: episodes.map(span => [{ xAxis: Math.max(span.start, view.fromMs) }, { xAxis: Math.min(span.end, view.toMs) }]).filter(([a, b]) => (b as { xAxis: number }).xAxis > (a as { xAxis: number }).xAxis)
      },
      markLine: {
        silent: true, animation: false, symbol: 'none', label: { show: false },
        data: markers.filter(m => m.t >= view.fromMs && m.t <= view.toMs).map(m => ({ xAxis: m.t, lineStyle: { color: m.edge === 'stop' ? pal.bad : pal.accent, type: 'dashed' as const, width: 1 } }))
      }
    });
  });
  return {
    animation: false,
    grid: grids, xAxis: xAxes, yAxis: yAxes, title: titles, series,
    axisPointer: { link: [{ xAxisIndex: 'all' }] },
    tooltip: {
      trigger: 'axis', confine: true, backgroundColor: pal.surface, borderColor: pal.axisLine, textStyle: { color: pal.text, fontSize: 12 },
      formatter: (items: Array<{ axisValue: number; seriesName: string; value: [number, number | null]; marker: string }>) => {
        const shown = items.filter(item => item.seriesName && item.value && item.value[1] != null);
        if (shown.length === 0) return '';
        return escapeHTML(chartTime(shown[0].axisValue, zone)) + '<br/>' + shown.map(item => item.marker + escapeHTML(item.seriesName) + ': ' + escapeHTML(String(item.value[1]))).join('<br/>');
      }
    }
  };
}
