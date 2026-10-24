<script lang="ts">
  // STUB: Task E replaces this component. Props are the contract with App.svelte.
  let { onClose }: { onClose: () => void } = $props();
</script>

<button type="button" class="drawer-scrim" aria-label="Close ledger" onclick={onClose}></button>
<div class="connections-drawer glossary-drawer" role="dialog" aria-modal="true" aria-labelledby="ledger-title" tabindex="-1">
  <div class="drawer-header"><div><h2 id="ledger-title">Ledger</h2></div><button type="button" class="icon-button drawer-close" aria-label="Close ledger" onclick={onClose}>×</button></div>
</div>
