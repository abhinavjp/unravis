<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { CaseApiError, importProgressPercent } from '../lib/cases';
  import {
    CANCEL_GUARD_MS, DEFAULT_SAMPLER_INTERVAL, GROUPS, MAX_FINDINGS_PER_CALL, SAMPLER_INTERVALS, WINDOW_CHOICES, ageMinutes, cachedEntryState, cancelAllowed, classifyError,
    customTimeProblem, defaultSelection, defaultWindowChoice, discoverClient, effectiveCost, estimateBytes, groupOf, incidentWindow, isSelectable, pickedFindings, scanExpired, selectAllSmall, toggled, waitForScan, watchJob, windowFor,
    windowProblem, type DetectorProgress, type DiscoverClient, type EntryState, type Finding, type JobRef, type RowOutcome, type SamplerStatus, type Scan, type WindowChoice
  } from '../lib/discover';
  import {
    DISCOVER_TEXT, blockedText, detectorLine, estimateText, finishedText, foundText, gapReasonText, hostNameText, howText, importButtonText, liveStatusText, liveStopText,
    liveToggleLabel, privilegeBadge, problemText, rowMetaText, rowOutcomeText, rowOutcomeTone, rowProgressText, showsText, timeFieldText, toneLabel, windowChoiceLabel,
    windowProblemText
  } from '../lib/copy/discoverCopy';
  import { ackTextFor } from '../lib/copy/importCopy';
  import { formatTime } from '../lib/time';
  import type { Query } from '../lib/types';

  // The "This computer" panel: scan this Windows machine for logs, tick what to read, pick a time range, read the notice, import.
  // The scan reads names and sizes only. Nothing is opened until Import. The panel names a source by the id the server gave it and never sends a path.
  // Each step has its own heading, and focus moves to it when the step changes. Progress is in aria-live regions.
  // It needs a case to keep files in: without one the checklist still works, and Import says to choose a case first.
  let { caseId = '', caseOpen = true, query, zone = 'UTC', client = discoverClient, focusOnOpen = false, onClose, onImported }: {
    caseId?: string; caseOpen?: boolean; query: Query; zone?: string; client?: DiscoverClient; focusOnOpen?: boolean; onClose: () => void; onImported?: () => void;
  } = $props();

  type Step = 'intro' | 'scanning' | 'list' | 'importing' | 'results';
  interface RowState { id: string; display: string; state: string; percent: number | null; outcome: RowOutcome | null }

  let entry = $state.raw<EntryState>({ kind: 'loading' });
  let step = $state<Step>('intro');
  let problem = $state('');
  // A plain message that is not a fault, such as a scan the person cancelled.
  let info = $state('');

  let scan = $state.raw<Scan | null>(null);
  let scanProgress = $state.raw<DetectorProgress[]>([]);
  let cancellingScan = $state(false);
  let scanId = '';
  let cancelWanted = false;
  // A cancel control can appear where the start control was, so it waits a moment before it takes a click (a double-click must not cancel).
  let scanShownAt = 0;
  let scanCancelReady = $state(false);
  let importShownAt = 0;
  let importCancelReady = $state(false);

  let picked = $state.raw<Set<string>>(new Set());
  let choice = $state<WindowChoice>('24h');
  let customFrom = $state('');
  let customTo = $state('');
  let acknowledged = $state(false);

  let rows = $state<RowState[]>([]);
  let jobIds: Record<string, string> = {};
  let runCase = '';
  let cancellingImport = $state(false);

  let sampler = $state.raw<SamplerStatus | null>(null);
  let interval = $state<number>(DEFAULT_SAMPLER_INTERVAL);
  let liveBusy = $state(false);

  let stepHeading = $state<HTMLElement>();
  let panelHeading = $state<HTMLElement>();
  let controller: AbortController | null = null;
  let generation = 0;

  const elevated = $derived(entry.kind === 'ready' && entry.elevated);
  const incident = $derived(incidentWindow(query.start, query.end));
  const findings = $derived(scan?.findings ?? []);
  const gaps = $derived(scan?.gaps ?? []);
  const groups = $derived(GROUPS.map((id) => ({ id, findings: findings.filter((f) => groupOf(f) === id), gaps: gaps.filter((g) => groupOf(g) === id) })).filter((g) => g.findings.length + g.gaps.length > 0));
  const chosen = $derived(pickedFindings(findings, picked));
  const choices = $derived(WINDOW_CHOICES.filter((c) => c !== 'incident' || incident !== null));
  const range = $derived(windowFor(choice, Date.now(), incident, { from: customFrom, to: customTo }));
  const rangeProblem = $derived(windowProblem(range));
  const customTyped = $derived(customFrom.trim() !== '' || customTo.trim() !== '');
  // Each custom time is checked on its own once anything is typed, so the message names the field and the fault.
  const fromProblem = $derived(choice === 'custom' && customTyped ? customTimeProblem(customFrom) : null);
  const toProblem = $derived(choice === 'custom' && customTyped ? customTimeProblem(customTo) : null);
  const fromText = $derived(fromProblem ? timeFieldText('from', fromProblem) : '');
  const toText = $derived(toProblem ? timeFieldText('to', toProblem) : '');
  const fieldsBad = $derived(fromProblem !== null || toProblem !== null);
  // The range message (order, too long, nothing chosen) shows only when no field has its own message, so one fault is told once.
  const rangeShown = $derived(rangeProblem !== null && !fieldsBad && (choice !== 'custom' || customTyped));
  const windowShown = $derived(fieldsBad || rangeShown);
  const rangeInvalid = $derived(choice === 'custom' && rangeShown);
  const estimate = $derived(range && !rangeProblem ? estimateBytes(chosen, range) : null);
  const onlyDenied = $derived(!elevated && scan !== null && findings.length === 0 && gaps.length > 0 && gaps.every((g) => g.reason === 'needs-elevation'));
  const caseReady = $derived(caseId !== '' && caseOpen);
  const block = $derived(
    !caseReady ? 'case' : chosen.length === 0 ? 'none' : chosen.length > MAX_FINDINGS_PER_CALL ? 'tooMany' : rangeProblem ? 'window' : !acknowledged ? 'ack' : null
  );
  const added = $derived(rows.filter((r) => r.outcome?.kind === 'added').length);
  const recording = $derived(sampler?.state === 'recording');
  const now = () => Date.now();

  const isAbort = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';
  const errorText = (error: unknown): string => problemText(classifyError(error), error instanceof CaseApiError ? error : undefined);
  const at = (value: string) => formatTime(value, { zone, precision: 'minute' });

  function focusStep() { void tick().then(() => stepHeading?.focus()); }

  onMount(() => {
    let alive = true;
    // Opened by a button inside the page: focus goes to the panel's heading, which also scrolls it into view.
    if (focusOnOpen) panelHeading?.focus();
    void cachedEntryState(client).then((state) => { if (alive) entry = state; });
    return () => {
      alive = false;
      controller?.abort();
      // A scan the panel was still waiting on is stopped, so it does not run for nobody. An import keeps running on the server.
      if (step === 'scanning' && scanId) void client.cancelScan(scanId).catch(() => {});
    };
  });

  async function retryEntry() {
    entry = { kind: 'loading' };
    entry = await cachedEntryState(client, true);
  }

  /* ---------------------------------------------------------------- scanning */

  async function startScan() {
    // A second click of a double-click, or a click while a scan runs, does nothing.
    if (step === 'scanning' || step === 'importing') return;
    problem = ''; info = '';
    scan = null; scanProgress = []; scanId = ''; cancelWanted = false; cancellingScan = false;
    const mine = ++generation;
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    step = 'scanning';
    scanShownAt = now();
    scanCancelReady = false;
    setTimeout(() => { if (mine === generation) scanCancelReady = true; }, CANCEL_GUARD_MS);
    focusStep();
    try {
      scanId = await client.startScan(signal);
      if (cancelWanted) await client.cancelScan(scanId, signal).catch(() => {});
      const done = await waitForScan(client, scanId, { signal, onUpdate: (next) => { if (mine === generation) scanProgress = next.progress; } });
      if (mine !== generation) return;
      if (done.state === 'cancelled') { info = DISCOVER_TEXT.scanCancelled; step = 'intro'; }
      else if (done.state === 'failed') { problem = DISCOVER_TEXT.scanFailed; step = 'intro'; }
      else {
        scan = done;
        picked = defaultSelection(done.findings, elevated);
        choice = defaultWindowChoice(incident);
        acknowledged = false;
        step = 'list';
      }
    } catch (error) {
      if (isAbort(error) || mine !== generation) return;
      problem = errorText(error);
      step = 'intro';
    }
    focusStep();
    if (step === 'list') void refreshSampler();
  }

  async function cancelScan() {
    if (cancellingScan || !cancelAllowed(scanShownAt, now())) return;
    cancellingScan = true;
    cancelWanted = true;
    if (!scanId) return;
    try { await client.cancelScan(scanId); } catch (error) { cancellingScan = false; problem = errorText(error); }
  }

  /* ---------------------------------------------------------------- the checklist */

  const tickable = (f: Finding) => isSelectable(f);
  const isLarge = (f: Finding) => effectiveCost(f) === 'large';
  const needsExport = (f: Finding) => f.kind === 'export-needed' || f.kind === 'remote-only';

  function tick1(f: Finding, on: boolean) { picked = toggled(picked, f.id, on); }
  const tickSmall = () => { picked = selectAllSmall(findings, picked); };
  const clearAll = () => { picked = new Set(); };

  /* ---------------------------------------------------------------- importing */

  function setRow(id: string, patch: Partial<RowState>) {
    const row = rows.find((r) => r.id === id);
    if (row) Object.assign(row, patch);
  }

  async function runImports() {
    if (block !== null || !scan || !range) return;
    if (scanExpired(scan.startedAt, now())) { problem = DISCOVER_TEXT.scanExpired; return; }
    const sending = chosen;
    // The case is fixed for this run: picking another case in the drawer does not move these files.
    const forCase = caseId;
    runCase = forCase;
    const mine = ++generation;
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    problem = ''; info = '';
    cancellingImport = false;
    importShownAt = now();
    importCancelReady = false;
    setTimeout(() => { if (mine === generation) importCancelReady = true; }, CANCEL_GUARD_MS);
    jobIds = {};
    rows = sending.map((f) => ({ id: f.id, display: f.display, state: 'queued', percent: null, outcome: null }));
    step = 'importing';
    focusStep();
    let refs: JobRef[];
    try {
      if (scan.host?.proposed) await client.confirmHost?.(scan.host, signal);
      refs = await client.startImports(forCase, { scanId, findingIds: sending.map((f) => f.id), window: range, ackGating: true, ackText: ackTextFor() }, signal);
    } catch (error) {
      if (isAbort(error) || mine !== generation) return;
      acknowledged = false;
      problem = errorText(error);
      step = 'list';
      focusStep();
      return;
    }
    if (mine !== generation) return;
    for (const ref of refs) jobIds[ref.findingId] = ref.jobId;
    await Promise.all(sending.map(async (f) => {
      const ref = refs.find((r) => r.findingId === f.id);
      if (!ref) { setRow(f.id, { outcome: { kind: 'failed', reason: 'unreachable' } }); return; }
      try {
        const outcome = await watchJob(client, forCase, ref, {
          signal,
          onUpdate: (watch) => { if (mine === generation && watch.job) setRow(f.id, { state: watch.job.state, percent: importProgressPercent(watch.job) }); }
        });
        if (mine === generation) setRow(f.id, { outcome });
      } catch (error) {
        if (!isAbort(error) && mine === generation) setRow(f.id, { outcome: { kind: 'failed', reason: 'unreachable' } });
      }
    }));
    if (mine !== generation) return;
    step = 'results';
    focusStep();
    if (added > 0) onImported?.();
  }

  async function cancelImports() {
    if (cancellingImport || !cancelAllowed(importShownAt, now())) return;
    cancellingImport = true;
    await Promise.all(rows.filter((r) => r.outcome === null && jobIds[r.id]).map((r) => client.cancelJob(runCase, jobIds[r.id]).catch(() => {})));
  }

  function backToList() {
    problem = '';
    acknowledged = false;
    step = 'list';
    focusStep();
  }

  const rowText = (row: RowState): string => (row.outcome ? rowOutcomeText(row.outcome) : rowProgressText(row.state, row.percent));

  /* ---------------------------------------------------------------- live recording */

  async function refreshSampler() {
    if (!caseReady) return;
    try {
      const before = sampler?.samples ?? 0;
      sampler = await client.getSampler(caseId);
      // Recorded samples are imports too: the history and the period the files cover are read again when the count moved.
      if (sampler.samples !== before) onImported?.();
    } catch { /* No answer means no recording to show. */ }
  }

  // While a recording is on, its sample count is read again every few seconds. The check stops when the recording does.
  $effect(() => {
    if (!recording || !caseReady) return;
    const timer = setInterval(() => { void refreshSampler(); }, 5000);
    return () => clearInterval(timer);
  });

  async function toggleLive(event: Event) {
    const box = event.currentTarget as HTMLInputElement;
    const on = box.checked;
    if (liveBusy) { box.checked = recording; return; }
    liveBusy = true;
    problem = '';
    try {
      if (on && scan?.host?.proposed) await client.confirmHost?.(scan.host);
      if (on) sampler = await client.startSampler(caseId, { intervalSec: interval, ackText: ackTextFor() });
      else {
        await client.stopSampler(caseId);
        sampler = await client.getSampler(caseId).catch(() => ({ state: 'stopped', since: sampler?.since ?? '', samples: sampler?.samples ?? 0, stopReason: '' }) as SamplerStatus);
        onImported?.();
      }
    } catch (error) {
      problem = errorText(error);
    } finally {
      liveBusy = false;
      // The box follows what the server says, not what was clicked.
      box.checked = recording;
    }
  }

  const liveLine = $derived(
    sampler?.state === 'recording' ? liveStatusText(sampler.since ? at(sampler.since) : '', sampler.samples)
      : sampler?.state === 'stopped' ? liveStopText(sampler.stopReason, sampler.samples) : ''
  );
