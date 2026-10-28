<script lang="ts">
  import { onMount } from 'svelte';
  import { draftSnapshot, wizard } from '../../lib/wizard.svelte';
  import { scanIsStale } from '../../lib/wizardSources.svelte';
  import { REVIEW_WINDOWS, blockers, chooseWindow, createView, loadTitles, pickFile, pickedFile, setAck, titleRepeats } from '../../lib/wizardCreate.svelte';
  import { filesMissing, importPlan, isFileSource, reviewCounts } from '../../lib/wizardSubmit';
  import { sourceKey } from '../../lib/wizardSources';
  import { kindLabel } from '../../lib/copy/wizardSourcesCopy';
  import { ACK_LABEL, GATING_BANNER } from '../../lib/copy/importCopy';
  import { windowChoiceLabel } from '../../lib/copy/discoverCopy';
  import { CREATE_TEXT, chooseFileLabel, failureText, fileChosenText, summaryText, tagLine, whatHappens } from '../../lib/copy/wizardCreateCopy';
  import HelpNote from '../HelpNote.svelte';

  // Step 3 of the create guide: what will be made, and the notice that must be ticked before anything is read. Nothing here talks to the
  // server except the list of case names (to warn about a repeat). The button that makes the case is in the footer of the frame (Landing.svelte), after this notice.
  const uid = $props.id();
  const draft = $derived(draftSnapshot());
  const counts = $derived(reviewCounts(draft.envs, draft.sources));
  const plan = $derived(importPlan(draft.sources));
  const needsNotice = $derived(plan.scan + plan.files > 0);
  const stale = $derived(plan.scan > 0 && scanIsStale());
  const missing = $derived(filesMissing(draft.sources, Object.fromEntries(draft.sources.map((s) => [sourceKey(s), pickedFile(sourceKey(s))]))).length);
  const repeats = $derived(titleRepeats(draft.title));
  const pending = $derived(blockers());
  const scanGone = $derived(pending.includes('scan-busy'));
  const caseProblem = $derived(createView.result && !createView.result.caseId && createView.result.failures.length > 0 ? createView.result.failures[0] : null);

  onMount(() => { void loadTitles(); });

  function chosen(key: string, event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    pickFile(key, input.files?.[0] ?? null);
  }
</script>

