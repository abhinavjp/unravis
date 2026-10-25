<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { formatTime } from '../lib/time';
  import {
    EXPORT_TEXT, RECORD_FIELDS, canSave, contentMatches, describeCount, downloadName, errorText, exportInput, exportSummary, qualityRows, savedLine, stateLabel,
    type ExportPhase
  } from '../lib/export';
  import type { Query } from '../lib/types';
  import Hint from './Hint.svelte';
  import Drawer from './Drawer.svelte';
  import HelpNote from './HelpNote.svelte';

  // The Export evidence drawer. Three steps: choose options, preview the whole file, then save it.
  // Nothing is saved until the analyst presses "Save file", and only what the preview showed can be saved.
  let { query, zone, sourceLabels, onClose }: { query: Query; zone: string; sourceLabels: Map<string, string>; onClose: () => void } = $props();
  let phase = $state<ExportPhase>({ kind: 'idle' });
  let includePseudonyms = $state(false);
  let includeLedger = $state(false);
  let problem = $state('');
  let firstOption = $state<HTMLInputElement>();
  let controller: AbortController | null = null;

  const sources = $derived(query.sourceIds.map((id) => sourceLabels.get(id) ?? id).join(', '));
  const at = (value: string) => formatTime(value, { zone });
  const busy = $derived(phase.kind === 'previewing' || phase.kind === 'saving');

  onMount(() => firstOption?.focus());
  $effect(() => () => controller?.abort());

  // Changing an option makes the shown preview out of date, so it is dropped rather than saved by mistake.
  function optionChanged() {
    if (phase.kind === 'previewing') controller?.abort();
    if (phase.kind !== 'saving') { phase = { kind: 'idle' }; problem = ''; }
  }
  function reset() {
    controller?.abort();
    phase = { kind: 'idle' };
    problem = '';
  }
  async function preview() {
    if (busy) return;
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    problem = '';
    phase = { kind: 'previewing' };
    try {
      const result = await api.exportPreview(exportInput(query, includePseudonyms, includeLedger));
      if (mine.signal.aborted) return;
      phase = { kind: 'preview', preview: result };
    } catch (cause) {
      if (mine.signal.aborted) return;
      phase = { kind: 'idle' };
      problem = errorText(cause, 'preview');
    }
  }
  async function save() {
    if (!canSave(phase) || phase.kind !== 'preview') return;
    const shown = phase.preview;
    problem = '';
    phase = { kind: 'saving', preview: shown };
    try {
      const file = await api.exportConfirm(shown.previewId);
      // The confirm call used up the preview, so on a mismatch nothing is saved and a new preview is needed.
      if (!(await contentMatches(file.content, file.sha256))) {
        problem = EXPORT_TEXT.hashMismatch;
        phase = { kind: 'idle' };
        return;
      }
      const filename = downloadName(file);
      const url = URL.createObjectURL(new Blob([file.content], { type: 'application/json' }));
      const link = document.createElement('a');
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 0);
      phase = { kind: 'saved', preview: shown, filename, sha256: file.sha256 };
    } catch (cause) {
      problem = errorText(cause, 'save');
      phase = { kind: 'preview', preview: shown };
    }
  }
</script>

