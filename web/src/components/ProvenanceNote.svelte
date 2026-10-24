<script lang="ts">
  import { provenanceRows } from '../lib/provenance';
  import type { Provenance } from '../lib/types';
  import Hint from './Hint.svelte';

  // How each read of the records ran: one plain line per source and period. It shows nothing when the server sent no provenance,
  // so an older server looks the same as before. The words come from lib/provenance.ts.
  let { provenance, sourceLabels = new Map<string, string>() }: { provenance?: Provenance[] | null; sourceLabels?: ReadonlyMap<string, string> } = $props();
  const rows = $derived(provenanceRows(provenance, sourceLabels));
</script>

{#if rows.length}
  <ul class="provenance-note" aria-label="How the records were read">
    {#each rows as row (row.key)}
      <li class={row.tone}>
        <span class="prov-label">{row.label}</span>
        <span class="prov-line">{row.line}{#if row.note} {row.note}{/if}{#each row.terms as id (id)}<Hint termId={id} />{/each}</span>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .provenance-note { list-style: none; margin: 0; position: relative; padding: 8px 0 8px 20px; display: grid; gap: 6px; background: transparent; border: 0; border-top: 1px solid var(--hairline); border-bottom: 1px solid var(--hairline); border-radius: 0; min-width: 0; }
  .provenance-note::before { content: ""; position: absolute; left: 0; top: 12px; width: 9px; height: 9px; border-radius: 50%; background: var(--hatch); box-shadow: 0 0 0 1px var(--pending); }
  .provenance-note li { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 10px; font-size: 12px; line-height: 1.5; color: var(--text-2); min-width: 0; }
  .provenance-note li.warn { color: var(--text); }
  .provenance-note li.warn .prov-label { color: var(--warn); }
  .prov-label { flex: 0 0 auto; font-weight: 600; color: var(--text); }
  .prov-line { flex: 1 1 260px; min-width: 0; }
</style>
