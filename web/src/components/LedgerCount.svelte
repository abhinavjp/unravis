<script lang="ts">
  import { onMount } from 'svelte';
  import { ledger, refreshLedger } from '../lib/ledgerStore.svelte';
  import { app } from '../lib/appState.svelte';

  // The small count shown on the header Ledger button once there is at least one hypothesis.
  // On a case page the page reads the theories itself (an archived case answers 409, so the header does not ask first).
  onMount(() => { if (!ledger.loaded && app.screen !== 'case') refreshLedger().catch(() => {}); });
  const count = $derived(ledger.items.length);
</script>

{#if count > 0}<span class="ledger-count" aria-hidden="true">{count}</span><span class="visually-hidden">, {count} {count === 1 ? 'hypothesis' : 'hypotheses'}</span>{/if}

<style>
  .ledger-count { margin-left: var(--space-2); color: var(--text-2); font-size: var(--fs-2); font-variant-numeric: tabular-nums; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
</style>
