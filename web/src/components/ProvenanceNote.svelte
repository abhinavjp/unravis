<script lang="ts">
  import { provenanceRows } from '../lib/provenance';
  import { provenanceSummary } from '../lib/copy/screenCopy';
  import type { Provenance } from '../lib/types';
  import Hint from './Hint.svelte';

  // How each read of the records ran: one plain line per source and period. It shows nothing when the server sent no provenance,
  // so an older server looks the same as before. The words come from lib/provenance.ts.
  let { provenance, sourceLabels = new Map<string, string>() }: { provenance?: Provenance[] | null; sourceLabels?: ReadonlyMap<string, string> } = $props();
  const rows = $derived(provenanceRows(provenance, sourceLabels));
  // When every read finished, one line is enough and the per-source detail sits one click deeper. A partial or hint-mode read always stays in view.
  const summary = $derived(provenanceSummary(provenance));
</script>

{#snippet list()}
  <ul class="provenance-note" aria-label="How the records were read">
    {#each rows as row (row.key)}
      <li class={row.tone}>
        <span class="prov-label">{row.label}</span>
        <span class="prov-line">{row.line}{#if row.note}{' '}{row.note}{/if}{#each row.terms as id (id)}<Hint termId={id} />{/each}</span>
      </li>
    {/each}
  </ul>
{/snippet}

{#if rows.length}
  {#if summary}
    <div class="provenance-summary">
      <p class="prov-summary-line">{summary}<Hint termId="complete" /></p>
      <details><summary>Details for each source and period</summary>{@render list()}</details>
    </div>
  {:else}
    {@render list()}
  {/if}
{/if}

<style>
  .provenance-note { list-style: none; margin: 0; padding: 8px 12px; display: grid; gap: 6px; background: var(--surface); border: 1px solid var(--line-soft); border-left: 3px solid var(--line); border-radius: var(--radius); min-width: 0; }
  .provenance-note li { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 10px; font-size: 12px; line-height: 1.5; color: var(--text-2); min-width: 0; }
  .provenance-note li.warn { color: var(--text); }
  .provenance-note li.warn .prov-label { color: var(--warn); }
  .provenance-summary { display: grid; gap: 6px; min-width: 0; font-size: 12px; color: var(--text-2); }
  .prov-summary-line { margin: 0; }
  .provenance-summary summary { cursor: pointer; width: fit-content; }
  .provenance-summary .provenance-note { margin-top: 6px; }
  .prov-label { flex: 0 0 auto; font-weight: 600; color: var(--text); }
  .prov-line { flex: 1 1 260px; min-width: 0; }
</style>
