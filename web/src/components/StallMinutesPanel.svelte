<script lang="ts">
  import { api } from '../lib/api';
  import { formatTime } from '../lib/time';
  import { STALL_MINUTES_TEXT as TEXT, bucketMinutes, emptyWindowLine, loadMinuteRows, minuteWindow, peakLine, unplacedLine, type MinuteCounts } from '../lib/stallMinutes';

  // The per-minute view of one episode. It reads the Activity endpoint only when opened, one episode at a time.
  let { episode, sourceIds, stallMs, zone }: { episode: { start: string; end: string }; sourceIds: string[]; stallMs: number; zone: string } = $props();
  type Phase =
    | { kind: 'closed' }
    | { kind: 'running' }
    | { kind: 'error'; text: string }
    | { kind: 'done'; counts: MinuteCounts; partial: boolean; clamped: boolean; failed: string[] };
  let phase = $state<Phase>({ kind: 'closed' });
  let controller: AbortController | null = null;
  let seq = 0;

  const minuteLabel = (ms: number) => formatTime(new Date(ms).toISOString(), { zone, precision: 'minute' });
  const clockLabel = (ms: number) => formatTime(new Date(ms).toISOString(), { zone, precision: 'minute', date: false, suffix: false });

  $effect(() => () => controller?.abort());

  async function open() {
    const window = minuteWindow(episode);
    if (!window) { phase = { kind: 'error', text: TEXT.failed }; return; }
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    const turn = ++seq;
    phase = { kind: 'running' };
    try {
      const load = await loadMinuteRows((input, signal) => api.activity(input, signal), sourceIds, window, mine.signal);
      if (mine.signal.aborted || turn !== seq) return;
      phase = { kind: 'done', counts: bucketMinutes(load.rows, window, stallMs), partial: load.partial, clamped: window.clamped, failed: load.failed.map((f) => f.sourceId) };
    } catch (cause) {
      if (mine.signal.aborted || turn !== seq) return;
      phase = { kind: 'error', text: cause instanceof Error && cause.message ? cause.message : TEXT.failed };
    }
  }
  function close() { controller?.abort(); seq++; phase = { kind: 'closed' }; }
</script>

<div class="stall-minutes">
  {#if phase.kind === 'closed'}
    <button type="button" class="minutes-toggle" onclick={open}>{TEXT.open}</button>
  {:else}
    <div class="minutes-head">
      <h4>{TEXT.title}</h4>
      <button type="button" class="minutes-toggle" onclick={close}>{TEXT.close}</button>
    </div>
    <p class="patterns-note">{TEXT.intro}</p>
    <div aria-live="polite">
      {#if phase.kind === 'running'}<p class="patterns-note">{TEXT.running}</p>{/if}
      {#if phase.kind === 'error'}
        <div class="inline-error" role="alert">{phase.text} <button type="button" class="minutes-toggle" onclick={open}>{TEXT.retry}</button></div>
      {/if}
      {#if phase.kind === 'done'}
        {@const counts = phase.counts}
        {@const unplaced = unplacedLine(counts)}
        {@const peak = Math.max(1, ...counts.buckets.map((b) => b.stallsInFlight))}
        {#if counts.rows === 0}
          <p class="patterns-note">{TEXT.empty} {emptyWindowLine()}</p>
        {:else}
          <p class="minutes-peak">{peakLine(counts.buckets, minuteLabel)}</p>
          <div class="minutes-scroll">
            <table class="patterns-table inner">
              <caption class="sr-only">{TEXT.tableLabel}</caption>
              <thead><tr><th scope="col">{TEXT.minute}</th><th scope="col" class="num">{TEXT.started}</th><th scope="col" class="num">{TEXT.completed}</th><th scope="col" class="num">{TEXT.inFlight}</th><th scope="col" class="num">{TEXT.stalls}</th></tr></thead>
              <tbody>
                {#each counts.buckets as bucket (bucket.startMs)}
                  <tr class:quiet={bucket.inFlight === 0 && bucket.started === 0 && bucket.completed === 0}>
                    <th scope="row">{clockLabel(bucket.startMs)}</th>
                    <td class="num">{bucket.started}</td>
                    <td class="num">{bucket.completed}</td>
                    <td class="num">{bucket.inFlight}</td>
                    <td class="num"><span class="bar" style:width={(bucket.stallsInFlight / peak) * 48 + 'px'} aria-hidden="true"></span> {bucket.stallsInFlight}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
        {#if unplaced}<p class="patterns-note">{unplaced}</p>{/if}
        {#if phase.clamped}<div class="notice" role="status">{TEXT.clamped}</div>{/if}
        {#if phase.partial}<div class="notice" role="status">{TEXT.partial}</div>{/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  .stall-minutes { display: grid; gap: 6px; margin-top: 10px; min-width: 0; }
  .minutes-head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: 6px 12px; }
  .minutes-head h4 { margin: 0; }
  .minutes-toggle { justify-self: start; padding: 3px 0; color: var(--text-2); background: none; border: 0; font-size: var(--fs-2); text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .minutes-toggle:hover { color: var(--text); }
  .minutes-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .minutes-peak { margin: 0; font-size: var(--fs-3); }
  .minutes-scroll { max-height: 280px; overflow: auto; border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .quiet { color: var(--text-2); }
  .bar { display: inline-block; height: 8px; margin-right: 4px; background: var(--accent); border-radius: 2px; vertical-align: middle; }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>
