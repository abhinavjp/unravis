import { changeText, msChangeText, pointsText, rateText } from './overview';
import { p95Change, type P95Change } from './copy/screenCopy';
import type { Analysis, Comparison, Kind, Metric, OverviewRow, Quantile, Rollup } from './types';

/** The measure a headline figure opens in the ranked "where is it" view. */
export type Lens = 'requests' | 'completion' | 'p95' | 'client' | 'server';

export interface Figure {
  id: Lens;
  label: string;
  /** The large number, or an em dash when it cannot be stated honestly. */
  value: string;
  /** One short line under the number. */
  hint: string;
  /** Tooltip with the exact meaning. */
  tip: string;
  /** How the figure compares with the earlier period, when that can be stated (the P95 only). */
  change?: P95Change;
}

const dash = '—';
const finite = (value: number | null | undefined): value is number => value != null && Number.isFinite(value);

/** A request type with its own latency histogram. Latency is only stated for one of these: histograms are never merged across request types. */
export const singleKind = (kind: Kind): boolean => kind === 'synchronous' || kind === 'asynchronous' || kind === 'hangfire' || kind === 'http_unknown';
const overviewKind = (kind: Kind) => kind === 'http_unknown' ? 'unknown' : kind;

export const durationText = (ms: number | null | undefined): string => {
  if (!finite(ms)) return dash;
  if (ms >= 1000) return (ms / 1000).toFixed(ms >= 10_000 ? 1 : 2) + ' s';
  return (ms < 10 ? ms.toFixed(1) : String(Math.round(ms))) + ' ms';
};

const quantileMs = (quantile: Quantile | null | undefined) => quantile?.valueMs ?? null;
const share = (part: number, whole: number) => whole > 0 ? (part / whole * 100).toFixed(1) + '%' : dash;

/**
 * The five headline figures of the pulse, summed over the overview rows (one per request type in scope). Completion is the pooled
 * completed / all events, so it matches the Completed tile of each kind. The P95 is only given when the query names one request type.
 */
export function pulseFigures(overview: OverviewRow[], queryKind: Kind, metric: Metric, metricLabel: string): Figure[] {
  const events = overview.reduce((sum, row) => sum + row.events, 0);
  const completed = overview.reduce((sum, row) => sum + row.completed, 0);
  const client = overview.reduce((sum, row) => sum + row.clientErrors, 0);
  const server = overview.reduce((sum, row) => sum + row.serverErrors, 0);
  const single = singleKind(queryKind);
  const row = single ? overview.find(item => item.kind === overviewKind(queryKind)) : undefined;
  const p95 = row ? quantileMs(row.p95) : null;
  const change = single ? p95Change(p95, quantileMs(row?.reference?.p95)) : null;
  const p95Hint = !single ? 'Choose one request type' : p95 == null ? 'No timed requests' : metricLabel;
  return [
    { id: 'requests', label: 'Requests', value: events.toLocaleString(), hint: events ? 'in the selected period' : 'none in this period', tip: 'Every event of the request types in scope that matches the filters, including incomplete and pending ones.' },
    { id: 'completion', label: 'Completion', value: share(completed, events), hint: events ? completed.toLocaleString() + ' completed' : 'nothing to measure', tip: 'Completed ÷ all events. Incomplete, pending and unusable events are in the denominator and never counted as fast or successful.' },
    { id: 'p95', change: change ?? undefined, label: metric === 'api' ? 'API P95' : 'Observed P95', value: single ? durationText(p95) : dash, hint: p95Hint, tip: single ? 'A histogram estimate over the valid timings of this request type. ' + metricLabel + '.' : 'Latency is not pooled across request types. Pick Sync, Async, Hangfire or HTTP (polling type unknown) to see a P95.' },
    { id: 'client', label: '4xx', value: client.toLocaleString(), hint: events ? share(client, events) + ' of requests' : '—', tip: 'Client-error responses.' },
    { id: 'server', label: '5xx', value: server.toLocaleString(), hint: events ? share(server, events) + ' of requests' : '—', tip: 'Server-error responses, including failed Hangfire jobs.' }
  ];
}

export interface HeroWindow { at: number; count: number; latency: number | null }

/** Whether the hero timeline has anything to draw: at least one window with requests. */
export const heroHasData = (windows: readonly Pick<HeroWindow, 'count'>[]): boolean => windows.some(window => window.count > 0);

export interface LensRead {
  /** The number operations are ranked by; null when the operation has no honest value for this lens. */
  sort: number | null;
  value: string;
  /** What the value is, in two words or fewer. */
  label: string;
  /** The change against the earlier period, already formatted; an em dash when it is not available. */
  delta: string;
}

const finiteNumber = (value: number | null | undefined): value is number => value != null && Number.isFinite(value);

/** The one number that matters for an operation under the chosen lens, with its change against the earlier period. */
export function lensRead(row: Rollup, lens: Lens): LensRead {
  switch (lens) {
    case 'completion': return { sort: finiteNumber(row.completionRate) ? row.completionRate : null, value: rateText(row.completionRate), label: 'completed', delta: pointsText(row.completionDelta) };
    case 'p95': return { sort: finiteNumber(row.p95Ms) ? row.p95Ms : null, value: durationText(row.p95Ms), label: 'P95', delta: msChangeText(row.p95DeltaMs) };
    case 'client': return { sort: finiteNumber(row.clientErrorRate) ? row.clientErrorRate : null, value: rateText(row.clientErrorRate), label: '4xx', delta: pointsText(row.clientErrorDelta) };
    case 'server': return { sort: finiteNumber(row.serverErrorRate) ? row.serverErrorRate : null, value: rateText(row.serverErrorRate), label: '5xx', delta: pointsText(row.serverErrorDelta) };
    default: {
      const events = row.events ?? row.requests;
      return { sort: events, value: events.toLocaleString(), label: 'requests', delta: changeText(row.trafficDelta) };
    }
  }
}

/** Operations ordered so the worst comes first: lowest completion, otherwise highest value. Operations without a value for the lens go last. */
export function rankOperations(rows: readonly Rollup[], lens: Lens): Rollup[] {
  const direction = lens === 'completion' ? 1 : -1;
  return rows.map((row, index) => ({ row, index, sort: lensRead(row, lens).sort })).sort((a, b) => {
    if (a.sort == null && b.sort == null) return a.index - b.index;
    if (a.sort == null) return 1;
    if (b.sort == null) return -1;
    return (a.sort - b.sort) * direction || a.index - b.index;
  }).map(item => item.row);
}

/** The per-interval values drawn as the operation's sparkline, from its busiest comparison group; empty when the operation has no chart series. */
export function sparkSeries(analysis: Pick<Analysis, 'apis' | 'timeline'>, row: Pick<Rollup, 'controller' | 'action' | 'kind'>, lens: Lens): Array<number | null> {
  let best: Comparison | undefined;
  for (const api of analysis.apis) {
    if (api.controller !== row.controller || api.action !== row.action) continue;
    if (row.kind != null && api.kind !== row.kind) continue;
    if (!best || api.current.count > best.current.count) best = api;
  }
  const series = best && analysis.timeline.find(item => item.key === best!.key);
  if (!series) return [];
  return series.points.map(point => {
    if (point.count == null) return null;
    switch (lens) {
      case 'requests': return point.count + (point.incomplete ?? 0);
      case 'completion': return point.incomplete ?? 0;
      case 'client': return point.count4xx ?? 0;
      case 'server': return point.count5xx ?? 0;
      default: return point.p95?.valueMs ?? null;
    }
  });
}
