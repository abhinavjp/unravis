<script lang="ts">
  import { onMount } from 'svelte';
  import type { Analysis } from '../lib/types';
  import { alignedPoints, baselineChange, echarts, multiTimelineOption, timelineColors } from '../lib/charts';
  let { analysis, keys, sources, zone, onRemove }: { analysis: Analysis; keys: string[]; sources: Map<string, string>; zone: string; onRemove: (key: string) => void } = $props();
  let mode = $state<'percent' | 'milliseconds'>('percent');
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();
  const rows = $derived(keys.flatMap(key => { const row = analysis.apis.find(item => item.key === key); return row ? [row] : []; }));
  const plotted = $derived(rows.some(row => alignedPoints(analysis, row.key).some(point => baselineChange(row, point, mode) != null)));
  const stamp = (value: string) => new Intl.DateTimeFormat(undefined, { timeZone: zone, month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(value));
  onMount(() => {
    if (!node) return;
    chart = echarts.init(node);
    const observer = new ResizeObserver(() => chart?.resize()); observer.observe(node);
    return () => { observer.disconnect(); chart?.dispose(); };
  });
  $effect(() => { if (chart) chart.setOption(multiTimelineOption(analysis, keys, sources, zone, mode), true); });
</script>

<section class="data-section multi-timeline" aria-label="Multiple API timeline">
  <div class="section-heading"><div><h3>When did APIs slow down together?</h3><p>Compare up to eight API groups using the Timeline checkboxes above. Each API is compared with its own earlier reference.</p></div><label>Show change in<select aria-label="Timeline change unit" bind:value={mode}><option value="percent">Percent (%)</option><option value="milliseconds">Milliseconds (ms)</option></select></label></div>
  <div class="timeline-explanation"><p><strong>Above zero = slower. Below zero = faster.</strong> {mode === 'percent' ? '+100% means twice as long; −50% means half as long.' : '+100 ms means one tenth of a second longer.'} Similar rises at the same time are a clue to investigate a shared cause.</p><p>These are estimates for the slower requests (P95). Hollow, faded points have fewer than 20 timings in the slot or an unreliable overall comparison. Gaps mean no observations, not zero response time. Hover to see counts and actual durations.</p></div>
  {#if rows.length}<ol class="timeline-selected">{#each rows as row, index (row.key)}<li><span class="series-marker" style:background={timelineColors[index]} aria-hidden="true"></span><div><strong>{index + 1}. {row.controller}.{row.action}</strong><small>{sources.get(row.sourceId) ?? row.sourceId} · {row.kind} · {row.outcome}</small></div><button class="quiet-button" aria-label={'Remove ' + row.controller + '.' + row.action + ' from timeline'} onclick={() => onRemove(row.key)}>Remove</button></li>{/each}</ol>{/if}
  <div class="multi-chart-stage"><div class="multi-chart" class:chart-hidden={!plotted} bind:this={node} role="img" aria-hidden={!plotted} aria-label={'Selected API P95 changes from each own reference, in ' + mode}></div>{#if !plotted}<div class="multi-chart-empty"><strong>{rows.length ? 'No comparable time points for these API groups' : 'Choose API groups to compare their timelines'}</strong><p>{rows.length ? 'Try milliseconds to inspect available absolute changes, or choose APIs with usable timings in both periods.' : 'Tick Timeline beside an API name in the table above. Your choices stay selected while you search or change pages.'}</p></div>{/if}</div>
  <p class="timeline-footnote">Investigation: {stamp(analysis.query.start)}–{stamp(analysis.query.end)}. Reference: {stamp(analysis.query.baselineStart)}–{stamp(analysis.query.baselineEnd)}. Time zone: {zone}. Each point summarizes one chart interval; it cannot prove the exact moment an incident began. Selecting APIs or changing units uses this result and does not run another database query.</p>
</section>
