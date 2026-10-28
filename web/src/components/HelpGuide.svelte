<script lang="ts">
  import { tick } from 'svelte';
  import { HELP_GUIDE_TEXT } from '../lib/copy/helpCopy';
  import { helpPage, helpPages, helpSlugs } from '../lib/help/pages';
  import { resolveHelpLink } from '../lib/help/links';
  import type { Block, Inline } from '../lib/help/markdown';

  // The user guide inside the Help drawer: the pages of docs/user as Svelte markup. Nothing here builds HTML from text,
  // so a page can never inject markup. Pictures are left out of the app and show their description as a muted line.
  let { slug, anchor, onNavigate }: { slug: string; anchor: string; onNavigate: (slug: string, anchor: string) => void; onBack: () => void } = $props(); // onBack is part of the contract; the Back button itself lives in the drawer, which owns the focus return.
  let root = $state<HTMLElement>();
  const page = $derived(helpPage(slug));

  function scrollParent(el: HTMLElement): HTMLElement | null {
    for (let p = el.parentElement; p; p = p.parentElement) {
      const overflow = getComputedStyle(p).overflowY;
      if ((overflow === 'auto' || overflow === 'scroll') && p.scrollHeight > p.clientHeight) return p;
    }
    return null;
  }
  // Scroll to a heading of this page and focus it, or go to the top of the page and focus its first heading.
  async function reveal(pageSlug: string, target: string) {
    await tick();
    if (!root) return;
    const heading = target ? document.getElementById('help-' + pageSlug + '-' + target) : null;
    if (heading && root.contains(heading)) {
      heading.scrollIntoView({ block: 'start' });
      heading.focus({ preventScroll: true });
      return;
    }
    const parent = scrollParent(root);
    if (parent) parent.scrollTop = 0;
    root.querySelector<HTMLElement>('.guide-heading')?.focus({ preventScroll: true });
  }
  $effect(() => {
    const pageSlug = slug;
    const target = anchor;
    void reveal(pageSlug, target);
  });
  function go(targetSlug: string, targetAnchor: string) {
    if (targetSlug === slug) void reveal(slug, targetAnchor);
    else onNavigate(targetSlug, targetAnchor);
  }
</script>

