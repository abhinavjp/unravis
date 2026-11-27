<script lang="ts">
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';

  // A block of text the person can copy: commands that only read, or a message for the server admin. Nothing here runs by itself.
  let { label, text, open = false }: { label: string; text: string; open?: boolean } = $props();

  let state = $state<'idle' | 'copied' | 'failed'>('idle');

  async function copy() {
    try { await navigator.clipboard.writeText(text); state = 'copied'; } catch { state = 'failed'; }
    setTimeout(() => { state = 'idle'; }, 2500);
  }
</script>

<details class="sv-cmd" {open}>
  <summary>{label}</summary>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <pre tabindex="0">{text}</pre>
  <div class="sv-cmd-actions">
    <button type="button" class="secondary-button" onclick={copy}>{state === 'copied' ? T.copied : T.copy}</button>
    {#if state === 'failed'}<span class="sv-bad" role="status">{T.copyFailed}</span>{/if}
  </div>
</details>

<style>
  .sv-cmd { font-size: 13px; }
  .sv-cmd summary { width: fit-content; font-weight: 500; }
  .sv-cmd pre { margin: var(--space-2) 0; padding: var(--space-3); max-height: 220px; overflow: auto; font-family: var(--mono); font-size: 12px; line-height: 1.5; white-space: pre-wrap; overflow-wrap: anywhere; color: var(--text); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .sv-cmd-actions { display: flex; align-items: center; gap: var(--space-3); }
  .sv-bad { color: var(--bad); font-size: 12px; }
</style>
