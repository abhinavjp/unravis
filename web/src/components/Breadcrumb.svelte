<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Crumb } from '../lib/depth';
  import { collapseCrumbs, nextMenuIndex } from '../lib/wayfinding';

  // The trail beyond D0. Every earlier level is a button that steps back to it; the last is the current place.
  // On a narrow screen the middle levels fold into one "..." button that opens them as a menu, so the first and last stay in view.
  let { crumbs, onNavigate }: { crumbs: Crumb[]; onNavigate: (crumb: Crumb) => void } = $props();
  let narrow = $state(false);
  let open = $state(false);
  let wrap = $state<HTMLElement>();
  let gapButton = $state<HTMLButtonElement>();
  const shown = $derived(narrow ? collapseCrumbs(crumbs) : { visible: crumbs, hidden: [] as Crumb[], gapAfter: -1 });
  const earlier = $derived(shown.hidden.map(crumb => crumb.label).join(', '));

  onMount(() => {
    const query = window.matchMedia('(max-width: 767px)');
    const sync = () => { narrow = query.matches; if (!narrow) open = false; };
    sync();
    query.addEventListener('change', sync);
    return () => query.removeEventListener('change', sync);
  });

  const items = () => [...(wrap?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
  async function toggle() {
    open = !open;
    if (open) { await tick(); items()[0]?.focus(); }
  }
  function close(returnFocus: boolean) { open = false; if (returnFocus) gapButton?.focus(); }
  function choose(crumb: Crumb) { open = false; onNavigate(crumb); }
  function onMenuKey(event: KeyboardEvent) {
    if (event.key === 'Tab') { close(false); return; }
    const list = items();
    const next = nextMenuIndex(event.key, list.indexOf(document.activeElement as HTMLElement), list.length);
    if (next != null) { event.preventDefault(); list[next].focus(); }
  }
  $effect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => { if (wrap && !wrap.contains(event.target as Node)) close(false); };
    // Esc closes the menu wherever focus is, and does not also step the page up a level.
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true); } };
    window.addEventListener('pointerdown', away);
    window.addEventListener('keydown', escape, true);
    return () => { window.removeEventListener('pointerdown', away); window.removeEventListener('keydown', escape, true); };
  });
</script>

{#if crumbs.length}
  <nav class="trail" aria-label="Breadcrumb">
    <ol>
      {#each shown.visible as crumb, index (crumb.id)}
        <li class:last={crumb.current}>
          {#if crumb.current}<span aria-current="page" title={crumb.label}>{crumb.label}</span>
          {:else}<button type="button" title={crumb.label} aria-label={'Back to ' + crumb.label} onclick={() => onNavigate(crumb)}>{crumb.label}</button>{/if}
        </li>
        {#if index === shown.gapAfter}
          <li class="gap" bind:this={wrap}>
            <button type="button" class="gap-button" bind:this={gapButton} aria-haspopup="menu" aria-expanded={open} aria-label={'Earlier steps: ' + earlier} title={earlier} onclick={toggle}>&hellip;</button>
            {#if open}
              <ul class="gap-menu" role="menu" aria-label="Earlier steps" onkeydown={onMenuKey}>
                {#each shown.hidden as hidden (hidden.id)}
                  <li role="none"><button type="button" role="menuitem" onclick={() => choose(hidden)}>{hidden.label}</button></li>
                {/each}
              </ul>
            {/if}
          </li>
        {/if}
      {/each}
    </ol>
  </nav>
{/if}

<style>
  .trail { min-width: 0; flex: 1 1 auto; }
  ol { display: flex; align-items: center; flex-wrap: nowrap; gap: 2px; margin: 0; padding: 0; list-style: none; min-width: 0; }
  li { display: flex; align-items: center; min-width: 0; position: relative; }
  ol > li + li::before { content: "\203A"; padding: 0 6px; color: var(--text-2); }
  button, [aria-current] { max-width: 28ch; min-height: 28px; display: inline-flex; align-items: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 4px 6px; border: 0; border-radius: var(--radius-sm); background: transparent; font-size: 13px; }
  button { color: var(--text-2); }
  button:hover { color: var(--text); background: var(--surface-2); }
  [aria-current] { color: var(--text); font-weight: 600; }
  .gap-button { padding: 4px 8px; letter-spacing: .06em; }
  .gap-menu { position: absolute; z-index: 30; left: 0; top: calc(100% + 4px); min-width: 180px; max-width: min(320px, calc(100vw - 28px)); margin: 0; padding: 4px; list-style: none; background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-md); box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 70%, transparent); }
  .gap-menu li { display: block; }
  .gap-menu button { width: 100%; max-width: none; min-height: 36px; justify-content: flex-start; padding: 6px 10px; color: var(--text); }
  @media (max-width: 767px) {
    li.last { flex: 1 1 auto; }
    [aria-current] { max-width: none; overflow: visible; text-overflow: clip; white-space: normal; overflow-wrap: anywhere; line-height: 1.25; }
    button { max-width: 16ch; min-height: 32px; }
  }
</style>
