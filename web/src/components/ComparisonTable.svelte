<script lang="ts">
  import type { Comparison, Rollup, Series } from '../lib/types';
  import { loadBadge, loadTip, rollupFor } from '../lib/traffic';
  import { observedPartialTip, outcomeRateText, rankComparisons, statusRates, type RankField } from '../lib/presentation';
  import PhaseBar from './PhaseBar.svelte';
  import { p99NeedsSamples, quantileText } from '../lib/slowness';
  let { rows, selectedKey, sources, outcomeFilter, onSelect, timelineKeys = [], onToggleTimeline = () => {}, rollups = [], timeline = [], kindFilter = 'all' }: {
    rows: Comparison[]; selectedKey: string; sources: Map<string, string>; outcomeFilter: string; onSelect: (key: string) => void
    timelineKeys?: string[]; onToggleTimeline?: (key: string) => void; rollups?: Rollup[]; timeline?: Series[]; kindFilter?: string;
  } = $props();
  let sort = $state<RankField>('impact');
  let direction = $state(-1);
  let search = $state('');
  let scope = $state('all');
  let page = $state(0);
  const pageSize = 10;
  const stateLabels: Record<string, string> = {
    compared: 'Compared',
    missing_current: 'No current samples',
    missing_reference: 'No reference samples',
    insufficient_data: 'Too few samples',
    undefined_baseline: 'No usable reference'
  };
  const stateDetails: Record<string, string> = {
    compared: 'Both periods have enough valid response-time samples for a comparison. This is not a service-health verdict.',
    missing_current: 'No valid response-time samples were found in the current period.',
    missing_reference: 'No valid response-time samples were found in the reference period.',
    insufficient_data: 'At least one period has fewer than 20 valid response-time samples.',
    undefined_baseline: 'The reference response time is too close to zero to calculate a meaningful ratio.'
  };
  let filtered = $derived(rows.filter(row => {
    const text = `${row.controller}.${row.action} ${sources.get(row.sourceId) ?? ''} ${row.kind} ${row.outcome}`.toLowerCase();
    return text.includes(search.trim().toLowerCase()) && (scope === 'all' || (scope === 'slower' ? (row.p95Ratio ?? 0) > 1 : scope === 'compared' ? row.state === 'compared' : row.state !== 'compared'));
  }));
  let ordered = $derived(rankComparisons(filtered, sort, direction));
  let pageCount = $derived(Math.max(1, Math.ceil(ordered.length / pageSize)));
  let currentPage = $derived(Math.min(page, pageCount - 1));
  let visible = $derived(ordered.slice(currentPage * pageSize, (currentPage + 1) * pageSize));
  const sortBy = (field: RankField) => {
    if (sort === field) direction *= -1;
    else { sort = field; direction = -1; }
    page = 0;
  };
  const ratio = (value: number | null) => value == null ? 'N/A' : '≈ ' + value.toFixed(2) + '×';
  const ms = (q: Comparison['current']['p99']) => q?.valueMs == null ? 'N/A' : '≈ ' + q.valueMs.toFixed(1) + ' ms';
  const impactText = (value: number | null | undefined) => {
    if (value == null) return 'N/A';
    const sign = value > 0 ? '+' : value < 0 ? '−' : '';
    const abs = Math.abs(value);
    return '≈ ' + sign + (abs >= 1000 ? (abs / 1000).toFixed(1) + ' s' : abs.toFixed(0) + ' ms');
  };
  // Impact is the extra time versus the reference mean across all current requests; z comes from the whole histogram.
  const impactTip = (row: Comparison) => row.impactMs == null
    ? 'Total impact needs a comparable group with enough samples in both periods.'
    : 'Extra time versus the reference average, summed over the current requests (volume × mean change).'
      + (row.shiftZ == null ? '' : ` Distribution shift z = ${row.shiftZ.toFixed(1)}; ${row.significant ? 'a clear, larger-than-bin shift.' : 'not a clear shift beyond histogram resolution.'}`);
  let loadByKey = $derived(new Map(timeline.map(series => [series.key, series.loadCorrelation])));
  // P99 needs at least 100 samples per period; the bin-width caveat applies to every histogram quantile.
  const p99Tip = (row: Comparison) => row.current.p99 == null || row.reference.p99 == null
    ? 'P99 needs at least 100 valid samples in each period.'
    : `P99 current ${ms(row.current.p99)}, reference ${ms(row.reference.p99)}` + (row.p99Ratio == null ? '' : ` (${ratio(row.p99Ratio)})`) + '. Estimates carry about ±5% error from the histogram bin width.';
