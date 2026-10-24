<script lang="ts">
  import { formatCount } from '../lib/format';
  import { onMount } from 'svelte';
  import type { Analysis, BreakdownDim, BreakdownFilter, BreakdownRow, Query } from '../lib/types';
  import { echarts, failureSeriesFor, failureTrendOption } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  import BreakdownPanel from './BreakdownPanel.svelte';
  // cohortKeys narrows the trend to those comparison groups (an operation's); null keeps every cohort.
  let { analysis, query, sourceLabels, zone, onDrill, cohortKeys = null }: {
    analysis: Analysis; query: Query; sourceLabels: Map<string, string>; zone: string;
    onDrill: (filter: BreakdownFilter, row: BreakdownRow, dim: BreakdownDim) => void; cohortKeys?: string[] | null;
  } = $props();
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();
  const client = $derived(failureSeriesFor(analysis, cohortKeys, 'count4xx'));
  const server = $derived(failureSeriesFor(analysis, cohortKeys, 'count5xx'));
  // A slot is observed when any cohort has a point there; observed slots with no failure are real zeros.
  const observed = $derived(client.some(value => value != null));
  const total = (series: Array<number | null>) => series.reduce<number>((sum, value) => sum + (value ?? 0), 0);
  const clientTotal = $derived(total(client));
  const serverTotal = $derived(total(server));
  onMount(() => {
    if (!node) return;
    chart = echarts.init(node);
    const observer = new ResizeObserver(() => chart?.resize()); observer.observe(node);
    return () => { observer.disconnect(); chart?.dispose(); };
  });
  $effect(() => { void theme.epoch; if (chart) chart.setOption(failureTrendOption(analysis, zone, cohortKeys), true); });
</script>

<section class="failure-panel" id="failure-investigation" aria-label="Failure investigation">
  <section class="data-section">
    <div class="section-heading"><div><h3>Are requests failing, and with what status?</h3><p>The trend counts 4xx client errors and 5xx server errors separately; 5xx includes failed background jobs. Requests that are missing a timing or still pending are not counted as failures. Choose a status below to list the requests behind it.</p></div></div>
    <div class="failure-summary"><span><strong>{formatCount(clientTotal)}</strong> 4xx client errors</span><span><strong>{formatCount(serverTotal)}</strong> 5xx server errors and failed jobs</span></div>
    <div class="failure-chart-stage"><div class="failure-chart" class:chart-hidden={!observed} bind:this={node} role="img" aria-hidden={!observed} aria-label={'4xx and 5xx responses per interval, in ' + zone}></div>{#if !observed}<div class="failure-chart-empty"><strong>No observations in this period</strong><p>Gaps mean no requests were seen in that interval, not zero failures.</p></div>{/if}</div>
  </section>
  <BreakdownPanel {query} {sourceLabels} {onDrill} lockedDim="statusCode" panelId="failure-status" heading="Which statuses are the requests returning?" />
</section>
