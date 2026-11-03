<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { fetchTracing, nextRequestSeq, tracingErrorText, type TracingResponse } from '../lib/tracing/model';
  import { WINDOW_TEXT, checkWindows, defaultWindows, fixOverlap, windowIsEmpty, windowNow, type WindowFields } from '../lib/tracing/windows';
  import { TRACING_TEXT } from '../lib/copy/tracingCopy';
  import TracingChart from './TracingChart.svelte';
  import TraceToolbar from './TraceToolbar.svelte';
  import { allLanesVisible, toggleLane } from '../lib/tracing/toolbar';
  import { exportScrub } from '../lib/exportScrub.svelte';
  import { timeZone } from '../lib/timeZone.svelte';
  import { handOffBucket, slotToBucket, HANDOFF_TEXT } from '../lib/tracing/handoff';
  import { app, setWindowFromHour, openAnalysis } from '../lib/appState.svelte';

  // The Investigation Timeline screen shell (WP-16b): one request on open, a loading state with Cancel, a plain
  // error with Try again, and four stable regions (windows, toolbar, chart, notes) that later packets fill in.
  // `readOnly` disables the window inputs and Start tracing (read in the template below).
  let { caseId, closed = false, readOnly = false, onClose }: { caseId: string; closed?: boolean; readOnly?: boolean; onClose: () => void } = $props();

  type Status = 'idle' | 'loading' | 'ready' | 'error';
  let status = $state<Status>('idle');
  let result = $state.raw<TracingResponse | null>(null);
  // Which rows the chart shows (WP-11): kept across re-runs and zoom, never sent to the server.
  let visible = $state(allLanesVisible());
  let errorText = $state('');
  // The plain sentence shown when a clicked bar has nothing to analyse (WP-61).
  let handoffNote = $state('');
  // Set by Cancel, cleared by the next run: the chart region then says what happened instead of going blank.
  let cancelled = $state(false);
  let controller: AbortController | null = null;
  // The last body that was sent, so Try again repeats that request and not whatever the boxes say now.
  let lastBody: ReturnType<typeof checkWindows>['body'] = null;

  // The window picker (WP-22): times are typed in UTC; the server gets the checked ISO strings only.
  // In demo mode the default window counts back from the data reference time, not the real clock.
  let fields = $state<WindowFields>(defaultWindows(windowNow(app.info, Date.now())));
  let problems = $state<string[]>([]);
  let invalidFields = $state<(keyof WindowFields)[]>([]);
  let overlapping = $state(false);
  const invalidIf = (name: keyof WindowFields): true | undefined => (invalidFields.includes(name) ? true : undefined);
  const describedBy = $derived(problems.length > 0 ? 'tp-problems' : undefined);
  const emptyWindow = $derived(result ? windowIsEmpty(result.lanes) : false);

  async function run(body = checkWindows(fields).body) {
    if (closed) {
      status = 'error';
      errorText = TRACING_TEXT.closedReason;
      return;
    }
    if (!body) return;
    lastBody = body;
    handoffNote = '';
    cancelled = false;
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    status = 'loading';
    try {
      const res = await fetchTracing(caseId, body, nextRequestSeq(), mine.signal);
      if (mine.signal.aborted) return;
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
    invalidFields = check.invalid;
    overlapping = check.overlap;
    if (check.body) void run(check.body);
  }

  function clearProblems() {
    if (problems.length > 0) { problems = []; invalidFields = []; }
  }

  function moveReference() {
    fields = fixOverlap(fields);
    overlapping = checkWindows(fields).overlap;
    clearProblems();
  }

  // Cancel keeps the previous chart when there is one; with none, the region says so rather than staying empty.
  function cancel() {
    controller?.abort();
    cancelled = true;
    status = result ? 'ready' : 'idle';
  }

  function onBarSelect(index: number) {
    handoffNote = '';
    cancelled = false;
    const ok = result ? handOffBucket(slotToBucket(result, visible, index), { setWindow: setWindowFromHour, open: openAnalysis }) : false;
    if (!ok) handoffNote = HANDOFF_TEXT.empty;
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
        <label class="window-field"><span>{WINDOW_TEXT.start}</span><input type="datetime-local" bind:value={fields.incidentStart} oninput={clearProblems} aria-invalid={invalidIf('incidentStart')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
        <label class="window-field"><span>{WINDOW_TEXT.end}</span><input type="datetime-local" bind:value={fields.incidentEnd} oninput={clearProblems} aria-invalid={invalidIf('incidentEnd')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
      </fieldset>
      <fieldset class="window-set">
        <legend>{WINDOW_TEXT.referenceLabel}</legend>
        <label class="window-field"><span>{WINDOW_TEXT.start}</span><input type="datetime-local" bind:value={fields.referenceStart} oninput={clearProblems} aria-invalid={invalidIf('referenceStart')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
        <label class="window-field"><span>{WINDOW_TEXT.end}</span><input type="datetime-local" bind:value={fields.referenceEnd} oninput={clearProblems} aria-invalid={invalidIf('referenceEnd')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
      </fieldset>
      <div class="window-actions">
        <button type="submit" class="primary-button" disabled={readOnly || closed || status === 'loading'}>{WINDOW_TEXT.startTracing}</button>
        <small>{WINDOW_TEXT.utcNote}</small>
        <small>{WINDOW_TEXT.endNote}</small>
      </div>
    </form>
    {#if problems.length > 0}
      <div role="alert" id="tp-problems" class="window-problems">
        <ul>{#each problems as problem (problem)}<li>{problem}</li>{/each}</ul>
        {#if result}<p>{WINDOW_TEXT.keptPrevious}</p>{/if}
      </div>
    {/if}
    {#if overlapping && problems.length === 0}
      <p role="status" class="window-overlap">{WINDOW_TEXT.overlap}
        <button type="button" class="secondary-button" onclick={moveReference} disabled={readOnly || closed}>{WINDOW_TEXT.overlapFix}</button>
      </p>
    {/if}
  </div>
  <div data-region="toolbar">
    {#if !(status === 'loading' && !result)}
      <TraceToolbar {visible} onToggle={(key) => (visible = toggleLane(visible, key))} scrub={exportScrub.forCase(caseId)} />
    {/if}
  </div>
  <div data-region="chart">
    {#if status === 'error'}
      <p role="status">{errorText}</p>
      <button type="button" class="secondary-button" onclick={() => void run(lastBody ?? undefined)}>{TRACING_TEXT.retry}</button>
    {:else if status === 'loading' && !result}
      <p role="status">{TRACING_TEXT.readingWindow}</p>
      <button type="button" class="secondary-button" onclick={cancel}>{TRACING_TEXT.cancel}</button>
    {:else}
      {#if cancelled}<p role="status" class="cancel-note">{result ? WINDOW_TEXT.cancelledKept : WINDOW_TEXT.cancelledNone}</p>{/if}
      {#if result}
        {#if result.sources.length === 0 && result.lanes.length === 0}
          <p role="status">{TRACING_TEXT.emptyNoSources}</p>
        {:else}
          {#if emptyWindow}<p class="empty-hint">{WINDOW_TEXT.noEvents}</p>{/if}
          <!-- A re-run keeps the old chart at its own height under a veil, so nothing below it jumps. -->
          <div class="chart-hold" aria-busy={status === 'loading' ? 'true' : undefined}>
            <div inert={status === 'loading'}>
              <TracingChart response={result} zone={timeZone.zone} {visible} onSelect={onBarSelect} />
            </div>
            {#if status === 'loading'}
              <div class="chart-veil">
                <p role="status">{TRACING_TEXT.readingWindow}</p>
                <button type="button" class="secondary-button" onclick={cancel}>{TRACING_TEXT.cancel}</button>
              </div>
            {/if}
          </div>
        {/if}
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
  .window-problems { color: var(--bad); font-size: var(--fs-2); }
  .window-problems ul { margin: 0; padding-left: var(--space-4); }
  .window-problems p { margin: var(--space-1) 0 0; }
  .window-overlap { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; margin: 0; font-size: var(--fs-2); }
  .cancel-note, .empty-hint { margin: 0 0 var(--space-2); color: var(--text-2); font-size: var(--fs-2); }
  .chart-hold { position: relative; }
  .chart-veil { position: absolute; inset: 0; display: flex; flex-wrap: wrap; align-content: flex-start; gap: var(--space-2); align-items: center; padding: var(--space-3); background: color-mix(in srgb, var(--bg) 72%, transparent); }
  .chart-veil p { margin: 0; }
</style>
