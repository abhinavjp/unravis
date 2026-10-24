<script lang="ts">
  import type { FilterChip } from '../lib/depth';
  // The filters that live inside More filters and are active, each removable in place.
  let { chips, onRemove, onClear }: { chips: FilterChip[]; onRemove: (chip: FilterChip) => void; onClear: () => void } = $props();
</script>

{#if chips.length}
  <div class="chip-row" role="group" aria-label="Active filters">
    {#each chips as chip (chip.id)}
      <span class="filter-chip"><span>{chip.label}</span><strong>{chip.value}</strong><button type="button" aria-label={'Remove ' + chip.label + ' filter: ' + chip.value} title="Remove filter" onclick={() => onRemove(chip)}>×</button></span>
    {/each}
    {#if chips.length > 1}<button type="button" class="chip-clear" onclick={onClear}>Clear all</button>{/if}
  </div>
{/if}
