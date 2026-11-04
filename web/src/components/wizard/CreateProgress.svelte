<script lang="ts">
  import { tick } from 'svelte';
  import { draftSnapshot } from '../../lib/wizard.svelte';
  import { createView, createNow, failedFileKeys, leaveOutFile, pickHost, pickedFile, pickedHost, progressSummary } from '../../lib/wizardCreate.svelte';
  import { isFileSource, isImportable, shouldOpenAnalysis } from '../../lib/wizardSubmit';
  import type { StepState } from '../../lib/wizardSubmit';
  import { sourceKey } from '../../lib/wizardSources';
  import { hostNeed } from '../../lib/fileKinds';
  import { fileKindLabel } from '../../lib/copy/fileKindsCopy';
  import { kindLabel } from '../../lib/copy/wizardSourcesCopy';
  import { rowOutcomeText } from '../../lib/copy/discoverCopy';
  import { CREATE_TEXT, countCell, failureText, fileStatusText, isHostReason, leaveOutLabel, resultText, rowText, stateText, tryAgainLabel } from '../../lib/copy/wizardCreateCopy';
  import ZoneChoice from './ZoneChoice.svelte';

  // The last screen of the create guide: what the run is doing, source by source, and what to do about anything that did not go through.
  // The case is never deleted here. It opens by itself when every scan source was read and no file was picked; with files the screen stays,
  // because the table of what was read from each file is the answer the person came for (packet P5), with the actions of packet P10:
  // open the timeline, open the case, try the failed files again, add more files.
  let { onOpen, onTimeline, onAddMore }: { onOpen: (caseId: string) => void; onTimeline?: (caseId: string) => void; onAddMore?: (caseId: string) => void } = $props();

  const uid = $props.id();
  let box = $state<HTMLElement>();
  let opened = false;
  let note = $state('');

  const draft = $derived(draftSnapshot());
  const submit = $derived(createView.submit);
  const result = $derived(createView.result);
  const summary = $derived(progressSummary());
  const importable = $derived(draft.sources.filter(isImportable));
  const rows = $derived(importable.filter((source) => !isFileSource(source)).map((source) => ({ key: sourceKey(source), label: source.label || kindLabel(source.kind), row: submit.imports[sourceKey(source)] })));
  const fileRows = $derived(importable.filter(isFileSource).map((source) => {
    const key = sourceKey(source);
    const held = pickedFile(key);
    return { key, name: held ? held.webkitRelativePath || held.name : source.label || kindLabel(source.kind), kind: source.kind, row: submit.imports[key] };
  }));
  const finished = $derived(result !== null && result.finished && !createView.running);
  const locked = $derived(submit.locked && !createView.running);
  const failedKeys = $derived(failedFileKeys());
  const busy = $derived(createView.running);

  function overall(states: StepState[]): StepState {
    if (states.length === 0) return { status: 'skipped', reason: '' };
    if (states.some((s) => s.status === 'running')) return { status: 'running', reason: '' };
    if (states.some((s) => s.status === 'failed')) return { status: 'failed', reason: '' };
    if (states.every((s) => s.status === 'done' || s.status === 'skipped')) return { status: 'done', reason: '' };
    return { status: 'waiting', reason: '' };
  }

  const steps = $derived<{ id: string; label: string; state: Pick<StepState, 'status'> }[]>([
    { id: 'case', label: CREATE_TEXT.stepCase, state: { status: submit.caseId ? 'done' : createView.running ? 'running' : submit.createError ? 'failed' : 'waiting' } },
    { id: 'envs', label: CREATE_TEXT.stepEnvs, state: submit.envs },
    { id: 'tags', label: CREATE_TEXT.stepTags, state: overall([...Object.values(submit.tags), ...Object.values(submit.audit)]) },
    { id: 'imports', label: CREATE_TEXT.stepImports, state: overall(Object.values(submit.imports)) }
  ]);

  // A file that failed is told in its own row of the table, so the list under it keeps only the other steps.
  const fileKeys = $derived(new Set(fileRows.map((item) => item.key)));
  const failures = $derived(summary.failures.filter((failure) => !(failure.step === 'import' && fileKeys.has(failure.key))).map((failure) => {
    const row = failure.step === 'import' ? submit.imports[failure.key] : undefined;
    return { key: failure.step + ':' + failure.key, text: failureText(failure, row?.outcome ? rowOutcomeText(row.outcome) : undefined) };
  }));

  // Everything went through and no file was read: open the analysis at once. With files, or after anything failed, the person reads it first.
  $effect(() => {
    if (opened || !result || createView.running || fileRows.length > 0 || !shouldOpenAnalysis(result)) return;
    opened = true;
    onOpen(result.caseId);
  });

  // The end of a run moves focus to the result so a keyboard or screen reader user lands on what happened.
  $effect(() => {
    if (!finished && !locked) return;
    void tick().then(() => box?.focus());
  });

  const openLabel = $derived(summary.imported > 0 ? CREATE_TEXT.openCase : CREATE_TEXT.openAnyway);

  async function retryOne(key: string) { note = ''; await createNow(undefined, [key]); }
  async function retryAll() { note = ''; await createNow(undefined, failedKeys); }
  async function leaveOut(key: string) {
    leaveOutFile(key);
    note = CREATE_TEXT.leftOutNote;
    await createNow(undefined, []);
  }

  const tone = (row: { status: string; outcome: { kind: string } | null } | undefined): string =>
    !row ? 'waiting' : row.outcome?.kind === 'added' ? 'good' : row.outcome?.kind === 'duplicate' ? 'neutral' : row.status === 'failed' || row.status === 'skipped' ? 'bad' : row.status === 'running' ? 'run' : 'waiting';
  const canFixHost = (kind: string, row: { status: string; reason: string } | undefined): boolean =>
    !!row && (row.status === 'failed' || row.status === 'skipped') && hostNeed(kind) !== 'none' && (isHostReason(row.reason) || hostNeed(kind) !== 'optional');
