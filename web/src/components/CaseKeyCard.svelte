<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { caseCode } from '../lib/cases';
  import { unlockCase } from '../lib/caseKeyApi';
  import { caseKeyFailureOf, keyEndingLabel } from '../lib/caseKey';
  import { CASE_KEY_TEXT, caseKeyErrorText } from '../lib/copy/caseKeyCopy';

  // Screen 7: open a case that has its own key. One field, one primary button. The PC key does not open this case.
  // The key (never logged, never stored): it lives only in `pass` below, goes only in the unlock request body, and is cleared the moment
  // it is read. A refused key reads the same every time and says nothing was opened or changed.
  let { caseId, ending, onOpened, onCancel }: { caseId: string; ending?: string; onOpened: () => void; onCancel: () => void } = $props();

  let pass = $state('');
  let busy = $state(false);
  let problem = $state('');
  let held = $state(false);
  let field = $state<HTMLInputElement>();
  const controller = new AbortController();
  let waitTimer: ReturnType<typeof setTimeout> | undefined;

  onMount(() => { void tick().then(() => field?.focus()); });
  onDestroy(() => { pass = ''; controller.abort(); if (waitTimer) clearTimeout(waitTimer); });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || held) return;
    const attempt = pass;
    pass = '';
    if (!attempt) { problem = CASE_KEY_TEXT.unlockWrong; return; }
    busy = true;
    problem = '';
    try {
      await unlockCase(caseId, attempt, controller.signal);
      onOpened();
      return;
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      const failure = caseKeyFailureOf(error);
      problem = caseKeyErrorText(failure, 'unlock');
      if (failure.kind === 'throttled') {
        // The server holds the case for the wait it named; the button is held for the same time so a press is not wasted.
        held = true;
        waitTimer = setTimeout(() => { held = false; }, (failure.retryAfterSeconds ?? 1) * 1000);
      }
    } finally {
      busy = false;
    }
    await tick();
    field?.focus();
  }

  const endingText = $derived(keyEndingLabel(ending));
</script>

<section class="key-card" aria-labelledby="key-card-title">
  <p class="eyebrow">Case {caseCode(caseId)}{#if endingText}<span class="sticker">{endingText}</span>{/if}</p>
  <h2 id="key-card-title" tabindex="-1">{CASE_KEY_TEXT.unlockTitle}</h2>
  <p class="note">{CASE_KEY_TEXT.unlockIntro}</p>
  <form onsubmit={submit}>
    <label for="case-key-unlock">{CASE_KEY_TEXT.unlockLabel}</label>
    <input id="case-key-unlock" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={field} bind:value={pass} disabled={busy} aria-describedby="case-key-unlock-help case-key-unlock-problem" aria-invalid={problem ? true : undefined} />
    <p id="case-key-unlock-help" class="note">{CASE_KEY_TEXT.unlockHelp}</p>
    <p id="case-key-unlock-problem" class="inline-error" role="alert" hidden={!problem}>{problem}</p>
    <div class="actions">
      <button type="submit" class="primary-button" disabled={busy || held}>{busy ? CASE_KEY_TEXT.unlocking : CASE_KEY_TEXT.unlockSubmit}</button>
      <button type="button" class="secondary-button" disabled={busy} onclick={onCancel}>{CASE_KEY_TEXT.unlockCancel}</button>
    </div>
  </form>
  <p class="note">{CASE_KEY_TEXT.unlockNote}</p>
</section>

<style>
  .key-card { display: grid; gap: var(--space-3); max-width: 520px; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .key-card h2 { margin: 0; font-size: 20px; font-weight: 600; }
  .eyebrow { margin: 0; display: flex; align-items: center; gap: 8px; color: var(--text-2); font-size: 12px; letter-spacing: 0.04em; text-transform: uppercase; }
  .sticker { padding: 0 6px; font-size: 11px; letter-spacing: 0.02em; text-transform: none; border: 1px solid var(--line); border-radius: 4px; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  form { display: grid; gap: var(--space-2); justify-items: start; }
  label { font-size: var(--fs-3); font-weight: 600; }
  input { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
