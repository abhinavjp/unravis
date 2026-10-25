<script lang="ts">
  import { go, noteKeyStatus } from '../lib/appState.svelte';
  import { canSubmitSetup, setupFailureOf, strengthHint } from '../lib/masterKey';
  import { rewrapCounts, rewrapCases, setupMasterKey } from '../lib/masterKeyApi';
  import { KEY_TEXT, rewrapAction, rewrapDone, rewrapOffer, setupErrorText } from '../lib/copy/masterKeyCopy';
  import HelpNote from './HelpNote.svelte';

  // Set a master key: type it twice, read the strength line, tick the box. The button stays off until the key is at least 12 characters, both
  // entries match and the box is ticked. Afterwards, older cases can be protected with the key too.
  // The key (I5): it lives only in `pass` and `confirm` below, goes only in the setup request body, and both are cleared the moment they are read.
  // It is never put in a URL, storage, the console or an error message.
  let pass = $state('');
  let confirm = $state('');
  let ack = $state(false);
  let busy = $state(false);
  let problem = $state('');
  let phase = $state<'form' | 'done'>('form');
  // After a successful set-up: how many older cases could be protected too, and what the offer did.
  let older = $state(0);
  let rewrapNote = $state('');
  let rewrapBusy = $state(false);

  const ready = $derived(canSubmitSetup(pass, confirm, ack));
  const hint = $derived(strengthHint(pass));
  const mismatch = $derived(confirm.length > 0 && pass !== confirm);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !canSubmitSetup(pass, confirm, ack)) return;
    const first = pass, second = confirm;
    pass = ''; confirm = ''; ack = false;
    busy = true;
    problem = '';
    try {
      const status = await setupMasterKey(first, second);
      noteKeyStatus(status);
      older = status.dpapiCases;
      phase = 'done';
    } catch (error) {
      const failure = setupFailureOf(error);
      if (failure.kind === 'incomplete') {
        // The key file exists but is not open yet: the unlock screen takes it from here.
        noteKeyStatus({ state: 'locked', dpapiCases: 0 });
        go('unlock');
        return;
      }
      if (failure.kind === 'damaged') {
        // The key file is unreadable, not missing: the damaged screen explains it and offers the start-over.
        noteKeyStatus({ state: 'damaged', dpapiCases: 0 });
        go('unlock');
        return;
      }
      problem = setupErrorText(failure.kind, failure.retryAfterSeconds);
    } finally {
      busy = false;
    }
  }

  async function protect() {
    if (rewrapBusy) return;
    rewrapBusy = true;
    rewrapNote = '';
    try {
      const counts = rewrapCounts(await rewrapCases({ all: true }));
      rewrapNote = counts.done + counts.failed === 0 ? KEY_TEXT.rewrapNone : rewrapDone(counts.done, counts.failed);
      older = counts.failed;
    } catch {
      rewrapNote = rewrapDone(0, older);
    } finally {
      rewrapBusy = false;
    }
  }
</script>

<section class="setup" aria-labelledby="setup-title">
  {#if phase === 'form'}
    <h2 id="setup-title">{KEY_TEXT.setupTitle}</h2>
    <p class="intro">{KEY_TEXT.setupIntro}</p>
    <ul class="facts">
      <li>{KEY_TEXT.protects}</li>
      <li>{KEY_TEXT.notSent}</li>
      <li>{KEY_TEXT.noRecovery}</li>
    </ul>
    <HelpNote id="masterkey" />
    <form onsubmit={submit}>
      <label for="setup-key">{KEY_TEXT.keyLabel}</label>
      <input id="setup-key" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:value={pass} disabled={busy} aria-describedby="setup-hint" />
      <p id="setup-hint" class="hint" aria-live="polite">{hint}</p>
      <label for="setup-confirm">{KEY_TEXT.confirmLabel}</label>
      <input id="setup-confirm" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:value={confirm} disabled={busy} aria-describedby="setup-mismatch" aria-invalid={mismatch ? true : undefined} />
      <p id="setup-mismatch" class="inline-error" hidden={!mismatch}>{KEY_TEXT.mismatch}</p>
      <label class="ack"><input type="checkbox" bind:checked={ack} disabled={busy} /> <span>{KEY_TEXT.ackLabel}</span></label>
      <p class="inline-error" role="alert" hidden={!problem}>{problem}</p>
      <div class="actions">
        <button type="submit" class="primary-button" disabled={!ready || busy}>{busy ? KEY_TEXT.submitting : KEY_TEXT.submit}</button>
        <button type="button" class="quiet-button" disabled={busy} onclick={() => go('start')}>{KEY_TEXT.later}</button>
      </div>
    </form>
  {:else}
    <h2 id="setup-title">{KEY_TEXT.doneTitle}</h2>
    <p class="intro">{KEY_TEXT.doneText}</p>
    {#if older > 0}
      <p class="intro">{rewrapOffer(older)}</p>
      <div class="actions">
        <button type="button" class="secondary-button" disabled={rewrapBusy} onclick={() => void protect()}>{rewrapBusy ? KEY_TEXT.rewrapping : rewrapAction(older)}</button>
      </div>
    {/if}
    <p class="intro" role="status">{rewrapNote}</p>
    <div class="actions"><button type="button" class="primary-button" onclick={() => go('start')}>{KEY_TEXT.continue}</button></div>
  {/if}
</section>

<style>
  .setup { display: grid; gap: var(--space-3); max-width: 560px; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-family: var(--mono); }
  .setup h2 { margin: 0; font-size: 20px; font-weight: 600; }
  .intro, .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .hint { font-size: var(--fs-2); }
  .facts { margin: 0; padding-left: var(--space-5); color: var(--text-2); font-size: var(--fs-3); }
  form { display: grid; gap: var(--space-2); justify-items: start; }
  label { font-size: var(--fs-3); font-weight: 600; }
  input[type='password'] { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .ack { display: flex; align-items: flex-start; gap: var(--space-2); font-weight: 400; }
  .ack input { margin-top: 3px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