</script>

<div class="group-toolbar">
  <label>Find an API<input type="search" placeholder="Search API name or source" bind:value={search} oninput={() => page = 0} /></label>
  <label>Show<select bind:value={scope} onchange={() => page = 0}><option value="all">All API groups</option><option value="slower">Slower than reference</option><option value="compared">Comparable groups</option><option value="unavailable">Unable to compare</option></select></label>
  <label class="mobile-group-sort">Sort by<select value={sort + ':' + direction} onchange={(event) => { const [field, order] = event.currentTarget.value.split(':'); sort = field as RankField; direction = Number(order); page = 0; }}><option value="impact:-1">Largest total impact</option><option value="ratio:-1">Largest slowdown</option><option value="ratio:1">Smallest ratio</option><option value="delta:-1">Largest time increase</option><option value="count:-1">Most current requests</option></select></label>
  <span>Choose an API name to inspect its charts and requests below.</span>
</div>
<div class="table-scroll group-table-scroll">
  <table class="comparison-table">
    <thead><tr>
      <th>Timeline</th>
      <th title="Each group stays distinct by source, controller, and action.">API group</th>
      <th title="Request type and outcome remain separate parts of each group.">Kind and outcome</th>
      <th aria-sort={sort === 'count' ? direction < 0 ? 'descending' : 'ascending' : 'none'}><button class="sort-button" title="Current-period requests with a valid response-time sample." onclick={() => sortBy('count')}>Current requests{sort === 'count' ? direction < 0 ? ' ↓' : ' ↑' : ''}</button></th>
      <th title="Reference-period requests with a valid response-time sample.">Reference requests</th>
      <th aria-sort={sort === 'impact' ? direction < 0 ? 'descending' : 'ascending' : 'none'}><button class="sort-button" title="Extra time versus the reference average, summed over all current requests. A marker means the whole latency distribution clearly shifted." onclick={() => sortBy('impact')}>Total impact{sort === 'impact' ? direction < 0 ? ' ↓' : ' ↑' : ''}</button></th>
      <th aria-sort={sort === 'ratio' ? direction < 0 ? 'descending' : 'ascending' : 'none'}><button class="sort-button" title="P95 is the time within which about 95 of 100 requests finish. 2× means twice as long; 0.5× means half as long." onclick={() => sortBy('ratio')}>P95 vs reference{sort === 'ratio' ? direction < 0 ? ' ↓' : ' ↑' : ''}</button></th>
      <th><button class="sort-button" title="Difference between current and earlier reference P95 estimates, in milliseconds. P95 is the estimated time within which about 95 of 100 requests with usable timings finish." onclick={() => sortBy('delta')}>P95 difference (ms)</button></th>
      <th title="Share of current-period requests with a known result that were client errors (4xx), for the whole API. Needs the outcome filter set to All.">4xx %</th>
      <th title="Share of current-period requests with a known result that were server errors (5xx, including failed Hangfire jobs), for the whole API. Needs the outcome filter set to All.">5xx %</th>
      <th title="Median: half of the current-period requests finished sooner. A histogram estimate.">P50</th>
      <th title="About 90 of 100 current-period requests finished within this time. A histogram estimate.">P90</th>
      <th title="About 99 of 100 current-period requests finished within this time. A histogram estimate; shown only with at least 100 samples.">P99</th>
      <th title="Shows whether the available samples support a comparison.">Comparison</th>
    </tr></thead>
    <tbody>
      {#each visible as row (row.key)}
        {@const rates = statusRates(rollupFor(rollups, row), outcomeFilter, kindFilter)}
        <tr class:chosen={row.key === selectedKey}>
          <td class="timeline-choice"><label><input type="checkbox" checked={timelineKeys.includes(row.key)} disabled={timelineKeys.length >= 8 && !timelineKeys.includes(row.key)} aria-label={'Compare timeline for ' + row.controller + '.' + row.action + ' in ' + (sources.get(row.sourceId) ?? row.sourceId) + ' · ' + row.kind + ' · ' + row.outcome} onchange={() => onToggleTimeline(row.key)} /><span>Compare</span></label></td>
          <td><button class="api-select" aria-pressed={row.key === selectedKey} onclick={() => onSelect(row.key)}>{row.controller}.{row.action}</button><small title={sources.get(row.sourceId) ?? row.sourceId}>{sources.get(row.sourceId) ?? row.sourceId}</small><PhaseBar phases={row.current.phases} delta={row.phaseDeltaMs} /></td>
          <td class="group-kind" data-label="Kind and outcome">{row.kind}<small>{outcomeRateText(rows, row, outcomeFilter)}</small></td><td data-label="Current requests" title={observedPartialTip(row.current.observedPartial)}>{row.current.count.toLocaleString()}</td><td data-label="Reference requests" title={observedPartialTip(row.reference.observedPartial)}>{row.reference.count.toLocaleString()}</td>
          <td class="numeric" data-label="Total impact" title={impactTip(row)}>{impactText(row.impactMs)}{#if row.significant}<small>significant shift</small>{/if}</td>
          <td class="numeric" data-label="P95 vs reference" title={p99Tip(row)}>{ratio(row.p95Ratio)}</td><td class="numeric" data-label="P95 difference (ms)">{row.p95DeltaMs == null ? 'N/A' : '≈ ' + row.p95DeltaMs.toFixed(1)}</td>
          <td class="numeric" data-label="4xx %" title={rates.tip}>{rates.client}</td>
          <td class="numeric" data-label="5xx %" title={rates.tip}>{rates.server}</td>
          <td class="numeric" data-label="P50" title="Histogram estimate; carries about ±5% bin-width error.">{quantileText(row.current.p50)}</td>
          <td class="numeric" data-label="P90" title="Histogram estimate; carries about ±5% bin-width error.">{quantileText(row.current.p90)}</td>
          <td class="numeric" data-label="P99" title={p99Tip(row)}>{quantileText(row.current.p99, p99NeedsSamples)}</td>
          <td class="group-status" data-label="Comparison"><span class="state-pill" title={stateDetails[row.state] ?? 'Comparison status'}>{stateLabels[row.state] ?? row.state.replaceAll('_', ' ')}</span>{#if loadBadge(loadByKey.get(row.key))}<span class="state-pill" title={loadTip(loadByKey.get(row.key) ?? 0)}>{loadBadge(loadByKey.get(row.key))}</span>{/if}</td>
        </tr>
      {:else}<tr><td colspan="14" class="table-empty">{rows.length ? 'No API groups match this search and view. Clear the search or choose All API groups.' : 'No API comparisons are available for this query.'}</td></tr>{/each}
    </tbody>
  </table>
</div>
<div class="pagination-row group-pagination">
  <span aria-live="polite">{ordered.length ? currentPage * pageSize + 1 : 0}–{Math.min((currentPage + 1) * pageSize, ordered.length)} of {ordered.length} groups{filtered.length !== rows.length ? ` (${rows.length} total)` : ''} · Page {currentPage + 1} of {pageCount}</span>
  <div class="page-actions"><button class="secondary-button" disabled={currentPage === 0} onclick={() => page = currentPage - 1}>Previous groups</button><button class="secondary-button" disabled={currentPage + 1 >= pageCount} onclick={() => page = currentPage + 1}>Next groups</button></div>
</div>
