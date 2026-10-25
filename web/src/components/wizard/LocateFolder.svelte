<script lang="ts">
  import { tick } from 'svelte';
  import { locateSource, scanIsStale } from '../../lib/wizardSources.svelte';
  import { cleanPath } from '../../lib/wizardSources';
  import { MAX_LOCATE_PATH } from '../../lib/discover';
  import { SOURCES_TEXT, locateFailText } from '../../lib/copy/wizardSourcesCopy';

  // "Where is it?" for a source the scan found but could not read. The person types the place; the server checks it and, when it is the right
  // kind of log, the source becomes readable and can be added. Nothing is added by this control itself.
  let { findingId, label, onFound }: { findingId: string; label: string; onFound?: (files: number) => void } = $props();

  const uid = $props.id();
  let open = $state(false);
  let path = $state('');
  let busy = $state(false);
  let line = $state('');
  let failed = $state(false);
  let trigger = $state<HTMLButtonElement>();
  let field = $state<HTMLInputElement>();

  async function show() {
    open = true;
    line = '';
    await tick();
    field?.focus();
  }

  async function hide() {
    open = false;
    line = '';
    failed = false;
    await tick();
    trigger?.focus();
  }

  async function check() {
    const clean = cleanPath(path);
    if (clean === '' || busy) {
      failed = true;
      line = locateFailText('path-not-absolute');
      return;
    }
    if (scanIsStale()) { failed = true; line = SOURCES_TEXT.locateExpired; return; }
    busy = true;
    failed = false;
    line = SOURCES_TEXT.locateChecking;
    const result = await locateSource(findingId, clean);
    busy = false;
    if (result.ok) {
      // The finding is verified now; this row is replaced by a ticked-able one. The parent moves focus to it.
      open = false;
      line = '';
      path = '';
      onFound?.(result.files);
      return;
    }
    failed = true;
    line = 'reason' in result ? locateFailText(result.reason) : result.problem;
    await tick();
    field?.focus();
  }

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); void hide(); }
    else if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); event.stopPropagation(); void check(); }
  }
</script>

<div class="locate">
  {#if !open}
    <button type="button" class="secondary-button" bind:this={trigger} aria-label="{SOURCES_TEXT.locateOpen} {label}" aria-expanded="false" aria-controls="{uid}-form" onclick={() => void show()}>{SOURCES_TEXT.locateOpen}</button>
  {:else}
    <!-- Escape and Enter work from the field and the buttons; the group itself is not a control. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="form" id="{uid}-form" role="group" aria-label="{SOURCES_TEXT.locateOpen} {label}" onkeydown={onKey}>
      <label for="{uid}-path">{SOURCES_TEXT.locateLabel}</label>
      <input id="{uid}-path" type="text" bind:this={field} bind:value={path} maxlength={MAX_LOCATE_PATH} autocomplete="off" autocapitalize="off" spellcheck="false"
        aria-describedby="{uid}-help {uid}-line" aria-invalid={failed ? true : undefined} />
      <p class="hint" id="{uid}-help">{SOURCES_TEXT.locateHelp}</p>
      <div class="actions">
        <button type="button" class="primary-button" disabled={busy} onclick={() => void check()}>{busy ? SOURCES_TEXT.locateChecking : SOURCES_TEXT.locateCheck}</button>
        <button type="button" class="quiet-button" onclick={() => void hide()}>{SOURCES_TEXT.locateCancel}</button>
      </div>
    </div>
  {/if}
  <p class="line" class:bad={failed} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
</div>

<style>
  .locate { display: grid; gap: var(--space-2); min-width: 0; }
  .form { display: grid; gap: var(--space-2); min-width: 0; max-width: 640px; }
  .form label { font-size: var(--fs-3); font-weight: 600; }
  .form input { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .form input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .form input[aria-invalid='true'] { border-color: var(--bad); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .line { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .line.bad { color: var(--text); }
  .line[hidden] { display: none; }
</style>
