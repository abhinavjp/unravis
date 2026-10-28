<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { caseCode } from '../lib/cases';
  import { changeCaseKey, lockCase, removeCaseKey, setCaseKey } from '../lib/caseKeyApi';
  import { caseKeyFailureOf, changeReady, keyEndingLabel, newKeyHint, newKeyProblem, repeatMismatch, type CaseKeyStatus, type KeyChange } from '../lib/caseKey';
  import { CASE_KEY_TEXT, caseKeyErrorText, newKeyProblemText } from '../lib/copy/caseKeyCopy';
  import type { LockState } from '../lib/masterKey';

  // Screens 125 and 126: add a case key, or change or remove the one the case has. The key fields live only in `current`, `next` and
  // `again` below. Each is cleared the moment it is read and when the page closes; none is put in a URL, storage, the console or an error.
  let { caseId, status, masterState = 'none', onChanged, onCancel }: {
    caseId: string; status: CaseKeyStatus; masterState?: LockState | 'unknown'; onChanged: (status: CaseKeyStatus, note: string) => void; onCancel: () => void;
  } = $props();

  let mode = $state<KeyChange>('change');
  let current = $state('');
  let next = $state('');
  let again = $state('');
  let busy = $state(false);
  let problem = $state('');
  let currentProblem = $state('');
  let held = $state(false);
  let curField = $state<HTMLInputElement>();
  let newField = $state<HTMLInputElement>();
  const focusFirst = () => (has ? curField : newField)?.focus();
  const controller = new AbortController();
  let waitTimer: ReturnType<typeof setTimeout> | undefined;

  const has = $derived(status.ownKey);
  const endingText = $derived(keyEndingLabel(status.ending));
  const sealed = $derived(has ? CASE_KEY_TEXT.sealedCase : masterState === 'unlocked' ? CASE_KEY_TEXT.sealedPc : CASE_KEY_TEXT.sealedWindows);
  const newProblem = $derived(next.length > 0 || again.length > 0 ? newKeyProblem(next, again) : null);
  const ready = $derived(has ? changeReady(mode, current, next, again) : newKeyProblem(next, again) === null);
  const hint = $derived(newKeyHint(next));
  const mismatch = $derived(repeatMismatch(next, again));

  function wipe() { current = ''; next = ''; again = ''; }

  onMount(() => { void tick().then(focusFirst); });
  onDestroy(() => { wipe(); controller.abort(); if (waitTimer) clearTimeout(waitTimer); });

  function refused(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    const failure = caseKeyFailureOf(error);
    if (failure.kind === 'wrong-key') currentProblem = caseKeyErrorText(failure, 'current');
    else problem = caseKeyErrorText(failure, has ? 'current' : 'new');
    if (failure.kind === 'throttled') {
      held = true;
      waitTimer = setTimeout(() => { held = false; }, (failure.retryAfterSeconds ?? 1) * 1000);
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || held || !ready) return;
    const a = current, b = next, c = again;
    wipe();
    busy = true;
    problem = '';
    currentProblem = '';
    try {
      let done: CaseKeyStatus;
      let note: string;
      if (!has) { done = await setCaseKey(caseId, b, c, controller.signal); note = CASE_KEY_TEXT.saved; }
      else if (mode === 'remove') { done = await removeCaseKey(caseId, a, controller.signal); note = CASE_KEY_TEXT.removed; }
      else { done = await changeCaseKey(caseId, a, b, c, controller.signal); note = CASE_KEY_TEXT.changed; }
      onChanged(done, note);
      return;
    } catch (error) {
      refused(error);
    } finally {
      busy = false;
    }
    await tick();
    focusFirst();
  }

  async function lock() {
    if (busy) return;
    busy = true;
    problem = '';
    try {
      onChanged(await lockCase(caseId, controller.signal), '');
    } catch (error) {
      refused(error);
    } finally {
      busy = false;
    }
  }
</script>

