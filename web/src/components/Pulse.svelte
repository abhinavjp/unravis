<script lang="ts">
  import { onMount } from 'svelte';
  import type { Analysis } from '../lib/types';
  import { echarts, heroOption } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  import { trafficData } from '../lib/traffic';
  import { heroHasData, pulseFigures, singleKind, type Lens } from '../lib/pulse';
  import { heroOutliers } from '../lib/chartHero';
  import { axisDuration } from '../lib/chartScale';
  import { metricLabels } from '../lib/presentation';
  import { plainIncomplete } from '../lib/copy/plainSummary';
  import Hint from './Hint.svelte';
  import PlainLine from './PlainLine.svelte';

  // D0: five headline figures and one timeline. Every figure opens the ranked view with that lens.
  let { analysis, zone, onOpen }: { analysis: Analysis; zone: string; onOpen: (lens: Lens) => void } = $props();
  const figures = $derived(pulseFigures(analysis.overview ?? [], analysis.query.kind, analysis.query.metric, metricLabels[analysis.query.metric]));
  const windows = $derived(trafficData(analysis.query, analysis.loadWindows ?? [], Number.POSITIVE_INFINITY, 'p95').windows);
  const hasData = $derived(heroHasData(windows));
  const withLatency = $derived(singleKind(analysis.query.kind));
  // Intervals whose slowest request is far above the usual P95: said in words under the chart as well as marked on it.
  const outliers = $derived(withLatency ? heroOutliers(windows) : { typical: null, flagged: [], labelled: [] });
  const slowestSeen = $derived(outliers.flagged.reduce((top, index) => Math.max(top, windows[index].max ?? 0), 0));
  // The headline in plain words: how many requests had a final result. Built only from the per-operation totals the page already holds.
  const plain = $derived.by(() => {
    const rollups = analysis.rollups ?? [];
    if (!rollups.length || rollups.some(row => row.completed == null)) return null;
    const sum = (pick: (row: (typeof rollups)[number]) => number | undefined) => rollups.reduce((total, row) => total + (pick(row) ?? 0), 0);
    return plainIncomplete({ completed: sum(row => row.completed), incomplete: sum(row => row.incomplete), pending: sum(row => row.pending), invalid: sum(row => row.invalid), window: { start: analysis.query.start, end: analysis.query.end } });
  });
  const toneOf = (id: Lens, value: string) => value === '0' || value === '—' ? (value === '—' ? 'muted' : '') : id === 'client' ? 'warn' : id === 'server' ? 'bad' : '';
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();
  onMount(() => {
    if (!node) return;
    chart = echarts.init(node);
    const observer = new ResizeObserver(() => chart?.resize()); observer.observe(node);
    return () => { observer.disconnect(); chart?.dispose(); };
  });
  $effect(() => { void theme.epoch; if (chart && hasData) chart.setOption(heroOption(windows, zone, withLatency), true); });
</script>

<section class="pulse" aria-label="Pulse">
  <div class="figures">
    {#each figures as figure (figure.id)}
      <button type="button" class="figure" data-tone={toneOf(figure.id, figure.value)} title={figure.tip} onclick={() => onOpen(figure.id)}>
        <span class="figure-label">{figure.label}</span>
        <span class="figure-value">{figure.value}</span>
        <span class="figure-hint">{figure.hint}</span>
        <span class="figure-go" aria-hidden="true">Where is it? ›</span>
      </button>
    {/each}
  </div>
  {#if plain}<PlainLine result={plain} hints={['incomplete-request']} />{/if}
  <section class="hero" aria-label="Requests over time">
    <div class="hero-head">
      <h2>Requests over time <span class="hero-zone">Times in {zone}</span></h2>
      <div class="hero-legend">
        <span><i class="swatch-count"></i>Requests per interval</span>
        {#if withLatency}
          <span><i class="swatch-p95"></i>P95 · {metricLabels[analysis.query.metric]}<Hint termId="p95" /></span>
          <span><i class="swatch-gap"></i>No timed requests between</span>
          <span><i class="swatch-max"></i>Slowest request in the interval</span>
        {/if}
      </div>
    </div>
    <div class="hero-stage">
      <div class="hero-chart" class:chart-hidden={!hasData} bind:this={node} role="img" aria-hidden={!hasData} aria-label={'Requests per interval' + (withLatency ? ' with the 95th percentile and the slowest request of each interval' : '') + ', in ' + zone}></div>
      {#if !hasData}<div class="hero-empty"><strong>No requests in this period</strong><p>Widen the range, or check that the selected sources have audit rows for it.</p></div>{/if}
    </div>
    {#if withLatency && outliers.flagged.length}<p class="figure-hint hero-note">{outliers.flagged.length === 1 ? 'One interval holds' : outliers.flagged.length + ' intervals hold'} a request far slower than usual, up to {axisDuration(slowestSeen)}. They are marked with a red diamond. This shows when slow requests happened; it does not show why.</p>{/if}
    {#if !withLatency}<p class="figure-hint">Latency is shown for one request type at a time. Choose Sync, Async, Hangfire or HTTP to add the P95 line.</p>{/if}
  </section>
</section>

<style>
  /* Figures: one row separated by hairlines, no boxes (the global .figure rules still supply the type; these take the fill, border and radius away). */
  .figures { gap: 0; grid-template-columns: repeat(5, minmax(0, 1fr)); border-top: var(--hairline-width) solid var(--hairline); border-bottom: var(--hairline-width) solid var(--hairline); }
  .figures .figure { padding: var(--space-4); gap: var(--space-1); background: transparent; border: 0; border-radius: 0; }
  .figures .figure + .figure { border-left: var(--hairline-width) solid var(--hairline); }
  .figures .figure:hover { background: var(--surface-2); }
  .figure-label { font-size: var(--fs-1); letter-spacing: .08em; }
  .figure-value { font-size: clamp(28px, 3.4vw, 40px); font-weight: 300; line-height: 1.1; }
  .figure-hint { font-size: var(--fs-2); }
  @media (max-width: 899px) {
    .figures { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .figures .figure:first-child { grid-column: 1 / -1; }
    .figures > .figure:last-child:nth-child(odd) { grid-column: auto; }
    .figures .figure + .figure { border-left: 0; border-top: var(--hairline-width) solid var(--hairline); }
    .figures .figure:nth-child(odd):not(:first-child) { border-left: var(--hairline-width) solid var(--hairline); }
  }
  /* Hero: the chart is the centre of the page, so it sits on the page, not in a card. */
  .hero { padding: var(--space-4) 0 0; background: transparent; border: 0; border-radius: 0; }
  .hero-head h2 { font-size: var(--fs-3); }
  .hero-zone { margin-left: var(--space-2); color: var(--text-2); font-size: var(--fs-2); font-weight: 400; }
  .hero-legend { align-items: center; }
  .hero-legend span { display: inline-flex; align-items: center; }
  .hero-legend .swatch-count { background: var(--accent); opacity: .3; }
  .hero-legend .swatch-gap { width: 14px; height: 0; border-radius: 0; border-top: 1.5px dashed var(--accent); background: transparent; }
  .hero-legend .swatch-max { width: 8px; height: 8px; border-radius: 0; background: var(--bad); transform: rotate(45deg); }
  .hero-note { margin: var(--space-2) 0 0; }
</style>
