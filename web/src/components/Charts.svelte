<script lang="ts">
  import { onMount } from 'svelte';
  import type { Analysis, Comparison } from '../lib/types';
  import { echarts, histogramOption, ratioOption, timelineOption } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  let { analysis, selected, zone, targetMs }: {
    analysis: Analysis; selected: Comparison | null; zone: string; targetMs: number | null
  } = $props();
  let timeline = $state<HTMLDivElement>();
  let ratio = $state<HTMLDivElement>();
  let histogram = $state<HTMLDivElement>();
  let charts = $state<ReturnType<typeof echarts.init>[]>([]);
  const hasCurrent = $derived((selected?.current.count ?? 0) > 0);
  const hasRatio = $derived(selected?.state === 'compared' && analysis.timeline.some(series => series.key === selected?.key && series.points.some(point => point.p95Ratio != null)));
  const hasDistribution = $derived((selected?.current.count ?? 0) + (selected?.reference.count ?? 0) > 0);
  onMount(() => {
    if (!timeline || !ratio || !histogram) return;
    charts = [timeline, ratio, histogram].map(node => echarts.init(node, undefined, { renderer: 'canvas' }));
    const observer = new ResizeObserver(() => charts.forEach(chart => chart.resize()));
    [timeline, ratio, histogram].forEach(node => observer.observe(node));
    return () => { observer.disconnect(); charts.forEach(chart => chart.dispose()); };
  });
  $effect(() => {
    if (!charts.length) return;
    void theme.epoch;
    charts[0].setOption(hasCurrent ? timelineOption(analysis, selected, zone, targetMs) : { series: [] }, true);
    charts[1].setOption(hasRatio ? ratioOption(analysis, selected, zone) : { series: [] }, true);
    charts[2].setOption(hasDistribution ? histogramOption(selected) : { series: [] }, true);
  });
</script>

<section class="chart-grid" aria-label="Selected API charts">
  <div class="plot-panel timeline-panel">
    <div class="plot-heading"><div><h3>Response time over time</h3><p>P95: about 95 of 100 requests finish within this time. Median: half finish sooner and half take longer. Dashed lines show the reference period.</p></div></div>
    <div class="chart-stage"><div class="chart" class:chart-hidden={!hasCurrent} bind:this={timeline} role="img" aria-label="Selected API response times over time" aria-hidden={!hasCurrent}></div>
    {#if !hasCurrent}<div class="chart-empty"><strong>No usable timings in this investigation period</strong><p>This API may have reference data only. Choose another API above or change the investigation dates.</p></div>{/if}</div>
  </div>
  <div class="plot-panel ratio-panel">
    <div class="plot-heading"><div><h3>Change versus reference</h3><p>2× means twice as long. 0.5× means half as long. 1× means no change in the P95 estimate.</p></div></div>
    <div class="chart-stage compact-stage"><div class="chart compact-chart" class:chart-hidden={!hasRatio} bind:this={ratio} role="img" aria-label="Selected API P95 compared with reference" aria-hidden={!hasRatio}></div>
    {#if !hasRatio}<div class="chart-empty"><strong>A slowdown ratio is unavailable</strong><p>{!hasCurrent ? 'This API has no usable timings in the investigation period.' : (selected?.reference.count ?? 0) === 0 ? 'This API has no usable timings in the reference period.' : selected?.state === 'insufficient_data' ? 'At least one period has fewer than 20 usable timings. More samples are needed for a meaningful comparison.' : 'The available timings do not support a meaningful comparison with the reference.'}</p></div>{/if}</div>
  </div>
  <div class="plot-panel histogram-panel">
    <div class="plot-heading"><div><h3>How requests are distributed</h3><p>Number of requests in each response-time range. Current and reference periods may contain different numbers of requests.</p></div></div>
    <div class="chart-stage compact-stage"><div class="chart compact-chart" class:chart-hidden={!hasDistribution} bind:this={histogram} role="img" aria-label="Selected API request counts by response time" aria-hidden={!hasDistribution}></div>
    {#if !hasDistribution}<div class="chart-empty"><strong>No usable response times</strong><p>Neither period contains timings to plot for this API.</p></div>{/if}</div>
  </div>
</section>
