<script lang="ts">
  import { onMount } from 'svelte';
  import type { Analysis } from '../lib/types';
  import { echarts, heroOption } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  import { trafficData } from '../lib/traffic';
  import { heroHasData, pulseFigures, singleKind, type Lens } from '../lib/pulse';
  import { metricLabels } from '../lib/presentation';

  // D0: five headline figures and one timeline. Every figure opens the ranked view with that lens.
  let { analysis, zone, onOpen }: { analysis: Analysis; zone: string; onOpen: (lens: Lens) => void } = $props();
  const figures = $derived(pulseFigures(analysis.overview ?? [], analysis.query.kind, analysis.query.metric, metricLabels[analysis.query.metric]));
  const windows = $derived(trafficData(analysis.query, analysis.loadWindows ?? [], Number.POSITIVE_INFINITY, 'p95').windows);
  const hasData = $derived(heroHasData(windows));
  const withLatency = $derived(singleKind(analysis.query.kind));
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
  <section class="hero" aria-label="Requests over time">
    <div class="hero-head">
      <h2>Requests over time</h2>
      <div class="hero-legend"><span><i class="swatch-count"></i>Requests per interval</span>{#if withLatency}<span><i class="swatch-p95"></i>P95 ({metricLabels[analysis.query.metric]})</span>{/if}</div>
    </div>
    <div class="hero-stage">
      <div class="hero-chart" class:chart-hidden={!hasData} bind:this={node} role="img" aria-hidden={!hasData} aria-label={'Requests per interval' + (withLatency ? ' with the 95th percentile' : '') + ', in ' + zone}></div>
      {#if !hasData}<div class="hero-empty"><strong>No requests in this period</strong><p>Widen the range, or check that the selected sources have audit rows for it.</p></div>{/if}
    </div>
    {#if !withLatency}<p class="figure-hint">Latency is shown for one request type at a time. Choose Sync, Async, Hangfire or HTTP to add the P95 line.</p>{/if}
  </section>
</section>
