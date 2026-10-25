<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { takeDiscoverIntent } from '../discoverIntent';
  import { takeCaseIntent } from '../startIntent';
  import DiscoverPanel from '../../components/DiscoverPanel.svelte';
  import {
    CaseApiError, cancelImportJob, caseCode, checkAllSupplied, commitBlock, fetchImportJob, fileSizeProblem, importProgressPercent, isCaseGone, listHosts, listImporters, listImports,
    previewKey, reasonCounts, startCommitJob, startPreviewJob, waitForImportJob, type CaseInfo, type ImportJob, type ImporterInfo, type ImportRecord, type ImportReport
  } from '../cases';
  import {
    ACK_LABEL, GATING_BANNER, IMPORT_TEXT, ackTextFor, coverageText, detectedText, errorText, formatBytes, importedText, importerLabel, isUnknownHost,
    jobProgressText, qualityDetails, reasonText, sizeProblemText, statusLabel, suppliedPairs
  } from '../copy/importCopy';
  import { importWork } from '../appState.svelte';
  import { goHome } from '../appState.svelte';
  import { formatTime } from '../time';
  import { caseSpan } from '../resourceSpan';
  import Drawer from '../../components/Drawer.svelte';
  import type { Query } from '../types';
  import CasePanel from './CasePanel.svelte';
  import CaseTools from './CaseTools.svelte';
  import HostsEditor from './HostsEditor.svelte';
  import SuppliedSettings from './SuppliedSettings.svelte';
  import HelpNote from '../../components/HelpNote.svelte';

  // The import screen. Create or pick a case, choose a file, read the preview, tick the notice, then import.
  // The notice is always on screen. Nothing is kept until Import file, and an unknown host or a changed file blocks it.
  // The server does the reading: this screen sends the file to the local server only and shows the counts that come back.
  // A file is read in the background: the server answers at once with a job, and this screen asks about it once a second.
  // An import that is keeping a file is noted in importWork (outside this drawer), so closing and reopening the drawer finds it again.
  // The query and zone are the audit window on screen: the timeline uses them to pick its sources and show its times.
  let { onClose, onImported, query, zone = 'UTC' }: { onClose: () => void; onImported?: (record: ImportRecord) => void; query: Query; zone?: string } = $props();

  let cases = $state<CaseInfo[]>([]);
  let selectedId = $state(untrack(() => importWork.running?.caseId ?? ''));
  // The case a person chose on the start screen (taken once), unless an import is already running on another case.
  untrack(() => { const chosen = takeCaseIntent(); if (chosen && !selectedId) selectedId = chosen; });
  let casePanel = $state<CasePanel>();
  let hosts = $state<string[]>([]);
  let imports = $state<ImportRecord[]>([]);

  let file = $state.raw<File | null>(null);
  let fileInput = $state<HTMLInputElement>();
  let importer = $state('');
  let tied = $state<string[]>([]);
  let host = $state('');
  // Settings the owner told you: what the importers declare, and what is typed for the chosen one. Typed values are not logged.
  let importerInfos = $state.raw<ImporterInfo[]>([]);
  let drafts = $state<Record<string, string>>({});
  // A refused file that no importer claimed: the person may name the type, which opens its settings.
  let undetected = $state(false);
  let report = $state.raw<ImportReport | null>(null);
  let previewedKey = $state('');
  let unknownHost = $state(false);
  let acknowledged = $state(false);
  let phase = $state<'idle' | 'previewing' | 'importing'>('idle');
  let problem = $state('');
  let result = $state('');
  let controller: AbortController | null = null;
  let generation = 0;
  // The background job being followed, and what it is for. A running import found again after the drawer was reopened has no file here.
  let job = $state.raw<ImportJob | null>(null);
  let jobKind = $state<'preview' | 'commit'>('commit');
  let resumed = $state(false);
  let cancelling = $state(false);

  const selected = $derived(cases.find((c) => c.id === selectedId) ?? null);
  const caseOpen = $derived(selected !== null && !selected.closed);
  const specs = $derived(importerInfos.find((info) => info.name === importer)?.supplied ?? []);
  const checkedSettings = $derived(checkAllSupplied(specs, drafts));
  const supplied = $derived(checkedSettings.values);
  const settingsProblem = $derived(Object.keys(checkedSettings.problems).length > 0);
  const pickable = $derived(importerInfos.filter((info) => info.supplied.length > 0).map((info) => info.name));
  const pickerNames = $derived(tied.length > 1 ? tied : undetected ? pickable : []);
  const currentKey = $derived(previewKey(file, importer, host, supplied));
  const block = $derived(commitBlock({ caseOpen, previewedKey, currentKey, importer, report, unknownHost, acknowledged, busy: phase !== 'idle', settingsProblem }));
  // What time the case's kept files cover, for the resource panel's notice when the window on screen holds nothing.
  const span = $derived(caseSpan(imports));
  const reasons = $derived(report ? reasonCounts(report.errors) : []);
  const details = $derived(report ? qualityDetails(report.quality) : []);
  const percent = $derived(job ? importProgressPercent(job) : null);
  const progress = $derived(job ? jobProgressText(job.state, percent) : '');
  const showTimeZone = $derived(report?.quality.timezoneRule ?? '');

  let drawer = $state<HTMLElement>();
  // The "This computer" panel. It shows from the start when the start screen asked for it, and when a button inside the drawer opens it.
  let discoverOpen = $state(false);
  let discoverFocus = $state(false);

  function openDiscover() { discoverFocus = true; discoverOpen = true; }
  function closeDiscover() { discoverOpen = false; discoverFocus = false; void tick().then(() => drawer?.focus()); }
  // Files added from a scan change what the case holds, so the history and the case counts are read again.
  function discovered() { void Promise.all([loadImports(selectedId), casePanel?.refresh()]); }

  onMount(() => {
    // Focus starts inside the dialog. Left on the button that opened it, Tab would walk the page behind the panel and the Tab trap would never start.
    drawer?.focus();
    if (takeDiscoverIntent()) discoverOpen = true;
    listHosts().then((labels) => { hosts = labels; }).catch(() => { hosts = []; });
    // A server without the list, or one that cannot be reached, simply offers no settings.
    listImporters().then((list) => { importerInfos = list; }).catch(() => { importerInfos = []; });
    // Closing the drawer stops the asking. A preview is cancelled (nothing else can use it); an import that keeps a file runs on.
    return () => leaveJob();
  });

  // Picking another case starts the form over and loads that case's imports.
  $effect(() => {
    const id = selectedId;
    untrack(() => { resetForm(); result = ''; });
    imports = [];
    if (id) {
      loadImports(id);
      untrack(() => { void resumeRunning(id); });
    }
  });

  async function loadImports(id: string) {
    try {
      const list = await listImports(id);
      if (id === selectedId) imports = list;
    } catch (error) { if (id === selectedId) fail(error); }
  }

  function fail(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    // An erased case drops out of the list, which also clears this form, so the case list says what happened.
    if (isCaseGone(error)) { void casePanel?.noteGone(); return; }
    problem = error instanceof CaseApiError ? errorText(error) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  // Stops following the job. A preview that is still running is cancelled on the server, so it does not hold the case for nothing.
  function leaveJob() {
    controller?.abort();
    const current = job;
    if (current && jobKind === 'preview' && (current.state === 'queued' || current.state === 'running')) void cancelImportJob(selectedId, current.id).catch(() => {});
    job = null;
    cancelling = false;
    resumed = false;
  }

  function resetForm() {
    generation++;
    leaveJob();
    file = null;
    if (fileInput) fileInput.value = '';
    importer = '';
    tied = [];
    host = '';
    drafts = {};
    undetected = false;
    report = null;
    previewedKey = '';
    unknownHost = false;
    acknowledged = false;
    phase = 'idle';
    problem = '';
  }

  function onFile(event: Event) {
    const chosen = (event.currentTarget as HTMLInputElement).files?.[0] ?? null;
    generation++;
    leaveJob();
    importer = ''; tied = []; drafts = {}; undetected = false; report = null; previewedKey = ''; unknownHost = false; acknowledged = false; result = ''; problem = ''; phase = 'idle';
    file = chosen;
    if (chosen) runPreview();
  }

  function pickImporter(name: string) {
    // Another type reads other settings, so what was typed for the last one is dropped.
    if (name !== importer) drafts = {};
    importer = name;
    if (name) runPreview();
  }

  // A setting was picked or a box was left. Only values that fit their key are sent; a bad one holds the import back until it is fixed.
  function applySettings() {
    acknowledged = false;
    if (settingsProblem || !file || !importer) return;
    runPreview();
  }

  function pickHost(label: string) {
    host = label;
    acknowledged = false;
    if (file) runPreview();
  }

  // One read of the whole file, rolled back on the server. A newer request replaces an older one that is still running.
  async function runPreview() {
    if (!file || !selectedId || !caseOpen) return;
    const tooBig = fileSizeProblem(file.size, importer);
    report = null; previewedKey = ''; unknownHost = false; acknowledged = false; result = '';
    if (tooBig) { problem = sizeProblemText(tooBig, file.size); return; }
    problem = '';
    const mine = ++generation;
    leaveJob();
    controller = new AbortController();
    const signal = controller.signal;
    phase = 'previewing';
    jobKind = 'preview';
    const sent = file;
    const caseId = selectedId;
    const sentSupplied = $state.snapshot(supplied);
    try {
      const first = await startPreviewJob({ caseId, file: sent, importer, host, supplied: sentSupplied }, signal);
      // Replaced while the file was still going up: the job exists on the server, so stop it.
      if (mine !== generation) { void cancelImportJob(caseId, first.id).catch(() => {}); return; }
      const answer = await waitForImportJob(caseId, first, { signal, onUpdate: (next) => { if (mine === generation) job = next; } });
      if (mine !== generation) return;
      if (!importer && answer.tied.length > 1) {
        tied = answer.tied;
      } else {
        tied = [];
        undetected = false;
        importer = answer.importer || importer;
        report = answer.report;
        previewedKey = previewKey(sent, importer, host, sentSupplied);
      }
    } catch (error) {
      if (mine !== generation) return;
      if (error instanceof CaseApiError && isUnknownHost(error)) unknownHost = true;
      // Refused with no type named: the owner may know the type, and some types need a setting to be read at all.
      else if (!importer && error instanceof CaseApiError && error.status === 422) undetected = true;
      fail(error);
    } finally {
      if (mine === generation) { phase = 'idle'; job = null; }
    }
  }

  const isAbort = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';
  const forgetRunning = (jobId: string) => { if (importWork.running?.jobId === jobId) importWork.running = null; };

  async function doImport() {
    if (block !== null || !file) return;
    const mine = ++generation;
    leaveJob();
    controller = new AbortController();
    const signal = controller.signal;
    phase = 'importing';
    jobKind = 'commit';
    problem = '';
    const sent = file;
    const caseId = selectedId;
    let first: ImportJob;
    try {
      first = await startCommitJob({ caseId, file: sent, importer, host, supplied: $state.snapshot(supplied), ackText: ackTextFor() }, signal);
    } catch (error) {
      if (mine !== generation) return;
      acknowledged = false;
      fail(error);
      phase = 'idle';
      return;
    }
    if (mine !== generation) return;
    await watchCommit(mine, caseId, first, signal);
  }

  // Follows an import that keeps its file, from the first answer to the last. It is noted in importWork until it ends.
  async function watchCommit(mine: number, caseId: string, first: ImportJob, signal: AbortSignal) {
    importWork.running = { caseId, jobId: first.id };
    try {
      const answer = await waitForImportJob(caseId, first, { signal, onUpdate: (next) => { if (mine === generation) job = next; } });
      forgetRunning(first.id);
      if (mine !== generation) return;
      const record = answer.record;
      if (record && record.status === 'ok') {
        result = importedText(record);
        onImported?.(record);
        resetForm();
      } else {
        problem = errorText({ status: 200, reason: record?.status === 'duplicate' ? 'duplicate' : record?.reason ?? '', importId: record?.id, message: '' });
        acknowledged = false;
      }
      await Promise.all([loadImports(caseId), casePanel?.refresh()]);
    } catch (error) {
      // Closing the drawer or picking another case only stops the asking; the import goes on and is found again.
      if (isAbort(error)) return;
      forgetRunning(first.id);
      if (mine !== generation) return;
      acknowledged = false;
      fail(error);
      await Promise.all([loadImports(caseId), casePanel?.refresh()]);
    } finally {
      if (mine === generation) { phase = 'idle'; job = null; cancelling = false; resumed = false; }
    }
  }

  // The drawer was opened while an import was still running: show it again, and follow it to the end.
  async function resumeRunning(id: string) {
    const running = importWork.running;
    if (!running || running.caseId !== id) return;
    const mine = ++generation;
    controller = new AbortController();
    const signal = controller.signal;
    phase = 'importing';
    jobKind = 'commit';
    resumed = true;
    problem = '';
    let first: ImportJob;
    try {
      first = await fetchImportJob(running.caseId, running.jobId, signal);
    } catch (error) {
      if (isAbort(error)) return;
      forgetRunning(running.jobId);
      if (mine !== generation) return;
      phase = 'idle';
      resumed = false;
      fail(error);
      return;
    }
    if (mine !== generation) return;
    await watchCommit(mine, running.caseId, first, signal);
  }

  // Cancel asks the server to stop the job. The next answer says it failed as cancelled, and the usual failure line shows.
  async function cancelJob() {
    const current = job;
    if (!current || cancelling) return;
    cancelling = true;
    try {
      // A job that just finished keeps its answer: only a job still running is cancelled.
      const latest = await fetchImportJob(selectedId, current.id);
      if (latest.state === 'queued' || latest.state === 'running') await cancelImportJob(selectedId, current.id);
    } catch (error) {
      cancelling = false;
      fail(error);
    }
  }

  const at = (value: string) => formatTime(value, { zone: 'UTC', year: true });
</script>

<Drawer class="import-drawer" title={IMPORT_TEXT.title} titleId="import-title" intro={IMPORT_TEXT.intro} closeLabel={IMPORT_TEXT.closeLabel} bind:element={drawer} {onClose}>
<HelpNote id="import" />
  <p class="gate" role="note" aria-label={IMPORT_TEXT.noticeLabel}>{GATING_BANNER}</p>

  <CasePanel bind:this={casePanel} bind:cases bind:selectedId onClosed={goHome} />

  <!-- Outside the {#key} below, so a new import does not close the panel or reset its checklist. -->
  {#if discoverOpen}
    <DiscoverPanel caseId={selectedId} {caseOpen} {query} {zone} focusOnOpen={discoverFocus} onClose={closeDiscover} onImported={discovered} />
  {/if}

  {#if selected}
    <section class="step" aria-labelledby="file-heading">
      <h3 id="file-heading">{IMPORT_TEXT.fileHeading}</h3>
      {#if selected.closed}
        <p class="note">{IMPORT_TEXT.closedCase}</p>
      {:else}
        <div class="field">
          <label for="import-file">{IMPORT_TEXT.fileLabel}</label>
          <input id="import-file" type="file" bind:this={fileInput} disabled={phase === 'importing'} onchange={onFile} />
          <p class="note">{IMPORT_TEXT.fileHelp}</p>
        </div>

        {#if file}
          <div class="field">
            <label for="import-host">{IMPORT_TEXT.hostLabel}</label>
            <select id="import-host" value={host} disabled={phase !== 'idle'} onchange={(event) => pickHost(event.currentTarget.value)}>
              <option value="">{IMPORT_TEXT.hostNone}</option>
              {#each hosts as label (label)}<option value={label}>{label}</option>{/each}
            </select>
            <p class="note">{hosts.length === 0 ? IMPORT_TEXT.noHosts : IMPORT_TEXT.hostNote}</p>
          </div>
        {/if}

        {#if pickerNames.length > 0}
          <div class="field">
            <label for="import-type">{IMPORT_TEXT.pickerLabel}</label>
            <select id="import-type" value={importer} disabled={phase !== 'idle'} onchange={(event) => pickImporter(event.currentTarget.value)}>
              <option value="" disabled>{IMPORT_TEXT.pickerPlaceholder}</option>
              {#each pickerNames as name (name)}<option value={name}>{importerLabel(name)}</option>{/each}
            </select>
            <p class="note">{tied.length > 1 ? IMPORT_TEXT.pickerTie : IMPORT_TEXT.pickerRefused}</p>
          </div>
        {:else if importer && file}
          <p class="detected">{detectedText(importer)} <code>{importer}</code></p>
        {/if}

        {#if importer && file && specs.length > 0}
          {#key importer}
            <SuppliedSettings {specs} bind:drafts disabled={phase === 'importing'} onApply={applySettings} />
          {/key}
        {/if}
      {/if}
    </section>
  {/if}

  {#if phase !== 'idle'}
    <section class="job" aria-label={progress || IMPORT_TEXT.importing}>
      {#if resumed}<p class="note">{IMPORT_TEXT.jobResumed}</p>{/if}
      <p class="progress-line">{job ? progress : phase === 'previewing' ? IMPORT_TEXT.previewing : IMPORT_TEXT.importing}</p>
      {#if job}
        <progress max="100" value={percent ?? undefined} aria-label={progress}></progress>
        <div class="actions">
          <button type="button" class="secondary-button" disabled={cancelling} onclick={cancelJob}>{cancelling ? IMPORT_TEXT.jobCancelling : IMPORT_TEXT.jobCancel}</button>
        </div>
        <p class="note">{IMPORT_TEXT.jobCancelNote}{jobKind === 'commit' ? ' ' + IMPORT_TEXT.jobCloseNote : ''}</p>
      {/if}
    </section>
  {/if}

  <div class="messages" aria-live="polite">
    {#if unknownHost}<p class="unknown"><span class="badge">{IMPORT_TEXT.unknownHostBadge}</span></p>{/if}
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if result}<p class="done" role="status">{result}</p>{/if}
  </div>

  {#if report}
    <section class="step" aria-labelledby="preview-heading">
      <h3 id="preview-heading">{IMPORT_TEXT.previewHeading}</h3>
      <dl class="facts">
        <div><dt>{IMPORT_TEXT.rowsRead}</dt><dd>{report.rows}</dd></div>
        <div><dt>{IMPORT_TEXT.rowsKept}</dt><dd>{report.emitted}</dd></div>
        <div><dt>{IMPORT_TEXT.rowsRejected}</dt><dd>{report.rejected}</dd></div>
        <div><dt>{IMPORT_TEXT.timeCovered}</dt><dd>{coverageText(report)}</dd></div>
        {#if showTimeZone}<div><dt>{IMPORT_TEXT.timeZoneRule}</dt><dd>{showTimeZone}</dd></div>{/if}
      </dl>

      <h4>{IMPORT_TEXT.reasonsHeading}</h4>
      {#if report.rejected === 0 && reasons.length === 0}
        <p class="note">{IMPORT_TEXT.noRejected}</p>
      {:else}
        <ul class="plain">
          {#each reasons as r (r.reason)}<li><strong>{r.count}</strong> {reasonText(r.reason) ?? r.reason}</li>{/each}
        </ul>
        <p class="note">{IMPORT_TEXT.reasonsSample}</p>
      {/if}

      <h4>{IMPORT_TEXT.gapsHeading}</h4>
      {#if report.knownGaps.length === 0}
        <p class="note">{IMPORT_TEXT.noGaps}</p>
      {:else}
        <ul class="plain">{#each report.knownGaps as gap, i (i)}<li>{gap}</li>{/each}</ul>
      {/if}

      {#if details.length > 0}
        <h4>{IMPORT_TEXT.detailsHeading}</h4>
        <dl class="facts">{#each details as d (d.label)}<div><dt>{d.label}</dt><dd>{d.value}</dd></div>{/each}</dl>
      {/if}

      <p class="note">{IMPORT_TEXT.readOnlyNote}</p>
      <p class="note">{IMPORT_TEXT.previewNote}</p>
    </section>
  {/if}

  {#if selected && !selected.closed && file}
    <section class="step commit" aria-label={IMPORT_TEXT.importButton}>
      <label class="ack">
        <input type="checkbox" bind:checked={acknowledged} disabled={phase !== 'idle'} />
        <span>{ACK_LABEL}</span>
      </label>
      <div class="actions">
        <button type="button" class="primary-button" disabled={block !== null} onclick={doImport}>{phase === 'importing' ? IMPORT_TEXT.importing : IMPORT_TEXT.importButton}</button>
      </div>
      {#if block && block !== 'busy'}<p class="note">{IMPORT_TEXT.blocked[block]}</p>{/if}
    </section>
  {/if}

  <HostsEditor onSaved={(labels) => { hosts = labels; }} />

  {#if selected}
    <!-- A new import changes what the case holds, so a timeline or export preview built before it starts over. -->
    {#key selected.id + ':' + imports.length}<CaseTools caseId={selected.id} {query} {zone} {span} onGone={() => casePanel?.noteGone()} onDiscover={discoverOpen ? undefined : openDiscover} />{/key}
  {/if}

  {#if selected}
    <section class="step" aria-labelledby="history-heading">
      <h3 id="history-heading">{IMPORT_TEXT.historyHeading} <span class="meta">({caseCode(selected.id)})</span></h3>
      {#if imports.length === 0}
        <p class="note">{IMPORT_TEXT.noImports}</p>
      {:else}
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">{IMPORT_TEXT.historyColumns.file}</th>
                <th scope="col">{IMPORT_TEXT.historyColumns.type}</th>
                <th scope="col">{IMPORT_TEXT.historyColumns.host}</th>
                <th scope="col" class="num">{IMPORT_TEXT.historyColumns.rows}</th>
                <th scope="col" class="num">{IMPORT_TEXT.historyColumns.rejected}</th>
                <th scope="col">{IMPORT_TEXT.historyColumns.status}</th>
              </tr>
            </thead>
            <tbody>
              {#each imports as r (r.id)}
                <tr>
                  <td title={r.imported ? at(r.imported) : undefined}>
                    {r.fileName} <span class="meta">({formatBytes(r.bytes)})</span>
                    {#if suppliedPairs(r.supplied).length > 0}
                      <ul class="supplied-list" aria-label={IMPORT_TEXT.suppliedHistory}>
                        {#each suppliedPairs(r.supplied) as pair (pair.key)}<li>{pair.label}: <code>{pair.value}</code></li>{/each}
                      </ul>
                    {/if}
                  </td>
                  <td>{importerLabel(r.importer)}</td>
                  <td>{r.hostLabel || IMPORT_TEXT.noHost}</td>
                  <td class="num">{r.rows}</td>
                  <td class="num">{r.rejected}</td>
                  <td>{statusLabel(r.status)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  {/if}
</Drawer>

<style>
  :global(.connections-drawer.import-drawer) { overflow-x: hidden; gap: var(--space-4); width: min(640px, calc(100vw - 24px)); }
  .gate { margin: var(--space-3) 0 0; padding: 10px 12px; font-size: var(--fs-3); line-height: 1.5; border: 1px solid var(--warn); border-radius: var(--radius-sm); background: color-mix(in srgb, var(--warn) 9%, var(--surface)); overflow-wrap: anywhere; }
  .step { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; letter-spacing: -.01em; }
  h4 { margin: var(--space-2) 0 0; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .field { display: grid; gap: 4px; min-width: 0; }
  .field label { color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .field select, .field input[type='file'] { width: 100%; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .meta { color: var(--text-2); font-size: var(--fs-2); font-weight: 400; }
  .detected { margin: 0; font-size: var(--fs-3); }
  .detected code { font-family: var(--mono); color: var(--text-2); }
  .job { display: grid; gap: var(--space-2); min-width: 0; }
  .job p { margin: 0; }
  .progress-line { font-size: var(--fs-3); font-variant-numeric: tabular-nums; }
  .job progress { width: 100%; height: 8px; accent-color: var(--accent, currentColor); }
  .messages { display: grid; gap: var(--space-2); }
  .messages p { margin: 0; }
  .unknown .badge { padding: 2px 8px; color: var(--bad); border: 1px solid var(--bad); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 500; }
  .done { padding: 8px 0; border: 0; border-top: 1px solid var(--line); border-radius: 0; background: transparent; font-size: var(--fs-3); }
  .facts { display: grid; gap: 4px; margin: 0; font-size: var(--fs-3); }
  .facts div { display: flex; gap: var(--space-3); min-width: 0; }
  .facts dt { flex: 0 0 110px; color: var(--text-2); }
  .facts dd { margin: 0; min-width: 0; overflow-wrap: anywhere; font-variant-numeric: tabular-nums; }
  .plain { display: grid; gap: 4px; margin: 0; padding: 0 0 0 18px; font-size: var(--fs-3); line-height: 1.5; }
  .commit { gap: var(--space-3); }
  .ack { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--fs-3); font-weight: 500; }
  .ack input { flex: 0 0 auto; width: 15px; height: 15px; margin: 3px 0 0; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: var(--fs-2); }
  th, td { padding: 6px 8px; text-align: left; border-bottom: 1px solid var(--line-soft); vertical-align: top; overflow-wrap: anywhere; }
  th { color: var(--text-2); font-weight: 500; border-bottom-color: var(--line); }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
  .supplied-list { display: grid; gap: 2px; margin: 4px 0 0; padding: 0; list-style: none; color: var(--text-2); }
  .supplied-list code { font-family: var(--mono); overflow-wrap: anywhere; }
</style>
