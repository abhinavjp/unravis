<script lang="ts">
  import Drawer from '../Drawer.svelte';
  import { resourceCopy, rangeText, ruleHint, type RuleRow } from '../../lib/resource';

  // The rule rows behind a chip, a band or a click: the rule's wording and label as sent, the limits it used, the evidence ids and the stall period.
  // The case tools sit inside another drawer, so this one is moved to the page body: a drawer inside a drawer would be clipped by it.
  let { rows, title = resourceCopy.drawerHeading, zone, onClose }: { rows: RuleRow[]; title?: string; zone: string; onClose: () => void } = $props();

  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return { destroy() { node.remove(); } };
  }
</script>

<div class="portal" use:portal>
  <Drawer {title} titleId="resource-rows-title" closeLabel={resourceCopy.drawerClose} focusTitle returnFocus {onClose}>
    {#if rows.length === 0}
      <p class="none">{resourceCopy.drawerNoRows}</p>
    {:else}
      <ul class="rows">
        {#each rows as row, i (i)}
          <li class="row">
            <h3>{row.title}<span class="label">{row.label}</span></h3>
            <p class="meta">{resourceCopy.hostLabel}: {row.host}</p>
            <p class="wording"><span class="key">{resourceCopy.wordingLabel}</span> {row.wording}</p>
            <p class="hint">{ruleHint(row.id)}</p>
            <p class="meta">{resourceCopy.whenLabel}: {row.episode ? rangeText(row.episode.start, row.episode.end, zone) : resourceCopy.anyTime}</p>
            {#if row.thresholds.length > 0}
              <p class="key">{resourceCopy.thresholdsLabel}</p>
              <ul class="plain">{#each row.thresholds as item, k (k)}<li>{item}</li>{/each}</ul>
            {/if}
            <p class="key">{resourceCopy.evidenceLabel}</p>
            {#if row.evidence.length === 0}
              <p class="meta">{resourceCopy.noEvidence}</p>
            {:else}
              <ul class="ids">{#each row.evidence as id, k (k)}<li><code>{id}</code></li>{/each}</ul>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </Drawer>
</div>

<style>
  .portal { position: relative; z-index: 60; }
  .none { margin: var(--space-3) 0 0; color: var(--text-2); font-size: var(--fs-3); }
  .rows { display: grid; gap: var(--space-4); margin: var(--space-3) 0 0; padding: 0; list-style: none; }
  .row { display: grid; gap: 4px; padding-top: var(--space-3); border-top: 1px solid var(--hairline); }
  .row:first-child { padding-top: 0; border-top: 0; }
  h3 { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; margin: 0; font-size: var(--fs-4); font-weight: 600; letter-spacing: -.01em; }
  .label { padding: 0 6px; color: var(--text-2); font-size: 12px; font-weight: 400; border: 1px solid var(--line); border-radius: var(--radius-sm); }
  p { margin: 0; font-size: var(--fs-3); line-height: 1.5; overflow-wrap: anywhere; }
  .meta, .hint { color: var(--text-2); font-size: var(--fs-2); }
  .key { margin-top: 4px; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .plain, .ids { display: grid; gap: 2px; margin: 0; padding: 0 0 0 18px; font-size: var(--fs-2); }
  .ids { padding-left: 0; list-style: none; }
  .ids code { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; user-select: all; }
</style>
