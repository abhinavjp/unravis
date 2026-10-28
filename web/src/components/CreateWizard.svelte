<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Snippet } from 'svelte';
  import { backStep, draftEnvs, draftKeyProblem, goToStep, nextStep, resetWizard, resumeWizard, wipeCaseKey, wizard, wizardIsDirty } from '../lib/wizard.svelte';
  import { newKeyHint, repeatMismatch } from '../lib/caseKey';
  import { CASE_KEY_TEXT, newKeyProblemText } from '../lib/copy/caseKeyCopy';
  import { ENV_FIELD_MAX, hasDropped } from '../lib/wizardEnvs';
  import { STEPS, TITLE_MAX, stepViews, titleLength, titleProblem, titleProblemShows } from '../lib/wizardFlow';
  import type { StepId } from '../lib/wizardFlow';
  import { STEP_TEXT, TITLE_PROBLEM_TEXT, WIZARD_TEXT, chosenText, envCountText, envNotes, nextBlockedLine, stepButtonLabel, stepOfText, titleCountText } from '../lib/copy/wizardCopy';
  import HelpNote from './HelpNote.svelte';

  // The frame of the guide that creates a new case (layout B, Workspace): three steps (Name, Sources, Review), a progress strip, Back and Next,
  // and a draft that keeps every entry while the person moves between steps. Nothing here talks to the server; the case is made on the last step.
  //
  // Slots for the next tasks (each is drawn inside the frame, below the step heading):
  //   `sources`  the sources step (TSK-18): the gallery of what this PC offers and the connections to other computers.
  //   `review`   the review step (TSK-19): the summary and the consent.
  //   `finish`   the last step's main button (TSK-19: "Create case"), drawn where Next is on the other steps.
  // `busy` (TSK-19, while the case is being made) freezes the steps and the Leave button. `onLeave` takes the person out of the guide; the draft
  // is already thrown away by then. Both slots read and write the draft in lib/wizard.svelte.ts.
  let { sources, review, finish, busy = false, onLeave }: {
    sources?: Snippet;
    review?: Snippet;
    finish?: Snippet;
    busy?: boolean;
    onLeave: () => void;
  } = $props();

  const uid = $props.id();
  const titleId = uid + '-title';
  const titleFieldId = uid + '-name';
  const titleHintId = uid + '-name-hint';
  const titleProblemId = uid + '-name-problem';
  const envFieldId = uid + '-envs';
  const envHintId = uid + '-envs-hint';
  const nextHintId = uid + '-next-hint';

  let heading = $state<HTMLElement>();
  let titleField = $state<HTMLInputElement>();
  let keepButton = $state<HTMLButtonElement>();
  let leaving = $state(false);
  let resumed = $state(false);
  // The name problem is shown once the person has left the field or tried to go on; a fresh empty field is not scolded.
  let titleTouched = $state(false);

  const keyProblem = $derived(draftKeyProblem());
  const keyTyped = $derived(wizard.caseKey.key.length > 0 || wizard.caseKey.again.length > 0);
  const facts = $derived({ title: wizard.title, keyOk: keyProblem === null });
  const views = $derived(stepViews(wizard.step, facts));
  const current = $derived(views.find((view) => view.state === 'current') ?? views[0]);
  const problem = $derived(titleProblem(wizard.title));
  const showProblem = $derived(titleProblemShows(problem, titleTouched));
  const parsed = $derived(draftEnvs());
  const notes = $derived(envNotes(parsed.dropped));
  const dirty = $derived(wizardIsDirty());
  const last = $derived(wizard.step === STEPS[STEPS.length - 1]);
  const nextBlocked = $derived(!last && (problem !== null || keyProblem !== null));
  const blockedLine = $derived(problem !== null ? nextBlockedLine(problem) : keyProblem ? newKeyProblemText(keyProblem) : WIZARD_TEXT.nextBlocked);

  async function focusHeading() {
    await tick();
    heading?.focus();
  }

  onMount(() => {
    // A draft left behind (the person used Home in the header) is offered again, never thrown away without a word.
    if (wizardIsDirty()) { resumed = true; resumeWizard(); }
    void focusHeading();
  });

  // A page reload or a closed tab would lose the draft, so the browser asks first while there is something to lose.
  $effect(() => {
    if (!dirty && !busy) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  });

  async function go(to: StepId) {
    if (busy) return;
    if (!goToStep(to)) {
      titleTouched = true;
      await tick();
      titleField?.focus();
      return;
    }
    leaving = false;
    await focusHeading();
  }

  async function onNext() {
    if (busy || last) return;
    titleTouched = true;
    if (!nextStep()) { await tick(); titleField?.focus(); return; }
    await focusHeading();
  }

  async function onBack() {
    if (busy) return;
    if (backStep()) await focusHeading();
  }

  function titleKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); void onNext(); }
  }

  async function requestLeave() {
    if (busy) return;
    if (!dirty) { doLeave(); return; }
    leaving = true;
    await tick();
    keepButton?.focus();
  }

  function doLeave() {
    resetWizard();
    leaving = false;
    onLeave();
  }

  function keepWorking() {
    leaving = false;
  }

  function leaveKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.stopPropagation(); keepWorking(); }
  }

  function startOver() {
    resetWizard();
    resumed = false;
    titleTouched = false;
    void focusHeading();
  }
