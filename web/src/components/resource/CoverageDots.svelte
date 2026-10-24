<script lang="ts">
  import { coverageDot, resourceCopy, type CoverageItem } from '../../lib/resource';

  // One small dot per counter the host has: how much of the window it covers. The fill is a shape (full, most, some, empty), not only a colour,
  // and each dot says the same in words for a screen reader and on hover.
  let { coverage }: { coverage: CoverageItem[] } = $props();
  const dots = $derived(coverage.map(item => ({ key: item.metric, ...coverageDot(item) })));
</script>

{#if dots.length > 0}
  <ul class="dots" aria-label={resourceCopy.coverageLabel}>
    {#each dots as dot (dot.key)}
      <li class="dot {dot.level}" role="img" aria-label={dot.text} title={dot.text}></li>
    {/each}
  </ul>
{/if}

<style>
  .dots { display: flex; flex-wrap: wrap; gap: 4px; margin: 0; padding: 0; list-style: none; }
  .dot { flex: 0 0 auto; width: 10px; height: 10px; border: 1.5px solid var(--text-2); border-radius: 50%; background: transparent; }
  .dot.full { background: var(--text-2); }
  .dot.most { background: conic-gradient(var(--text-2) 0 66%, transparent 66% 100%); }
  .dot.some { background: conic-gradient(var(--text-2) 0 33%, transparent 33% 100%); }
  .dot.none { border-style: dashed; opacity: 0.6; }
</style>