{#snippet inline(nodes: Inline[])}
  {#each nodes as node}
    {#if node.t === 'text'}{node.text}
    {:else if node.t === 'code'}<code>{node.text}</code>
    {:else if node.t === 'strong'}<strong>{@render inline(node.children)}</strong>
    {:else if node.t === 'em'}<em>{@render inline(node.children)}</em>
    {:else if node.t === 'image'}<span class="guide-image">{HELP_GUIDE_TEXT.guideImage}{node.alt}</span>
    {:else if node.t === 'link'}
      {@const target = resolveHelpLink(node.href, helpSlugs)}
      {#if target.kind === 'page'}<button type="button" class="guide-link" onclick={() => go(target.slug, target.anchor)}>{@render inline(node.children)}</button>
      {:else if target.kind === 'anchor'}<button type="button" class="guide-link" onclick={() => void reveal(slug, target.anchor)}>{@render inline(node.children)}</button>
      {:else if target.kind === 'external'}<a href={target.href} target="_blank" rel="noopener noreferrer">{@render inline(node.children)}</a>
      {:else}{@render inline(node.children)}{/if}
    {/if}
  {/each}
{/snippet}

{#snippet blocksView(list: Block[])}
  {#each list as block}
    {#if block.t === 'heading'}
      <svelte:element this={'h' + Math.min(6, block.level + 2)} class="guide-heading" id={'help-' + slug + '-' + block.id} tabindex="-1">{@render inline(block.children)}</svelte:element>
    {:else if block.t === 'p'}
      {#if block.children.length === 1 && block.children[0].t === 'image'}
        <p class="guide-image">{HELP_GUIDE_TEXT.guideImage}{block.children[0].alt}</p>
      {:else}
        <p>{@render inline(block.children)}</p>
      {/if}
    {:else if block.t === 'list'}
      <svelte:element this={block.ordered ? 'ol' : 'ul'}>
        {#each block.items as item}
          <li>{@render inline(item.children)}{#if item.sub.length}{@render blocksView(item.sub)}{/if}</li>
        {/each}
      </svelte:element>
    {:else if block.t === 'code'}
      <pre><code>{block.text}</code></pre>
    {:else if block.t === 'quote'}
      <blockquote>{@render blocksView(block.blocks)}</blockquote>
    {:else if block.t === 'table'}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="guide-table" role="region" aria-label={page?.title ?? ''} tabindex="0">
        <table>
          <thead><tr>{#each block.head as cell}<th scope="col">{@render inline(cell)}</th>{/each}</tr></thead>
          <tbody>
            {#each block.rows as row}<tr>{#each row as cell}<td>{@render inline(cell)}</td>{/each}</tr>{/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/each}
{/snippet}

<div class="help-guide" bind:this={root}>
  <nav aria-label={HELP_GUIDE_TEXT.guideContents}>
    <ul class="guide-nav">
      {#each helpPages as entry (entry.slug)}
        <li><button type="button" class="guide-nav-button" data-testid={'guide-nav-' + entry.slug} aria-current={entry.slug === slug ? 'page' : undefined} onclick={() => go(entry.slug, '')}>{entry.title}</button></li>
      {/each}
    </ul>
  </nav>
  <div class="guide-body" data-testid="guide-body">
    {#if page}
      {@render blocksView(page.blocks)}
    {:else}
      <p class="guide-missing">{HELP_GUIDE_TEXT.guideMissing}</p>
    {/if}
  </div>
</div>

<style>
  .help-guide { min-width: 0; font-size: 13px; line-height: 1.5; }
  .guide-nav { margin: 0; padding: 0; list-style: none; display: flex; flex-wrap: wrap; gap: 6px; }
  .guide-nav-button { border: 1px solid var(--control-line); background: transparent; color: var(--text); padding: 4px 10px; border-radius: var(--radius-sm); font: inherit; font-size: 12px; cursor: pointer; }
  .guide-nav-button:hover { background: var(--surface-2); }
  .guide-nav-button[aria-current='page'] { background: var(--surface-2); font-weight: 600; }
  .guide-nav-button:focus-visible, .guide-link:focus-visible, .guide-table:focus-visible, .guide-body :global(a:focus-visible), pre:focus-visible { outline: 2px solid var(--focus); }
  .guide-body { margin-top: 14px; padding-top: 4px; border-top: 1px solid var(--line-soft); overflow-wrap: anywhere; }
  .guide-body :global(.guide-heading) { margin: 18px 0 6px; font-weight: 600; letter-spacing: -.01em; line-height: 1.3; }
  .guide-body :global(.guide-heading:focus) { outline: none; }
  .guide-body :global(.guide-heading:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .guide-body :global(h3) { font-size: 16px; margin-top: 12px; }
  .guide-body :global(h4) { font-size: 14px; }
  .guide-body :global(h5), .guide-body :global(h6) { font-size: 13px; }
  p { margin: 0 0 8px; }
  ul, ol { margin: 0 0 8px; padding-left: 20px; display: grid; gap: 3px; }
  li :global(ul), li :global(ol) { margin: 3px 0 0; }
  code { font-family: var(--mono); font-size: 12px; padding: 0 3px; background: var(--surface-2); border-radius: 3px; }
  pre { margin: 0 0 8px; padding: 8px 10px; max-width: 100%; overflow-x: auto; background: var(--surface-2); border-radius: var(--radius-sm); }
  pre code { padding: 0; background: transparent; white-space: pre; overflow-wrap: normal; }
  blockquote { margin: 0 0 8px; padding: 2px 0 2px 10px; border-left: 2px solid var(--control-line); color: var(--text-2); }
  blockquote :global(p:last-child) { margin-bottom: 0; }
  .guide-table { margin: 0 0 8px; max-width: 100%; overflow-x: auto; }
  table { border-collapse: collapse; min-width: 480px; width: 100%; font-size: 12px; }
  th, td { padding: 5px 8px; text-align: left; vertical-align: top; border-bottom: 1px solid var(--line-soft); }
  th { color: var(--text-2); font-weight: 600; }
  .guide-link { display: inline; padding: 0; border: 0; background: transparent; color: inherit; font: inherit; text-decoration: underline; cursor: pointer; text-align: left; }
  .guide-image { color: var(--text-2); font-style: italic; }
  .guide-missing { color: var(--text-2); }
</style>