<Drawer class="glossary-drawer export-drawer" title={EXPORT_TEXT.title} titleId="export-title" intro={EXPORT_TEXT.intro} closeLabel="Close export" focusable={'button:not(:disabled), input:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])'} {onClose}>
<HelpNote id="export" />

  <section class="export-window" aria-label="Window">
    <dl>
      <div><dt>Sources</dt><dd>{sources || 'None selected'}</dd></div>
      <div><dt>From</dt><dd>{at(query.start)}</dd></div>
      <div><dt>To</dt><dd>{at(query.end)}</dd></div>
      <div><dt>Time zone</dt><dd>{zone}</dd></div>
    </dl>
  </section>

  <fieldset class="export-options" disabled={busy}>
    <legend>{EXPORT_TEXT.options}</legend>
    <label class="export-check">
      <input type="checkbox" bind:this={firstOption} bind:checked={includePseudonyms} onchange={optionChanged} aria-describedby="export-pseudonyms-note" />
      <span>{EXPORT_TEXT.pseudonymsLabel}<Hint termId="pseudonym" /></span>
    </label>
    <p class="export-note" id="export-pseudonyms-note">{EXPORT_TEXT.pseudonymsNote}</p>
    <label class="export-check">
      <input type="checkbox" bind:checked={includeLedger} onchange={optionChanged} aria-describedby="export-ledger-note" />
      <span>{EXPORT_TEXT.ledgerLabel}</span>
    </label>
    <p class="export-note" id="export-ledger-note">{EXPORT_TEXT.ledgerNote}</p>
  </fieldset>

  <div class="export-actions">
    <button type="button" class="primary-button" disabled={busy || phase.kind === 'saved' || query.sourceIds.length === 0} onclick={preview}>
      {phase.kind === 'previewing' ? EXPORT_TEXT.previewing : phase.kind === 'preview' || phase.kind === 'saving' ? EXPORT_TEXT.previewAgain : EXPORT_TEXT.preview}
    </button>
    {#if phase.kind === 'preview' || phase.kind === 'saved'}
      <button type="button" class="secondary-button" onclick={reset}>{phase.kind === 'saved' ? EXPORT_TEXT.previewAgain : EXPORT_TEXT.cancel}</button>
    {/if}
  </div>
  {#if query.sourceIds.length === 0}<p class="export-note">{EXPORT_TEXT.noSources}</p>{:else if phase.kind === 'idle'}<p class="export-note">{EXPORT_TEXT.previewNote}</p>{/if}

  <div aria-live="polite">
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if phase.kind === 'previewing'}<p class="export-note" role="status">{EXPORT_TEXT.previewing}</p>{/if}
    {#if phase.kind === 'preview' || phase.kind === 'saving' || phase.kind === 'saved'}
      {@const exp = phase.preview.export}
      {@const summary = exportSummary(exp)}
      {#if summary.empty}
        <p class="export-empty">{EXPORT_TEXT.noFindings}</p>
      {/if}

      <section class="export-block" aria-label={EXPORT_TEXT.masking}>
        <h3>{EXPORT_TEXT.masking}<Hint termId="thin-cell" /></h3>
        <ul class="export-statements">
          {#each summary.statements as statement, i (i)}<li>{statement}</li>{/each}
        </ul>
      </section>

      {#if !summary.empty}
        <p class="export-summary">{summary.line}</p>

        <section class="export-block" aria-label={EXPORT_TEXT.findings}>
          <h3>{EXPORT_TEXT.findings}</h3>
          <ul class="export-records">
            {#each exp.body.records as record (record.evidenceId)}
              <li class="export-record">
                <div class="record-meta">
                  <span class="record-label">{record.label}</span>
                  <span class="record-rule">{record.rule}</span>
                  <span class="record-n">n = {describeCount(record.n)}</span>
                  {#if record.lowN}<span class="low-n">{EXPORT_TEXT.lowN}</span>{/if}
                </div>
                <p class="record-headline">{record.headline}</p>
                <dl class="record-facts">
                  <div><dt>{RECORD_FIELDS.evidenceId}</dt><dd><code>{record.evidenceId}</code></dd></div>
                  <div><dt>{RECORD_FIELDS.source}</dt><dd>{record.source}</dd></div>
                  <div><dt>{RECORD_FIELDS.window}</dt><dd>{record.windowStart} to {record.windowEnd}</dd></div>
                  <div><dt>{RECORD_FIELDS.join}</dt><dd>{record.joinClass}</dd></div>
                  <div><dt>{RECORD_FIELDS.tolerance}</dt><dd>{record.tolerance}</dd></div>
                  <div><dt>{RECORD_FIELDS.provenance}</dt><dd>{record.provenance.kind}: {record.provenance.ref} <code>{record.provenance.hash}</code></dd></div>
                </dl>
                <h4>{EXPORT_TEXT.knownGaps}</h4>
                {#if record.knownGaps?.length}
                  <ul class="record-list">{#each record.knownGaps as gap, i (i)}<li>{gap}</li>{/each}</ul>
                {:else}
                  <p class="export-note">{EXPORT_TEXT.noGaps}</p>
                {/if}
                {#if qualityRows(record.sourceQuality).length}
                  <h4>{EXPORT_TEXT.sourceQuality}</h4>
                  <dl class="record-facts">
                    {#each qualityRows(record.sourceQuality) as row (row.label)}<div><dt>{row.label}</dt><dd>{row.value}</dd></div>{/each}
                  </dl>
                {/if}
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      {#if exp.body.pseudonyms}
        <section class="export-block" aria-label={EXPORT_TEXT.pseudonyms}>
          <h3>{EXPORT_TEXT.pseudonyms}<Hint termId="pseudonym" /></h3>
          {#if exp.body.pseudonyms.length === 0}
            <p class="export-note">{EXPORT_TEXT.pseudonymsNone}</p>
          {:else}
            <div class="table-scroll">
              <table class="export-table">
                <thead><tr><th scope="col">Label</th><th scope="col" class="num">Requests</th></tr></thead>
                <tbody>
                  {#each exp.body.pseudonyms as row, i (i)}
                    <tr><th scope="row">{row.label}</th><td class="num">{describeCount(row.requests)}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>
      {/if}

      {#if exp.body.ledger}
        <section class="export-block" aria-label={EXPORT_TEXT.ledger}>
          <h3>{EXPORT_TEXT.ledger}</h3>
          {#if exp.body.ledger.length === 0}
            <p class="export-note">{EXPORT_TEXT.ledgerNone}</p>
          {:else}
            <ul class="export-records">
              {#each exp.body.ledger as entry, i (i)}
                <li class="export-record">
                  <p class="record-headline">{entry.text}</p>
                  <div class="record-meta">
                    <span class="record-label">{stateLabel(entry.state)}</span>
                    <span class="record-n">{entry.evidenceIds?.length ? entry.evidenceIds.join(', ') : EXPORT_TEXT.noEvidenceIds}</span>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {/if}

      <section class="export-block" aria-label={EXPORT_TEXT.manifest}>
        <h3>{EXPORT_TEXT.manifest}</h3>
        <dl class="record-facts">
          <div><dt>{EXPORT_TEXT.hashLabel}</dt><dd><code>{summary.hash}</code></dd></div>
        </dl>
        <details class="export-raw">
          <summary>{EXPORT_TEXT.showFile}</summary>
          <p class="export-note">{EXPORT_TEXT.showFileNote}</p>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre tabindex="0" aria-label={EXPORT_TEXT.showFile}>{JSON.stringify(exp, null, 2)}</pre>
        </details>
      </section>

      <div class="export-save">
        {#if phase.kind === 'saved'}
          <p class="export-saved" role="status">{savedLine(phase.sha256)}</p>
          <p class="export-note">{phase.filename}</p>
        {:else}
          <button type="button" class="primary-button" disabled={!canSave(phase)} onclick={save}>{phase.kind === 'saving' ? EXPORT_TEXT.saving : EXPORT_TEXT.save}</button>
          {#if !summary.empty}<p class="export-note">{EXPORT_TEXT.saveNote}</p>{/if}
        {/if}
      </div>
    {/if}
  </div>
</Drawer>

<style>
  :global(.connections-drawer.export-drawer) { overflow-x: hidden; gap: 14px; }
  .export-window { margin-top: 13px; min-width: 0; }
  .export-window dl { display: grid; gap: 4px; margin: 0; font-size: 13px; }
  .export-window dl div { display: flex; gap: 10px; min-width: 0; }
  .export-window dt { flex: 0 0 74px; color: var(--text-2); }
  .export-window dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .export-options { display: grid; gap: 6px; margin: 0; padding: 10px 0 0; border: 0; border-top: 1px solid var(--line); border-radius: 0; min-width: 0; }
  .export-options legend { padding: 0 6px 0 0; color: var(--text-2); font-size: 12px; font-weight: 500; }
  .export-check { display: flex; align-items: flex-start; gap: 8px; font-size: 14px; font-weight: 500; }
  .export-check input { flex: 0 0 auto; width: 15px; height: 15px; margin: 3px 0 0; }
  .export-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .export-options .export-note { margin: 0 0 6px 23px; }
  .export-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .export-empty { margin: 0; padding: 10px 0; font-size: 14px; border: 0; border-top: 1px solid var(--line); border-radius: 0; background: transparent; }
  .export-summary { margin: 0; font-size: 15px; font-weight: 600; font-variant-numeric: tabular-nums; }
  .export-block { min-width: 0; margin-top: 4px; }
  .export-block h3 { margin: 0 0 8px; font-size: 14px; font-weight: 600; letter-spacing: -.01em; }
  .export-statements { display: grid; gap: 6px; margin: 0; padding: 0 0 0 18px; font-size: 13px; line-height: 1.5; }
  .export-records { display: grid; gap: 0; margin: 0; padding: 0; list-style: none; }
  .export-record { min-width: 0; padding: 10px 0; background: transparent; border: 0; border-top: 1px solid var(--line-soft); border-radius: 0; }
  .export-record h4 { margin: 10px 0 4px; color: var(--text-2); font-size: 12px; font-weight: 500; }
  .record-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; font-size: 12px; }
  .record-label { padding: 2px 8px; color: var(--text); border: 1px solid var(--line); border-radius: var(--radius-sm); font-weight: 500; }
  .record-rule, .record-n { color: var(--text-2); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .low-n { padding: 1px 6px; color: var(--warn); border: 1px solid var(--warn); border-radius: var(--radius-sm); }
  .record-headline { margin: 6px 0 0; font-size: 13px; overflow-wrap: anywhere; }
  .record-facts { display: grid; gap: 3px; margin: 8px 0 0; font-size: 12px; }
  .record-facts div { display: flex; gap: 10px; min-width: 0; }
  .record-facts dt { flex: 0 0 110px; color: var(--text-2); }
  .record-facts dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .record-facts code, .export-block code { font-family: var(--mono); font-size: 11px; user-select: all; overflow-wrap: anywhere; }
  .record-list { margin: 0; padding-left: 18px; font-size: 12px; }
  .table-scroll { overflow-x: auto; }
  .export-table { width: 100%; border-collapse: collapse; font-size: 12px; }
  .export-table th, .export-table td { padding: 5px 8px; text-align: left; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .export-table thead th { background: transparent; color: var(--text-2); font-weight: 500; border-bottom-color: var(--line); }
  .export-table .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .export-raw { margin-top: 10px; }
  .export-raw summary { width: fit-content; color: var(--text-2); font-size: 13px; }
  .export-raw pre { max-height: 320px; margin: 6px 0 0; padding: 8px 10px; overflow: auto; font-family: var(--mono); font-size: 11px; line-height: 1.45; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius-sm); white-space: pre-wrap; overflow-wrap: anywhere; }
  .export-save { display: grid; justify-items: start; gap: 6px; margin-top: 8px; padding-top: 12px; border-top: 1px solid var(--line); }
  .export-saved { margin: 0; font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
</style>
