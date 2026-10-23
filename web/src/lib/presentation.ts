import type { Comparison, Metric, Quality, Query, Rollup } from './types';

export type RankField = 'impact' | 'ratio' | 'delta' | 'count';

// Rank comparable groups by the chosen field; groups without a value always stay last.
// 'impact' is the extra time spent versus the reference across all current requests.
export const rankComparisons = (rows: Comparison[], field: RankField = 'ratio', direction = -1) => rows.slice().sort((a, b) => {
  const value = (row: Comparison) => field === 'impact' ? row.impactMs : field === 'ratio' ? row.p95Ratio : field === 'delta' ? row.p95DeltaMs : row.current.count;
  const av = value(a), bv = value(b);
  if (av == null || !Number.isFinite(av)) return bv == null || !Number.isFinite(bv) ? a.key.localeCompare(b.key) : 1;
  if (bv == null || !Number.isFinite(bv)) return -1;
  return direction * (av - bv) || b.current.count - a.current.count || a.key.localeCompare(b.key);
});

export const initialComparison = (rows: Comparison[]) => rankComparisons(rows, rows.some(row => row.impactMs != null) ? 'impact' : 'ratio').find(row => row.state === 'compared' && row.current.count > 0)
  ?? rows.slice().sort((a, b) => b.current.count - a.current.count)[0] ?? null;

// Record filters count as unused when absent, empty or blank, so a draft with an
// emptied field and the server's echo of it (which drops empties) match; lists
// are sets, so their order does not matter.
const filterList = (values: readonly (string | number)[] | undefined) => [...new Set((values ?? []).map(value => String(value).trim()).filter(Boolean))].sort();
const filterText = (value: string | undefined) => (value ?? '').trim();
export const queryFingerprint = (query: Query) => JSON.stringify([
  [...query.sourceIds].sort(), ...[query.start, query.end, query.baselineStart, query.baselineEnd].map(value => Date.parse(value)),
  query.kind, query.outcome, query.metric, query.bucketSeconds, query.controller ?? '', query.action ?? '',
  filterList(query.userIds), filterList(query.uiVersions), filterText(query.userAgentContains), filterList(query.userIps),
  filterText(query.urlContains), filterText(query.auditId), filterList(query.statusCodes),
  query.completion === 'all' ? '' : query.completion ?? '', filterText(query.operation)
]);

export const resultNotes = (rows: Comparison[]) => {
  const lowSamples = rows.filter(row => row.state === 'insufficient_data').length;
  const noCurrent = rows.filter(row => row.state === 'missing_current').length;
  const noReference = rows.filter(row => row.state === 'missing_reference').length;
  const nearZero = rows.filter(row => row.state === 'undefined_baseline').length;
  return [
    'Response times are estimates. P95 means about 95 out of 100 requests with usable timings finish within that time. A change does not by itself tell you whether an API is healthy.',
    ...(lowSamples ? [`${lowSamples} API groups have fewer than 20 usable timings in at least one period. Their slowdown ratios are unavailable because the sample is too small.`] : []),
    ...(noCurrent ? [`${noCurrent} API groups have no usable timings in the investigation period.`] : []),
    ...(noReference ? [`${noReference} API groups have no usable timings in the reference period.`] : []),
    ...(nearZero ? [`${nearZero} API groups have a reference time too close to zero to calculate a meaningful slowdown ratio.`] : [])
  ];
};

export const outcomeRateText = (rows: Comparison[], row: Comparison, outcomeFilter: string) => {
  if (row.outcome === 'unknown') return 'unknown · ' + row.currentEvents.toLocaleString() + ' events';
  const denominator = rows
    .filter(item => item.sourceId === row.sourceId && item.controller === row.controller && item.action === row.action && item.kind === row.kind && (item.outcome === 'success' || item.outcome === 'failure'))
    .reduce((sum, item) => sum + item.currentEvents, 0);
  if (outcomeFilter !== 'all' || denominator <= 0 || (row.outcome !== 'success' && row.outcome !== 'failure')) {
    return row.outcome + ' · ' + row.currentEvents.toLocaleString() + ' events';
  }
  const percentage = (row.currentEvents / denominator * 100).toFixed(1);
  return row.outcome + ' · ' + row.currentEvents.toLocaleString() + ' / ' + denominator.toLocaleString() + ' known events (' + percentage + '%)';
};