<div class="review">
  <p class="lead">{CREATE_TEXT.intro}</p>
  <HelpNote id="wizard-review" />

  {#if caseProblem}
    <p class="problem" role="alert">{failureText(caseProblem)}</p>
  {/if}

  <section class="block" aria-labelledby="{uid}-sum">
    <h4 id="{uid}-sum">{CREATE_TEXT.summaryLabel}</h4>
    <p class="summary">{summaryText(counts)}</p>
    <p class="hint">{CREATE_TEXT.sharedNote}</p>
    <dl class="facts">
      <dt>{CREATE_TEXT.caseLabel}</dt>
      <dd>{draft.title}</dd>
      <dt>{CREATE_TEXT.envsLabel}</dt>
      <dd>{#if draft.envs.length}{draft.envs.join(', ')}{:else}{CREATE_TEXT.noEnvs}{/if}</dd>
      <dt>{CREATE_TEXT.keyLabel}</dt>
      <dd>{wizard.caseKey.on ? CREATE_TEXT.keyOn : CREATE_TEXT.keyOff}</dd>
    </dl>
    <p class="hint" role="status">{#if repeats}{CREATE_TEXT.duplicateTitle}{/if}</p>
  </section>

  <section class="block" aria-labelledby="{uid}-src">
    <h4 id="{uid}-src">{CREATE_TEXT.sourcesLabel}</h4>
    {#if draft.sources.length === 0}
      <p class="hint">{CREATE_TEXT.noSources}</p>
    {:else}
      <ul class="sources">
        {#each draft.sources as source (sourceKey(source))}
          {@const key = sourceKey(source)}
          <li class="source">
            <p class="name"><strong>{source.label || kindLabel(source.kind)}</strong> <span class="kind">{kindLabel(source.kind)}</span></p>
            <p class="path">{tagLine(source)}</p>
            <p class="hint">{whatHappens(source)}</p>
            {#if isFileSource(source)}
              {@const picked = pickedFile(key)}
              <label class="pick" for="{uid}-f-{key}">{chooseFileLabel(source.label || kindLabel(source.kind))}</label>
              <input id="{uid}-f-{key}" type="file" onchange={(event) => chosen(key, event)} />
              <p class="hint">{picked ? fileChosenText(picked.name) : CREATE_TEXT.fileNone}</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if plan.scan > 0}
    <fieldset class="block window">
      <legend>{CREATE_TEXT.windowLabel}</legend>
      <div class="radios">
        {#each REVIEW_WINDOWS as choice (choice)}
          <label class="radio"><input type="radio" name="{uid}-window" value={choice} checked={createView.window === choice} onchange={() => chooseWindow(choice)} /> <span>{windowChoiceLabel(choice)}</span></label>
        {/each}
      </div>
      <p class="hint">{CREATE_TEXT.windowHint}</p>
    </fieldset>
  {/if}

  {#if scanGone}<p class="problem" role="alert">{CREATE_TEXT.scanBusy}</p>{/if}
  {#if stale}<p class="problem" role="alert">{CREATE_TEXT.scanStale}</p>{/if}
  {#if plan.files > 0 && missing > 0}<p class="hint" role="status">{CREATE_TEXT.needFiles}</p>{/if}

  {#if needsNotice}
    <div class="notice">
      <p class="gate" role="note" aria-label={CREATE_TEXT.noticeLabel}>{GATING_BANNER}</p>
      <label class="ack"><input type="checkbox" checked={createView.ack} onchange={(event) => setAck(event.currentTarget.checked)} /> <span>{ACK_LABEL}</span></label>
      <p class="hint" role="status">{#if !createView.ack && pending.includes('no-ack')}{CREATE_TEXT.needAck}{/if}</p>
    </div>
  {/if}

  <p class="hint">{CREATE_TEXT.keepNote}</p>
</div>

<style>
  .review { display: grid; gap: var(--space-4); max-width: 72ch; min-width: 0; }
  .lead { margin: 0; color: var(--text-2); font-size: var(--fs-5); }
  .block { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
  .block h4, .window legend { margin: 0; padding: 0; font-size: var(--fs-4); font-weight: 600; }
  .summary { margin: 0; font-size: var(--fs-5); font-weight: 600; overflow-wrap: anywhere; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .facts { display: grid; grid-template-columns: max-content 1fr; gap: var(--space-1) var(--space-4); margin: 0; font-size: var(--fs-4); }
  .facts dt { color: var(--text-2); }
  .facts dd { margin: 0; overflow-wrap: anywhere; }
  .sources { display: grid; gap: var(--space-3); margin: 0; padding: 0; list-style: none; }
  .source { display: grid; gap: var(--space-1); padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); min-width: 0; }
  .source p { margin: 0; }
  .name { font-size: var(--fs-4); overflow-wrap: anywhere; }
  .kind { color: var(--text-2); font-size: var(--fs-3); }
  .path { font-size: var(--fs-3); overflow-wrap: anywhere; }
  .pick { font-size: var(--fs-3); font-weight: 600; }
  input[type='file'] { max-width: 100%; font: inherit; font-size: var(--fs-3); }
  input[type='file']:focus-visible, input[type='radio']:focus-visible, input[type='checkbox']:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .radios { display: flex; flex-wrap: wrap; gap: var(--space-4); }
  .radio, .ack { display: inline-flex; align-items: center; gap: var(--space-2); min-height: 44px; font-size: var(--fs-4); cursor: pointer; }
  .ack { align-items: flex-start; }
  .notice { display: grid; gap: var(--space-2); padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--accent); border-radius: var(--radius-sm); }
  .gate { margin: 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .problem { margin: 0; padding: var(--space-3); color: var(--text); background: var(--surface); border: var(--hairline-width) solid var(--bad); border-radius: var(--radius-sm); font-size: var(--fs-3); overflow-wrap: anywhere; }
</style>
