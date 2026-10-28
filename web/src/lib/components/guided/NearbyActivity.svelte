<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '../../api';
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { defaultAround, durationBand, NEARBY_ROWS, nearbyWindow, QUICK_SPANS, type DurationBand } from '../../guided/nearby';
  import { fmtMs } from '../../guided/stepModel';
  import type { RequestRecord } from '../../types';

  // Nearby activity: what the audit source recorded around one moment. Each call shows its API, its start and its time, and nothing about who
  // sent it: no user, address or URL. The SQL Server, MongoDB and Host lanes say Cannot test yet until the case holds those sources.
  let { store, sourceIds }: { store: GuidedStore; sourceIds: string[] } = $props();

  // The starting moment is read once, when the panel opens; after that the person owns the field.
  let around = $state(untrack(() => defaultAround(store.result?.facts.onset.t0, store.frame.incidentStart)));
  let before = $state(5);
  let after = $state(5);
  let phase = $state<'idle' | 'loading' | 'done' | 'failed'>('idle');
  let rows = $state.raw<RequestRecord[]>([]);
  let more = $state(false);
  let touched = $state(false);
  let controller: AbortController | null = null;
  let busy = false;

  const win = $derived(nearbyWindow(around, Number(before), Number(after)));
  const longest = $derived(rows.reduce((m, r) => Math.max(m, r.durationMs ?? 0), 0));
  const BAND_WORD: Record<DurationBand, string> = { fast: T.nearbyBandFast, slow: T.nearbyBandSlow, 'very-slow': T.nearbyBandVerySlow, unknown: T.nearbyBandUnknown };

  async function run() {
    if (busy) return;
    if (!win || sourceIds.length === 0) { phase = 'failed'; return; }
    busy = true;
    const mine = new AbortController();
    controller = mine;
    phase = 'loading';
    try {
      const page = await api.activity({ sourceIds: [...sourceIds], start: win.start, end: win.end, cursor: '', limit: NEARBY_ROWS }, mine.signal);
      if (mine.signal.aborted) return;
      rows = page?.rows ?? [];
      more = !!page?.nextCursor || !!page?.truncated;
      phase = 'done';
    } catch {
      if (!mine.signal.aborted) phase = 'failed';
    } finally {
      busy = false;
    }
  }
  $effect(() => () => controller?.abort());

  const clock = (iso: string | null) => (iso && iso.length >= 19 ? iso.slice(11, 19) : '');
  const pick = (b: number, a: number) => { before = b; after = a; touched = true; };
</script>

<section class="nearby" aria-labelledby="nearby-title">
  <header><h3 id="nearby-title">{T.nearbyHeading}</h3></header>
  <p class="lead">{T.nearbyLead}</p>
  {#if sourceIds.length === 0}
    <p class="notice">{T.nearbyNoCase}</p>
  {:else}
    <div class="form">
      <label>{T.around} (UTC)<input type="datetime-local" bind:value={around} oninput={() => (touched = true)} /></label>
      <label>{T.before}<input type="number" min="0" max="120" step="1" bind:value={before} oninput={() => (touched = true)} /></label>
      <label>{T.after}<input type="number" min="0" max="120" step="1" bind:value={after} oninput={() => (touched = true)} /></label>
    </div>
    <div class="picks" role="group" aria-label={T.picks}>
      {#each QUICK_SPANS as q (q.label)}<button type="button" class="quiet-button" onclick={() => pick(q.before, q.after)}>{q.label}</button>{/each}
    </div>
    {#if touched && !win}<p class="note" role="status">{T.nearbyBad}</p>{/if}
    <div><button type="button" class="primary-button" disabled={!win || phase === 'loading'} onclick={() => void run()}>{T.nearbyRun}</button></div>
  {/if}

  {#if phase === 'loading'}<p class="loading-state" role="status">{T.running}</p>{/if}
  {#if phase === 'failed' && win}<p class="inline-error" role="alert">{T.nearbyFailed}</p>{/if}

  <div class="lanes">
    <section class="lane" aria-labelledby="lane-api">
      <h4 id="lane-api">{T.laneApi}</h4>
      {#if phase !== 'done'}
        <p class="note">{T.nearbyScale}</p>
      {:else if rows.length === 0}
        <p class="note">{T.nearbyEmpty}</p>
      {:else}
        <ul class="calls">
          {#each rows as row (row.id)}
            {@const band = durationBand(row.durationMs)}
            <li>
              <span class="when">{clock(row.initiatedAt)}</span>
              <span class="route">{row.controller}.{row.action}</span>
              <span class="bar" role="img" aria-label={`${fmtMs(row.durationMs ?? undefined)}, ${BAND_WORD[band]}`}>
                <span class={'fill band-' + band} style={`width:${longest > 0 ? Math.max(2, Math.round(((row.durationMs ?? 0) / longest) * 100)) : 0}%`}></span>
              </span>
              <span class="ms">{fmtMs(row.durationMs ?? undefined)}</span>
            </li>
          {/each}
        </ul>
        {#if more}<p class="note">{T.nearbyMore(NEARBY_ROWS)}</p>{/if}
        <p class="note">{T.nearbyScale}</p>
      {/if}
    </section>
    {#each [T.laneSql, T.laneMongo, T.laneHost] as lane (lane)}
      <section class="lane">
        <h4>{lane}</h4>
        <p class="note">{T.laneNone}</p>
      </section>
    {/each}
  </div>
</section>

<style>
  .nearby { display: grid; gap: 12px; min-width: 0; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  h3 { margin: 0; font-size: 16px; }
  h4 { margin: 0; font-size: 14px; }
  .lead, .note { margin: 0; font-size: 13px; }
  .note { color: var(--text-2); font-size: 12px; }
  .form { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr)); gap: 8px; }
  .form label { display: grid; gap: 4px; font-size: 13px; }
  .picks { display: flex; flex-wrap: wrap; gap: 8px; }
  .lanes { display: grid; gap: 12px; }
  .lane { display: grid; gap: 8px; min-width: 0; padding: 12px; border: 1px solid var(--line-soft); border-radius: var(--radius-sm); }
  .calls { display: grid; gap: 4px; margin: 0; padding: 0; list-style: none; max-height: 360px; overflow: auto; }
  .calls li { display: grid; grid-template-columns: 64px minmax(0, 1.4fr) minmax(40px, 1fr) 72px; align-items: center; gap: 8px; font-size: 12px; font-variant-numeric: tabular-nums; }
  .route { overflow-wrap: anywhere; }
  .bar { display: block; height: 10px; background: var(--chart-empty); border-radius: var(--radius-sm); overflow: hidden; }
  .fill { display: block; height: 100%; }
  .band-fast { background: var(--suspect-green); }
  .band-slow { background: var(--suspect-orange); }
  .band-very-slow { background-color: var(--suspect-red); background-image: var(--hatch); }
  .band-unknown { background: var(--suspect-grey); }
  .ms { text-align: right; }
  @media (max-width: 480px) {
    .calls li { grid-template-columns: 56px minmax(0, 1fr) 64px; }
    .bar { grid-column: 1 / -1; grid-row: 2; }
  }
</style>
