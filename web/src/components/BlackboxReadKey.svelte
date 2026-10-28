<script lang="ts">
  import { onDestroy } from 'svelte';
  import { blackboxApi, type BlackboxItem } from '../lib/blackbox';
  import { copyKeyOnce, KEY_SHOW_MS } from '../lib/blackboxKey';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // Screen 65: the read key, masked, with Show and Copy. It is only for the Blackbox installed on this PC. The key lives in
  // this component's own variable while it is on view. It is not written to storage, to the app state or to a case, and it
  // is let go when it is hidden, after a short time, and when this screen closes.
  let { item }: { item: BlackboxItem } = $props();

  let key = $state('');
  let busy = $state('');
  let note = $state('');
  let problem = $state('');
  let timer: ReturnType<typeof setTimeout> | undefined;

  function letGo() {
    key = '';
    if (timer) { clearTimeout(timer); timer = undefined; }
  }
  onDestroy(letGo);

  async function show() {
    if (busy) return;
    busy = T.keyWorking; note = ''; problem = '';
    try {
      key = await blackboxApi.revealKey(item.id);
      timer = setTimeout(() => { letGo(); note = T.keyHiddenAgain; }, KEY_SHOW_MS);
    } catch (error) {
      letGo();
      problem = error instanceof Error && error.message ? error.message : T.keyFailed;
    } finally {
      busy = '';
    }
  }
  function hide() { letGo(); note = ''; }
  async function copy() {
    if (busy) return;
    busy = T.keyWorking; note = ''; problem = '';
    const done = await copyKeyOnce(() => key ? Promise.resolve(key) : blackboxApi.revealKey(item.id), (text) => navigator.clipboard.writeText(text));
    busy = '';
    if (done === 'copied') note = T.keyCopied;
    else problem = done === 'no-key' ? T.keyFailed : T.keyCopyFailed;
  }
</script>

<section class="bb-key" aria-label={T.keyTitle}>
  <h4>{T.keyTitle}</h4>
  <p class="bb-key-value" aria-live="polite">{#if key}<code>{key}</code>{:else}<span aria-hidden="true">••••••••••••••••</span>{/if}</p>
  <p class="bb-note">{T.keyHereOnly}</p>
  <div class="bb-actions">
    {#if key}
      <button type="button" class="secondary-button" onclick={hide}>{T.keyHide}</button>
    {:else}
      <button type="button" class="secondary-button" disabled={!!busy} onclick={show}>{T.keyShow}</button>
    {/if}
    <button type="button" class="secondary-button" disabled={!!busy} onclick={copy}>{T.keyCopy}</button>
  </div>
  <div aria-live="polite">
    {#if busy}<p class="bb-note" role="status">{busy}</p>{/if}
    {#if note}<p class="bb-ok" role="status">{note}</p>{/if}
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
  </div>
</section>

<style>
  .bb-key { display: grid; gap: var(--space-2); padding-top: var(--space-3); border-top: 1px solid var(--line); min-width: 0; }
  .bb-key h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-key p { margin: 0; font-size: 13px; line-height: 1.5; }
  .bb-key-value { overflow-wrap: anywhere; word-break: break-all; }
  .bb-key-value code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 12px; }
  .bb-note { color: var(--text-2); font-size: 12px !important; }
  .bb-ok { color: var(--tone-ok); }
  .bb-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