/**
 * Client (4xx) and server (5xx, including failed Hangfire jobs) error shares of an API's requests
 * with a known result, with the combined rate in the tooltip. The sources apply the outcome and kind
 * filters before summarizing, so the shares only exist under outcome All: otherwise every cell is "—".
 */
export const statusRates = (rollup: Rollup | undefined, outcomeFilter = 'all', kindFilter = 'all') => {
  const percent = (rate: number | null | undefined) => outcomeFilter !== 'all' || rate == null || !Number.isFinite(rate) ? '—' : (rate * 100).toFixed(1) + '%';
  const client = percent(rollup?.clientErrorRate), server = percent(rollup?.serverErrorRate), merged = percent(rollup?.errorRate);
  let tip: string;
  if (outcomeFilter !== 'all') tip = '4xx and 5xx shares need the outcome filter set to All.';
  else if (!rollup || rollup.errorRate == null) tip = 'No requests with a known success or failure result in the current period.';
  else if (rollup.clientErrorRate == null || rollup.serverErrorRate == null) {
    tip = 'Combined error rate ' + merged + ': ' + rollup.failures.toLocaleString() + ' failed of ' + rollup.requests.toLocaleString() + ' requests with a known result. The 4xx and 5xx split is unavailable because the status counts do not cover every failure of this API.';
  } else {
    const kind = rollup.kind || (kindFilter === 'all' ? '' : kindFilter);
    const scope = 'Covers all sources merged' + (kind ? ', for the ' + kind + ' requests of this API' : ' (rollup without a kind)');
    tip = '4xx are client errors; 5xx are server errors, including failed Hangfire jobs. Combined error rate ' + merged + ': ' + rollup.failures.toLocaleString() + ' failed of ' + rollup.requests.toLocaleString() + ' requests with a known result. ' + scope + '; unclassified results are left out.';
  }
  return { client, server, merged, tip };
};

export const unknownKindTip = 'Incomplete HTTP rows have no polling type until the audit update is written; they cannot be attributed to Synchronous or Asynchronous.';

/** Display label for a row or rollup kind; the pipeline value unknown means an HTTP row whose polling type is not recorded yet. */
export const kindLabel = (kind?: string) => kind === 'unknown' ? 'HTTP (polling type unknown)' : kind ?? '';

/** One line splitting the quality counts by row kind; empty when there is nothing to split. */
export const qualityByKindText = (quality?: Quality) => {
  const parts = Object.entries(quality?.byKind ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([kind, c]) => {
    const bits = [c.incomplete ? c.incomplete.toLocaleString() + ' missing timing' : '', c.invalid ? c.invalid.toLocaleString() + ' unusable timing' : '', c.unknownOutcome ? c.unknownOutcome.toLocaleString() + ' unclassified result' : ''].filter(Boolean);
    return bits.length ? kindLabel(kind) + ': ' + bits.join(', ') : '';
  }).filter(Boolean);
  return parts.length ? 'By request type: ' + parts.join(' · ') : '';
};

/** Display label for a row state (valid, incomplete, invalid) and for a quality drill-down filter (pending, unclassified). */
export const stateLabel = (state?: string) => ({ valid: 'Complete', incomplete: 'Missing timing', invalid: 'Unusable timing', pending: 'Pending async', unclassified: 'Unclassified result' } as Record<string, string>)[state ?? ''] ?? state ?? '';

export const metricLabels: Record<Metric, string> = {
  total: 'Observed API time (AuditCreateTime + ApiTime)',
  api: 'API processing'
};

export const observedPartialTip = (count?: number) => count && count > 0
  ? `${count.toLocaleString()} samples lack AuditCreateTime (legacy)`
  : undefined;
