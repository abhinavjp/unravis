<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { finishUnlock, noteKeyStatus } from '../lib/appState.svelte';
  import { unlockFailureOf, unlockMessage } from '../lib/masterKey';
  import { getMasterKeyStatus, unlockMasterKey } from '../lib/masterKeyApi';
  import { KEY_TEXT } from '../lib/copy/masterKeyCopy';
  import HelpNote from './HelpNote.svelte';

  // Unlock: one field and one button. Every failure reads the same, so the screen never says which part was wrong.
  // The key (I5): it lives only in `pass` below, goes only in the unlock request body, and is cleared the moment it is read.
  // It is never put in a URL, storage, the console or an error message.
  let pass = $state('');
  let busy = $state(false);
  let problem = $state('');
  let field = $state<HTMLInputElement>();

  // A locked screen can be reached by a 423 while the key file is in fact damaged. Ask once: if so, the damaged screen replaces this one
  // before anyone types a key that no one could accept. Any other answer, or no answer, leaves this screen as it is.
  onMount(() => {
    void tick().then(() => field?.focus());
    void getMasterKeyStatus().then((status) => { if (status.state === 'damaged') noteKeyStatus(status); }, () => {});
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    const attempt = pass;
    pass = '';
    if (!attempt) { problem = unlockMessage({ kind: 'wrong-key' }); return; }
    busy = true;
    problem = '';
    try {
      finishUnlock(await unlockMasterKey(attempt));
    } catch (error) {
      const failure = unlockFailureOf(error);
      // The file became unreadable: the damaged screen takes over (Landing swaps on the key state).
      if (failure.kind === 'damaged') noteKeyStatus({ state: 'damaged', dpapiCases: 0 });
      problem = unlockMessage(failure);
      // The field is disabled while busy, and a disabled field cannot take focus: clear busy and let the page update first.
      busy = false;
      await tick();
      field?.focus();
    } finally {
      busy = false;
    }
  }
</script>

<section class="unlock" aria-labelledby="unlock-title">
  <h2 id="unlock-title">{KEY_TEXT.unlockTitle}</h2>
  <p class="intro">{KEY_TEXT.unlockIntro}</p>
  <HelpNote id="unlock" />
  <form onsubmit={submit}>
    <label for="unlock-key">{KEY_TEXT.unlockLabel}</label>
    <input id="unlock-key" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={field} bind:value={pass} disabled={busy} aria-describedby={problem ? 'unlock-problem' : undefined} aria-invalid={problem ? true : undefined} />
    <button type="submit" class="primary-button" disabled={busy}>{busy ? KEY_TEXT.unlocking : KEY_TEXT.unlockSubmit}</button>
    <p id="unlock-problem" class="inline-error" role="alert" hidden={!problem}>{problem}</p>
  </form>
</section>

<style>
  .unlock { display: grid; gap: var(--space-3); max-width: 480px; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-family: var(--mono); }
  .unlock h2 { margin: 0; font-size: 20px; font-weight: 600; }
  .intro { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  form { display: grid; gap: var(--space-2); justify-items: start; }
  label { font-size: var(--fs-3); font-weight: 600; }
  input { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
</style>
