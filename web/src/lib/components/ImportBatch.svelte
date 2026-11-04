<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import ImportFilesPanel from '../../components/wizard/ImportFilesPanel.svelte';
  import { importerFor, usableFiles } from '../fileKinds';
  import type { StagedFile } from '../fileKinds';
  import { importRunner, runImportQueue, tallyBatch } from '../cases';
  import type { BatchItem, RowResult } from '../cases';
  import { BATCH_TEXT, ackTextFor, batchProgressText, batchSummaryText } from '../copy/importCopy';

  // Many files into an open case at once (packet P9, wireframe w112). The list, the drop zone, the kind and computer choices and the Result
  // column are the wizard's own pieces (ImportFilesPanel in drawer mode). This part sends the files one after the other: for each, the
  // file is read once to find (or confirm) its type, then kept with the notice acknowledged. A file that fails never stops the next one.
  let { caseId, disabled = false, busy = $bindable(false), staged = $bindable(0), onFinished }: {
    caseId: string;
    /** Another import on this case is in progress: nothing is sent from here until it ends. */
    disabled?: boolean;
    /** A batch is being sent: the page locks the other import controls of the case while it runs. */
    busy?: boolean;
    /** How many listed files are not imported yet (the Add data card and the leave question use it). */
    staged?: number;
    /** Files were kept (or tried): the history and the case summary should be read again. */
    onFinished?: () => void;
  } = $props();

  let acknowledged = $state(false);
  let stopping = $state(false);
  let stopped = $state(false);
  let progress = $state('');
  let summary = $state('');
  let results = $state<Record<number, RowResult>>({});
  let summaryEl = $state<HTMLElement>();

  // Stopping is polite: the file being sent finishes, the rest wait. Leaving the screen is not: the request is dropped.
  let stopAfter: AbortController | null = null;
  const leave = new AbortController();
  // Leaving ends the run, so the page is told it is no longer busy (the page keeps this value for its own single-file controls).
  onDestroy(() => { leave.abort(); busy = false; staged = 0; });

  const set = (id: number, result: RowResult | null) => {
    const next = { ...results };
    if (result === null) delete next[id]; else next[id] = result;
    results = next;
  };

  function itemOf(row: StagedFile): BatchItem {
    // The type is named only when the person changed it; otherwise the server reads the file and says what it is.
    return { key: String(row.id), file: row.file, importer: row.changed ? importerFor(row.choice) : '', host: row.host };
  }

  // The Stop button and the button that was pressed are gone or disabled when the run ends: the focus goes to the summary, unless the person already moved it.
  async function settleFocus() {
    await tick();
    const active = document.activeElement;
    if (active && active !== document.body && active.isConnected && !(active instanceof HTMLButtonElement && active.disabled)) return;
    summaryEl?.focus();
  }

  async function run(picked: StagedFile[]) {
    // A file set to "Don't use this file" (or refused) is never sent, also not by Try again.
    const rows = usableFiles(picked);
    if (busy || disabled || rows.length === 0) return;
    if (!acknowledged) { summary = BATCH_TEXT.ackFirst; return; }
    busy = true;
    stopping = false;
    stopped = false;
    summary = '';
    stopAfter = new AbortController();
    const mine = caseId;
    const items = rows.map(itemOf);
    for (const row of rows) set(row.id, { state: 'waiting' });
    const runner = importRunner(mine, ackTextFor());
    let started = 0;
    try {
      const done = await runImportQueue(items, (item) => runner(item, leave.signal), {
        signal: stopAfter.signal,
        onStart: (key) => { started += 1; progress = batchProgressText(started, items.length); set(Number(key), { state: 'sending' }); },
        onDone: (key, outcome) => set(Number(key), { state: 'done', outcome })
      });
      if (leave.signal.aborted) return;
      stopped = stopAfter.signal.aborted && done.length < items.length;
      // Files that were not started (or were cut off) go back to being plain rows.
      for (const row of rows) if (results[row.id]?.state !== 'done') set(row.id, null);
      const text = batchSummaryText(tallyBatch(done.map((entry) => entry.outcome)));
      summary = [text, stopped ? BATCH_TEXT.stopped : ''].filter(Boolean).join(' ');
    } finally {
      busy = false;
      stopping = false;
      progress = '';
      acknowledged = false;
      stopAfter = null;
      if (!leave.signal.aborted) { onFinished?.(); void settleFocus(); }
    }
  }

  function stop() {
    stopping = true;
    stopAfter?.abort();
  }
</script>

<section class="step batch" aria-label={BATCH_TEXT.heading}>
  <ImportFilesPanel drawer {busy} canImport={acknowledged && !disabled} {results} bind:pending={staged} onImport={(rows) => void run(rows)} onRetry={(row) => void run([row])} onListChange={() => { acknowledged = false; }}>
    {#snippet footer()}
      <label class="ack">
        <input type="checkbox" bind:checked={acknowledged} disabled={busy || disabled} />
        <span>{BATCH_TEXT.ack}</span>
      </label>
    {/snippet}
  </ImportFilesPanel>

  <div class="status" role="status" aria-live="polite">
    {#if busy}
      <p class="progress">{progress}</p>
      <button type="button" class="quiet-button" disabled={stopping} onclick={stop}>{stopping ? BATCH_TEXT.stopping : BATCH_TEXT.stop}</button>
    {:else if summary !== ''}
      <p class="summary" tabindex="-1" bind:this={summaryEl}>{summary}</p>
    {/if}
  </div>
</section>

<style>
  .step { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  .ack { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--fs-3); font-weight: 500; }
  .ack input { flex: 0 0 auto; width: 15px; height: 15px; margin: 3px 0 0; }
  .status { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-width: 0; }
  .status:empty { display: none; }
  .progress, .summary { margin: 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .summary { font-weight: 600; }
  .summary:focus { outline: none; }
  .summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
</style>