<section class="key-page" aria-labelledby="key-page-title">
  <p class="eyebrow">Case {caseCode(caseId)} · {CASE_KEY_TEXT.pageTitle}{#if endingText}<span class="sticker">{endingText}</span>{/if}</p>
  <h3 id="key-page-title">{CASE_KEY_TEXT.pageTitle}</h3>
  <p class="note"><strong>{sealed}</strong> {CASE_KEY_TEXT.separate}</p>
  <form onsubmit={submit}>
    {#if has}
      <label for="ck-current">{CASE_KEY_TEXT.currentLabel}</label>
      <input id="ck-current" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={curField} bind:value={current} disabled={busy} aria-describedby="ck-current-help ck-current-problem" aria-invalid={currentProblem ? true : undefined} />
      <p id="ck-current-help" class="note">{CASE_KEY_TEXT.currentHelp}</p>
      <p id="ck-current-problem" class="inline-error" role="alert" hidden={!currentProblem}>{currentProblem}</p>
      <fieldset disabled={busy}>
        <legend class="sr-only">{CASE_KEY_TEXT.pageTitle}</legend>
        <label class="choice"><input type="radio" name="ck-mode" value="change" bind:group={mode} /> <span>{CASE_KEY_TEXT.changeOption}</span></label>
        <label class="choice"><input type="radio" name="ck-mode" value="remove" bind:group={mode} /> <span>{CASE_KEY_TEXT.removeOption}</span></label>
      </fieldset>
      {#if mode === 'remove'}<p class="note">{CASE_KEY_TEXT.removeHelp}</p>{/if}
    {/if}
    {#if !has || mode === 'change'}
      <label for="ck-new">{CASE_KEY_TEXT.newLabel}</label>
      <input id="ck-new" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={newField} bind:value={next} disabled={busy} aria-describedby="ck-new-help" />
      <p id="ck-new-help" class="note" aria-live="polite">{next.length > 0 ? hint : CASE_KEY_TEXT.newHelp}</p>
      <label for="ck-again">{CASE_KEY_TEXT.repeatLabel}</label>
      <input id="ck-again" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:value={again} disabled={busy} aria-describedby="ck-again-problem" aria-invalid={mismatch ? true : undefined} />
      <p id="ck-again-problem" class="inline-error" hidden={!(mismatch || (newProblem && newProblem !== 'mismatch'))}>{newProblem ? newKeyProblemText(newProblem) : ''}</p>
      <p class="warning">{CASE_KEY_TEXT.loseWarning}</p>
      {#if has}<p class="note">{CASE_KEY_TEXT.oldFileNote}</p>{/if}
    {/if}
    <p class="note">{CASE_KEY_TEXT.lockNote}</p>
    <p class="inline-error" role="alert" hidden={!problem}>{problem}</p>
    <div class="actions">
      <button type="submit" class="primary-button" disabled={!ready || busy || held}>{busy ? CASE_KEY_TEXT.saving : has ? CASE_KEY_TEXT.saveChange : CASE_KEY_TEXT.save}</button>
      {#if has}<button type="button" class="secondary-button" disabled={busy} onclick={() => void lock()}>{CASE_KEY_TEXT.lockCase}</button>{/if}
      <button type="button" class="secondary-button" disabled={busy} onclick={onCancel}>{CASE_KEY_TEXT.cancel}</button>
    </div>
  </form>
</section>

<style>
  .key-page { display: grid; gap: var(--space-3); max-width: 560px; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .key-page h3 { margin: 0; font-size: 18px; font-weight: 600; }
  .eyebrow { margin: 0; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; color: var(--text-2); font-size: 12px; letter-spacing: 0.04em; text-transform: uppercase; }
  .sticker { padding: 0 6px; font-size: 11px; letter-spacing: 0.02em; text-transform: none; border: 1px solid var(--line); border-radius: 4px; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .warning { margin: 0; padding: 8px 10px; font-size: var(--fs-3); border: 1px solid var(--warn); border-radius: var(--radius-sm); }
  form { display: grid; gap: var(--space-2); justify-items: start; }
  label { font-size: var(--fs-3); font-weight: 600; }
  fieldset { display: grid; gap: 6px; margin: 0; padding: 0; border: 0; }
  .choice { display: flex; align-items: flex-start; gap: var(--space-2); font-weight: 400; }
  .choice input { margin-top: 3px; }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
  input[type='password'] { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
