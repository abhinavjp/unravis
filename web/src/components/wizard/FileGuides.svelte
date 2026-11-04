<script lang="ts">
  import { CARD_GUIDES } from '../../lib/copy/fileKindsCopy';

  // The short guides of the application-log and Query Store cards (wireframe w37). The IIS guide is IisGuide. The words are in copy/fileKindsCopy.ts.
</script>

{#each CARD_GUIDES as guide (guide.id)}
  <details class="guide" data-guide={guide.id}>
    <summary>{guide.heading}</summary>
    <ol>
      {#each guide.steps as step, i (i)}<li>{step}</li>{/each}
    </ol>
  </details>
{/each}

<style>
  .guide { min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  summary { font-size: var(--fs-4); font-weight: 600; cursor: pointer; overflow-wrap: anywhere; }
  summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  ol { display: grid; gap: var(--space-2); margin: var(--space-3) 0 0; padding-left: var(--space-5); color: var(--text-2); font-size: var(--fs-3); }
  li { min-width: 0; overflow-wrap: anywhere; }
</style>
