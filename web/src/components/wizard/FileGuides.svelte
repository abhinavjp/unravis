<script lang="ts">
  import { CARD_GUIDES } from '../../lib/copy/fileKindsCopy';

  // The three short guides of the file cards (wireframe w37): IIS time-taken, application logs and Query Store export. They are always shown, as boxes
  // under the cards, so a person who does not know where a file comes from never has to open anything. The words are in copy/fileKindsCopy.ts.
</script>

{#each CARD_GUIDES as guide (guide.id)}
  <section class="guide" data-guide={guide.id} aria-labelledby="guide-{guide.id}">
    <b id="guide-{guide.id}">{guide.heading}</b>
    <ol>
      {#each guide.steps as step, i (i)}<li>{step}</li>{/each}
    </ol>
    {#if guide.why}<p class="why">{guide.why}</p>{/if}
  </section>
{/each}

<style>
  .guide { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  b { font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  ol { display: grid; gap: var(--space-1); margin: 0; padding-left: var(--space-5); color: var(--text-2); font-size: var(--fs-3); }
  li { min-width: 0; overflow-wrap: anywhere; }
  .why { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
</style>
