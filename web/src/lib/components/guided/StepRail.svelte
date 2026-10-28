<script lang="ts">
  import { GUIDED_TEXT } from '../../copy/guidedCopy';
  import type { RailItem } from '../../guided/stepModel';
  import Sticker from './Sticker.svelte';

  // The left rail of Steps 0 to 10. Each row is a button (outline = press me); the status next to it is a sticker (information).
  let { items, onSelect }: { items: RailItem[]; onSelect: (step: number) => void } = $props();
</script>

<nav class="rail" aria-label={GUIDED_TEXT.railLabel}>
  <ol>
    {#each items as item (item.number)}
      <li>
        <button type="button" class="row" class:current={item.current} aria-current={item.current ? 'step' : undefined} onclick={() => onSelect(item.number)}>
          <span class="num">{item.number}</span>
          <span class="label">{item.label}</span>
          <Sticker word={item.sticker.word} tone={item.sticker.tone} outOfDate={item.sticker.outOfDate} />
        </button>
      </li>
    {/each}
  </ol>
  <p class="hint">{GUIDED_TEXT.railHint}</p>
</nav>

<style>
  .rail { display: grid; gap: 8px; align-content: start; min-width: 0; }
  ol { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; }
  .row { display: grid; grid-template-columns: 24px 1fr; grid-template-areas: 'num label' '. sticker'; align-items: center; gap: 2px 8px; width: 100%; padding: 8px; text-align: left; color: var(--text); background: transparent; border: 1px solid var(--line); border-radius: var(--radius-sm); font-size: 13px; }
  .row:hover { border-color: var(--text-2); background: var(--surface-2); }
  .row.current { border-color: var(--accent); box-shadow: inset 2px 0 0 var(--accent); font-weight: 600; }
  .row :global(.sticker) { grid-area: sticker; justify-self: start; white-space: normal; }
  .num { grid-area: num; color: var(--text-2); font-variant-numeric: tabular-nums; }
  .label { grid-area: label; overflow-wrap: anywhere; }
  .hint { margin: 0; color: var(--text-2); font-size: 12px; }
</style>
