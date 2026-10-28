<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { contentMatches } from '../../export';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { byteLabel, reportFileName, reportWarningCodes, reportWarnings } from '../../guided/report';
  import type { ReportInclude } from '../../guided/types';

  // Export: an HTML report or a CSV of the numbers, built on the server from the run on screen. No payloads, no user ids, no connection
  // details, no passwords. The file is saved only when its checksum matches what the server sent.
  let { store, sourceIds }: { store: GuidedStore; sourceIds: string[] } = $props();

  let include = $state<ReportInclude>({ figures: true, suspects: true, checklist: true, sources: true });
  let preview = $state('');
  let saved = $state('');
  let problem = $state('');
  let ready = $state('');
  let previewButton = $state<HTMLButtonElement>();
  let closeButton = $state<HTMLButtonElement>();

  function closePreview() {
    preview = '';
    previewButton?.focus();
  }
  // Opening the preview puts focus on its Close button, so Escape reaches this page (a key pressed inside the sandboxed frame does not).
  $effect(() => {
    if (preview) closeButton?.focus();
  });

  const empty = $derived(!store.answer);
  const warnings = $derived(reportWarnings(store.answer?.read, store.outOfDate(sourceIds), T.truncated, T.exportStale));
  const building = $derived(store.reportState === 'building');
  const OPTIONS: ReadonlyArray<{ key: keyof ReportInclude; label: string }> = [
    { key: 'figures', label: T.includeFigures }, { key: 'suspects', label: T.includeSuspects },
    { key: 'checklist', label: T.includeChecklist }, { key: 'sources', label: T.includeSources }
  ];

  async function build(format: 'html' | 'csv') {
    problem = '';
    saved = '';
    ready = '';
    // The warnings travel as codes inside `include`; the server prints a fixed sentence for each in the saved file.
    const sent: ReportInclude & { warnings: string[] } = { ...include, warnings: reportWarningCodes(store.outOfDate(sourceIds), store.answer?.read) };
    return store.buildReport(format, sent);
  }

  async function showPreview() {
    const out = await build('html');
    if (out) { preview = out.content; ready = T.exportReady(reportFileName(out), byteLabel(out.bytes)); }
  }

  async function download(format: 'html' | 'csv') {
    const out = await build(format);
    if (!out) return;
    // A file that does not match its checksum is not saved: it may have been changed on the way.
    if (!(await contentMatches(out.content, out.sha256))) { problem = T.exportMismatch; return; }
    const name = reportFileName(out);
    const url = URL.createObjectURL(new Blob([out.content], { type: out.mime || (format === 'csv' ? 'text/csv' : 'text/html') }));
    const link = document.createElement('a');
    link.href = url;
    link.download = name;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
    saved = T.exportSaved(out.sha256);
    ready = T.exportReady(name, byteLabel(out.bytes));
  }
</script>

<section class="export" aria-labelledby="export-title">
  <header><h3 id="export-title">{T.exportHeading}</h3></header>
  <p class="lead">{T.exportLead}</p>
  {#if empty}<p class="notice" role="status">{T.exportEmpty}</p>{/if}

  <fieldset>
    <legend>{T.exportOptions}</legend>
    {#each OPTIONS as option (option.key)}
      <label class="check"><input type="checkbox" bind:checked={include[option.key]} />{option.label}</label>
    {/each}
    <label class="check off"><input type="checkbox" disabled />{T.includeNearby}</label>
    <p class="note">{T.includeNearbyNeeds}</p>
  </fieldset>

  {#if warnings.length > 0}
    <div class="warn" role="alert">
      <strong>{T.exportWarningsHeading}</strong>
      <ul>{#each warnings as w (w)}<li>{w}</li>{/each}</ul>
    </div>
  {/if}

  <div class="row">
    <button type="button" class="primary-button" disabled={building} onclick={() => void download('html')}>{warnings.length > 0 ? T.downloadHtmlWarn : T.downloadHtml}</button>
    <button type="button" class="secondary-button" disabled={building} onclick={() => void download('csv')}>{T.downloadCsv}</button>
    <button type="button" class="secondary-button" bind:this={previewButton} disabled={building} onclick={() => void showPreview()}>{T.preview}</button>
  </div>
  <p class="note">{T.exportPrivacy}</p>

  {#if building}<p class="loading-state" role="status">{T.exportBuilding}</p>{/if}
  {#if store.reportState === 'failed'}<p class="inline-error" role="alert">{store.reportError || T.exportFailed}</p>{/if}
  {#if problem}<p class="inline-error" role="alert">{problem}</p>{/if}
  {#if ready}<p class="note" role="status">{ready}</p>{/if}
  {#if saved}<p class="note" role="status">{saved}</p>{/if}

  {#if preview}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="preview" onkeydown={(event) => { if (event.key === 'Escape') { event.stopPropagation(); closePreview(); } }}>
      <iframe title={T.preview} sandbox="" srcdoc={preview}></iframe>
      <div><button type="button" class="quiet-button" bind:this={closeButton} onclick={closePreview}>{T.exportClose}</button></div>
    </div>
  {/if}
</section>

<style>
  .export { display: grid; gap: 12px; min-width: 0; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  h3 { margin: 0; font-size: 16px; }
  .lead, .note { margin: 0; font-size: 13px; }
  .note { color: var(--text-2); font-size: 12px; }
  fieldset { display: grid; gap: 4px; margin: 0; padding: 8px 12px; border: 1px solid var(--line-soft); border-radius: var(--radius-sm); }
  legend { padding: 0 4px; font-size: 12px; font-weight: 600; }
  .check { display: flex; align-items: center; gap: 8px; font-size: 13px; }
  .off { color: var(--text-2); }
  .warn { display: grid; gap: 4px; padding: 8px 12px; border: 1px solid var(--warn); border-radius: var(--radius-sm); font-size: 13px; }
  .warn ul { margin: 0; padding-left: 18px; }
  .row { display: flex; flex-wrap: wrap; gap: 8px; }
  .preview { display: grid; gap: 8px; }
  iframe { width: 100%; height: 420px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
</style>
