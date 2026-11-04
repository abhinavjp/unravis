<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { fetchTracing, nextRequestSeq, tracingErrorText, type TracingResponse } from '../lib/tracing/model';
  import { WINDOW_TEXT, checkWindows, defaultWindows, fixOverlap, windowHasNoData, windowIsEmpty, windowNow, loadWindows, saveWindows, type WindowFields } from '../lib/tracing/windows';
  import { TRACING_TEXT } from '../lib/copy/tracingCopy';
  import TracingChart from './TracingChart.svelte';
  import TraceToolbar from './TraceToolbar.svelte';
  import { allLanesVisible, toggleLane } from '../lib/tracing/toolbar';
  import { exportScrub } from '../lib/exportScrub.svelte';
  import { timeZone } from '../lib/timeZone.svelte';
  import { handOffBucket, otherLanesNote, slotToBucket, HANDOFF_TEXT } from '../lib/tracing/handoff';
  import { app, setTracingHandoff, setWindowFromHour, openAnalysis } from '../lib/appState.svelte';

  // The Investigation Timeline screen shell (WP-16b): one request on open, a loading state with Cancel, a plain
  // error with Try again, and four stable regions (windows, toolbar, chart, notes) that later packets fill in.
  // `readOnly` disables the window inputs and Start tracing (read in the template below).
  // `title` names the case being traced (shown as text under the heading).
  let { caseId, title = '', closed = false, readOnly = false, onClose }: { caseId: string; title?: string; closed?: boolean; readOnly?: boolean; onClose: () => void } = $props();

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
  // Windows typed earlier for this case in this tab come back after a reload; only typing keeps them, so the defaults still move with the clock.
  const startingWindows = (): WindowFields => loadWindows(caseId) ?? defaultWindows(windowNow(app.info, Date.now()));
  let fields = $state<WindowFields>(startingWindows());
  let problems = $state<string[]>([]);
  let invalidFields = $state<(keyof WindowFields)[]>([]);
  let overlapping = $state(false);
  const invalidIf = (name: keyof WindowFields): true | undefined => (invalidFields.includes(name) ? true : undefined);
  const describedBy = $derived(problems.length > 0 ? 'tp-problems' : undefined);
  const emptyWindow = $derived(result ? windowIsEmpty(result.lanes) : false);
  // Lanes came back but none holds an event (for example a case with no files in the window): one line says what to do.
  const noData = $derived(result ? windowHasNoData(result.lanes) : false);

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

  // Kept after the first edit, not on open: untouched defaults must keep following the clock. An effect saves, so it sees the bound value.
  let edited = false;
  function onFieldInput() {
    edited = true;
    clearProblems();
  }
  $effect(() => {
    const { incidentStart, incidentEnd, referenceStart, referenceEnd } = fields;
    if (edited) saveWindows(caseId, { incidentStart, incidentEnd, referenceStart, referenceEnd });
  });

  function moveReference() {
    fields = fixOverlap(fields);
    overlapping = checkWindows(fields).overlap;
    edited = true;
    clearProblems();
  }

  // Try again and Cancel remove the button that was pressed. When the keyboard was on it, focus moves to the chart region
  // (which stays in place) instead of dropping to the page, so the next Tab continues from here.
  let chartRegion: HTMLDivElement | undefined;
  async function keepFocusInChart(action: () => void) {
    const hadFocus = !!chartRegion && chartRegion.contains(document.activeElement);
    action();
    if (!hadFocus) return;
    await tick();
    const active = document.activeElement;
    if (!active || active === document.body) chartRegion?.focus();
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
    const shown = result;
    const ok = shown
      ? handOffBucket(slotToBucket(shown, visible, index), {
          // The note is set after the window, which clears any earlier one; it says why the analysis may be empty.
          setWindow: (window) => { setWindowFromHour(window); setTracingHandoff(window, otherLanesNote(shown, visible, index)); },
          open: openAnalysis
        })
      : false;
    if (!ok) handoffNote = HANDOFF_TEXT.empty;
  }

  onMount(() => { void run(); });
  onDestroy(() => { controller?.abort(); });
</script>

