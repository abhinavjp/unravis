<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { app, scope, setWindowFromHour } from '../lib/appState.svelte';
  import { rollupCopy, rollupParams, rollupRanges, zoneNote, type DailyResponse, type HourGridResponse } from '../lib/rollup';
  import DailyTable from './DailyTable.svelte';
  import HourHeatmap from './HourHeatmap.svelte';
  import DayOverlay from './DayOverlay.svelte';
  import { overlayCopy } from '../lib/rollupOverlay';
  import { WAIT_TEXT } from '../lib/copy/waitCopy';
  import { createSlowRead } from '../lib/slowRead.svelte';

  // Closed until asked: opening it is what reads the sources, so a closed view never scans anything.
  let open = $state(false);
  let rangeId = $state('7d');
  let daily = $state<DailyResponse | null>(null);
  let grid = $state<HourGridResponse | null>(null);
  let loading = $state(false);
  let error = $state('');
  let refused = $state(false);
  // Set by Cancel, cleared by the next read: the view then says what happened instead of going quiet.
  let cancelled = $state(false);
  const waiting = createSlowRead(() => loading);
  let controller: AbortController | undefined;
  let sequence = 0;

  const zone = $derived(scope.zone);
  const sourceIds = $derived(scope.query.sourceIds);
  const failing = $derived((daily?.sources ?? []).filter(source => !source.ok));
  const requestKey = $derived(JSON.stringify([open, rangeId, zone, sourceIds]));

  async function load() {
    controller?.abort();
    controller = new AbortController();
    const run = ++sequence;
    const range = rollupRanges.find(item => item.id === rangeId) ?? rollupRanges[1];
    const base = new Date(app.info?.now ?? '');
    const params = rollupParams(range, Number.isFinite(base.getTime()) ? base : new Date(), zone, [...sourceIds]);
    loading = true; error = ''; refused = false; cancelled = false;
    try {
      // Both reads start together: they ask the same question of the same sources and do not depend on each other.
      const [days, hours] = await Promise.all([api.daily(params, controller.signal), api.hourGrid(params, controller.signal)]);
      if (run !== sequence) return;
      daily = days; grid = hours;
    } catch (cause) {
      if (run !== sequence || (cause instanceof DOMException && cause.name === 'AbortError')) return;
      daily = null; grid = null;
      refused = cause instanceof ApiError && cause.status === 409;
      error = cause instanceof Error && cause.message ? cause.message : rollupCopy.error;
    } finally { if (run === sequence) loading = false; }
  }

  function cancel() {
    sequence++; controller?.abort(); loading = false; cancelled = true;
  }

  $effect(() => {
    const key = requestKey;
    untrack(() => {
      if (!open) { sequence++; controller?.abort(); loading = false; cancelled = false; return; }
      if (!sourceIds.length) { sequence++; controller?.abort(); daily = null; grid = null; loading = false; error = ''; cancelled = false; return; }
      void key;
      void load();
    });
  });
  onMount(() => () => { sequence++; controller?.abort(); });
</script>

<section class="data-section rollup" aria-label="Daily and hourly view">
  <div class="rollup-bar">
    <button type="button" class="quiet-button rollup-toggle" aria-expanded={open} aria-controls="rollup-body" onclick={() => open = !open}>{rollupCopy.openLabel} {open ? '▾' : '▸'}</button>
  </div>
  {#if open}
    <div class="rollup-body" id="rollup-body">
      <p class="rollup-intro">{rollupCopy.intro}</p>
      <div class="rollup-controls">
        <label for="rollup-range">{rollupCopy.spanLabel}</label>
        <select id="rollup-range" bind:value={rangeId}>
          {#each rollupRanges as item (item.id)}<option value={item.id}>{item.label}</option>{/each}
        </select>
        <span class="rollup-zone">{zoneNote(zone)}</span>
      </div>
      {#if !sourceIds.length}
        <p class="table-empty">Choose at least one source to see this view.</p>
      {:else}
        <div aria-live="polite" class="rollup-status">
          {#if loading}
            <span>{waiting.slow ? WAIT_TEXT.stillReading : rollupCopy.reading}</span>
            <button type="button" class="secondary-button" title={WAIT_TEXT.cancelTitle} onclick={cancel}>{WAIT_TEXT.cancel}</button>
          {:else if cancelled}
            <span>{WAIT_TEXT.cancelledRead}</span>
            <button type="button" class="secondary-button" title={WAIT_TEXT.retryTitle} onclick={() => void load()}>{WAIT_TEXT.retry}</button>
          {/if}
        </div>
        {#if error}
          <div class="inline-error" role="alert">
            <span>{error}{#if refused} {rollupCopy.refusedHelp}{/if}</span>
            <button type="button" class="secondary-button" title={WAIT_TEXT.retryTitle} onclick={() => void load()}>{WAIT_TEXT.retry}</button>
          </div>
        {/if}
        {#if failing.length}<div class="inline-error" role="alert">{#each failing as source}<span>{app.sourceLabels.get(source.sourceId) ?? source.sourceId}: {source.error || 'This source did not answer.'} </span>{/each}</div>{/if}
        {#if daily && grid}
          <div class="rollup-block" aria-busy={loading}>
            <h4>Each day</h4>
            <DailyTable days={daily.days} {zone} />
            <h4>Each hour</h4>
            <HourHeatmap {grid} {zone} onChoose={setWindowFromHour} />
            <h4>{overlayCopy.heading}</h4>
            <DayOverlay {grid} {zone} />
          </div>
        {/if}
      {/if}
    </div>
  {/if}
</section>

<style>
  .rollup { margin-top: var(--space-3, 12px); }
  .rollup-bar { padding: 4px 6px; }
  .rollup-toggle { font-size: 13px; font-weight: 600; color: var(--text); }
  .rollup-body { display: flex; flex-direction: column; gap: var(--space-2, 8px); padding: 4px 14px 14px; }
  .rollup-intro { margin: 0; color: var(--text-2); font-size: 13px; max-width: 70ch; }
  .rollup-controls { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 13px; }
  .rollup-zone { color: var(--text-2); font-size: 12px; }
  .rollup-status { min-height: 1.2em; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; color: var(--text-2); font-size: 12px; }
  .rollup-body :global(.inline-error) { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .rollup-block { display: flex; flex-direction: column; gap: var(--space-2, 8px); }
  .rollup-block h4 { margin: 8px 0 0; font-size: 13px; font-weight: 600; }
</style>
