<script lang="ts">
  import type { Query } from '../lib/types';
  // STUB: Task D replaces this component. Props are the contract with App.svelte.
  let { query, zone, sourceLabels, onClose }: { query: Query; zone: string; sourceLabels: Map<string, string>; onClose: () => void } = $props();
</script>

<button type="button" class="drawer-scrim" aria-label="Close patterns" onclick={onClose}></button>
<div class="connections-drawer glossary-drawer" role="dialog" aria-modal="true" aria-labelledby="patterns-title" tabindex="-1">
  <div class="drawer-header"><div><h2 id="patterns-title">Patterns</h2><p hidden>{query.sourceIds.length} {zone} {sourceLabels.size}</p></div><button type="button" class="icon-button drawer-close" aria-label="Close patterns" onclick={onClose}>×</button></div>
</div>
