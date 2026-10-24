<script lang="ts">
  import { onMount } from 'svelte';
  import type { HourGridResponse } from '../lib/rollup';
  import { echarts, palette } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  import {
    defaultOverlayDays, hourText, overlayCaption, overlayChoices, overlayCopy, overlayDay, overlayLook, overlayMetrics, overlayNotes,
    overlayOption, overlayRows, overlaySummary, type OverlayMetric
  } from '../lib/rollupOverlay';
  let { grid, zone }: { grid: HourGridResponse; zone: string } = $props();

  let metric = $state<OverlayMetric>('p95');
  // An empty pick means "use the default". A pick that is no longer in the loaded span also falls back to the default.
  let pickFirst = $state('');
  let pickSecond = $state('');
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();

  const choices = $derived(overlayChoices(grid));
  const defaults = $derived(defaultOverlayDays(choices));
  const has = (date: string) => choices.some(choice => choice.date === date);
  const firstDate = $derived(has(pickFirst) ? pickFirst : defaults?.[0] ?? '');
  const secondDate = $derived(has(pickSecond) ? pickSecond : defaults?.[1] ?? '');
  const same = $derived(!!firstDate && firstDate === secondDate);
  const first = $derived(firstDate ? overlayDay(grid, firstDate) : null);
  const second = $derived(secondDate ? overlayDay(grid, secondDate) : null);
  const ready = $derived(!!first && !!second && !same);
  const summary = $derived(first && second ? overlaySummary(first, second, metric, zone) : '');
  const notes = $derived(first && second ? overlayNotes([first, second], metric) : []);
  const rows = $derived(first && second ? overlayRows(first, second, metric) : []);

  onMount(() => {
    if (!node) return;
    chart = echarts.init(node);
    const observer = new ResizeObserver(() => chart?.resize()); observer.observe(node);
    return () => { observer.disconnect(); chart?.dispose(); chart = undefined; };
  });
  $effect(() => {
    void theme.epoch;
    if (chart && ready && first && second) chart.setOption(overlayOption(first, second, metric, palette()), true);
  });
  const colors = $derived((void theme.epoch, palette().series));
</script>

<div class="ov">
  <p class="ov-intro">{overlayCopy.intro}</p>
  {#if choices.length < 2}
    <p class="table-empty">{overlayCopy.tooFew}</p>
  {:else}
    <div class="ov-controls">
      <label for="ov-first">{overlayCopy.firstDay}</label>
      <select id="ov-first" value={firstDate} onchange={event => pickFirst = event.currentTarget.value}>
        {#each choices as choice (choice.date)}<option value={choice.date}>{choice.label}</option>{/each}
      </select>
      <label for="ov-second">{overlayCopy.secondDay}</label>
      <select id="ov-second" value={secondDate} onchange={event => pickSecond = event.currentTarget.value}>
        {#each choices as choice (choice.date)}<option value={choice.date}>{choice.label}</option>{/each}
      </select>
      <div class="ov-metric" role="group" aria-label={overlayCopy.showLabel}>
        {#each overlayMetrics as item (item.id)}
          <button type="button" class="chip" aria-pressed={metric === item.id} class:on={metric === item.id} onclick={() => metric = item.id}>{item.label}</button>
        {/each}
      </div>
    </div>
    {#if same}<p class="inline-error" role="alert">{overlayCopy.sameDay}</p>{/if}
    <div class="ov-body" class:ov-off={!ready}>
      {#if first && second}
        <ul class="ov-key" aria-hidden="true">
          {#each [first, second] as day, index (index)}
            <li>
              <svg viewBox="0 0 28 10" width="28" height="10"><line x1="1" y1="5" x2="27" y2="5" stroke={colors[index]} stroke-width={overlayLook[index].width} stroke-linecap="round" /></svg>
              <span>{day.label}</span>
            </li>
          {/each}
          {#if metric === 'count'}
            <li>
              <svg viewBox="0 0 28 10" width="28" height="10"><line x1="1" y1="5" x2="27" y2="5" stroke="currentColor" stroke-width="2" stroke-dasharray="4 3" stroke-linecap="round" /></svg>
              <span>{overlayCopy.keyFew}</span>
            </li>
          {/if}
        </ul>
      {/if}
      <div class="ov-chart" bind:this={node} role="img" aria-label={summary}></div>
      <p class="ov-caption">{overlayCaption(metric, zone)}</p>
      <ul class="ov-notes">{#each notes as note}<li>{note}</li>{/each}</ul>
      {#if first && second}
        <details class="ov-table">
          <summary>{overlayCopy.tableToggle}</summary>
          <!-- A scroll area a keyboard user can reach, so a wide table can be scrolled without a mouse. -->
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div class="table-scroll" role="region" aria-label={overlayCopy.heading} tabindex="0">
            <table class="ov-values">
              <caption class="sr-only">{overlayCopy.tableCaption} {overlayCaption(metric, zone)}</caption>
              <thead><tr><th scope="col">{overlayCopy.hourHeader}</th><th scope="col">{first.label}</th><th scope="col">{second.label}</th></tr></thead>
              <tbody>
                {#each rows as row, hour (hour)}
                  <tr><th scope="row">{hourText(hour)}</th><td>{row.first}</td><td>{row.second}</td></tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>
      {/if}
    </div>
  {/if}
</div>

<style>
  .ov { display: flex; flex-direction: column; gap: var(--space-2, 8px); }
  .ov-intro { margin: 0; color: var(--text-2); font-size: 13px; max-width: 70ch; }
  .ov-controls { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 13px; }
  .ov-controls select { max-width: 100%; }
  .ov-metric { display: flex; gap: 6px; flex-wrap: wrap; }
  .ov-body { display: flex; flex-direction: column; gap: var(--space-2, 8px); }
  .ov-body.ov-off { display: none; }
  .ov-key { list-style: none; margin: 0; padding: 0; display: flex; gap: 14px; flex-wrap: wrap; font-size: 12px; color: var(--text-2); }
  .ov-key li { display: inline-flex; align-items: center; gap: 6px; }
  .ov-chart { width: 100%; height: 300px; }
  .ov-caption { margin: 0; color: var(--text-2); font-size: 12px; }
  .ov-notes { margin: 0; padding-left: 18px; color: var(--text-2); font-size: 12px; max-width: 70ch; }
  .ov-table summary { cursor: pointer; font-size: 13px; color: var(--text); }
  .ov-values { border-collapse: collapse; font-size: 12px; margin-top: 6px; }
  .ov-values th, .ov-values td { padding: 3px 12px 3px 0; text-align: left; white-space: nowrap; border-bottom: 1px solid var(--line-soft); }
  .ov-values thead th { color: var(--text-2); font-weight: 600; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
</style>