</script>

<section class="discover" aria-labelledby="discover-heading">
  <div class="head">
    <h3 id="discover-heading" tabindex="-1" bind:this={panelHeading}>{DISCOVER_TEXT.heading}</h3>
    <button type="button" class="secondary-button" onclick={onClose}>{DISCOVER_TEXT.close}</button>
  </div>

  <div class="messages" aria-live="polite">
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if info}<p class="note" role="status">{info}</p>{/if}
  </div>

  {#if entry.kind === 'loading'}
    <p class="note" role="status">{DISCOVER_TEXT.entryLoading}</p>
  {:else if entry.kind === 'unsupported'}
    <p class="note">{DISCOVER_TEXT.windowsOnly}</p>
  {:else if entry.kind === 'unavailable'}
    <p class="note">{DISCOVER_TEXT.unavailable}</p>
  {:else if entry.kind === 'hidden'}
    <p class="note">{problemText('unreachable')}</p>
    <div class="actions"><button type="button" class="secondary-button" onclick={retryEntry}>{DISCOVER_TEXT.retry}</button></div>
  {:else if step === 'intro'}
    <p class="note">{DISCOVER_TEXT.intro}</p>
    <p class="badge-row"><span class="badge" class:on={elevated}>{privilegeBadge(elevated)}</span></p>
    <div class="actions"><button type="button" class="primary-button" onclick={startScan}>{scan ? DISCOVER_TEXT.scanAgain : DISCOVER_TEXT.scanButton}</button></div>
  {:else if step === 'scanning'}
    <div class="step" aria-live="polite">
      <h4 tabindex="-1" bind:this={stepHeading}>{DISCOVER_TEXT.scanning}</h4>
      <progress aria-label={DISCOVER_TEXT.scanning}></progress>
      {#if scanProgress.length > 0}
        <ul class="plain">{#each scanProgress as item (item.detector)}<li>{detectorLine(item.detector, item.state)}</li>{/each}</ul>
      {:else}
        <p class="note">{DISCOVER_TEXT.scanningNow}</p>
      {/if}
    </div>
    <div class="actions"><button type="button" class="secondary-button" disabled={cancellingScan || !scanCancelReady} onclick={cancelScan}>{cancellingScan ? DISCOVER_TEXT.scanCancelling : DISCOVER_TEXT.scanCancel}</button></div>
  {:else if step === 'list' && scan}
    <div class="step">
      <h4 tabindex="-1" bind:this={stepHeading}>{DISCOVER_TEXT.listHeading}</h4>
      <p class="note" role="status">{foundText(findings.length, gaps.length)}</p>
      {#if scan.host?.label}<p class="note">{hostNameText(scan.host.label)}. {DISCOVER_TEXT.hostNote}</p>{/if}
      {#if findings.length === 0}
        <p class="empty">{onlyDenied ? DISCOVER_TEXT.allDenied : DISCOVER_TEXT.nothingFound}</p>
        <div class="actions"><button type="button" class="secondary-button" onclick={startScan}>{DISCOVER_TEXT.scanAgain}</button></div>
      {:else}
        <p class="note">{DISCOVER_TEXT.listNote}</p>
        <div class="actions">
          <button type="button" class="secondary-button" onclick={tickSmall}>{DISCOVER_TEXT.tickSmall}</button>
          <button type="button" class="secondary-button" onclick={clearAll}>{DISCOVER_TEXT.clearAll}</button>
        </div>
      {/if}

      {#each groups as group (group.id)}
        <fieldset class="group">
          <legend>{DISCOVER_TEXT.groups[group.id]}</legend>
          {#each group.findings as f (f.id)}
            {@const meta = rowMetaText(f, ageMinutes(f.newest, now()))}
            <div class="row">
              {#if tickable(f)}
                <label class="pick">
                  <input type="checkbox" checked={picked.has(f.id)} onchange={(event) => tick1(f, event.currentTarget.checked)} />
                  <span class="name">{f.display}</span>
                </label>
              {:else}
                <p class="name plain-name">{f.display}</p>
              {/if}
              <div class="tags">
                {#if f.needsAdmin}<span class="tag warn">{DISCOVER_TEXT.tagAdmin}</span>{/if}
                {#if isLarge(f)}<span class="tag">{DISCOVER_TEXT.tagLarge}</span>{/if}
                {#if needsExport(f)}<span class="tag">{DISCOVER_TEXT.tagExport}</span>{/if}
              </div>
              <p class="shows">{showsText(f.shows, f.importer, needsExport(f))}</p>
              {#if meta}<p class="meta">{meta}</p>{/if}
              {#if needsExport(f)}
                <p class="meta">{DISCOVER_TEXT.exportFirst}</p>
                <p class="how">{howText(f.how)}</p>
              {:else if f.kind === 'live-series'}
                <p class="meta">{DISCOVER_TEXT.liveRow}</p>
              {/if}
            </div>
          {/each}
          {#each group.gaps as g, i (i)}
            <div class="row gap">
              <label class="pick">
                <input type="checkbox" disabled aria-describedby={'gap-' + group.id + '-' + i} />
                <span class="name">{g.display}</span>
              </label>
              <p class="meta" id={'gap-' + group.id + '-' + i}>{gapReasonText(g.reason)}</p>
            </div>
          {/each}
        </fieldset>
      {/each}

      {#if chosen.length > 0}
        <fieldset class="group window">
          <legend>{DISCOVER_TEXT.windowHeading}</legend>
          <div class="choices">
            {#each choices as c (c)}
              <label class="choice"><input type="radio" name="discover-window" value={c} bind:group={choice} /><span>{windowChoiceLabel(c)}</span></label>
            {/each}
          </div>
          {#if choice === 'custom'}
            <div class="custom">
              <div class="custom-field">
                <label>{DISCOVER_TEXT.customFrom}<input type="text" inputmode="numeric" placeholder="2001-09-11 08:00" autocomplete="off" bind:value={customFrom}
                  aria-invalid={fromProblem || rangeInvalid ? 'true' : undefined} aria-describedby={'discover-custom-help' + (fromText ? ' discover-from-problem' : '') + (rangeInvalid ? ' discover-window-problem' : '')} /></label>
                <div aria-live="polite">{#if fromText}<p class="note problem" id="discover-from-problem">{fromText}</p>{/if}</div>
              </div>
              <div class="custom-field">
                <label>{DISCOVER_TEXT.customTo}<input type="text" inputmode="numeric" placeholder="2001-09-11 09:00" autocomplete="off" bind:value={customTo}
                  aria-invalid={toProblem || rangeInvalid ? 'true' : undefined} aria-describedby={'discover-custom-help' + (toText ? ' discover-to-problem' : '') + (rangeInvalid ? ' discover-window-problem' : '')} /></label>
                <div aria-live="polite">{#if toText}<p class="note problem" id="discover-to-problem">{toText}</p>{/if}</div>
              </div>
            </div>
            <p class="note" id="discover-custom-help">{DISCOVER_TEXT.customHelp}</p>
          {/if}
          <div aria-live="polite">
            {#if rangeShown && rangeProblem}
              <p class="note problem" id="discover-window-problem">{windowProblemText(rangeProblem)}</p>
            {:else if !windowShown && estimate !== null}
              <p class="estimate">{estimateText(estimate)}</p>
            {/if}
          </div>
        </fieldset>
      {/if}

      {#if findings.length > 0}
        <section class="notice" aria-labelledby="discover-notice-heading">
          <h4 id="discover-notice-heading">{DISCOVER_TEXT.noticeHeading}</h4>
          <p class="note">{DISCOVER_TEXT.noticePointer}</p>
          <label class="ack">
            <input type="checkbox" bind:checked={acknowledged} />
            <span>{DISCOVER_TEXT.ack}</span>
          </label>
          <div class="actions">
            <button type="button" class="primary-button" disabled={block !== null || chosen.length === 0} onclick={runImports}>{importButtonText(chosen.length)}</button>
            <button type="button" class="secondary-button" onclick={startScan}>{DISCOVER_TEXT.scanAgain}</button>
          </div>
          {#if block && !(block === 'window' && windowShown)}<p class="note">{blockedText(block)}</p>{/if}
        </section>
      {/if}

      <section class="live" aria-labelledby="discover-live-heading">
        <h4 id="discover-live-heading">{DISCOVER_TEXT.liveHeading}</h4>
        <p class="note">{DISCOVER_TEXT.liveHelp}</p>
        {#if !elevated}<p class="note">{DISCOVER_TEXT.liveAdminNote}</p>{/if}
        {#if findings.length === 0 && !recording}
          <p class="note">{DISCOVER_TEXT.noticePointer}</p>
          <label class="ack">
            <input type="checkbox" bind:checked={acknowledged} />
            <span>{DISCOVER_TEXT.ack}</span>
          </label>
        {/if}
        {#if !recording}
          <label class="inline-field">{DISCOVER_TEXT.liveIntervalLabel}
            <select bind:value={interval} disabled={liveBusy}>{#each SAMPLER_INTERVALS as seconds (seconds)}<option value={seconds}>{seconds}</option>{/each}</select>
          </label>
        {/if}
        <label class="ack">
          <input type="checkbox" checked={recording} disabled={liveBusy || (!recording && (!caseReady || !acknowledged))} onchange={toggleLive} />
          <span>{liveBusy ? (recording ? DISCOVER_TEXT.liveStopping : DISCOVER_TEXT.liveStarting) : liveToggleLabel(interval)}</span>
        </label>
        {#if !recording && !caseReady}<p class="note">{blockedText('case')}</p>{:else if !recording && !acknowledged}<p class="note">{blockedText('ack')}</p>{/if}
        <p class="note" aria-live="polite">{liveLine}</p>
      </section>
    </div>
  {:else if step === 'importing' || step === 'results'}
    <div class="step">
      <h4 tabindex="-1" bind:this={stepHeading}>{step === 'importing' ? DISCOVER_TEXT.importing : DISCOVER_TEXT.resultsHeading}</h4>
      <p class="visually-hidden" role="status">{step === 'results' ? finishedText(added, rows.length) : DISCOVER_TEXT.importing}</p>
      <ul class="rows">
        {#each rows as row (row.id)}
          {@const tone = rowOutcomeTone(row.outcome)}
          <li class="result {tone}">
            <span class="name">{row.display}</span>
            <span class="status">{#if tone !== 'pending'}<span class="mark" data-tone={tone}><span aria-hidden="true">{tone === 'good' ? '✓' : tone === 'bad' ? '✕' : '–'}</span> {toneLabel(tone)}.</span> {/if}{rowText(row)}</span>
            {#if row.outcome === null}<progress max="100" value={row.percent ?? undefined} aria-label={row.display + ': ' + rowText(row)}></progress>{/if}
          </li>
        {/each}
      </ul>
      {#if step === 'results'}<p class="done" aria-hidden="true">{finishedText(added, rows.length)}</p>{/if}
    </div>
    <div class="actions">
      {#if step === 'importing'}
        <button type="button" class="secondary-button" disabled={cancellingImport || !importCancelReady} onclick={cancelImports}>{cancellingImport ? DISCOVER_TEXT.importCancelling : DISCOVER_TEXT.importCancel}</button>
      {:else}
        <button type="button" class="primary-button" onclick={backToList}>{DISCOVER_TEXT.chooseAgain}</button>
        <button type="button" class="secondary-button" onclick={onClose}>{DISCOVER_TEXT.close}</button>
      {/if}
    </div>
  {/if}
</section>

<style>
  .discover { display: grid; gap: var(--space-3); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  .head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; letter-spacing: -.01em; }
  h4 { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  h4:focus-visible { outline: 2px solid var(--focus, currentColor); outline-offset: 3px; border-radius: var(--radius); }
  .step { display: grid; gap: var(--space-2); min-width: 0; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; overflow-wrap: anywhere; }
  .problem { color: var(--bad); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .messages { display: grid; gap: var(--space-2); }
  .empty, .done { margin: 0; padding: 8px 0; border: 0; border-top: 1px solid var(--line); border-radius: 0; background: transparent; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .badge-row { margin: 0; }
  .badge { padding: 2px 8px; color: var(--text-2); border: 1px solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 500; }
  .badge.on { color: var(--text); border-color: var(--text-2); }
  .plain { display: grid; gap: 4px; margin: 0; padding: 0 0 0 18px; font-size: var(--fs-3); line-height: 1.5; }
  progress { width: 100%; height: 8px; accent-color: var(--accent, currentColor); }
  .group { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: var(--space-3) 0 0; border: 0; border-top: 1px solid var(--hairline); }
  .group legend { padding: 0 var(--space-2) 0 0; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .row { display: grid; gap: 2px; min-width: 0; padding: 6px 0; border-bottom: 1px solid var(--line-soft); }
  /* A gap row is dimmed by its name alone. Its reason keeps the full secondary text colour, which clears 4.5:1 in both themes (an opacity on the row did not). */
  .row.gap .name { color: var(--text-2); }
  /* Every tick box and its label is a target at least 24px high and wide. */
  .pick, .ack, .choice { display: flex; align-items: flex-start; gap: var(--space-2); min-height: 24px; font-size: var(--fs-3); font-weight: 500; }
  .choice { align-items: center; font-weight: 400; }
  .pick input, .ack input, .choice input { flex: 0 0 auto; width: 24px; height: 24px; margin: 0; }
  .pick .name, .ack span, .choice span { align-self: center; padding-block: 2px; }
  .name { min-width: 0; overflow-wrap: anywhere; }
  .plain-name { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  .tags { display: flex; flex-wrap: wrap; gap: 4px; }
  .tags:empty { display: none; }
  .tag { padding: 1px 7px; color: var(--text-2); border: 1px solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 500; }
  .tag.warn { color: var(--text); border-color: var(--warn); }
  .shows, .meta, .how { margin: 0; font-size: var(--fs-2); line-height: 1.5; overflow-wrap: anywhere; }
  .shows { color: var(--text); }
  .meta { color: var(--text-2); font-variant-numeric: tabular-nums; }
  .how { padding: 4px 8px; font-family: var(--mono); background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius-sm); }
  .choices { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-4); }
  .custom { display: flex; flex-wrap: wrap; gap: var(--space-3); }
  .custom-field { display: grid; gap: 4px; align-content: start; min-width: 0; }
  .custom label, .inline-field { display: grid; gap: 4px; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .custom input, .inline-field select { min-height: 28px; }
  .estimate { margin: 0; font-size: var(--fs-3); font-weight: 600; font-variant-numeric: tabular-nums; }
  .notice, .live { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-3); border-top: 1px solid var(--hairline); }
  .rows { display: grid; gap: 0; margin: 0; padding: 0; list-style: none; }
  .result { display: grid; gap: 4px; min-width: 0; padding: 8px 0; border: 0; border-top: 1px solid var(--line-soft); border-radius: 0; background: transparent; }
  .mark { color: var(--text); font-weight: 600; }
  .mark[data-tone='bad'] { color: var(--bad); }
  .status { color: var(--text-2); font-size: var(--fs-2); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
</style>
