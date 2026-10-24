<script lang="ts">
  import { MIN_SAMPLE, P99_MIN_SAMPLE } from '../lib/thresholds';
  import { onMount } from 'svelte';
  import type { Analysis } from '../lib/types';
  import { alignedPoints, baselineChange, echarts, failureField, isFailureSeries, multiTimelineOption, seriesColors, timelineDashArray, timelineLineStyle, timelineSeriesOptions, type PercentileSeries, type TimelineSeries } from '../lib/charts';
  import { chartRange } from '../lib/chartTime';
  import { theme } from '../lib/theme.svelte';
  import { zoomView } from '../lib/zoomView.svelte';
  import { zoomable } from '../lib/chartZoom';
  import { zoomCopy } from '../lib/copy/zoomCopy';
  let { analysis: whole, keys, sources, zone, onRemove }: { analysis: Analysis; keys: string[]; sources: Map<string, string>; zone: string; onRemove: (key: string) => void } = $props();
  // The lines follow the zoom window and each group's own reference for it. A group with no requests in the window keeps its row (and its colour) but draws nothing.
  const view = $derived(zoomView.analysisFor(whole));
  const analysis = $derived<Analysis>(view === whole ? whole : { ...view, apis: [...view.apis, ...whole.apis.filter(row => !view.apis.some(item => item.key === row.key))] });
  let mode = $state<'percent' | 'milliseconds'>('percent');
  let series = $state<TimelineSeries>('p95');
  const failure = $derived(isFailureSeries(series));
  const seriesName = $derived(timelineSeriesOptions.find(item => item.value === series)?.label ?? series);
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();
  const rows = $derived(keys.flatMap(key => { const row = analysis.apis.find(item => item.key === key); return row ? [row] : []; }));
  const plotted = $derived(rows.some(row => alignedPoints(analysis, row.key).some(point => isFailureSeries(series) ? failureField(point, series) != null : baselineChange(row, point, mode, series as PercentileSeries) != null)));
  onMount(() => {
    if (!node) return;
    chart = echarts.init(node);
    const observer = new ResizeObserver(() => chart?.resize()); observer.observe(node);
    return () => { observer.disconnect(); chart?.dispose(); };
  });
  const colors = $derived((void theme.epoch, seriesColors()));
  $effect(() => { void theme.epoch; if (chart) chart.setOption(multiTimelineOption(analysis, keys, sources, zone, mode, series), true); });
</script>

<section class="data-section multi-timeline" aria-label="Multiple API timeline">
  <div class="section-heading"><div><h3>When did APIs slow down together?</h3><p>Compare up to eight API groups using the Compare checkboxes above. The list below is the key: each group keeps its colour and line style. Each API is compared with its own earlier reference.</p></div><div class="timeline-controls"><label>Show<select aria-label="Timeline series" bind:value={series}>{#each timelineSeriesOptions as item (item.value)}<option value={item.value}>{item.label}</option>{/each}</select></label>{#if !failure}<label>Show change in<select aria-label="Timeline change unit" bind:value={mode}><option value="percent">Percent (%)</option><option value="milliseconds">Milliseconds (ms)</option></select></label>{/if}</div></div>
  {#if failure}<div class="timeline-explanation"><p><strong>Counts, not changes.</strong> Each point is the number of {series === 'count4xx' ? '4xx client-error' : '5xx server-error and failed-job'} responses in that interval. A gap means the interval had no observations; it is not zero failures. Requests missing a timing or still pending are never counted as failures.</p></div>{:else}
  <div class="timeline-explanation"><p><strong>Above zero = slower. Below zero = faster.</strong> {mode === 'percent' ? '+100% means twice as long; −50% means half as long.' : '+100 ms means one tenth of a second longer.'} Similar rises at the same time are a clue to investigate a shared cause.</p><p>These are estimates of the {series === 'p95' ? 'slower requests (P95)' : series === 'p50' ? 'median (P50)' : series.toUpperCase() + (series === 'p99' ? ', shown only with at least ' + P99_MIN_SAMPLE + ' timings in the slot and the reference' : '')}. Hollow, faded points have fewer than {MIN_SAMPLE} timings in the slot or an unreliable overall comparison. Gaps mean no observations, not zero response time. Hover to see counts and actual durations.</p></div>{/if}
  {#if rows.length}<ol class="timeline-selected">{#each rows as row, index (row.key)}<li><svg class="series-key" viewBox="0 0 28 10" aria-hidden="true"><line x1="1" y1="5" x2="27" y2="5" stroke={colors[index]} stroke-width={timelineLineStyle(index).width} stroke-dasharray={timelineDashArray(index)} stroke-linecap="round" /></svg><div><strong>{index + 1}. {row.controller}.{row.action}</strong><small>{sources.get(row.sourceId) ?? row.sourceId} · {row.kind} · {row.outcome}</small></div><button class="quiet-button" aria-label={'Remove ' + row.controller + '.' + row.action + ' from timeline'} onclick={() => onRemove(row.key)}>Remove</button></li>{/each}</ol>{/if}
  <div class="multi-chart-stage"><div class="multi-chart" class:chart-hidden={!plotted} bind:this={node} use:zoomable={{ chart: () => chart }} role="img" aria-hidden={!plotted} aria-label={failure ? 'Selected API ' + seriesName + ' per interval' : 'Selected API ' + series.toUpperCase() + ' changes from each own reference, in ' + mode}></div>{#if !plotted && rows.length && zoomView.zoomed && !zoomView.loading}<div class="multi-chart-empty"><strong>{zoomCopy.noData}</strong></div>{:else if !plotted}<div class="multi-chart-empty"><strong>{rows.length ? 'No comparable time points for these API groups' : 'Choose API groups to compare their timelines'}</strong><p>{rows.length ? 'Try milliseconds to inspect available absolute changes, or choose APIs with usable timings in both periods.' : 'Tick Compare beside an API name in the table above. Your choices stay selected while you search or change pages.'}</p></div>{/if}</div>
  <p class="timeline-footnote">Investigation: {chartRange(analysis.query.start, analysis.query.end, zone, { seconds: false })}. Reference: {chartRange(analysis.query.baselineStart, analysis.query.baselineEnd, zone, { seconds: false })}. Each point summarizes one chart interval; it cannot prove the exact moment an incident began. Selecting APIs or changing units uses this result and does not run another database query.</p>
</section>

<style>
  /* The selected list is the legend: its swatch repeats the line style, so groups that overlap on the chart stay tellable apart. */
  .series-key { flex: 0 0 28px; width: 28px; height: 10px; margin-top: 5px; overflow: visible; }
</style>