</script>

<section class="wizard" aria-labelledby={titleId}>
  <header class="wiz-top">
    <div class="wiz-top-row">
      <h2 class="wiz-name" id={titleId}>{WIZARD_TEXT.title}</h2>
      <button type="button" class="quiet-button wiz-leave" disabled={busy} onclick={() => void requestLeave()}>&larr; {WIZARD_TEXT.leave}</button>
    </div>
    <p class="wiz-note">{WIZARD_TEXT.stepsNote}</p>
    <HelpNote id="wizard" />
    {#if resumed}
      <p class="wiz-resumed" role="status">{WIZARD_TEXT.resumed} <button type="button" class="link-button" disabled={busy} onclick={startOver}>{WIZARD_TEXT.startOver}</button></p>
    {/if}
  </header>

  {#if leaving}
    <!-- Escape works from either button inside; the group itself is not a control. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="wiz-leave-ask" role="group" aria-labelledby="{uid}-ask" onkeydown={leaveKey}>
      <strong id="{uid}-ask">{WIZARD_TEXT.leaveAskTitle}</strong>
      <p>{WIZARD_TEXT.leaveAskText}</p>
      <div class="wiz-leave-actions">
        <button type="button" class="primary-button" bind:this={keepButton} onclick={keepWorking}>{WIZARD_TEXT.leaveNo}</button>
        <button type="button" class="secondary-button" onclick={doLeave}>{WIZARD_TEXT.leaveYes}</button>
      </div>
    </div>
  {/if}

  <nav class="steps" aria-label={WIZARD_TEXT.stepsLabel}>
    <ol>
      {#each views as view (view.id)}
        <li>
          <button type="button" class="step" class:on={view.state === 'current'} class:done={view.state === 'done'} aria-current={view.state === 'current' ? 'step' : undefined} aria-label={stepButtonLabel(view.number, STEP_TEXT[view.id].label, view.state)} disabled={busy || !view.enabled} onclick={() => void go(view.id)}>
            <i aria-hidden="true">{view.state === 'done' ? '✓' : view.number}</i><span>{STEP_TEXT[view.id].label}</span>
          </button>
        </li>
      {/each}
    </ol>
  </nav>

  <div class="step-body">
    <p class="step-count">{stepOfText(current.number, views.length, STEP_TEXT[current.id].label)}</p>
    <h3 class="step-heading" tabindex="-1" bind:this={heading}>{STEP_TEXT[wizard.step].heading}</h3>

    {#if wizard.step === 'name'}
      <div class="name-step">
        <p class="lead">{WIZARD_TEXT.nameIntro}</p>

        <div class="field">
          <label for={titleFieldId}>{WIZARD_TEXT.titleLabel}</label>
          <input id={titleFieldId} type="text" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={titleField} bind:value={wizard.title} onblur={() => { if (wizard.title !== '') titleTouched = true; }} onkeydown={titleKey} aria-describedby={titleHintId + (showProblem ? ' ' + titleProblemId : '')} aria-invalid={showProblem ? true : undefined} aria-required="true" />
          <div class="field-foot">
            <p class="hint" id={titleHintId}>{WIZARD_TEXT.titleHint}</p>
            <span class="count" class:over={problem === 'too-long'} aria-hidden="true">{titleCountText(titleLength(wizard.title), TITLE_MAX)}</span>
          </div>
          <p class="inline-error" id={titleProblemId} role="alert" hidden={!showProblem}>{problem ? TITLE_PROBLEM_TEXT[problem] : ''}</p>
        </div>

        <div class="field">
          <label for={envFieldId}>{WIZARD_TEXT.envsLabel}</label>
          <textarea id={envFieldId} rows="2" maxlength={ENV_FIELD_MAX} autocomplete="off" autocapitalize="off" spellcheck="false" placeholder={WIZARD_TEXT.envsPlaceholder} bind:value={wizard.envText} aria-describedby={envHintId}></textarea>
          <p class="hint" id={envHintId}>{WIZARD_TEXT.envsHint}</p>
          <HelpNote id="wizard-envs" />
          <div class="envs">
            <p class="envs-label" id="{uid}-envs-list">{WIZARD_TEXT.envsListLabel}{parsed.names.length ? ' (' + envCountText(parsed.names.length) + ')' : ''}</p>
            {#if parsed.names.length}
              <ul class="chips" aria-labelledby="{uid}-envs-list">
                {#each parsed.names as name (name.toLowerCase())}<li class="chip">{name}</li>{/each}
              </ul>
              <p class="hint">{WIZARD_TEXT.envsTag}</p>
            {:else}
              <p class="hint">{WIZARD_TEXT.envsEmpty}</p>
            {/if}
            <div class="env-notes" role="status">
              {#if hasDropped(parsed.dropped)}{#each notes as note (note)}<p class="hint">{note}</p>{/each}{/if}
            </div>
          </div>
        </div>

        <div class="field">
          <label class="check" for="{uid}-own-key">
            <input id="{uid}-own-key" type="checkbox" checked={wizard.caseKey.on} onchange={(event) => { wizard.caseKey.on = event.currentTarget.checked; if (!wizard.caseKey.on) wipeCaseKey(); }} aria-describedby="{uid}-own-key-help" />
            <span>{CASE_KEY_TEXT.createToggle}</span>
          </label>
          <p class="hint" id="{uid}-own-key-help">{CASE_KEY_TEXT.createHelp}</p>
          {#if wizard.caseKey.on}
            <label for="{uid}-ck">{CASE_KEY_TEXT.createKeyLabel}</label>
            <input id="{uid}-ck" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:value={wizard.caseKey.key} aria-describedby="{uid}-ck-hint" />
            <p class="hint" id="{uid}-ck-hint" aria-live="polite">{wizard.caseKey.key.length > 0 ? newKeyHint(wizard.caseKey.key) : CASE_KEY_TEXT.createKeyHelp}</p>
            <label for="{uid}-ck2">{CASE_KEY_TEXT.createRepeatLabel}</label>
            <input id="{uid}-ck2" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:value={wizard.caseKey.again} aria-describedby="{uid}-ck-problem" aria-invalid={repeatMismatch(wizard.caseKey.key, wizard.caseKey.again) ? true : undefined} />
            <p class="inline-error" id="{uid}-ck-problem" role="alert" hidden={!(keyTyped && keyProblem)}>{keyProblem ? newKeyProblemText(keyProblem) : ''}</p>
            <p class="hint">{CASE_KEY_TEXT.loseWarning}</p>
          {/if}
        </div>
      </div>
    {:else if wizard.step === 'sources'}
      {#if sources}{@render sources()}{:else}<p class="lead">{WIZARD_TEXT.sourcesSoon}</p>{/if}
    {:else}
      {#if review}{@render review()}{:else}<p class="lead">{WIZARD_TEXT.reviewSoon}</p>{/if}
    {/if}
  </div>

  <footer class="bar">
    {#if wizard.step !== STEPS[0]}<button type="button" class="secondary-button" disabled={busy} onclick={() => void onBack()}>{WIZARD_TEXT.back}</button>{/if}
    <span class="chosen" role="status">{chosenText(wizard.sources.length)}</span>
    <span class="spacer"></span>
    {#if nextBlocked}<span class="blocked" id={nextHintId}>{blockedLine}</span>{/if}
    {#if last}
      {#if finish}{@render finish()}{/if}
    {:else}
      <button type="button" class="primary-button" disabled={busy || nextBlocked} aria-describedby={nextBlocked ? nextHintId : undefined} onclick={() => void onNext()}>{WIZARD_TEXT.next}</button>
    {/if}
  </footer>
</section>

<style>
  .wizard { display: grid; gap: var(--space-4); width: 100%; max-width: 1100px; min-width: 0; margin: 0 auto; font-family: var(--mono); font-size: var(--fs-5); }
  .wiz-top { display: grid; gap: var(--space-2); max-width: 72ch; }
  .wiz-top-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-3); }
  .wiz-name { margin: 0; font-size: var(--fs-6); font-weight: 600; }
  .wiz-note { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .wiz-resumed { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .wiz-leave-ask { display: grid; gap: var(--space-2); max-width: 72ch; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--accent); border-radius: var(--radius-sm); }
  .wiz-leave-ask p { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .wiz-leave-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }

  .steps ol { display: flex; margin: 0; padding: 0; list-style: none; border-bottom: var(--hairline-width) solid var(--line); overflow-x: auto; }
  .step { display: flex; align-items: center; gap: var(--space-3); min-height: 44px; padding: var(--space-3) var(--space-5); color: var(--text-2); background: transparent; border: 0; border-bottom: 2px solid transparent; font: inherit; font-size: var(--fs-4); white-space: nowrap; cursor: pointer; }
  .step i { display: grid; place-items: center; width: 22px; height: 22px; font-style: normal; font-size: var(--fs-2); border: var(--hairline-width) solid var(--control-line); border-radius: 50%; }
  .step.on { color: var(--text); border-bottom-color: var(--accent); }
  .step.on i { color: var(--accent); border-color: var(--accent); }
  .step.done i { color: var(--accent-ink); background: var(--accent); border-color: var(--accent); }
  .step:hover:not(:disabled) { color: var(--text); background: var(--surface-2); }
  .step:disabled { cursor: default; opacity: 0.6; }
  .step:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }

  /* Not ".stage": that name is the app's scroll container, and its overscroll-behavior stopped the mouse wheel from reaching it. */
  .step-body { display: grid; gap: var(--space-3); align-content: start; min-width: 0; }
  .step-count { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .step-heading { margin: 0; font-size: clamp(24px, 4vw, 34px); font-weight: 600; line-height: 1.2; }
  .step-heading:focus { outline: none; }
  .step-heading:focus-visible { outline: 2px solid var(--focus); outline-offset: 4px; }
  .lead { margin: 0; max-width: 64ch; color: var(--text-2); font-size: var(--fs-5); overflow-wrap: anywhere; }
  .name-step { display: grid; gap: var(--space-5); max-width: 720px; min-width: 0; }
  .field { display: grid; gap: var(--space-2); min-width: 0; }
  .field label { font-size: var(--fs-4); font-weight: 600; }
  .check { display: flex; align-items: center; gap: var(--space-2); font-weight: 600; }
  .check input { width: auto; min-height: 0; }
  .field input[type='password'], .field input[type='text'], .field textarea { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 10px 12px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-5); }
  .field textarea { resize: vertical; }
  .field input:focus-visible, .field textarea:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .field input[aria-invalid='true'] { border-color: var(--bad); }
  .field-foot { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-3); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .count { flex-shrink: 0; color: var(--text-2); font-size: var(--fs-2); }
  .count.over { color: var(--bad); font-weight: 600; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .envs { display: grid; gap: var(--space-2); }
  .envs-label { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  .chips { display: flex; flex-wrap: wrap; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .chip { max-width: 100%; padding: 2px var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .env-notes { display: grid; gap: var(--space-1); }

  .bar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); padding-top: var(--space-3); border-top: var(--hairline-width) solid var(--line); }
  .bar .spacer { flex: 1 1 0; }
  .chosen { color: var(--text-2); font-size: var(--fs-3); }
  .blocked { color: var(--text-2); font-size: var(--fs-3); }
  .link-button { padding: 0; color: var(--accent); background: transparent; border: 0; font: inherit; font-size: var(--fs-3); text-decoration: underline; cursor: pointer; }
  .link-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (max-width: 720px) {
    .step { padding-inline: var(--space-3); }
    .bar { position: sticky; bottom: 0; background: var(--bg); padding-bottom: var(--space-3); }
  }
</style>