<section class="tracing-paper" aria-labelledby="tp-title">
  <h2 id="tp-title">{TRACING_TEXT.heading}</h2>
  {#if title}<p class="case-name" data-testid="tp-case-name">{title}</p>{/if}
  <div class="actions"><button type="button" class="secondary-button" onclick={onClose}>{TRACING_TEXT.backToCase}</button></div>

  <div data-region="windows" class="windows">
    <form class="window-form" onsubmit={(event) => { event.preventDefault(); startTracing(); }}>
      <fieldset class="window-set">
        <legend>{WINDOW_TEXT.incidentLabel}</legend>
        <label class="window-field"><span>{WINDOW_TEXT.start}</span><input type="datetime-local" bind:value={fields.incidentStart} oninput={onFieldInput} aria-invalid={invalidIf('incidentStart')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
        <label class="window-field"><span>{WINDOW_TEXT.end}</span><input type="datetime-local" bind:value={fields.incidentEnd} oninput={onFieldInput} aria-invalid={invalidIf('incidentEnd')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
      </fieldset>
      <fieldset class="window-set">
        <legend>{WINDOW_TEXT.referenceLabel}</legend>
        <label class="window-field"><span>{WINDOW_TEXT.start}</span><input type="datetime-local" bind:value={fields.referenceStart} oninput={onFieldInput} aria-invalid={invalidIf('referenceStart')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
        <label class="window-field"><span>{WINDOW_TEXT.end}</span><input type="datetime-local" bind:value={fields.referenceEnd} oninput={onFieldInput} aria-invalid={invalidIf('referenceEnd')} aria-describedby={describedBy} disabled={readOnly || closed} /></label>
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
  <div data-region="chart" class="chart-region" role="region" aria-label={TRACING_TEXT.chartLabel} tabindex="-1" bind:this={chartRegion}>
    {#if status === 'error'}
      <div class="error-panel">
        <span class="error-icon" aria-hidden="true">!</span>
        <div class="error-body">
          <p role="status">{errorText}</p>
          <!-- A closed case sends no request, so there is nothing to try again. -->
          {#if !closed}<button type="button" class="secondary-button" onclick={() => void keepFocusInChart(() => void run(lastBody ?? undefined))}>{TRACING_TEXT.retry}</button>{/if}
        </div>
      </div>
    {:else if status === 'loading' && !result}
      <p role="status">{TRACING_TEXT.readingWindow}</p>
      <button type="button" class="secondary-button" onclick={() => void keepFocusInChart(cancel)}>{TRACING_TEXT.cancel}</button>
    {:else}
      {#if result}
        {#if result.sources.length === 0 && result.lanes.length === 0}
          <p role="status">{TRACING_TEXT.emptyNoSources}</p>
        {:else}
          {#if emptyWindow}<p class="empty-hint">{WINDOW_TEXT.noEvents}</p>{:else if noData}<p class="empty-hint">{WINDOW_TEXT.noData}</p>{/if}
          <!-- A re-run keeps the old chart at its own height under a veil, so nothing below it jumps. -->
          <div class="chart-hold" aria-busy={status === 'loading' ? 'true' : undefined}>
            <div inert={status === 'loading'}>
              <TracingChart response={result} zone={timeZone.zone} {visible} onSelect={onBarSelect} />
            </div>
            {#if status === 'loading'}
              <div class="chart-veil">
                <p role="status">{TRACING_TEXT.readingWindow}</p>
                <button type="button" class="secondary-button" onclick={() => void keepFocusInChart(cancel)}>{TRACING_TEXT.cancel}</button>
              </div>
            {/if}
          </div>
        {/if}
      {/if}
    {/if}
  </div>
  <div data-region="notes">
    <!-- Below the chart, so saying "Cancelled" never pushes the chart down. -->
    {#if cancelled && status !== 'loading' && status !== 'error'}<p role="status" class="cancel-note">{result ? WINDOW_TEXT.cancelledKept : WINDOW_TEXT.cancelledNone}</p>{/if}
    {#if handoffNote}<p role="status">{handoffNote}</p>{/if}
  </div>
</section>

<style>
  .tracing-paper {
    display: grid;
    gap: 16px;
  }
  .case-name { margin: calc(var(--space-1) * -1) 0 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
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
  /* Touch targets on a tablet, a phone and any touch screen: the date boxes are 40 px high too. */
  @media (max-width: 1024px), (pointer: coarse) { .window-field input { min-height: 40px; } }
  /* A phone: each date box fills the row, level with the buttons below it. */
  @media (max-width: 720px) {
    .window-set { flex: 1 1 100%; }
    .window-field { flex: 1 1 100%; }
    .window-field input { width: 100%; box-sizing: border-box; }
  }
  .chart-region:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .error-panel { display: flex; gap: var(--space-3); align-items: flex-start; padding: var(--space-3) var(--space-4); background: var(--surface-2); border: var(--hairline-width) solid var(--bad); border-left-width: var(--space-1); border-radius: var(--radius-sm); }
  .error-icon { flex: none; width: var(--space-5); height: var(--space-5); border-radius: 50%; background: var(--bad); color: var(--bg); font-weight: 700; line-height: var(--space-5); text-align: center; }
  .error-body { display: grid; gap: var(--space-2); justify-items: start; min-width: 0; }
  .error-body p { margin: 0; color: var(--text); }
  .chart-hold { position: relative; }
  .chart-veil { position: absolute; inset: 0; display: flex; flex-wrap: wrap; align-content: flex-start; gap: var(--space-2); align-items: center; padding: var(--space-3); background: color-mix(in srgb, var(--bg) 72%, transparent); }
  .chart-veil p { margin: 0; }
</style>
