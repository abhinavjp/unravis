<script lang="ts">
  import { tick } from 'svelte';
  import { draftSnapshot } from '../../lib/wizard.svelte';
  import { createView, createNow, progressSummary } from '../../lib/wizardCreate.svelte';
  import { isImportable, shouldOpenAnalysis } from '../../lib/wizardSubmit';
  import type { StepState } from '../../lib/wizardSubmit';
  import { sourceKey } from '../../lib/wizardSources';
  import { kindLabel } from '../../lib/copy/wizardSourcesCopy';
  import { rowOutcomeText } from '../../lib/copy/discoverCopy';
  import { CREATE_TEXT, failureText, resultText, rowText, stateText } from '../../lib/copy/wizardCreateCopy';

  // The last screen of the create guide: what the run is doing, source by source, and what to do about anything that did not go through.
  // The case is never deleted here. It opens when at least one source was read, or when the person asks for it anyway.
  let { onOpen }: { onOpen: (caseId: string) => void } = $props();

  const uid = $props.id();
  let box = $state<HTMLElement>();
  let opened = false;

  const draft = $derived(draftSnapshot());
  const submit = $derived(createView.submit);
  const result = $derived(createView.result);
  const summary = $derived(progressSummary());
  const rows = $derived(draft.sources.filter(isImportable).map((source) => ({ key: sourceKey(source), label: source.label || kindLabel(source.kind), row: submit.imports[sourceKey(source)] })));
  const finished = $derived(result !== null && result.finished && !createView.running);
  const locked = $derived(submit.locked && !createView.running);

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

  const failures = $derived(summary.failures.map((failure) => {
    const row = failure.step === 'import' ? submit.imports[failure.key] : undefined;
    return { key: failure.step + ':' + failure.key, text: failureText(failure, row?.outcome ? rowOutcomeText(row.outcome) : undefined) };
  }));

  // Everything went through: open the analysis at once. Anything else waits for the person to read it first.
  $effect(() => {
    if (opened || !result || createView.running || !shouldOpenAnalysis(result)) return;
    opened = true;
    onOpen(result.caseId);
  });

  // The end of a run moves focus to the result so a keyboard or screen reader user lands on what happened.
  $effect(() => {
    if (!finished && !locked) return;
    void tick().then(() => box?.focus());
  });

  const openLabel = $derived(summary.imported > 0 ? CREATE_TEXT.openCase : CREATE_TEXT.openAnyway);
</script>

<div class="progress">
  <h4 class="head" id="{uid}-head">{CREATE_TEXT.progressHeading}</h4>

  <div class="live" role="status" tabindex="-1" bind:this={box} aria-labelledby="{uid}-head">
    {#if locked}
      <p>{CREATE_TEXT.locked}</p>
    {:else if createView.running}
      <p>{CREATE_TEXT.working_status}</p>
    {:else if result && finished}
      <p>{resultText(summary.imported, summary.total, failures.length)}</p>
    {/if}
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
      <button type="button" class="primary-button" onclick={() => { opened = true; onOpen(submit.caseId); }}>{openLabel}</button>
    {/if}
  </div>
</div>

<style>
  .progress { display: grid; gap: var(--space-4); max-width: 72ch; min-width: 0; }
  .head { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .live { min-width: 0; }
  .live:focus { outline: none; }
  .live:focus-visible { outline: 2px solid var(--focus); outline-offset: 4px; }
  .live p { margin: 0; font-size: var(--fs-4); overflow-wrap: anywhere; }
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
</style>
