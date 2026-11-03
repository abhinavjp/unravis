<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { fetchTracing, isStale, nextRequestSeq, tracingErrorText, type TracingResponse } from '../lib/tracing/model';
  import { WINDOW_TEXT, checkWindows, defaultWindows, fixOverlap, type WindowFields } from '../lib/tracing/windows';
  import { TRACING_TEXT } from '../lib/copy/tracingCopy';
  import TracingChart from './TracingChart.svelte';
  import TraceToolbar from './TraceToolbar.svelte';
  import { allLanesVisible, toggleLane } from '../lib/tracing/toolbar';
  import { exportScrub } from '../lib/exportScrub.svelte';
  import { timeZone } from '../lib/timeZone.svelte';
  import { handOffBucket, slotToBucket, HANDOFF_TEXT } from '../lib/tracing/handoff';
  import { setWindowFromHour, openAnalysis } from '../lib/appState.svelte';

  // The Investigation Timeline screen shell (WP-16b): one request on open, a loading state with Cancel, a plain
  // error with Try again, and four stable regions (windows, toolbar, chart, notes) that later packets fill in.
  // `readOnly` is accepted here and threaded to the regions that need it (WP-10, WP-22); this packet does not read it.
  let { caseId, closed = false, readOnly = false, onClose }: { caseId: string; closed?: boolean; readOnly?: boolean; onClose: () => void } = $props();
  $effect(() => { void readOnly; });

  type Status = 'idle' | 'loading' | 'ready' | 'error';
  let status = $state<Status>('idle');
  let result = $state.raw<TracingResponse | null>(null);
  // Which rows the chart shows (WP-11): kept across re-runs and zoom, never sent to the server.
  let visible = $state(allLanesVisible());
  let errorText = $state('');
  // The plain sentence shown when a clicked bar has nothing to analyse (WP-61).
  let handoffNote = $state('');
  let latestSeq = 0;
  let controller: AbortController | null = null;

  // The window picker (WP-22): times are typed in UTC; the server gets the checked ISO strings only.
  let fields = $state<WindowFields>(defaultWindows(Date.now()));
  let problems = $state<string[]>([]);
  let overlapping = $state(false);

  async function run(body = checkWindows(fields).body) {
    if (closed) {
      status = 'error';
      errorText = TRACING_TEXT.closedReason;
      return;
    }
    if (!body) return;
    handoffNote = '';
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    const seq = nextRequestSeq();
    latestSeq = seq;
    status = 'loading';
    try {
      const res = await fetchTracing(caseId, body, seq, mine.signal);
      if (mine.signal.aborted || isStale(res.requestSeq ?? seq, latestSeq)) return;
      result = res;
      status = 'ready';
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      result = null;
      errorText = tracingErrorText(error);
      status = 'error';
    }
  }

  function startTracing() {
    const check = checkWindows(fields);
    problems = check.errors;
    overlapping = check.overlap;
    if (check.body) void run(check.body);
  }

  function moveReference() {
    fields = fixOverlap(fields);
    overlapping = checkWindows(fields).overlap;
  }

  function cancel() {
    controller?.abort();
    status = 'idle';
  }

  onMount(() => { void run(); });
  onDestroy(() => { controller?.abort(); });
</script>

<section class="tracing-paper" aria-labelledby="tp-title">
  <h2 id="tp-title">{TRACING_TEXT.heading}</h2>
  <div class="actions"><button type="button" class="secondary-button" onclick={onClose}>{TRACING_TEXT.backToCase}</button></div>

  <div data-region="windows" class="windows">
    <form class="window-form" onsubmit={(event) => { event.preventDefault(); startTracing(); }}>
      <fieldset class="window-set">
        <legend>{WINDOW_TEXT.incidentLabel}</legend>
        <label class="window-field"><span>{WINDOW_TEXT.start}</span><input type="datetime-local" bind:value={fields.incidentStart} disabled={readOnly || closed} /></label>
        <label class="window-field"><span>{WINDOW_TEXT.end}</span><input type="datetime-local" bind:value={fields.incidentEnd} disabled={readOnly || closed} /></label>
      </fieldset>
      <fieldset class="window-set">
        <legend>{WINDOW_TEXT.referenceLabel}</legend>
        <label class="window-field"><span>{WINDOW_TEXT.start}</span><input type="datetime-local" bind:value={fields.referenceStart} disabled={readOnly || closed} /></label>
        <label class="window-field"><span>{WINDOW_TEXT.end}</span><input type="datetime-local" bind:value={fields.referenceEnd} disabled={readOnly || closed} /></label>
      </fieldset>
      <div class="window-actions">
        <button type="submit" class="primary-button" disabled={readOnly || closed || status === 'loading'}>{WINDOW_TEXT.startTracing}</button>
        <small>{WINDOW_TEXT.utcNote}</small>
      </div>
    </form>
    {#if problems.length > 0}
      <ul role="alert" class="window-problems">{#each problems as problem (problem)}<li>{problem}</li>{/each}</ul>
    {/if}
    {#if overlapping && problems.length === 0}
      <p role="status" class="window-overlap">{WINDOW_TEXT.overlap}
        <button type="button" class="secondary-button" onclick={moveReference} disabled={readOnly || closed}>{WINDOW_TEXT.overlapFix}</button>
      </p>
    {/if}
  </div>
  <div data-region="toolbar">
    {#if !(status === 'loading' && !result)}
      <TraceToolbar {visible} onToggle={(key) => (visible = toggleLane(visible, key))} scrub={exportScrub.value} />
    {/if}
  </div>
  <div data-region="chart">
    {#if status === 'loading'}
      <p role="status">{TRACING_TEXT.readingWindow}</p>
      <button type="button" class="secondary-button" onclick={cancel}>{TRACING_TEXT.cancel}</button>
    {:else if status === 'error'}
      <p role="status">{errorText}</p>
      <button type="button" class="secondary-button" onclick={() => void run()}>{TRACING_TEXT.retry}</button>
    {:else if status === 'ready' && result}
      {#if result.sources.length === 0 && result.lanes.length === 0}
        <p role="status">{TRACING_TEXT.emptyNoSources}</p>
      {:else}
        <TracingChart response={result} zone={timeZone.zone} {visible} onSelect={(i) => { handoffNote = ''; const ok = result ? handOffBucket(slotToBucket(result, visible, i), { setWindow: setWindowFromHour, open: openAnalysis }) : false; if (!ok) handoffNote = HANDOFF_TEXT.empty; }} />
      {/if}
    {/if}
  </div>
  <div data-region="notes">
    {#if handoffNote}<p role="status">{handoffNote}</p>{/if}
  </div>
</section>

<style>
  .tracing-paper {
    display: grid;
    gap: 16px;
  }
  .windows { display: grid; gap: var(--space-2); }
  .window-form { display: flex; flex-wrap: wrap; gap: var(--space-3); align-items: flex-end; }
  .window-set { display: flex; flex-wrap: wrap; gap: var(--space-2); min-width: 0; margin: 0; padding: var(--space-2) var(--space-3); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .window-set legend { padding: 0 var(--space-1); color: var(--text-2); font-size: var(--fs-2); }
  .window-field { display: grid; gap: 2px; min-width: 0; font-size: var(--fs-2); color: var(--text-2); }
  .window-actions { display: grid; gap: 2px; }
  .window-actions small { color: var(--text-2); font-size: var(--fs-2); }
  .window-problems { margin: 0; padding-left: var(--space-4); color: var(--bad); font-size: var(--fs-2); }
  .window-overlap { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; margin: 0; font-size: var(--fs-2); }
</style>
