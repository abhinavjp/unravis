<script lang="ts">
  import type { Payload } from '../lib/types';
  // `embedded` is for use inside another panel that already has a heading and a
  // Close button: it shows only the truncation note and the two payloads.
  let { payload, onClose, embedded = false }: { payload: Payload | null; onClose?: () => void; embedded?: boolean } = $props();
</script>

{#if payload}
  <section class="payload-panel" aria-label="Request payload details">
    {#if embedded}
      {#if payload.truncated}<p class="warning-text">Payload excerpt limited by the server, or a payload is absent.</p>{/if}
    {:else}
      <div class="payload-head"><div><h3>Request payload</h3>{#if payload.truncated}<p class="warning-text">Payload excerpt limited by the server.</p>{/if}</div><button class="quiet-button" onclick={onClose} aria-label="Close payload">Close</button></div>
    {/if}
    <div class="payload-columns">
      <div><h4>Request</h4><pre>{payload.request || 'No request payload'}</pre></div>
      <div><h4>Response</h4><pre>{payload.response || 'No response payload'}</pre></div>
    </div>
  </section>
{/if}