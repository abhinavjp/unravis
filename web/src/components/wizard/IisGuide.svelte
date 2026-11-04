<script lang="ts">
  import { IIS_GUIDE } from '../../lib/copy/fileKindsCopy';

  // "Before you add IIS logs: what has to be on" (gap G2, wireframe w37). A closed-by-choice note under the file panel: it is open when first
  // shown, because the person is about to add IIS logs and the settings are what make the lanes useful. The words are in copy/fileKindsCopy.ts.
  let { open = true }: { open?: boolean } = $props();
</script>

<details class="guide" {open}>
  <summary>{IIS_GUIDE.heading}</summary>
  <ul>
    {#each IIS_GUIDE.items as item (item.id)}
      <li>
        <strong>{item.lead}</strong>
        {#each item.parts as part, i (i)}{#if typeof part === 'string'}{part}{:else}<code>{part.code}</code>{/if}{/each}
      </li>
    {/each}
  </ul>
</details>

<style>
  .guide { min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  summary { font-size: var(--fs-4); font-weight: 600; cursor: pointer; overflow-wrap: anywhere; }
  summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  ul { display: grid; gap: var(--space-2); margin: var(--space-3) 0 0; padding-left: var(--space-5); color: var(--text-2); font-size: var(--fs-3); }
  li { min-width: 0; overflow-wrap: anywhere; }
  li strong { color: var(--text); }
  code { padding: 0 var(--space-1); color: var(--text); background: var(--surface-2); border-radius: var(--radius-sm); font-family: var(--mono); font-size: var(--fs-2); overflow-wrap: anywhere; }
</style>
