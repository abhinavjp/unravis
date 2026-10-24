<script lang="ts">
  import { CaseApiError, canSaveCaseExport, isCaseGone, caseExportSummary, confirmCaseExport, fetchTimeline, previewCaseExport, type CaseExportPreview } from '../cases';
  import { IMPORT_TEXT, caseExportSavedText, errorText } from '../copy/importCopy';
  import { contentMatches, describeCount, downloadName } from '../export';
  import type { TimelineView } from '../timeline';
  import type { Query } from '../types';
  import Timeline from './Timeline.svelte';

  // What a case offers once files are in it: a timeline against the audit window on screen, and a redacted export.
  // Both are read-only. The export is previewed whole first and only what the preview showed is saved.
  // The parent gives each case its own copy of this component ({#key}), so nothing here carries over to another case.
  let { caseId, query, zone, onGone }: { caseId: string; query: Query; zone: string; onGone?: () => void } = $props();

  let timeline = $state.raw<TimelineView | null>(null);
  let timelineBusy = $state(false);
  let timelineProblem = $state('');

  type Phase = { kind: 'idle' } | { kind: 'previewing' } | { kind: 'preview'; preview: CaseExportPreview } | { kind: 'saving'; preview: CaseExportPreview } | { kind: 'saved'; preview: CaseExportPreview; sha256: string };
  let phase = $state.raw<Phase>({ kind: 'idle' });
  let exportProblem = $state('');
  let controller: AbortController | null = null;

  const hasSource = $derived(query.sourceIds.length > 0);
  const preview = $derived(phase.kind === 'preview' || phase.kind === 'saving' || phase.kind === 'saved' ? phase.preview : null);
  const summary = $derived(preview ? caseExportSummary(preview.export) : null);

  $effect(() => () => controller?.abort());

  function failure(error: unknown): string {
    if (isCaseGone(error)) onGone?.();
    return error instanceof CaseApiError ? errorText(error) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  async function buildTimeline() {
    if (timelineBusy || !hasSource) return;
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    timelineBusy = true; timelineProblem = '';
    try {
      const view = await fetchTimeline(caseId, query, mine.signal);
      if (!mine.signal.aborted) timeline = view;
    } catch (error) {
      if (mine.signal.aborted || (error instanceof DOMException && error.name === 'AbortError')) return;
      timeline = null;
      timelineProblem = failure(error);
    } finally { if (!mine.signal.aborted) timelineBusy = false; }
  }

  async function buildPreview() {
    if (phase.kind === 'previewing' || phase.kind === 'saving') return;
    exportProblem = '';
    phase = { kind: 'previewing' };
    const mine = new AbortController();
    controller?.abort();
    controller = mine;
    try {
      const result = await previewCaseExport(caseId, mine.signal);
      if (!mine.signal.aborted) phase = { kind: 'preview', preview: result };
    } catch (error) {
      if (mine.signal.aborted || (error instanceof DOMException && error.name === 'AbortError')) return;
      phase = { kind: 'idle' };
      exportProblem = failure(error);
    }
  }

  function cancelPreview() {
    controller?.abort();
    phase = { kind: 'idle' };
    exportProblem = '';
  }

  function download(text: string, name: string, type: string) {
    const url = URL.createObjectURL(new Blob([text], { type }));
    const link = document.createElement('a');
    link.href = url;
    link.download = name;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  async function save() {
    if (phase.kind !== 'preview' || !canSaveCaseExport(phase.preview.export)) return;
    const shown = phase.preview;
    exportProblem = '';
    phase = { kind: 'saving', preview: shown };
    try {
      const file = await confirmCaseExport(shown.previewId);
      // Confirming used the preview up, so on a mismatch nothing is saved and a new preview is needed.
      const jsonOk = await contentMatches(file.content, file.sha256);
      const csvOk = await contentMatches(file.eventsCsv ?? '', file.eventsCsvSha256 ?? '');
      if (!jsonOk || !csvOk) {
        exportProblem = IMPORT_TEXT.exportMismatch;
        phase = { kind: 'idle' };
        return;
      }
      download(file.content, downloadName(file), 'application/json');
      const csvName = downloadName({ filename: file.eventsCsvFilename });
      download(file.eventsCsv ?? '', csvName === 'evidence-export.json' ? 'unravis-events.csv' : csvName, 'text/csv');
      phase = { kind: 'saved', preview: shown, sha256: file.sha256 };
    } catch (error) {
      exportProblem = failure(error);
      phase = { kind: 'preview', preview: shown };
    }
  }
</script>

<section class="tools" aria-labelledby="timeline-tool-heading">
  <h3 id="timeline-tool-heading">{IMPORT_TEXT.timelineHeading}</h3>
  <p class="note">{IMPORT_TEXT.timelineNote}</p>
  <div><button type="button" class="secondary-button" disabled={timelineBusy || !hasSource} onclick={buildTimeline}>{timelineBusy ? IMPORT_TEXT.timelineBuilding : IMPORT_TEXT.timelineBuild}</button></div>
  {#if !hasSource}<p class="note">{IMPORT_TEXT.timelineNoSource}</p>{/if}
  <div aria-live="polite">{#if timelineProblem}<div class="inline-error" role="alert">{timelineProblem}</div>{/if}</div>
  {#if timeline}<Timeline view={timeline} {zone} />{/if}
</section>

<section class="tools" aria-labelledby="case-export-heading">
  <h3 id="case-export-heading">{IMPORT_TEXT.exportHeading}</h3>
  <p class="note">{IMPORT_TEXT.exportNote}</p>
  <div class="actions">
    <button type="button" class="secondary-button" disabled={phase.kind === 'previewing' || phase.kind === 'saving'} onclick={buildPreview}>{phase.kind === 'previewing' ? IMPORT_TEXT.exportPreviewing : IMPORT_TEXT.exportPreview}</button>
    {#if phase.kind === 'preview'}<button type="button" class="secondary-button" onclick={cancelPreview}>{IMPORT_TEXT.exportCancel}</button>{/if}
  </div>

  <div aria-live="polite">
    {#if exportProblem}<div class="inline-error" role="alert">{exportProblem}</div>{/if}
    {#if phase.kind === 'previewing'}<p class="note" role="status">{IMPORT_TEXT.exportPreviewing}</p>{/if}
  </div>

  {#if preview && summary}
    {#if summary.empty}
      <p class="empty">{IMPORT_TEXT.exportEmpty}</p>
    {:else}
      <p class="line">{summary.line}</p>
    {/if}
    {#if summary.statements.length > 0}
      <h4>{IMPORT_TEXT.exportMasking}</h4>
      <ul class="plain">{#each summary.statements as statement, i (i)}<li>{statement}</li>{/each}</ul>
    {/if}
    {#if (preview.export.body.imports ?? []).length > 0}
      <div class="table-wrap">
        <table>
          <thead><tr><th scope="col">{IMPORT_TEXT.historyColumns.type}</th><th scope="col">{IMPORT_TEXT.historyColumns.host}</th><th scope="col" class="num">{IMPORT_TEXT.historyColumns.rows}</th><th scope="col" class="num">{IMPORT_TEXT.historyColumns.rejected}</th></tr></thead>
          <tbody>
            {#each preview.export.body.imports ?? [] as item (item.ref)}
              <tr><td>{item.importer}</td><td>{item.host || IMPORT_TEXT.noHost}</td><td class="num">{describeCount(item.rows)}</td><td class="num">{describeCount(item.rejected)}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    <p class="hash">{IMPORT_TEXT.exportHash}: <code>{summary.hash}</code></p>
    <details>
      <summary>{IMPORT_TEXT.exportShowFile}</summary>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <pre tabindex="0" aria-label={IMPORT_TEXT.exportShowFile}>{JSON.stringify(preview.export, null, 2)}</pre>
    </details>
    {#if phase.kind === 'saved'}
      <p class="done" role="status">{caseExportSavedText(phase.sha256)}</p>
    {:else}
      <div class="actions">
        <button type="button" class="primary-button" disabled={phase.kind !== 'preview' || !canSaveCaseExport(preview.export)} onclick={save}>{phase.kind === 'saving' ? IMPORT_TEXT.exportSaving : IMPORT_TEXT.exportSave}</button>
      </div>
      {#if !summary.empty}<p class="note">{IMPORT_TEXT.exportSaveNote}</p>{/if}
    {/if}
  {/if}
</section>

<style>
  .tools { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  h4 { margin: var(--space-2) 0 0; color: var(--text-2); font-size: var(--fs-2); font-weight: 650; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .line { margin: 0; font-size: var(--fs-3); font-weight: 600; font-variant-numeric: tabular-nums; }
  .empty, .done { margin: 0; padding: 8px 10px; font-size: var(--fs-3); border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface-2); overflow-wrap: anywhere; }
  .plain { display: grid; gap: 4px; margin: 0; padding: 0 0 0 18px; font-size: var(--fs-3); line-height: 1.5; }
  .hash { margin: 0; font-size: var(--fs-2); color: var(--text-2); overflow-wrap: anywhere; }
  .hash code { font-family: var(--mono); user-select: all; }
  details summary { width: fit-content; color: var(--text-2); font-size: var(--fs-2); }
  pre { max-height: 320px; margin: 6px 0 0; padding: 8px 10px; overflow: auto; font-family: var(--mono); font-size: 11px; line-height: 1.45; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); white-space: pre-wrap; overflow-wrap: anywhere; }
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: var(--fs-2); }
  th, td { padding: 6px 8px; text-align: left; border-bottom: 1px solid var(--line-soft); vertical-align: top; overflow-wrap: anywhere; }
  th { color: var(--text-2); font-weight: 650; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
</style>
