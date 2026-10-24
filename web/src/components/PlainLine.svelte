<script lang="ts">
  import type { PlainSummary } from '../lib/copy/plainSummary';
  import Hint from './Hint.svelte';

  // One plain-language line first; the technical detail sits one click deeper. The words come from lib/copy/plainSummary.ts, built from data the page already holds.
  let { result, hints = [] }: { result: PlainSummary; hints?: string[] } = $props();
  const uid = $props.id();
  const detailId = 'plain-detail-' + uid;
  let open = $state(false);
</script>

<div class="plain-line">
  <p class="plain-summary">{result.summary}{#each hints as id (id)}<Hint termId={id} />{/each}</p>
  <button type="button" class="plain-toggle" aria-expanded={open} aria-controls={detailId} onclick={() => open = !open}>{open ? 'Less detail' : 'More detail'}</button>
  {#if open}<p class="plain-detail" id={detailId}>{result.detail}{#if result.detail.includes('Evidence strength:')}<Hint termId="evidence-label" />{/if}</p>{/if}
</div>

<style>
  .plain-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; margin: 0 0 12px; padding: 9px 12px; background: var(--surface); border: 1px solid var(--line-soft); border-left: 3px solid var(--accent); border-radius: var(--radius); min-width: 0; }
  .plain-summary { flex: 1 1 280px; margin: 0; font-size: 13px; line-height: 1.5; min-width: 0; }
  .plain-toggle { flex: 0 0 auto; padding: 3px 6px; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius); font-size: 12px; text-decoration: underline; text-underline-offset: 3px; }
  .plain-toggle:hover { color: var(--text); background: var(--surface-2); }
  .plain-detail { flex: 1 1 100%; margin: 4px 0 0; padding-top: 8px; border-top: 1px solid var(--line-soft); color: var(--text-2); font-size: 12px; line-height: 1.55; }
</style>
