<script lang="ts">
  import type { Payload } from '../lib/types';
  let { payload, onClose }: { payload: Payload | null; onClose: () => void } = $props();
</script>

{#if payload}
  <section class="payload-panel" aria-label="Request payload details">
    <div class="payload-head"><div><h3>Request payload</h3>{#if payload.truncated}<p class="warning-text">Payload excerpt limited by the server.</p>{/if}</div><button class="quiet-button" onclick={onClose} aria-label="Close payload">Close</button></div>
    <div class="payload-columns">
      <div><h4>Request</h4><pre>{payload.request || 'No request payload'}</pre></div>
      <div><h4>Response</h4><pre>{payload.response || 'No response payload'}</pre></div>
    </div>
  </section>
{/if}