</script>

<div class="progress">
  <h4 class="head" id="{uid}-head">{CREATE_TEXT.progressHeading}</h4>

  <div class="live" role="status" tabindex="-1" bind:this={box} aria-labelledby="{uid}-head">
    {#if locked}
      <p>{CREATE_TEXT.locked}</p>
    {:else if createView.running}
      <p>{CREATE_TEXT.working_status}</p>
    {:else if result && finished}
      <p>{resultText(summary.imported, summary.total, summary.failures.length, fileRows.length > 0)}</p>
    {/if}
    {#if note !== '' && !createView.running}<p class="note">{note}</p>{/if}
  </div>

  <ol class="steps">
    {#each steps as step (step.id)}
      <li class="step" data-status={step.state.status}><span class="what">{step.label}</span><span class="state">{stateText(step.state)}</span></li>
    {/each}
  </ol>

  {#if rows.length > 0}
    <ul class="rows" aria-label={CREATE_TEXT.stepImports}>
      {#each rows as item (item.key)}
        <li class="row" data-status={item.row?.status ?? 'waiting'}>
          <span class="what">{item.label}</span>
          <span class="state">{item.row ? rowText(item.row) : CREATE_TEXT.waiting}</span>
          {#if item.row?.status === 'running' && item.row.percent !== null}<progress max="100" value={item.row.percent} aria-label={item.label}></progress>{/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#if fileRows.length > 0}
    <section class="files" aria-labelledby="{uid}-files">
      <h5 id="{uid}-files">{CREATE_TEXT.filesHeading}</h5>
      <div class="scroll">
        <table aria-label={CREATE_TEXT.filesCaption}>
          <thead>
            <tr>
              <th scope="col">{CREATE_TEXT.colFile}</th>
              <th scope="col">{CREATE_TEXT.colKind}</th>
              <th scope="col" class="num">{CREATE_TEXT.colRows}</th>
              <th scope="col" class="num">{CREATE_TEXT.colSkipped}</th>
              <th scope="col">{CREATE_TEXT.colResult}</th>
            </tr>
          </thead>
          <tbody>
            {#each fileRows as item (item.key)}
              {@const row = item.row}
              {@const added = row?.outcome?.kind === 'added' ? row.outcome : null}
              {@const failed = !!row && (row.status === 'failed' || row.status === 'skipped')}
              <tr data-status={row?.status ?? 'waiting'} data-tone={tone(row)} data-key={item.key}>
                <th scope="row" class="file"><span class="file-name">{item.name}</span></th>
                <td data-label={CREATE_TEXT.colKind}>{fileKindLabel(item.kind) || kindLabel(item.kind)}</td>
                <td class="num" data-label={CREATE_TEXT.colRows}>{countCell(added ? added.rows : row?.outcome?.kind === 'duplicate' ? 0 : null)}</td>
                <td class="num" data-label={CREATE_TEXT.colSkipped}>{countCell(added ? added.skipped ?? 0 : null)}</td>
                <td class="result">
                  <span class="badge" data-tone={tone(row)}>{row ? fileStatusText(row) : CREATE_TEXT.statusWaiting}</span>
                  <span class="reason">{row ? rowText(row) : CREATE_TEXT.waiting}</span>
                  {#if row?.status === 'running' && row.percent !== null}<progress max="100" value={row.percent} aria-label={item.name}></progress>{/if}
                  {#if failed && !busy}
                    {#if canFixHost(item.kind, row)}
                      <p class="fix">{CREATE_TEXT.hostFix}</p>
                      <ZoneChoice kind={item.kind} host={pickedHost(item.key)} name={item.name} onChange={(label) => pickHost(item.key, label)} />
                    {/if}
                    <span class="row-actions">
                      <button type="button" class="secondary-button" aria-label={tryAgainLabel(item.name)} onclick={() => void retryOne(item.key)}>{CREATE_TEXT.rowTry}</button>
                      <button type="button" class="quiet-button" aria-label={leaveOutLabel(item.name)} onclick={() => void leaveOut(item.key)}>{CREATE_TEXT.rowLeaveOut}</button>
                    </span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="hint">{CREATE_TEXT.skippedHint}</p>
    </section>
  {/if}

  {#if failures.length > 0 && !createView.running}
    <div class="problems" role="alert">
      <ul>
        {#each failures as failure (failure.key)}<li>{failure.text}</li>{/each}
      </ul>
    </div>
  {/if}

  <div class="actions">
    {#if locked}
      <button type="button" class="primary-button" onclick={() => void createNow()}>{CREATE_TEXT.lockedButton}</button>
    {:else if finished && submit.caseId}
      {#if summary.imported > 0 && onTimeline}
        <button type="button" class="primary-button" onclick={() => { opened = true; onTimeline(submit.caseId); }}>{CREATE_TEXT.openTimeline}</button>
      {/if}
      <button type="button" class={summary.imported > 0 && onTimeline ? 'secondary-button' : 'primary-button'} onclick={() => { opened = true; onOpen(submit.caseId); }}>{openLabel}</button>
      {#if failedKeys.length > 0}
        <button type="button" class="secondary-button" disabled={busy} onclick={() => void retryAll()}>{CREATE_TEXT.retryFailed}</button>
      {/if}
      {#if onAddMore}
        <button type="button" class="quiet-button" onclick={() => { opened = true; onAddMore(submit.caseId); }}>{CREATE_TEXT.addMore}</button>
      {/if}
    {/if}
  </div>
</div>

<style>
  .progress { display: grid; gap: var(--space-4); max-width: 72ch; min-width: 0; }
  .head { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .live { display: grid; gap: var(--space-1); min-width: 0; }
  .live:focus { outline: none; }
  .live:focus-visible { outline: 2px solid var(--focus); outline-offset: 4px; }
  .live p { margin: 0; font-size: var(--fs-4); overflow-wrap: anywhere; }
  .live .note { color: var(--text-2); font-size: var(--fs-3); }
  .steps, .rows { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .step, .row { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--space-2) var(--space-4); padding: var(--space-2) var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-4); min-width: 0; }
  .step[data-status='done'], .row[data-status='done'] { border-color: var(--accent); }
  .step[data-status='failed'], .row[data-status='failed'] { border-color: var(--bad); }
  .what { min-width: 0; overflow-wrap: anywhere; }
  .state { color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  progress { flex: 1 1 100%; width: 100%; height: 6px; accent-color: var(--accent); }
  .problems { padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--bad); border-radius: var(--radius-sm); font-size: var(--fs-3); }
  .problems ul { display: grid; gap: var(--space-2); margin: 0; padding-left: var(--space-5); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-3); }

  .files { display: grid; gap: var(--space-2); min-width: 0; }
  .files h5 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .scroll { min-width: 0; overflow-x: auto; border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  table { width: 100%; border-collapse: collapse; font-size: var(--fs-3); white-space: normal; }
  th, td { padding: var(--space-2) var(--space-3); text-align: left; vertical-align: top; border-bottom: var(--hairline-width) solid var(--line); }
  tbody tr:last-child > * { border-bottom: 0; }
  thead th { color: var(--text-2); background: var(--surface-2); font-size: var(--fs-2); font-weight: 600; white-space: nowrap; }
  tbody th.file { min-width: 140px; font-weight: 400; }
  .file-name { font-family: var(--mono); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .num { text-align: right; white-space: nowrap; }
  .result { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-1); min-width: 180px; max-width: 36ch; }
  .badge { justify-self: start; padding: 0 var(--space-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 600; }
  .badge[data-tone='good'] { border-color: var(--accent); }
  .badge[data-tone='bad'] { border-color: var(--bad); }
  .reason { color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .fix { margin: 0; font-size: var(--fs-2); overflow-wrap: anywhere; }
  .row-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  /* A phone is too narrow for five columns: each file becomes a stacked block, the labels come from the column heads. */
  @media (max-width: 560px) {
    .scroll { overflow-x: visible; border: 0; }
    table, tbody, tr, th, td { display: block; }
    thead { position: absolute; width: 1px; height: 1px; margin: -1px; overflow: hidden; clip: rect(0 0 0 0); }
    tbody tr { margin-bottom: var(--space-2); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
    tbody tr[data-tone='bad'] { border-color: var(--bad); }
    tbody th.file { min-width: 0; padding-bottom: 0; border-bottom: 0; background: transparent; }
    th, td { border-bottom: 0; padding-top: var(--space-1); padding-bottom: var(--space-1); }
    td[data-label]::before { content: attr(data-label) ': '; color: var(--text-2); }
    .num { text-align: left; }
    .result { min-width: 0; max-width: none; }
  }
</style>
