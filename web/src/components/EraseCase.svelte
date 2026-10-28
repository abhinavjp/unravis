<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { caseCode, eraseCase, type CaseState } from '../lib/cases';
  import { eraseFailureText, eraseReady } from '../lib/erase';
  import { ERASE_TEXT, eraseBody, eraseDoneText, erasePromptText } from '../lib/copy/eraseCopy';

  // Screens 138 and 139: Erase asks for the short case code to be typed, shows what goes, then says the case is removed.
  // The erase can wait up to 30 seconds for the case to be released (another tab may still be reading it), and says so while it waits.
  // Nothing typed here is kept: the code is cleared when the dialog closes. `onDone` runs when the person leaves the confirmation.
  let { caseId, archivePath, believed, onDone, onCancel }: { caseId: string; archivePath?: string; believed?: CaseState; onDone: () => void; onCancel: () => void } = $props();

  let typed = $state('');
  let busy = $state(false);
  let problem = $state('');
  let finished = $state(false);
  let field = $state<HTMLInputElement>();
  let doneButton = $state<HTMLButtonElement>();
  let sectionEl = $state<HTMLElement>();
  const controller = new AbortController();

  const code = $derived(caseCode(caseId));
  const ready = $derived(eraseReady(typed, caseId));

  onMount(() => {
    void tick().then(() => field?.focus());
    return () => { typed = ''; controller.abort(); };
  });

  // Escape leaves the page, as Cancel does, while nothing is being erased and focus is inside the page. After the erase it does nothing.
  function escape(event: KeyboardEvent) {
    if (event.key !== 'Escape' || busy || finished || !sectionEl?.contains(document.activeElement)) return;
    event.preventDefault();
    onCancel();
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !ready) return;
    const entered = typed;
    busy = true;
    problem = '';
    try {
      await eraseCase(caseId, entered, believed, controller.signal);
      typed = '';
      finished = true;
      busy = false;
      await tick();
      doneButton?.focus();
      return;
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      problem = eraseFailureText(error);
    } finally {
      busy = false;
    }
    await tick();
    field?.focus();
  }
</script>

<svelte:window onkeydown={escape} />
<section class="erase" aria-labelledby="erase-title" bind:this={sectionEl}>
  {#if finished}
    <h3 id="erase-title">{ERASE_TEXT.title}</h3>
    <p class="note" role="status">{eraseDoneText(code)}</p>
    <div class="actions"><button type="button" class="secondary-button" bind:this={doneButton} onclick={onDone}>{ERASE_TEXT.back}</button></div>
  {:else}
    <h3 id="erase-title">{ERASE_TEXT.title}</h3>
    <p class="note">{eraseBody(archivePath)}</p>
    <form onsubmit={submit}>
      <label for="erase-code">{erasePromptText(code)}</label>
      <input id="erase-code" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={field} bind:value={typed} disabled={busy} aria-describedby="erase-problem" />
      <p class="note" role="status" hidden={!busy}>{ERASE_TEXT.waiting}</p>
      <p id="erase-problem" class="inline-error" role="alert" hidden={!problem}>{problem}</p>
      <div class="actions">
        <button type="submit" class="primary-button danger-fill" disabled={!ready || busy}>{busy ? ERASE_TEXT.erasing : ERASE_TEXT.confirm}</button>
        <button type="button" class="secondary-button" disabled={busy} onclick={onCancel}>{ERASE_TEXT.cancel}</button>
      </div>
    </form>
  {/if}
</section>

<style>
  .erase { display: grid; gap: var(--space-3); box-sizing: border-box; width: 100%; max-width: 560px; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .erase h3 { margin: 0; font-size: 18px; font-weight: 600; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .note[hidden] { display: none; }
  form { display: grid; gap: var(--space-2); justify-items: start; }
  label { font-size: var(--fs-3); font-weight: 600; }
  input { box-sizing: border-box; width: 100%; max-width: 280px; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .danger-fill:not(:disabled) { background: var(--bad); border-color: var(--bad); color: var(--bg); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
