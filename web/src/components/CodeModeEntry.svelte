<script lang="ts">
  import { closeSidePanel } from '../lib/appState.svelte';
  import { codeModeStore as store } from '../lib/codeModeStore.svelte';
  import { openCodeMode } from '../lib/codeModeUi.svelte';
  import { CODE_TEXT as T } from '../lib/copy/codeModeCopy';

  // The way into the code-mode popup from the AI access drawer. The header badge only shows once code mode is on, so this is how
  // someone finds the setting while it is off.
  const body = $derived(store.settings?.damaged ? T.entryDamaged : store.settings?.enabled ? T.entryOnBody : T.entryBody);

  async function open() {
    await closeSidePanel();
    openCodeMode(document.querySelector<HTMLElement>('[data-testid="open-mcp"]'));
  }
</script>

{#if store.status !== 'unavailable'}
  <section class="code-entry" aria-labelledby="code-entry-title" data-testid="code-entry">
    <h3 id="code-entry-title">{T.entryTitle}</h3>
    <p>{body}</p>
    <button type="button" class="secondary-button" onclick={() => void open()}>{T.entryOpen}</button>
  </section>
{/if}

<style>
  .code-entry { display: grid; gap: var(--space-2); justify-items: start; padding: var(--space-4); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .code-entry h3 { margin: 0; font-size: 15px; font-weight: 600; }
  .code-entry p { max-width: 56ch; margin: 0; color: var(--text-2); font-size: 13px; }
</style>
