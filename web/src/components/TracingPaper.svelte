<script lang="ts">
  import { onDestroy, onMount, tick, untrack } from 'svelte';
  import { fetchTracing, nextRequestSeq, tracingErrorText, type TracingResponse } from '../lib/tracing/model';
  import { WINDOW_TEXT, checkWindows, defaultWindows, fixOverlap, windowHasNoData, windowIsEmpty, windowNow, loadWindows, saveWindows, type WindowFields } from '../lib/tracing/windows';
  import { TRACING_TEXT } from '../lib/copy/tracingCopy';
  import { WAIT_TEXT } from '../lib/copy/waitCopy';
  import { createSlowRead } from '../lib/slowRead.svelte';
  import TracingChart from './TracingChart.svelte';
  import WindowPicker from '../lib/components/WindowPicker.svelte';
  import { comparisonQuickRanges, equalPeriodBefore, incidentQuickChips, incidentQuickRanges, quickRangeIsActive, withQuickRange, type QuickRange } from '../lib/tracing/pickerModel';
  import TraceToolbar from './TraceToolbar.svelte';
  import TraceSources from './TraceSources.svelte';
  import { CaseApiError } from '../lib/cases';
  import { ALL_SOURCES, NO_SOURCES, SOURCES_TEXT, composeSourceIds, loadSources, mergeUniverse, saveSources, toggleSource, unselectedLanes, type SourceSelection, type SourceUniverse } from '../lib/tracing/sources';
  import { allLanesVisible, toggleLane } from '../lib/tracing/toolbar';
  import { exportScrub } from '../lib/exportScrub.svelte';
  import { timeZone } from '../lib/timeZone.svelte';
  import { handOffBucket, otherLanesNote, slotToBucket, HANDOFF_TEXT } from '../lib/tracing/handoff';
  import { app, panels, setTracingHandoff, setWindowFromHour, openAnalysis, openCaseImport, openConnections } from '../lib/appState.svelte';
  import { TZ_NOTICE_TEXT } from '../lib/copy/sourcesListCopy';
  import { requestSourcesList } from '../lib/sourcesNav.svelte';

  // The Investigation Timeline screen shell (WP-16b): one request on open, a loading state with Cancel, a plain
  // error with Try again, and four stable regions (windows, toolbar, chart, notes) that later packets fill in.
  // `readOnly` disables the window inputs and Start tracing (read in the template below). `closed` only adds the one line saying the case cannot be changed: the timeline reads, so a closed case is a viewer, not locked.
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
  // After a few seconds the plain reading line gives way to one that says a large window can take a minute.
  const waiting = createSlowRead(() => status === 'loading');
  let controller: AbortController | null = null;
  // The last body that was sent, so Try again repeats that request and not whatever the boxes say now.
  let lastBody: ReturnType<typeof checkWindows>['body'] = null;
  // The windows the chart on screen was read for (reactive, unlike lastBody): the "windows changed" line compares the boxes with it.
  let shownBody = $state.raw<ReturnType<typeof checkWindows>['body']>(null);

  // The window picker (WP-22): times are typed in UTC; the server gets the checked ISO strings only.
  // In demo mode the default window counts back from the data reference time, not the real clock.
  // Windows typed earlier for this case in this tab come back after a reload; only typing keeps them, so the defaults still move with the clock.
  const startingWindows = (): WindowFields => loadWindows(caseId) ?? defaultWindows(windowNow(app.info, Date.now()));
  let fields = $state<WindowFields>(startingWindows());
  let problems = $state<string[]>([]);
  let invalidFields = $state<(keyof WindowFields)[]>([]);
  let overlapping = $state(false);
  const isInvalid = (name: keyof WindowFields): boolean => invalidFields.includes(name);
  // "Today" and the quick ranges count from the data's own clock in demo mode.
  const pickerNow = () => windowNow(app.info, Date.now());
  const incidentQuick = () => incidentQuickRanges(pickerNow());
  const referenceQuick = () => comparisonQuickRanges({ start: fields.incidentStart, end: fields.incidentEnd });
  // A quick range for the incident also sets the comparison period to the equal period right before it.
  function onIncidentQuick(quick: QuickRange) {
    const before = quick.range ? equalPeriodBefore(quick.range) : null;
    if (before) { fields.referenceStart = before.start; fields.referenceEnd = before.end; }
  }
  // The "Quick range" chips above the windows (the wireframe's row): the same ranges as the popover. A chip sets the incident window and the equal
  // period right before it; it does not read by itself (Start tracing or Refresh timeline does), like the popover's quick ranges.
  let appliedQuick = $state.raw<QuickRange | null>(null);
  const quickChips = $derived(incidentQuickChips(pickerNow()));
  // Pressed while the boxes still hold what that chip set, or (for a fresh screen) the ranges that match the clock right now.
  const quickPressed = (quick: QuickRange): boolean => (appliedQuick?.id === quick.id && quickRangeIsActive(fields, appliedQuick)) || quickRangeIsActive(fields, quick);
  function applyQuickChip(id: string) {
    const quick = incidentQuickChips(pickerNow()).find((item) => item.id === id);
    if (!quick || readOnly) return;
    fields = withQuickRange(fields, quick);
    appliedQuick = quick;
    overlapping = checkWindows(fields).overlap;
    onFieldInput();
  }
  const describedBy = $derived(problems.length > 0 ? 'tp-problems' : undefined);
  // The "Sources traced" choice (kept per case in this tab) and the sources the case is known to have. Nothing selected reads nothing.
  const startingSources = () => loadSources(caseId);
  let selection = $state.raw<SourceSelection>(startingSources()?.selection ?? ALL_SOURCES);
  let universe = $state.raw<SourceUniverse>(startingSources()?.universe ?? { chips: [], saved: [] });
  const nothingSelected = $derived(composeSourceIds(selection).nothing);
  const offLanes = $derived(unselectedLanes(selection, universe.chips));
  // A lane the server left empty for a time zone that was not checked (packet P11). The notice names the lanes and leads to the Sources list.
  const uncheckedLanes = $derived(result ? result.lanes.filter((lane) => lane.state === 'clock-unchecked' || lane.disabled === 'tz-rule').map((lane) => lane.label) : []);
  async function seeSources() {
    requestSourcesList();
    onClose();
    await tick();
    requestAnimationFrame(() => document.getElementById('home-sources')?.scrollIntoView({ block: 'start' }));
  }
  const emptyWindow = $derived(result ? windowIsEmpty(result.lanes) : false);
  // Lanes came back but none holds an event (for example a case with no files in the window): one line says what to do.
  const noData = $derived(result ? windowHasNoData(result.lanes, result.warnings) : false);

  // With every source switched off nothing is read: any read in flight stops, the old chart goes, and the region says so.
  function stopReading() {
    controller?.abort();
    controller = null;
    result = null;
    shownBody = null;
    status = 'idle';
    cancelled = false;
    handoffNote = '';
  }

  async function run(body = checkWindows(fields).body, mayFallBack = true) {
    const wanted = composeSourceIds(selection);
    if (wanted.nothing) { stopReading(); return; }
    if (!body) return;
    lastBody = body;
    handoffNote = '';
    cancelled = false;
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    status = 'loading';
    try {
      const res = await fetchTracing(caseId, { ...body, sourceIds: wanted.ids }, nextRequestSeq(), mine.signal);
      if (mine.signal.aborted) return;
      universe = mergeUniverse(universe, res, wanted.ids.length === 0);
      saveSources(caseId, selection, universe);
      result = res;
      shownBody = body;
      status = 'ready';
    } catch (error) {
      if (mine.signal.aborted) return;
      // A superseded or cancelled read can surface as an ordinary error (the body read becomes a CaseApiError): the signal decides.
      if (error instanceof DOMException && error.name === 'AbortError') return;
      // A kept choice can name a source the case no longer has (the server answers 404): read everything once instead of failing.
      if (mayFallBack && wanted.ids.length > 0 && error instanceof CaseApiError && error.status === 404) {
        selection = ALL_SOURCES;
        saveSources(caseId, selection, universe);
        await run(body, false);
        return;
      }
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

  // Refresh timeline: the same read as Start tracing (same checks, same windows and sources), only usable once a first read has come back.
  const canRefresh = $derived(!readOnly && status !== 'loading' && (result !== null || status === 'error') && !nothingSelected);
  // The windows in the boxes differ from the ones the chart on screen was read for.
  const windowsChanged = $derived.by(() => {
    if (!result || !shownBody) return false;
    const body = checkWindows(fields).body;
    return !!body && JSON.stringify(body) !== JSON.stringify(shownBody);
  });
  // When the import drawer opened by Add evidence closes, read again so the new files show in the Sources row (and in the chart).
  let importWasOpen = false;
  $effect(() => {
    const open = panels.sidePanel === 'import';
    const closedNow = importWasOpen && !open;
    importWasOpen = open;
    if (closedNow) untrack(() => { if (status !== 'loading' && !composeSourceIds(selection).nothing) void run(lastBody ?? undefined); });
  });

  // A click on a source (or Select all / Clear all) keeps the choice and reads again with it, over the window of the last read.
  function changeSelection(next: SourceSelection) {
    selection = next;
    saveSources(caseId, next, universe);
    void run(lastBody ?? undefined);
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

  // The screen takes focus once when it opens (the button that opened it is gone), so a keyboard or screen-reader user starts at its heading.
  // Only when nothing else holds focus: later runs, Try again and Cancel never move it.
  let headingEl: HTMLHeadingElement | undefined;
  onMount(() => {
    const active = document.activeElement;
    if (!active || active === document.body) headingEl?.focus();
    void run();
  });
  onDestroy(() => { controller?.abort(); });
</script>

<section class="tracing-paper" aria-labelledby="tp-title">
  <h2 id="tp-title" tabindex="-1" bind:this={headingEl}>{TRACING_TEXT.heading}</h2>
  {#if title}<p class="case-name" data-testid="tp-case-name">{title}</p>{/if}
  <div class="actions"><button type="button" class="secondary-button" onclick={onClose}>{TRACING_TEXT.backToCase}</button></div>
  <!-- A closed case opens here as a viewer: the route only reads, so only the one line tells the reader nothing can be changed. -->
  {#if closed}<p class="closed-line" role="status" data-testid="tp-closed-line">{TRACING_TEXT.closedView}</p>{/if}

  <div data-region="windows" class="windows">
    <div class="quick-row" role="group" aria-label={WINDOW_TEXT.quickRow} data-testid="tp-quick-row">
      <span class="quick-label">{WINDOW_TEXT.quickRow}</span>
      {#each quickChips as quick (quick.id)}
        <button type="button" class="chip" data-quick-id={quick.id} aria-pressed={quickPressed(quick)} title={WINDOW_TEXT.quickRowTitle} disabled={readOnly} onclick={() => applyQuickChip(quick.id)}>{quick.label}</button>
      {/each}
    </div>
    <form class="window-form" onsubmit={(event) => { event.preventDefault(); startTracing(); }}>
      <div class="win-pair">
        <div class="win-box">
          <h3>{WINDOW_TEXT.incidentHeading}</h3>
          <p class="win-help">{WINDOW_TEXT.incidentHelp}</p>
          <!-- The legend keeps the group's spoken name ("Incident window (UTC)"); the heading above is what is drawn. -->
          <fieldset class="window-set">
            <legend class="visually-hidden">{WINDOW_TEXT.incidentLabel}</legend>
            <WindowPicker label={WINDOW_TEXT.incidentLabel} bind:start={fields.incidentStart} bind:end={fields.incidentEnd} invalidStart={isInvalid('incidentStart')} invalidEnd={isInvalid('incidentEnd')}
              describedby={describedBy} disabled={readOnly} quickRanges={incidentQuick} onquick={onIncidentQuick} onchange={onFieldInput} now={pickerNow} />
          </fieldset>
        </div>
        <div class="win-box">
          <h3>{WINDOW_TEXT.referenceHeading} <span class="win-heading-note">{WINDOW_TEXT.referenceHeadingNote}</span></h3>
          <p class="win-help">{WINDOW_TEXT.referenceHelp}</p>
          <fieldset class="window-set">
            <legend class="visually-hidden">{WINDOW_TEXT.referenceLabel}</legend>
            <WindowPicker label={WINDOW_TEXT.referenceLabel} bind:start={fields.referenceStart} bind:end={fields.referenceEnd} invalidStart={isInvalid('referenceStart')} invalidEnd={isInvalid('referenceEnd')}
              describedby={describedBy} disabled={readOnly} quickRanges={referenceQuick} onchange={onFieldInput} now={pickerNow} />
          </fieldset>
        </div>
      </div>
      <div class="window-actions">
        <button type="submit" class="primary-button" disabled={readOnly || status === 'loading'}>{WINDOW_TEXT.startTracing}</button>
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
    {#if windowsChanged && problems.length === 0}<p class="win-stale" data-testid="tp-stale">{WINDOW_TEXT.stale}</p>{/if}
    {#if overlapping && problems.length === 0}
      <p role="status" class="window-overlap">{WINDOW_TEXT.overlap}
        <button type="button" class="secondary-button" onclick={moveReference} disabled={readOnly}>{WINDOW_TEXT.overlapFix}</button>
      </p>
    {/if}
  </div>
  <div data-region="sources">
    <TraceSources chips={universe.chips} saved={universe.saved} {selection} disabled={readOnly}
      onToggle={(id) => changeSelection(toggleSource(selection, universe.chips, id))} onAll={() => changeSelection(ALL_SOURCES)} onNone={() => changeSelection(NO_SOURCES)}
      onConnect={() => void openConnections('new')}>
      {#snippet actions()}
        <button type="button" class="secondary-button" title={WINDOW_TEXT.addEvidenceTitle} aria-haspopup="dialog" disabled={closed || readOnly} onclick={openCaseImport}>{WINDOW_TEXT.addEvidence}</button>
        <button type="button" class="primary-button" title={WINDOW_TEXT.refreshTitle} disabled={!canRefresh} onclick={startTracing}>{WINDOW_TEXT.refresh}</button>
      {/snippet}
    </TraceSources>
  </div>
  <div data-region="toolbar">
    {#if !(status === 'loading' && !result)}
      <TraceToolbar {visible} onToggle={(key) => (visible = toggleLane(visible, key))} scrub={exportScrub.forCase(caseId)} />
    {/if}
  </div>
  <div data-region="chart" class="chart-region" role="region" aria-label={TRACING_TEXT.chartLabel} tabindex="-1" bind:this={chartRegion}>
    {#if nothingSelected}
      <p role="status" class="none-selected" data-testid="tp-none-selected">{SOURCES_TEXT.noneSelected}</p>
    {:else if status === 'error'}
      <div class="error-panel">
        <span class="error-icon" aria-hidden="true">!</span>
        <div class="error-body">
          <p role="status">{errorText}</p>
          <button type="button" class="secondary-button" title={WAIT_TEXT.retryTitle} onclick={() => void keepFocusInChart(() => void run(lastBody ?? undefined))}>{TRACING_TEXT.retry}</button>
        </div>
      </div>
    {:else if status === 'loading' && !result}
      <p role="status">{waiting.slow ? WAIT_TEXT.stillReading : TRACING_TEXT.readingWindow}</p>
      <button type="button" class="secondary-button" title={WAIT_TEXT.cancelTitle} onclick={() => void keepFocusInChart(cancel)}>{TRACING_TEXT.cancel}</button>
    {:else}
      {#if result}
        {#if result.sources.length === 0 && result.lanes.length === 0}
          <p role="status">{TRACING_TEXT.emptyNoSources}</p>
        {:else}
          {#if uncheckedLanes.length > 0}
            <div class="tz-notice" role="status" data-testid="tp-tz-notice">
              <p><b>{TZ_NOTICE_TEXT.title}.</b> {TZ_NOTICE_TEXT.lead(uncheckedLanes)}</p>
              <div class="tz-actions">
                <button type="button" class="secondary-button" onclick={() => void seeSources()}>{TZ_NOTICE_TEXT.action}</button>
                {#if !readOnly}<button type="button" class="secondary-button" onclick={openCaseImport}>{TZ_NOTICE_TEXT.addAgain}</button>{/if}
              </div>
            </div>
          {/if}
          {#if emptyWindow}<p class="empty-hint">{WINDOW_TEXT.noEvents}</p>{:else if noData}<p class="empty-hint">{WINDOW_TEXT.noData}</p>{/if}
          <!-- A re-run keeps the old chart at its own height under a veil, so nothing below it jumps. -->
          <div class="chart-hold" aria-busy={status === 'loading' ? 'true' : undefined}>
            <div inert={status === 'loading'}>
              <TracingChart response={result} zone={timeZone.zone} {visible} onSelect={onBarSelect} unselected={offLanes} {readOnly} onAddFiles={openCaseImport} />
            </div>
            {#if status === 'loading'}
              <div class="chart-veil">
                <p role="status">{waiting.slow ? WAIT_TEXT.stillReading : TRACING_TEXT.readingWindow}</p>
                <button type="button" class="secondary-button" title={WAIT_TEXT.cancelTitle} onclick={() => void keepFocusInChart(cancel)}>{TRACING_TEXT.cancel}</button>
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
  .closed-line { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .case-name { margin: calc(var(--space-1) * -1) 0 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .windows { display: grid; gap: var(--space-2); }
  .quick-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .quick-label { color: var(--text-2); font-size: var(--fs-2); }
  .quick-row .chip:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .window-form { display: grid; gap: var(--space-3); }
  .win-pair { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-4); align-items: stretch; }
  .win-box { display: grid; gap: var(--space-2); align-content: start; min-width: 0; }
  .win-box h3 { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  .win-heading-note { color: var(--text-2); font-size: var(--fs-2); font-weight: 400; }
  .win-help { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .win-stale { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .window-set { display: flex; flex-wrap: wrap; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
  .window-actions { display: grid; gap: 2px; }
  .window-actions small { color: var(--text-2); font-size: var(--fs-2); }
  .window-problems { color: var(--bad); font-size: var(--fs-2); }
  .window-problems ul { margin: 0; padding-left: var(--space-4); }
  .window-problems p { margin: var(--space-1) 0 0; }
  .window-overlap { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; margin: 0; font-size: var(--fs-2); }
  .none-selected { margin: 0; padding: var(--space-3) var(--space-4); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .cancel-note, .empty-hint { margin: 0 0 var(--space-2); color: var(--text-2); font-size: var(--fs-2); }
  .tz-notice { display: grid; gap: var(--space-2); margin: 0 0 var(--space-2); padding: var(--space-2) var(--space-3); border: var(--hairline-width, 1px) solid var(--line); border-left: 3px solid var(--warn); border-radius: var(--radius-sm); background: var(--surface); font-size: var(--fs-3); }
  .tz-notice p { margin: 0; }
  .tz-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  /* A phone: each window fills the row, level with the buttons below it. */
  @media (max-width: 900px) {
    .win-pair { grid-template-columns: 1fr; }
  }
  @media (max-width: 720px) {
    .window-set { flex: 1 1 100%; }
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
