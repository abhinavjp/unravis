<script lang="ts">
  import { onDestroy, onMount, tick, untrack } from 'svelte';
  import { fetchTracing, nextRequestSeq, tracingErrorText, type TracingLaneKey, type TracingResponse } from '../lib/tracing/model';
  import { WINDOW_TEXT, checkWindows, defaultWindows, fixOverlap, windowHasNoData, windowIsEmpty, windowNow, loadWindows, pickWindowFields, saveWindows, type WindowFields } from '../lib/tracing/windows';
  import { TRACING_TEXT } from '../lib/copy/tracingCopy';
  import { WAIT_TEXT } from '../lib/copy/waitCopy';
  import { createSlowRead } from '../lib/slowRead.svelte';
  import TracingChart from './TracingChart.svelte';
  import SuspectsRail from './SuspectsRail.svelte';
  import WindowPicker from '../lib/components/WindowPicker.svelte';
  import { comparisonQuickRanges, equalPeriodBefore, incidentQuickChips, incidentQuickRanges, quickRangeIsActive, withQuickRange, type QuickRange } from '../lib/tracing/pickerModel';
  import TraceToolbar from './TraceToolbar.svelte';
  import TraceSources from './TraceSources.svelte';
  import { CaseApiError, listImports } from '../lib/cases';
  import { listSourceTags } from '../lib/caseTagsApi';
  import { ALL_SOURCES, NO_SOURCES, SOURCES_TEXT, composeSourceIds, fileSourceIds, reconnectFailedText, savedStillClosed, loadSources, mergeUniverse, saveSources, switchOnNew, toggleOutcome, unselectedLanes, type SourceSelection, type SourceUniverse } from '../lib/tracing/sources';
  import { allLanesVisible, toggleLane } from '../lib/tracing/toolbar';
  import { exportScrub } from '../lib/exportScrub.svelte';
  import { timeZone } from '../lib/timeZone.svelte';
  import { handOffBucket, otherLanesNote, slotToBucket, HANDOFF_TEXT } from '../lib/tracing/handoff';
  import { app, panels, setTracingHandoff, setWindowFromHour, openAnalysis, openCaseImport, openConnections, openSlowCall, closeSlowCall, noteSlowCall } from '../lib/appState.svelte';
  import { TZ_NOTICE_TEXT } from '../lib/copy/sourcesListCopy';
  import { requestSourcesList } from '../lib/sourcesNav.svelte';
  import { api } from '../lib/api';
  import { fetchLaneDays, runProblem, runSqlLane } from '../lib/lanes';
  import { decideSqlRead, pickSqlSource, readKey, windowsOfBody, type SqlAutoVerdict, type SqlSource, type SqlStatusKind } from '../lib/tracing/sqlAuto';
  import { SQL_AUTO_TEXT } from '../lib/copy/sqlAutoCopy';
  import SqlLaneStatus from './SqlLaneStatus.svelte';
  import { lazyComponent } from '../lib/lazy';

  import { makeCallTarget } from '../lib/drill/callTarget';

  const SlowCallScreen = lazyComponent(() => import('./SlowCallScreen.svelte'));
  // Kept as a variable: a new run clears it (the Slow call screen itself is opened through the app state).
  let drillSlot = $state<number | null>(null);

  // The Investigation Timeline screen shell (WP-16b): one request on open, a loading state with Cancel, a plain
  // error with Try again, and four stable regions (windows, toolbar, chart, notes) that later packets fill in.
  // `readOnly` disables the window inputs and Start tracing (read in the template below). `closed` only adds the one line saying the case cannot be changed: the timeline reads, so a closed case is a viewer, not locked.
  // `title` names the case being traced (shown as text under the heading).
  let { caseId, title = '', closed = false, readOnly = false, onClose }: { caseId: string; title?: string; closed?: boolean; readOnly?: boolean; onClose: () => void } = $props();

  // While the Slow call screen is open for this case the timeline stays mounted but hidden, so Back returns to it as it was.
  const callShown = $derived(!!app.slowCall && app.slowCall.caseId === caseId);

  type Status = 'idle' | 'loading' | 'ready' | 'error';
  let status = $state<Status>('idle');
  let result = $state.raw<TracingResponse | null>(null);
  // Which rows the chart shows (WP-11): kept across re-runs and zoom, never sent to the server.
  let visible = $state(allLanesVisible());
  let errorText = $state('');
  // The plain sentence shown when a clicked bar has nothing to analyse (WP-61).
  let handoffNote = $state('');
  // The bar the Suspects rail asked the chart to show ("Show on chart"). A new token asks again; a new read clears it so a stale bar is never selected.
  let chartFocus = $state.raw<{ slot: number; token: number } | null>(null);
  let focusToken = 0;
  // A suspect's lane the person switched off is switched back on first, so the bar being shown is not left hidden.
  const showOnChart = (slot: number, lane?: TracingLaneKey) => {
    if (lane && !visible[lane]) visible = { ...visible, [lane]: true };
    chartFocus = { slot, token: ++focusToken };
  };
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
  // Order: what was typed in this tab, then the windows the case kept when Start tracing was last pressed (they survive closing the app), then the last hour.
  const startingWindows = (): WindowFields => loadWindows(caseId) ?? (app.savedWindows ? { ...app.savedWindows } : null) ?? defaultWindows(windowNow(app.info, Date.now()));
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
  // period right before it; it does not read by itself (Start tracing does), like the popover's quick ranges.
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
  // The line under the two windows (the wireframe's UTC echo): what the boxes hold, in the one format the server is sent, so a typed 12-hour time is never a guess.
  const echoUtc = (start: string, end: string): string => (start && end ? `UTC ${start}:00Z – ${end}:00Z` : `UTC ${WINDOW_TEXT.metaUnset}`);
  // The "Sources traced" choice (kept per case in this tab) and the sources the case is known to have. Nothing selected reads nothing.
  const startingSources = () => loadSources(caseId);
  let selection = $state.raw<SourceSelection>(startingSources()?.selection ?? app.savedTraceSources ?? ALL_SOURCES);
  let universe = $state.raw<SourceUniverse>(startingSources()?.universe ?? { chips: [], saved: [] });
  // Said when a source cannot be switched on because 50 are on already; cleared by the next change of the choice.
  let capNote = $state('');
  const nothingSelected = $derived(composeSourceIds(selection).nothing);
  const offLanes = $derived(unselectedLanes(selection, universe.chips));
  // Saved databases no open audit source answers to yet: only then does the page say it is reconnecting.
  const closedSaved = $derived(savedStillClosed(universe.saved, universe.chips, result?.auditSources ?? []));
  // A lane the server left empty for a time zone that was not checked (packet P11). The notice names the lanes and leads to the Sources list.
  const uncheckedLanes = $derived(result ? result.lanes.filter((lane) => lane.state === 'clock-unchecked' || lane.disabled === 'tz-rule').map((lane) => lane.label) : []);
  async function seeSources() {
    requestSourcesList();
    onClose();
    await tick();
    requestAnimationFrame(() => document.getElementById('home-sources')?.scrollIntoView({ block: 'start' }));
  }
  // The Suspects rail sits beside the chart once a read has come back with lanes in it (the wireframe's `hasrail`).
  const showRail = $derived(!!result && !nothingSelected && status !== 'error' && result.lanes.length > 0);
  const emptyWindow = $derived(result ? windowIsEmpty(result.lanes) : false);
  // Lanes came back but none holds an event (for example a case with no files in the window): one line says what to do.
  const noData = $derived(result ? windowHasNoData(result.lanes, result.warnings) : false);

  // With every source switched off nothing is read: any read in flight stops, the old chart goes, and the region says so.
  function stopReading() {
    controller?.abort();
    controller = null;
    result = null;
    chartFocus = null;
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
    drillSlot = null;
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
      chartFocus = null;
      shownBody = body;
      status = 'ready';
      void maybeReadSql(body);
    } catch (error) {
      if (mine.signal.aborted) return;
      // A superseded or cancelled read can surface as an ordinary error (the body read becomes a CaseApiError): the signal decides.
      if (error instanceof DOMException && error.name === 'AbortError') return;
      // A kept choice can name a source the case no longer has (the server answers 404): read everything once instead of failing.
      if (mayFallBack && wanted.ids.length > 0 && error instanceof CaseApiError && error.status === 404) {
        selection = ALL_SOURCES;
        saveSources(caseId, selection, universe); app.rememberTraceSources(selection);
        await run(body, false);
        return;
      }
      result = null;
      errorText = tracingErrorText(error);
      status = 'error';
    }
  }

  // The database for this window (P5). After the chart has loaded, the case's SQL source (the connection its newest saved SQL day came from, or the only
  // saved connection) is read once for the window through the existing read-only lane call, saved in the case, and the chart is read again. The Timeline
  // itself never opens a connection: nothing is read in the demo, on a closed or read-only case, with the toggle off, or for a login that needs its password typed.
  const autoKey = () => `unravis.sqlAuto.${caseId}`;
  const startingAuto = (): boolean => { try { return localStorage.getItem(autoKey()) !== 'off'; } catch { return true; } };
  let sqlOn = $state(startingAuto());
  let sqlKind = $state<SqlStatusKind | null>(null);
  let sqlSource = $state.raw<SqlSource | null>(null);
  let sqlReason = $state('');
  let sqlReading = false;
  let sqlSeq = 0;
  const sqlReadKeys = new Set<string>();
  let sqlController: AbortController | null = null;

  async function maybeReadSql(body: ReturnType<typeof checkWindows>['body'], confirmed = false) {
    const inert = !!app.info?.demo || closed || readOnly;
    if (inert) { sqlKind = null; return; }
    if (sqlReading) return;
    if (offLanes.includes('sql')) { sqlKind = null; return; }
    const seq = ++sqlSeq;
    let source: SqlSource | null = null;
    let verdict: SqlAutoVerdict;
    try {
      const [days, saved] = await Promise.all([fetchLaneDays(caseId), api.sqlConnections()]);
      if (seq !== sqlSeq) return;
      source = pickSqlSource(days, Array.isArray(saved) ? saved : []);
      const windows = windowsOfBody(body);
      verdict = decideSqlRead({ source, days, incident: windows?.incident ?? null, reference: windows?.reference ?? null, demo: false, closed: false, enabled: sqlOn || confirmed });
    } catch {
      if (seq === sqlSeq) sqlKind = null;
      return;
    }
    sqlSource = source;
    sqlReason = '';
    if (!source) { sqlKind = null; return; }
    if (verdict.kind === 'covered') { sqlKind = source.remembered ? 'kept' : null; return; }
    if (verdict.kind === 'overlap') { sqlKind = 'overlap'; return; }
    if (verdict.kind === 'skip') { sqlKind = verdict.why === 'password' ? 'password' : verdict.why === 'off' ? 'suggest' : null; return; }
    // The window still needs the database, so it is read now (owner rule, Nov 12): a case that has never read it reads its only saved connection
    // on the first Timeline load too, without a click. The toggle, the password, demo, closed and read-only guards above still stop it.
    const key = readKey(source, verdict.incident);
    if (!confirmed && sqlReadKeys.has(key)) return;
    sqlReadKeys.add(key);
    sqlReading = true;
    sqlKind = 'reading';
    sqlController = new AbortController();
    const mine = sqlController;
    try {
      const out = await runSqlLane(caseId, { savedId: source.connectionId, database: source.database }, { incident: verdict.incident, reference: verdict.reference }, mine.signal);
      if (mine.signal.aborted) return;
      sqlReading = false;
      if (!out.saved) { sqlKind = 'failed'; sqlReason = SQL_AUTO_TEXT.saveFailed(out.saveError || ''); return; }
      sqlKind = 'kept';
      void run(lastBody ?? body ?? undefined);
    } catch (error) {
      if (mine.signal.aborted) return;
      sqlReading = false;
      sqlKind = 'failed';
      sqlReason = runProblem(error).text;
    } finally {
      if (sqlController === mine) sqlReading = false;
    }
  }
  const readSqlNow = () => { void maybeReadSql(shownBody ?? lastBody, true); };
  function setSqlOn(on: boolean) {
    sqlOn = on;
    try { localStorage.setItem(autoKey(), on ? 'on' : 'off'); } catch { /* the choice only lasts while this screen is open */ }
    void maybeReadSql(shownBody ?? lastBody);
  }

  function startTracing() {
    const check = checkWindows(fields);
    problems = check.errors;
    invalidFields = check.invalid;
    overlapping = check.overlap;
    if (check.body) {
      app.rememberWindows(fields); // Start tracing keeps the windows with the case, so they are not picked again next time
      void run(check.body);
    }
  }

  // The case's kept windows can arrive after this screen opened (the case is still being restored). Unless the person already typed or picked
  // something, they replace the last-hour default and the screen reads again with them.
  $effect(() => {
    const saved = app.savedWindows;
    if (!saved) return;
    untrack(() => {
      if (edited || readOnly || loadWindows(caseId) !== null) return;
      if (JSON.stringify(pickWindowFields(fields)) === JSON.stringify(saved)) return;
      fields = { ...saved };
      overlapping = checkWindows(fields).overlap;
      void run();
    });
  });

  // The same for the case's kept source choice: it replaces "all sources" only while nothing was chosen here, and the screen reads again with it.
  $effect(() => {
    const saved = app.savedTraceSources;
    if (!saved) return;
    untrack(() => {
      if (readOnly || selection.mode !== 'all' || loadSources(caseId) !== null) return;
      selection = saved;
      void run(lastBody ?? undefined);
    });
  });

  // A source connected while this screen is open (the Connect button of a saved audit database, or one added from the header) is read at once,
  // with the windows of the last read, so the chart does not stay without it until the person presses Start tracing.
  let knownSourceIds: Set<string> | null = null;
  $effect(() => {
    const ids = app.sources.map((source) => source.id);
    untrack(() => {
      const before = knownSourceIds;
      knownSourceIds = new Set(ids);
      if (before === null || !ids.some((id) => !before.has(id))) return;
      if (readOnly || composeSourceIds(selection).nothing) return;
      void run(lastBody ?? undefined);
    });
  });
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
    if (closedNow) untrack(() => { if (status !== 'loading' && !composeSourceIds(selection).nothing) void readAfterAdd(); });
  });

  // A hand-picked list also switches on the sources the drawer just added (a new file would otherwise be traced by no one), then reads.
  // Which sources are new comes from the case's own tags and files; any failure here reads with the choice as it is.
  async function readAfterAdd() {
    if (selection.mode === 'some') {
      try {
        const [tags, imports] = await Promise.all([listSourceTags(caseId), listImports(caseId)]);
        const known = new Set(universe.chips.map((chip) => chip.id));
        const out = switchOnNew(selection, fileSourceIds(tags, imports).filter((id) => !known.has(id)));
        capNote = out.refused ? SOURCES_TEXT.newNotOn : '';
        if (out.selection !== selection) { selection = out.selection; saveSources(caseId, selection, universe); app.rememberTraceSources(selection); }
      } catch { /* the choice stays as it is */ }
    }
    void run(lastBody ?? undefined);
  }

  // A click on a source (or Select all / Clear all) keeps the choice and reads again with it, over the window of the last read.
  function changeSelection(next: SourceSelection) {
    selection = next;
    saveSources(caseId, next, universe);
    app.rememberTraceSources(next);
    void run(lastBody ?? undefined);
    capNote = '';
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

  function openCall(slot: number) {
    void drillSlot; // drillSlot is only ever cleared now (run() reset); this read keeps it a live variable for svelte-check
    if (!result) return;
    const target = makeCallTarget(caseId, result.window, slot, result.lanes.find((l) => l.key === 'audit'), result.sources);
    if (target) { returnSlot = slot; openSlowCall(target); }
  }

  // Open in Analysis: the analysis window is the call's own slot (start and end kept in the target), never the slot number read on the current result,
  // because after a reload the grid can be a different one. The call is cleared in place (no history step back) before the analysis opens.
  function openAnalysisFromCall() {
    const target = app.slowCall;
    if (!target) return;
    handoffNote = '';
    cancelled = false;
    const bucket = { start: target.start, end: target.end };
    const shown = result;
    const sameGrid = !!shown && slotToBucket(shown, visible, target.slot)?.start === target.start;
    closeSlowCall({ handoff: true });
    const ok = handOffBucket(bucket, {
      setWindow: (window) => { setWindowFromHour(window); setTracingHandoff(window, shown && sameGrid ? otherLanesNote(shown, visible, target.slot) : ''); },
      open: openAnalysis
    });
    if (!ok) handoffNote = HANDOFF_TEXT.empty;
  }

  // When the Slow call screen closes (Back, Esc, crumb) the timeline is shown again and focus goes to the slot that was opened (or the chart after a reload).
  let returnSlot: number | null = null;
  let callWasShown = false;
  $effect(() => {
    const shown = callShown;
    const wasShown = callWasShown;
    callWasShown = shown;
    if (!wasShown || shown) return;
    void tick().then(() => {
      if (returnSlot !== null && result) chartFocus = { slot: returnSlot, token: ++focusToken };
      else chartRegion?.focus();
    });
  });

  // The screen takes focus once when it opens (the button that opened it is gone), so a keyboard or screen-reader user starts at its heading.
  // Only when nothing else holds focus: later runs, Try again and Cancel never move it.
  let headingEl: HTMLHeadingElement | undefined;
  onMount(() => {
    const active = document.activeElement;
    if (!active || active === document.body) headingEl?.focus();
    void run();
  });
  onDestroy(() => { controller?.abort(); sqlController?.abort(); sqlSeq++; });
</script>

<section class="tracing-paper" aria-labelledby="tp-title" hidden={callShown}>
  <h2 id="tp-title" tabindex="-1" bind:this={headingEl}>{TRACING_TEXT.heading}</h2>
  {#if title}<p class="case-name" data-testid="tp-case-name">{title}</p>{/if}
  <div class="actions"><button type="button" class="secondary-button" data-testid="tp-case-home" onclick={onClose}><span aria-hidden="true">&larr;</span> {TRACING_TEXT.backToCase}</button></div>
  <!-- A closed case opens here as a viewer: the route only reads, so only the one line tells the reader nothing can be changed. -->
  {#if closed}<p class="closed-line" role="status" data-testid="tp-closed-line">{TRACING_TEXT.closedView}</p>{/if}

  <div class="trgrid" class:hasrail={showRail}>
  <div class="trtop">
  <!-- One card for everything that chooses what the chart shows (the wireframe's `Choose what to show` card): the quick ranges, the two windows,
       the UTC echo, the sources row with Start tracing and Add evidence, then the notices. -->
  <section class="card trctl" aria-label={WINDOW_TEXT.controlsLabel} data-testid="tp-controls">
  <div data-region="windows" class="windows">
    <div class="quick-row" role="group" aria-label={WINDOW_TEXT.quickRow} data-testid="tp-quick-row">
      <span class="quick-label">{WINDOW_TEXT.quickRow}</span>
      {#each quickChips as quick (quick.id)}
        <button type="button" class="chip" data-quick-id={quick.id} aria-pressed={quickPressed(quick)} title={WINDOW_TEXT.quickRowTitle} disabled={readOnly} onclick={() => applyQuickChip(quick.id)}>{quick.label}</button>
      {/each}
    </div>
    <form class="window-form" id="tp-window-form" onsubmit={(event) => { event.preventDefault(); startTracing(); }}>
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
      <p class="metaline" data-testid="tp-metaline"><span class="utc-echo" data-testid="utc-echo-incident">{echoUtc(fields.incidentStart, fields.incidentEnd)}</span> {WINDOW_TEXT.metaCompared} <span class="utc-echo" data-testid="utc-echo-reference">{echoUtc(fields.referenceStart, fields.referenceEnd)}</span> · {WINDOW_TEXT.metaZoneLead} <b>{timeZone.zone}</b> {WINDOW_TEXT.metaZoneTail}</p>
      <div class="window-actions">
        <small>{WINDOW_TEXT.utcNote}</small>
        <small>{WINDOW_TEXT.endNote}</small>
      </div>
    </form>
  </div>
  <div data-region="sources">
    <TraceSources chips={universe.chips} saved={universe.saved} {selection} disabled={readOnly}
      onToggle={(id) => { const out = toggleOutcome(selection, universe.chips, id); if (out.refused) capNote = SOURCES_TEXT.capNote; else changeSelection(out.selection); }} onAll={() => changeSelection(ALL_SOURCES)} onNone={() => changeSelection(NO_SOURCES)}
      onConnect={() => void openConnections('new')}>
      {#snippet actions()}
        <!-- Start tracing submits the windows form above (the form attribute), so Enter in a date box still starts it. -->
        <button type="submit" form="tp-window-form" class="primary-button" title={WINDOW_TEXT.startTitle} disabled={readOnly || status === 'loading'}>{WINDOW_TEXT.startTracing}</button>
        <button type="button" class="secondary-button" title={WINDOW_TEXT.addEvidenceTitle} aria-haspopup="dialog" disabled={closed || readOnly} onclick={openCaseImport}>{WINDOW_TEXT.addEvidence}</button>
      {/snippet}
    </TraceSources>
    {#if capNote !== ''}<p class="empty-hint" role="status" data-testid="tp-source-cap">{capNote}</p>{/if}
  </div>
  <div class="trctl-notices">
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
  </section>
  <div data-region="toolbar">
    {#if !(status === 'loading' && !result)}
      <TraceToolbar {visible} onToggle={(key) => (visible = toggleLane(visible, key))} scrub={exportScrub.forCase(caseId)} />
    {/if}
  </div>
  </div>
  <div class="trbot">
  {#if sqlKind && sqlSource}
    <SqlLaneStatus kind={sqlKind} source={sqlSource.label} reason={sqlReason} enabled={sqlOn} onToggle={setSqlOn} onRead={readSqlNow} onOpenSources={() => void seeSources()} />
  {/if}
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
      {#if closedSaved.length > 0}<p role="status" class="reconnecting" title={SOURCES_TEXT.reconnectingTitle} data-testid="tp-reconnecting">{SOURCES_TEXT.reconnecting}</p>{/if}
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
          <div class="card chartcard">
          <div class="chart-hold" aria-busy={status === 'loading' ? 'true' : undefined}>
            <div inert={status === 'loading'}>
              <TracingChart response={result} zone={timeZone.zone} {visible} onSelect={onBarSelect} focus={chartFocus} unselected={offLanes} {readOnly} onAddFiles={openCaseImport} onDiamond={openCall} />
            </div>
            {#if status === 'loading'}
              <div class="chart-veil">
                <p role="status">{waiting.slow ? WAIT_TEXT.stillReading : TRACING_TEXT.readingWindow}</p>
                {#if closedSaved.length > 0}<p role="status" class="reconnecting" title={SOURCES_TEXT.reconnectingTitle}>{SOURCES_TEXT.reconnecting}</p>{/if}
                <button type="button" class="secondary-button" title={WAIT_TEXT.cancelTitle} onclick={() => void keepFocusInChart(cancel)}>{TRACING_TEXT.cancel}</button>
              </div>
            {/if}
          </div>
          </div>
        {/if}
      {/if}
    {/if}
  </div>
  <div data-region="notes">
    <!-- Below the chart, so saying "Cancelled" never pushes the chart down. -->
    {#if cancelled && status !== 'loading' && status !== 'error'}<p role="status" class="cancel-note">{result ? WINDOW_TEXT.cancelledKept : WINDOW_TEXT.cancelledNone}</p>{/if}
    {#if handoffNote}<p role="status">{handoffNote}</p>{/if}
    {#if status !== 'loading' && result?.reconnectFailed}
      {#each result.reconnectFailed as failed, at (at)}<p role="status" class="reconnect-failed" data-testid="tp-reconnect-failed">{reconnectFailedText(failed.name, failed.reason, failed.action)}</p>{/each}
    {/if}
  </div>
  </div>
  {#if showRail && result}
    <div class="trrail" data-region="suspects"><SuspectsRail response={result} zone={timeZone.zone} onShow={showOnChart} /></div>
  {/if}
  </div>
</section>

{#if callShown && app.slowCall}
  {#key app.slowCall.slot}
    <SlowCallScreen target={app.slowCall} zone={timeZone.zone} demo={!!app.info?.demo}
      onBack={() => closeSlowCall()}
      onFound={(call) => noteSlowCall(call)}
      onOpenAnalysis={() => openAnalysisFromCall()} />
  {/key}
{/if}

<style>
  .tracing-paper {
    display: grid;
    gap: 16px;
  }
  .tracing-paper[hidden] { display: none; }
  /* The wireframe's grid: the controls and the chart in one column; with the rail, a 340px column to their right that stays in view while the page scrolls. Below 1480px the rail follows the chart, so the chart keeps the full width on ordinary screens. */
  .trgrid { display: grid; grid-template-columns: minmax(0, 1fr); gap: 24px; min-width: 0; }
  .trtop, .trbot { display: grid; gap: 24px; min-width: 0; align-content: start; grid-template-columns: minmax(0, 1fr); }
  .trrail { min-width: 0; }
  @media (min-width: 1480px) {
    .trgrid.hasrail { grid-template-columns: minmax(0, 1fr) 340px; align-items: start; }
    .hasrail .trtop { grid-column: 1; grid-row: 1; }
    .hasrail .trbot { grid-column: 1; grid-row: 2; }
    .hasrail .trrail { grid-column: 2; grid-row: 1 / span 2; align-self: start; position: sticky; top: 72px; max-height: calc(100vh - 96px); overflow: auto; padding-bottom: 120px; }
  }
  .closed-line { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .case-name { margin: calc(var(--space-1) * -1) 0 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  /* The one controls card. Nothing inside it has a card of its own, so every row starts at the card's padding edge at every width. */
  .trctl { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-3); min-width: 0; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .trctl-notices { display: grid; gap: var(--space-2); min-width: 0; }
  .trctl-notices:empty { display: none; }
  .metaline { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .utc-echo { font-family: var(--mono); white-space: nowrap; }
  .metaline b { color: var(--text); font-weight: 600; }
  .windows { display: grid; gap: var(--space-2); }
  .quick-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .quick-label { color: var(--text-2); font-size: var(--fs-2); }
  .quick-row .chip:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .window-form { display: grid; gap: var(--space-3); }
  /* The chart sits in its own card (the prototype's .card.chartcard) and fades in once, the first time a read comes back. */
  .chartcard { min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  @media (prefers-reduced-motion: no-preference) {
    .chartcard { animation: chart-reveal 320ms ease-out both; }
  }
  @keyframes chart-reveal { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: none; } }
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
    .trctl { padding: var(--space-3); }
    /* A full ISO stamp is wider than a phone card: let it wrap there instead of widening the whole card. */
    .utc-echo { white-space: normal; overflow-wrap: anywhere; }
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
