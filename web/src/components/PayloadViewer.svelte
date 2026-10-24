<script lang="ts">
  import type { Payload } from '../lib/types';
  // `embedded` is for use inside another panel that already has a heading and a
  // Close button: it shows only the truncation note and the two payloads.
  let { payload, onClose, embedded = false }: { payload: Payload | null; onClose?: () => void; embedded?: boolean } = $props();

  // Each payload is a monospace block with its own Copy button. The label reads "Copied" for a moment, and the status line tells a screen reader.
  let copied = $state<'' | 'request' | 'response' | 'failed'>('');
  let timer: ReturnType<typeof setTimeout> | undefined;
  async function copy(which: 'request' | 'response', text: string) {
    let ok = false;
    try { await navigator.clipboard.writeText(text); ok = true; } catch { ok = false; }
    copied = ok ? which : 'failed';
    clearTimeout(timer);
    timer = setTimeout(() => { copied = ''; }, 2000);
  }
</script>

{#if payload}
  <section class="payload-panel" aria-label="Request payload details">
    {#if embedded}
      {#if payload.truncated}<p class="warning-text">Payload excerpt limited by the server, or a payload is absent.</p>{/if}
    {:else}
      <div class="payload-head"><div><h3>Request payload</h3>{#if payload.truncated}<p class="warning-text">Payload excerpt limited by the server.</p>{/if}</div><button class="quiet-button" onclick={onClose} aria-label="Close payload">Close</button></div>
    {/if}
    <div class="payload-columns">
      <div>
        <div class="payload-title"><h4>Request</h4>{#if payload.request}<button type="button" class="quiet-button copy-button" onclick={() => copy('request', payload.request)}>{copied === 'request' ? 'Copied' : 'Copy'}<span class="sr-only"> request text</span></button>{/if}</div>
        <pre>{payload.request || 'No request payload'}</pre>
      </div>
      <div>
        <div class="payload-title"><h4>Response</h4>{#if payload.response}<button type="button" class="quiet-button copy-button" onclick={() => copy('response', payload.response)}>{copied === 'response' ? 'Copied' : 'Copy'}<span class="sr-only"> response text</span></button>{/if}</div>
        <pre>{payload.response || 'No response payload'}</pre>
      </div>
    </div>
    <p class="sr-only" role="status">{copied === 'request' ? 'Request text copied.' : copied === 'response' ? 'Response text copied.' : copied === 'failed' ? 'Could not copy. Select the text and copy it by hand.' : ''}</p>
    {#if copied === 'failed'}<p class="warning-text">Could not copy. Select the text and copy it by hand.</p>{/if}
  </section>
{/if}

<style>
  .payload-title { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); margin: 0 0 var(--space-1); }
  .payload-title :global(h4) { margin: 0; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
  .copy-button { min-height: 24px; padding: 2px var(--space-2); font-size: var(--fs-2); }
</style>
