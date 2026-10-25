<script lang="ts">
  import { tick } from 'svelte';
  import type { FilterChip } from '../lib/depth';
  // The filters that live inside More filters and are active, each removable in place.
  // Removing a chip re-renders the row, so focus is handed on explicitly: to the chip now at the same place (else the one before it),
  // and when none is left to onEmpty, so focus never drops to the page body.
  let { chips, onRemove, onClear, onEmpty }: { chips: FilterChip[]; onRemove: (chip: FilterChip) => void; onClear: () => void; onEmpty?: () => void } = $props();
  let row = $state<HTMLElement>();
  async function remove(chip: FilterChip, index: number) {
    onRemove(chip);
    await tick();
    const buttons = row?.querySelectorAll<HTMLButtonElement>('.filter-chip button') ?? [];
    if (buttons.length) buttons[Math.min(index, buttons.length - 1)].focus();
    else onEmpty?.();
  }
  async function clear() {
    onClear();
    await tick();
    onEmpty?.();
  }
</script>

{#if chips.length}
  <div class="chip-row" role="group" aria-label="Active filters" bind:this={row}>
    {#each chips as chip, index (chip.id)}
      <span class="filter-chip"><span>{chip.label}</span><strong>{chip.value}</strong><button type="button" aria-label={'Remove ' + chip.label + ' filter: ' + chip.value} title="Remove filter" onclick={() => remove(chip, index)}>×</button></span>
    {/each}
    {#if chips.length > 1}<button type="button" class="chip-clear" onclick={clear}>Clear all</button>{/if}
  </div>
{/if}
