<script lang="ts">
  import { resourceCopy, type Chip } from '../../lib/resource';

  // One chip per fired rule. The wording and the label are exactly what the server sent; the hint says in plain words what the rule looks at.
  // A chip opens the rule rows in the drawer.
  let { chips, showHost = false, onOpen }: { chips: Chip[]; showHost?: boolean; onOpen: (chip: Chip) => void } = $props();
</script>

{#if chips.length === 0}
  <p class="none">{resourceCopy.chipsNone}</p>
{:else}
  <ul class="chips" aria-label={resourceCopy.chipsHeading}>
    {#each chips as chip (chip.key)}
      <li>
        <button type="button" class="chip" aria-label={chip.aria} onclick={() => onOpen(chip)}>
          <span class="top"><strong>{chip.text}</strong><span class="label">{chip.label}</span>{#if showHost}<span class="host">{chip.host}</span>{/if}</span>
          <span class="wording">{chip.wording}</span>
          <span class="hint">{chip.hint}</span>
        </button>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .none { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .chips { display: flex; flex-wrap: wrap; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .chips li { min-width: 0; flex: 1 1 220px; }
  .chip { display: grid; gap: 2px; width: 100%; padding: 8px 10px; text-align: left; white-space: normal; color: var(--text); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .chip:hover { border-color: var(--text-2); }
  .top { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; font-size: var(--fs-3); }
  .label, .host { color: var(--text-2); font-size: var(--fs-1, 12px); }
  .label { padding: 0 6px; border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .wording { font-size: var(--fs-2); line-height: 1.4; overflow-wrap: anywhere; }
  .hint { color: var(--text-2); font-size: var(--fs-2); line-height: 1.4; }
</style>
