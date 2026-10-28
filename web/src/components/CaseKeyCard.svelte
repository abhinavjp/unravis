<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { caseCode } from '../lib/cases';
  import { unlockCase } from '../lib/caseKeyApi';
  import { caseKeyFailureOf, keyEndingLabel } from '../lib/caseKey';
  import { CASE_KEY_TEXT, caseKeyErrorText, secondsLeft } from '../lib/copy/caseKeyCopy';
  import { waitText } from '../lib/copy/masterKeyCopy';

  // Screen 7: open a case that has its own key. One field, one primary button. The PC key does not open this case.
  // The key (never logged, never stored): it lives only in `pass` below, goes only in the unlock request body, and is cleared the moment
  // it is read. A refused key reads the same every time and says nothing was opened or changed.
  let { caseId, ending, onOpened, onCancel }: { caseId: string; ending?: string; onOpened: () => void; onCancel: () => void } = $props();

  let pass = $state('');
  let busy = $state(false);
  let problem = $state('');
  let held = $state(false);
  // Whole seconds of the server's wait that are left. The wait line shows it; it ticks down from a fixed end time, so a slow tick never adds time.
  let remaining = $state(0);
  let waitEnds = 0;
  let field = $state<HTMLInputElement>();
  let sectionEl = $state<HTMLElement>();
  const controller = new AbortController();
  let waitTimer: ReturnType<typeof setInterval> | undefined;

  function stopWait() { if (waitTimer) clearInterval(waitTimer); waitTimer = undefined; }
  function startWait(seconds: number) {
    stopWait();
    waitEnds = Date.now() + seconds * 1000;
    remaining = secondsLeft(waitEnds, Date.now());
    held = true;
    waitTimer = setInterval(() => {
      remaining = secondsLeft(waitEnds, Date.now());
      if (remaining > 0) return;
      // The wait is over: free the button and drop the old "Too many tries." line.
      stopWait();
      held = false;
      problem = '';
    }, 500);
  }

  onMount(() => { void tick().then(() => field?.focus()); });
  onDestroy(() => { pass = ''; controller.abort(); stopWait(); });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || held) return;
    const attempt = pass;
    pass = '';
    if (!attempt) { problem = CASE_KEY_TEXT.unlockEmpty; return; }
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
        // The wait itself is its own line below, so it can count down; the alert says only that there were too many tries.
        problem = CASE_KEY_TEXT.throttled;
        startWait(Math.max(1, failure.retryAfterSeconds ?? 1));
      }
    } finally {
      busy = false;
    }
    await tick();
    field?.focus();
  }

  const endingText = $derived(keyEndingLabel(ending));

  // Escape leaves the card, as Cancel does, while nothing is being checked and focus is inside the card.
  function escape(event: KeyboardEvent) {
    if (event.key !== 'Escape' || busy || !sectionEl?.contains(document.activeElement)) return;
    event.preventDefault();
    onCancel();
  }
</script>

<svelte:window onkeydown={escape} />
<section class="key-card" aria-labelledby="key-card-title" bind:this={sectionEl}>
  <p class="eyebrow">Case {caseCode(caseId)}{#if endingText}<span class="sticker">{endingText}</span>{/if}</p>
  <h2 id="key-card-title" tabindex="-1">{CASE_KEY_TEXT.unlockTitle}</h2>
  <p class="note">{CASE_KEY_TEXT.unlockIntro}</p>
  <form onsubmit={submit}>
    <label for="case-key-unlock">{CASE_KEY_TEXT.unlockLabel}</label>
    <input id="case-key-unlock" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={field} bind:value={pass} disabled={busy} aria-describedby="case-key-unlock-help case-key-unlock-problem case-key-unlock-wait" aria-invalid={problem ? true : undefined} />
    <p id="case-key-unlock-help" class="note">{CASE_KEY_TEXT.unlockHelp}</p>
    <p id="case-key-unlock-problem" class="inline-error" role="alert" hidden={!problem}>{problem}</p>
    <p id="case-key-unlock-wait" class="inline-error" hidden={!held}>{waitText(remaining)}</p>
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
