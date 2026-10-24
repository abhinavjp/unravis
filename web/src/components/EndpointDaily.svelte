<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, api } from '../lib/api';
  import { DAILY_DAY_CHOICES, DEFAULT_DAILY_DAYS, STEP_TEXT, dailyChartRuns, dailyRows, dailyWindow } from '../lib/patterns';
  import type { EndpointDaily } from '../lib/types';

  // The daily response times of one operation: a small chart and a table, one row for each whole local day.
  // It reads the log one day at a time, so it runs when opened and again only when the span changes.
  let { operation, sourceIds, end, zone, onClose }: { operation: string; sourceIds: string[]; end: string; zone: string; onClose: () => void } = $props();
  type Phase = { kind: 'loading' } | { kind: 'error'; text: string } | { kind: 'done'; daily: EndpointDaily };
  let phase = $state<Phase>({ kind: 'loading' });
  let days = $state(String(DEFAULT_DAILY_DAYS));
  let heading = $state<HTMLHeadingElement>();

  const rows = $derived(phase.kind === 'done' ? dailyRows(phase.daily.days) : []);
  const runs = $derived(dailyChartRuns(rows));
  const WIDTH = 300, HEIGHT = 56;

  onMount(() => { heading?.focus(); });
  $effect(() => {
    const span = dailyWindow({ end }, Number(days));
    const ids = [...sourceIds];
    const controller = new AbortController();
    phase = { kind: 'loading' };
    if (!span) { phase = { kind: 'error', text: STEP_TEXT.dailyFailed }; return; }
    api.endpointDaily(operation, ids, span.start, span.end, zone, controller.signal).then(
      (daily) => { if (!controller.signal.aborted) phase = { kind: 'done', daily }; },
      (cause: unknown) => {
        if (controller.signal.aborted) return;
        phase = { kind: 'error', text: cause instanceof ApiError && cause.message ? cause.message : STEP_TEXT.dailyFailed };
      }
    );
    return () => controller.abort();
  });
</script>

<section class="endpoint-daily" aria-labelledby="endpoint-daily-title">
  <div class="daily-head">
    <h3 id="endpoint-daily-title" tabindex="-1" bind:this={heading}>{STEP_TEXT.dailyTitle}: {operation}</h3>
    <label class="daily-days">{STEP_TEXT.dailyDays}
      <select bind:value={days}>
        {#each DAILY_DAY_CHOICES as choice (choice)}<option value={String(choice)}>{choice}</option>{/each}
      </select>
    </label>
    <button type="button" class="secondary-button" onclick={onClose}>{STEP_TEXT.dailyClose}</button>
  </div>
  <p class="daily-note">{STEP_TEXT.dailyIntro}</p>
  <div aria-live="polite">
    {#if phase.kind === 'loading'}
      <p class="daily-note" role="status">{STEP_TEXT.dailyLoading}</p>
    {:else if phase.kind === 'error'}
      <div class="inline-error" role="alert">{phase.text}</div>
    {:else}
      {#each phase.daily.warnings ?? [] as warning (warning)}<div class="notice" role="status">{warning}</div>{/each}
      {#if rows.length === 0}
        <p class="daily-note">{STEP_TEXT.dailyEmpty}</p>
      {:else}
        {#if runs.length}
          <svg class="daily-chart" viewBox={`-4 -4 ${WIDTH + 8} ${HEIGHT + 8}`} role="img" aria-label={STEP_TEXT.dailyChart + ', ' + rows.length + ' days, table below'} preserveAspectRatio="none">
            {#each runs as run, i (i)}
              {#if run.length === 1}
                <circle cx={run[0].split(',')[0]} cy={run[0].split(',')[1]} r="2.5" />
              {:else}
                <polyline points={run.join(' ')} fill="none" />
              {/if}
            {/each}
          </svg>
        {/if}
        <div class="table-scroll daily-scroll">
          <table class="comparison-table daily-table">
            <caption class="sr-only">{STEP_TEXT.dailyTitle}: {operation}</caption>
            <thead><tr><th scope="col">{STEP_TEXT.dailyColDay}</th><th scope="col" class="numeric">{STEP_TEXT.dailyColRequests}</th><th scope="col" class="numeric">{STEP_TEXT.dailyColMedian}</th><th scope="col" class="numeric">{STEP_TEXT.dailyColSlowest}</th><th scope="col"><span class="sr-only">Note</span></th></tr></thead>
            <tbody>
              {#each rows as row (row.key)}
                <tr>
                  <th scope="row" data-label={STEP_TEXT.dailyColDay}>{row.date}</th>
                  <td class="numeric" data-label={STEP_TEXT.dailyColRequests}>{row.count}</td>
                  <td class="numeric" data-label={STEP_TEXT.dailyColMedian}>{row.median}</td>
                  <td class="numeric" data-label={STEP_TEXT.dailyColSlowest}>{row.slowest}</td>
                  <td class="daily-flag">{row.note}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {/if}
  </div>
</section>

<style>
  .endpoint-daily { display: grid; gap: 8px; margin: 12px 0; padding: 10px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); min-width: 0; }
  .daily-head { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; }
  .daily-head h3 { flex: 1 1 200px; margin: 0; font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .daily-head h3:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .daily-days { display: flex; align-items: center; gap: 6px; color: var(--text-2); font-size: var(--fs-3); }
  .daily-days select { min-height: 32px; padding: 4px 8px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); font-size: var(--fs-3); }
  .daily-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .daily-chart { width: 100%; max-width: 480px; height: 64px; }
  .daily-chart polyline { stroke: var(--accent); stroke-width: 2; vector-effect: non-scaling-stroke; stroke-linejoin: round; }
  .daily-chart circle { fill: var(--accent); }
  .endpoint-daily > * { min-width: 0; }
  .daily-scroll { max-height: 320px; overflow: auto; }
  .daily-table { min-width: 0; }
  .daily-table td:nth-child(2) { min-width: 0; max-width: none; white-space: nowrap; }
  .daily-table tbody tr { cursor: default; }
  .daily-table tbody tr:hover { background: none; }
  @media (max-width: 720px) {
    .daily-table td:nth-child(2) { grid-column: auto; }
  }
  .daily-flag { color: var(--text-2); font-size: var(--fs-1); }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>
