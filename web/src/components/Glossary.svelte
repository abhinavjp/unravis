<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../lib/api';
  import { findTerm } from '../lib/copy/lookup';
  import { searchTerms } from '../lib/copy/lookup';
  import { HELP_STEPS, HELP_TEXT, glossaryCount } from '../lib/copy/screenCopy';
  import { versionLine } from '../lib/versionLine';
  import Drawer from './Drawer.svelte';

  // The glossary drawer: every term in the copy deck, alphabetical, with a plain text filter. It opens from the Help button in the header.
  // Esc closes it and the page gives focus back to the button. Tab stays inside the panel while it is open, like the Connections drawer.
  // A hint's "Learn more in the glossary" button opens it on one term: that entry scrolls into view and takes focus, and is marked.
  let { onClose, focusTerm = null }: { onClose: () => void; focusTerm?: string | null } = $props();
  let query = $state('');
  let drawer = $state<HTMLElement>();
  let search = $state<HTMLInputElement>();
  const results = $derived(searchTerms(query));
  const summary = $derived(glossaryCount(results.length, query));
  // Which build this is, quietly, at the foot of the panel. If the call fails the line is simply absent.
  let build = $state('');
  onMount(() => {
    void api.version().then((info) => { build = versionLine(info); }).catch(() => { build = ''; });
  });
  onMount(() => { if (!focusTerm || !findTerm(focusTerm)) search?.focus(); });
  $effect(() => {
    const id = focusTerm;
    if (!id || !findTerm(id)) return;
    query = '';
    void tick().then(() => {
      const entry = drawer?.querySelector<HTMLElement>('#glossary-' + CSS.escape(id));
      if (!entry) return;
      entry.scrollIntoView({ block: 'start' });
      entry.focus({ preventScroll: true });
    });
  });
</script>

<Drawer class="glossary-drawer" title={HELP_TEXT.title} titleId="glossary-title" closeLabel="Close help and glossary" focusable={'button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'} bind:element={drawer} {onClose}>
  <section class="help-start" aria-labelledby="help-start-title">
    <h3 id="help-start-title">{HELP_TEXT.startHeading}</h3>
    <p>{HELP_TEXT.startIntro}</p>
    <ol>
      {#each HELP_STEPS as step (step)}<li>{step}</li>{/each}
    </ol>
  </section>
  <h3 class="glossary-heading">{HELP_TEXT.termsHeading}</h3>
  <div class="glossary-search">
    <label for="glossary-filter">Search terms</label>
    <input id="glossary-filter" type="search" bind:this={search} bind:value={query} autocomplete="off" spellcheck="false" aria-describedby="glossary-count" />
    <span id="glossary-count" class="glossary-count" role="status" aria-live="polite">{summary}</span>
  </div>
  <dl class="glossary-list">
    {#each results as term (term.id)}
      <div class="glossary-entry" class:glossary-focused={term.id === focusTerm} id={'glossary-' + term.id} tabindex="-1">
        <dt>{term.term}</dt>
        <dd class="glossary-hint">{term.hint}</dd>
        <dd>{term.definition}</dd>
        <dd class="glossary-not"><span>What it does not mean</span>{term.notMeaning}</dd>
        {#if term.aliases?.length}<dd class="glossary-alias"><span>Also called</span>{term.aliases.join(', ')}</dd>{/if}
      </div>
    {/each}
  </dl>
  {#if build}<p class="build-line" data-testid="build-line">{build}</p>{/if}
</Drawer>

<style>
  :global(.connections-drawer.glossary-drawer) { overflow-x: hidden; }
  .glossary-search { display: grid; gap: 4px; margin: 13px 0 6px; }
  .glossary-search label { color: var(--text-2); font-size: 12px; font-weight: 500; }
  .glossary-search input { width: 100%; min-width: 0; min-height: 34px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-size: 13px; }
  .glossary-count { color: var(--text-2); font-size: 12px; }
  .glossary-list { margin: 0; }
  .glossary-entry { padding: 12px 0; border-bottom: 1px solid var(--line-soft); }
  .glossary-entry:focus { outline: none; }
  .glossary-focused { margin: 0 -10px; padding: 12px 10px; background: var(--surface-2); border-radius: 0; box-shadow: inset 0 0 0 1px var(--accent); }
  .help-start { margin: 13px 0 0; padding-bottom: 12px; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
  .help-start h3, .glossary-heading { margin: 0 0 6px; font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .glossary-heading { margin-top: 14px; }
  .help-start p { margin: 0 0 6px; color: var(--text-2); }
  .help-start ol { margin: 0; padding-left: 20px; display: grid; gap: 4px; }
  .glossary-entry dt { font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .glossary-entry dd { margin: 5px 0 0; font-size: 13px; overflow-wrap: anywhere; }
  .build-line { margin: 14px 0 0; color: var(--text-2); font: 12px var(--mono); }
  .glossary-hint { font-weight: 600; }
  .glossary-not, .glossary-alias { color: var(--text-2); }
  .glossary-not span, .glossary-alias span { display: block; color: var(--text); font-size: 12px; font-weight: 600; }
</style>
