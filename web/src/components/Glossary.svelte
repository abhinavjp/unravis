<script lang="ts">
  import { onMount } from 'svelte';
  import { searchTerms } from '../lib/copy/lookup';

  // The glossary drawer: every term in the copy deck, alphabetical, with a plain text filter. It opens from the Help button in the header.
  // Esc closes it and the page gives focus back to the button. Tab stays inside the panel while it is open, like the Connections drawer.
  let { onClose }: { onClose: () => void } = $props();
  let query = $state('');
  let drawer = $state<HTMLElement>();
  let search = $state<HTMLInputElement>();
  const results = $derived(searchTerms(query));
  const summary = $derived(results.length === 0 ? 'No terms match' : results.length + (results.length === 1 ? ' term' : ' terms'));
  onMount(() => search?.focus());
  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (event.key !== 'Tab' || !drawer) return;
    const focusable = [...drawer.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])')];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
</script>

<button type="button" class="drawer-scrim" aria-label="Close glossary" onclick={onClose}></button>
<div class="connections-drawer glossary-drawer" bind:this={drawer} onkeydown={onKey} role="dialog" aria-modal="true" aria-labelledby="glossary-title" tabindex="-1">
  <div class="drawer-header"><div><h2 id="glossary-title">Glossary</h2></div><button type="button" class="icon-button drawer-close" aria-label="Close glossary" onclick={onClose}>×</button></div>
  <div class="glossary-search">
    <label for="glossary-filter">Search terms</label>
    <input id="glossary-filter" type="search" bind:this={search} bind:value={query} autocomplete="off" spellcheck="false" aria-describedby="glossary-count" />
    <span id="glossary-count" class="glossary-count" role="status" aria-live="polite">{summary}</span>
  </div>
  <dl class="glossary-list">
    {#each results as term (term.id)}
      <div class="glossary-entry" id={'glossary-' + term.id}>
        <dt>{term.term}</dt>
        <dd class="glossary-hint">{term.hint}</dd>
        <dd>{term.definition}</dd>
        <dd class="glossary-not"><span>What it does not mean</span>{term.notMeaning}</dd>
        {#if term.aliases?.length}<dd class="glossary-alias"><span>Also called</span>{term.aliases.join(', ')}</dd>{/if}
      </div>
    {/each}
  </dl>
</div>

<style>
  .glossary-drawer { overflow-x: hidden; }
  .glossary-search { display: grid; gap: 4px; margin: 13px 0 6px; }
  .glossary-search label { color: var(--text-2); font-size: 12px; }
  .glossary-search input { width: 100%; min-width: 0; min-height: 38px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); font-size: 14px; }
  .glossary-count { color: var(--text-2); font-size: 12px; }
  .glossary-list { margin: 0; }
  .glossary-entry { padding: 12px 0; border-bottom: 1px solid var(--line-soft); }
  .glossary-entry dt { font-size: 15px; font-weight: 650; }
  .glossary-entry dd { margin: 5px 0 0; font-size: 13px; overflow-wrap: anywhere; }
  .glossary-hint { font-weight: 600; }
  .glossary-not, .glossary-alias { color: var(--text-2); }
  .glossary-not span, .glossary-alias span { display: block; color: var(--text); font-size: 11px; font-weight: 650; letter-spacing: .09em; text-transform: uppercase; }
</style>
