<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { app } from '../lib/appState.svelte';
  import { listImports } from '../lib/cases';
  import { resolveCode } from '../lib/caseCode';
  import { restoreCaseFile } from '../lib/caseFileApi';
  import { takeRestoreIntent } from '../lib/restoreIntent';
  import { checkRestoreInput, restoreFailureOf, restoreNeedsKey, restoreView } from '../lib/restoreCase';
  import { ENTER_TEXT } from '../lib/copy/enterCopy';
  import { KEY_TEXT } from '../lib/copy/masterKeyCopy';
  import HelpNote from './HelpNote.svelte';

  // The body of the "Enter an existing case" card: a case code opens a case that is already on this computer, and a case file is restored
  // as a NEW case. A code that does not open anything, whatever the reason, reads as one sentence; so does a case file that does not open.
  // The master key (I5): it lives only in `pass` below, goes only in the restore request body, and is cleared before the request is sent.
  // It is never put in a URL, storage, the console or an error message. `onOpen` opens a case, `onSetup` opens the master key set-up.
  let { onOpen, onSetup }: { onOpen: (id: string) => void; onSetup: () => void } = $props();

  const uid = $props.id();
  const controller = new AbortController();
  let waitTimer: ReturnType<typeof setTimeout> | undefined;
  onDestroy(() => { controller.abort(); if (waitTimer) clearTimeout(waitTimer); });

  // The code
  let code = $state('');
  let codeBusy = $state(false);
  let codeProblem = $state('');
  let codeField = $state<HTMLInputElement>();

  async function openByCode(event: SubmitEvent) {
    event.preventDefault();
    if (codeBusy) return;
    codeBusy = true;
    codeProblem = '';
    try {
      const result = await resolveCode(code, (id) => listImports(id, controller.signal));
      if (result.ok) { code = ''; onOpen(result.id); return; }
      codeProblem = result.message;
      // The field is disabled while busy, and a disabled field cannot take focus: clear busy and let the page update first.
      codeBusy = false;
      await tick();
      codeField?.focus();
    } catch {
      // Only an abort gets here: the screen has gone, so there is nothing to show.
    } finally {
      codeBusy = false;
    }
  }

  // The case file
  // Opens already when the page for a case that cannot be opened sent the person here to restore.
  let open = $state(takeRestoreIntent());
  let path = $state('');
  let pass = $state('');
  let busy = $state(false);
  let held = $state(false);
  let problem = $state('');
  let offerSetup = $state(false);
  let pathField = $state<HTMLInputElement>();
  let keyField = $state<HTMLInputElement>();
  const needsKey = $derived(restoreNeedsKey(app.lockState));

  onMount(() => { if (open && !needsKey) void tick().then(() => pathField?.focus()); });

  async function toggle() {
    open = !open;
    if (open && !needsKey) { await tick(); pathField?.focus(); }
  }

  function hold(seconds: number) {
    held = true;
    if (waitTimer) clearTimeout(waitTimer);
    waitTimer = setTimeout(() => { held = false; }, seconds * 1000);
  }

  async function restore(event: SubmitEvent) {
    event.preventDefault();
    if (busy || held) return;
    const checked = checkRestoreInput(path, pass);
    if (!checked.ok) {
      problem = checked.message;
      offerSetup = false;
      await tick();
      (checked.field === 'path' ? pathField : keyField)?.focus();
      return;
    }
    // Read the key into a local constant and clear the field before anything is sent.
    const attempt = pass;
    pass = '';
    busy = true;
    problem = '';
    offerSetup = false;
    try {
      const restored = await restoreCaseFile(checked.path, attempt, controller.signal);
      path = '';
      open = false;
      onOpen(restored.id);
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      const view = restoreView(restoreFailureOf(error), app.lockState);
      problem = view.message;
      offerSetup = view.offerSetup;
      if (view.waitSeconds > 0) hold(view.waitSeconds);
      // The fields are disabled while busy, and a disabled field cannot take focus: clear busy and let the page update first.
      busy = false;
      await tick();
      if (view.focus === 'key') keyField?.focus();
      else if (view.focus === 'path') pathField?.focus();
    } finally {
      busy = false;
    }
  }
</script>

<div class="enter">
  <HelpNote id="enter" />

  <form class="part" onsubmit={openByCode}>
    <label for="{uid}-code">{ENTER_TEXT.codeLabel}</label>
    <input id="{uid}-code" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="64" bind:this={codeField} bind:value={code} disabled={codeBusy} aria-describedby={uid + '-code-hint' + (codeProblem ? ' ' + uid + '-code-problem' : '')} aria-invalid={codeProblem ? true : undefined} />
    <p class="hint" id="{uid}-code-hint">{ENTER_TEXT.codeHint}</p>
    <button type="submit" class="secondary-button" disabled={codeBusy}>{codeBusy ? ENTER_TEXT.codeChecking : ENTER_TEXT.codeButton}</button>
    <p class="inline-error" id="{uid}-code-problem" role="alert" hidden={!codeProblem}>{codeProblem}</p>
  </form>

  <div class="part restore">
    <button type="button" class="quiet-button" aria-expanded={open} aria-controls="{uid}-restore" onclick={() => void toggle()}>{ENTER_TEXT.restoreToggle}</button>
    <div id="{uid}-restore" class="restore-body" hidden={!open}>
      {#if open}
        <p class="hint">{ENTER_TEXT.restoreIntro}</p>
        {#if needsKey}
          <p class="hint" role="status">{ENTER_TEXT.needKey}</p>
          <button type="button" class="link-button" onclick={onSetup}>{KEY_TEXT.setupLink}</button>
        {:else}
          <form class="part" onsubmit={restore}>
            <label for="{uid}-path">{ENTER_TEXT.pathLabel}</label>
            <input id="{uid}-path" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={pathField} bind:value={path} disabled={busy} aria-describedby="{uid}-path-hint" />
            <p class="hint" id="{uid}-path-hint">{ENTER_TEXT.pathHint}</p>
            <label for="{uid}-key">{ENTER_TEXT.keyLabel}</label>
            <input id="{uid}-key" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" bind:this={keyField} bind:value={pass} disabled={busy} aria-describedby="{uid}-key-hint" />
            <p class="hint" id="{uid}-key-hint">{ENTER_TEXT.keyHint}</p>
            <button type="submit" class="secondary-button" disabled={busy || held}>{busy ? ENTER_TEXT.restoring : ENTER_TEXT.restoreButton}</button>
            <p class="inline-error" role="alert" hidden={!problem}>{problem}</p>
            {#if offerSetup}<button type="button" class="link-button" onclick={onSetup}>{KEY_TEXT.setupLink}</button>{/if}
          </form>
        {/if}
      {/if}
    </div>
  </div>
</div>

<style>
  .enter { display: grid; gap: var(--space-3); min-width: 0; }
  .part { display: grid; gap: var(--space-2); justify-items: start; min-width: 0; }
  .restore { border-top: var(--hairline-width) solid var(--line); padding-top: var(--space-3); }
  .restore-body { display: grid; gap: var(--space-2); width: 100%; min-width: 0; }
  .restore-body[hidden] { display: none; }
  label { font-size: var(--fs-3); font-weight: 600; }
  input { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .inline-error { margin: 0; overflow-wrap: anywhere; }
  .inline-error[hidden] { display: none; }
  .link-button { padding: 0; color: var(--accent); background: transparent; border: 0; font: inherit; font-size: var(--fs-3); text-align: start; text-decoration: underline; cursor: pointer; }
  .link-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
</style>
