<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { theme } from '../lib/theme.svelte';
  import { app, conn, deleteSource, goTo, navigation, onScopeChange, openConnections, openCoverage, openGlossary, openSidePanel, panels, scope, startNewConnection } from '../lib/appState.svelte';
  import { nextMenuIndex } from '../lib/wayfinding';
  import Breadcrumb from './Breadcrumb.svelte';
  import LedgerCount from './LedgerCount.svelte';
  import TimeZoneToggle from './TimeZoneToggle.svelte';
  import { timeZone } from '../lib/timeZone.svelte';
  import { formatTime } from '../lib/time';
  import SourcePopover from './SourcePopover.svelte';

  // The top bar: brand, the mode badge, the Sources popover, the drawer buttons and the theme toggle, always on one row.
  // Below 900px Patterns, Ledger, Export and Glossary fold into a "More" menu. The depth breadcrumb sits on its own bar beneath.
  const info = $derived(app.info);
  const crumbs = $derived(navigation.crumbs);
  const referenceTime = $derived(info?.demo ? formatTime(info.now, { zone: timeZone.zone }) : '');

  let compact = $state(false);
  let menuOpen = $state(false);
  let menuWrap = $state<HTMLElement>();
  let moreButton = $state<HTMLButtonElement>();

  const drawers = [
    { id: 'patterns', label: 'Patterns', open: () => openSidePanel('patterns') },
    { id: 'ledger', label: 'Ledger', open: () => openSidePanel('ledger') },
    { id: 'export', label: 'Export', open: () => openSidePanel('export') },
    { id: 'glossary', label: 'Glossary', open: () => openGlossary() }
  ];
  const isOpen = (id: string) => id === 'glossary' ? panels.glossaryOpen : panels.sidePanel === id;
  const anyOpen = $derived(drawers.some(item => isOpen(item.id)));

  onMount(() => {
    const query = window.matchMedia('(max-width: 899px)');
    const sync = () => { compact = query.matches; if (!compact) menuOpen = false; };
    sync();
    query.addEventListener('change', sync);
    return () => query.removeEventListener('change', sync);
  });

  const menuItems = () => [...(menuWrap?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
  async function toggleMenu() {
    menuOpen = !menuOpen;
    if (menuOpen) { await tick(); menuItems()[0]?.focus(); }
  }
  function closeMenu(returnFocus: boolean) { menuOpen = false; if (returnFocus) moreButton?.focus(); }
  // The drawer remembers the focused control to return to, so focus the More button first: the menu item is about to leave the page.
  function chooseDrawer(item: (typeof drawers)[number]) { closeMenu(true); item.open(); }
  function onMenuKey(event: KeyboardEvent) {
    if (event.key === 'Tab') { closeMenu(false); return; }
    const list = menuItems();
    const next = nextMenuIndex(event.key, list.indexOf(document.activeElement as HTMLElement), list.length);
    if (next != null) { event.preventDefault(); list[next].focus(); }
  }
  $effect(() => {
    if (!menuOpen) return;
    const away = (event: PointerEvent) => { if (menuWrap && !menuWrap.contains(event.target as Node)) closeMenu(false); };
    // Esc closes the menu wherever focus is, and does not also step the page up a level.
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); closeMenu(true); } };
    window.addEventListener('pointerdown', away);
    window.addEventListener('keydown', escape, true);
    return () => { window.removeEventListener('pointerdown', away); window.removeEventListener('keydown', escape, true); };
  });
</script>

<header class="topbar">
  <div class="brand-lockup"><div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div><h1>Unravis</h1></div>
  <div class="actions">
    {#if info}<span class:demo-badge={info.demo} class="mode-badge badge" title={info.demo ? 'Demo data. Reference time ' + referenceTime : 'Live mode'}>{info.demo ? 'Demo data' : 'Live mode'}</span>{/if}
    {#if info?.demo}<span class="updated-label reference">Data reference time <time>{referenceTime}</time></span>{/if}
    {#if info && !app.firstRun}
      <SourcePopover groups={app.connectionGroups} selectedIds={scope.query.sourceIds} total={app.sources.length} demo={info.demo} busyDelete={conn.busyDelete} sourceError={conn.sourceError} sourceSuccess={conn.sourceSuccess} savedCount={conn.recentConnections.length}
        onToggle={(id, checked) => { scope.query.sourceIds = checked ? [...scope.query.sourceIds.filter(item => item !== id), id] : scope.query.sourceIds.filter(item => item !== id); onScopeChange(); }}
        onDelete={deleteSource} onManage={() => openConnections('saved')} onAdd={startNewConnection} onCoverage={openCoverage} />
    {/if}
    {#if compact}
      <div class="more" bind:this={menuWrap}>
        <button type="button" class="header-button more-button" bind:this={moreButton} aria-haspopup="menu" aria-expanded={menuOpen} aria-label={anyOpen ? 'More: a panel is open' : 'More'} onclick={toggleMenu}>
          <svg viewBox="0 0 20 20" width="16" height="16" fill="currentColor" aria-hidden="true"><circle cx="4" cy="10" r="1.7"></circle><circle cx="10" cy="10" r="1.7"></circle><circle cx="16" cy="10" r="1.7"></circle></svg>
          <span class="more-label">More</span>
        </button>
        {#if menuOpen}
          <ul class="more-menu" role="menu" aria-label="More" tabindex="-1" onkeydown={onMenuKey}>
            {#each drawers as item (item.id)}
              <li role="none"><button type="button" role="menuitem" onclick={() => chooseDrawer(item)}>{item.label}{#if item.id === 'ledger'}<LedgerCount />{/if}</button></li>
            {/each}
          </ul>
        {/if}
      </div>
    {:else}
      {#each drawers as item (item.id)}
        <button type="button" class="header-button" aria-haspopup="dialog" aria-expanded={isOpen(item.id)} onclick={item.open}>{item.label}{#if item.id === 'ledger'}<LedgerCount />{/if}</button>
      {/each}
    {/if}
    <TimeZoneToggle />
    <button type="button" class="header-button theme-toggle" onclick={() => theme.cycle()} aria-label={'Theme: ' + theme.choice + '. Change theme'} title={'Theme: ' + theme.choice}>
      {#if theme.choice === 'light'}
        <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="10" cy="10" r="3.4"></circle><path d="M10 2.5v2M10 15.5v2M2.5 10h2M15.5 10h2M4.7 4.7l1.4 1.4M13.9 13.9l1.4 1.4M4.7 15.3l1.4-1.4M13.9 6.1l1.4-1.4"></path></svg>
      {:else if theme.choice === 'dark'}
        <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true"><path d="M16.5 11.6A6.6 6.6 0 0 1 8.4 3.5a6.6 6.6 0 1 0 8.1 8.1Z"></path></svg>
      {:else}
        <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true"><circle cx="10" cy="10" r="6.6"></circle><path d="M10 3.4a6.6 6.6 0 0 1 0 13.2Z" fill="currentColor" stroke="none"></path></svg>
      {/if}
    </button>
  </div>
</header>

{#if crumbs.length}
  <div class="crumbbar" inert={panels.modalOpen}>
    <Breadcrumb {crumbs} onNavigate={(crumb) => goTo(crumb.depth)} />
    {#if info?.demo}<span class="crumb-reference">Data reference time <time>{referenceTime}</time></span>{/if}
  </div>
{/if}

<style>
  /* One row at every width: nothing in the bar wraps, so the page never scrolls sideways because of it. */
  .topbar { flex-wrap: nowrap; gap: 12px; }
  .brand-lockup { min-width: 0; }
  .actions { margin-left: auto; display: flex; align-items: center; flex-wrap: nowrap; flex-shrink: 0; gap: 8px; }
  .actions > :global(*) { flex-shrink: 0; }
  .reference { display: none; }
  .badge { display: none; }
  .more { position: relative; }
  .more-menu { position: absolute; z-index: 30; right: 0; top: calc(100% + 6px); min-width: 170px; margin: 0; padding: 4px; list-style: none; background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius); box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 70%, transparent); }
  .more-menu button { display: flex; align-items: center; width: 100%; min-height: 40px; padding: 8px 12px; color: var(--text); background: transparent; border: 0; border-radius: var(--radius); font-size: 14px; text-align: left; }
  .more-menu button:hover, .more-menu button:focus-visible { background: color-mix(in srgb, var(--text) 8%, transparent); }

  .crumbbar { position: relative; z-index: 11; flex: 0 0 auto; min-height: 36px; padding: 0 24px; display: flex; align-items: center; justify-content: flex-start; gap: 16px; background: var(--surface); border-bottom: 1px solid var(--line); }
  .crumb-reference { display: none; margin-left: auto; flex-shrink: 0; color: var(--text-2); font-size: 12px; white-space: nowrap; }

  @media (min-width: 720px) { .badge { display: inline-block; } .crumb-reference { display: inline; } }
  @media (min-width: 1200px) { .reference { display: inline; } .crumb-reference { display: none; } }
  @media (min-width: 1100px) {
    /* The evidence drawer is fixed at the right; keep the trail clear of it. */
    :global(.app-shell:has(.stage.with-drawer)) .crumbbar { padding-right: calc(var(--drawer-w) + 24px); }
  }
  @media (max-width: 720px) {
    .topbar { padding: 0 14px; gap: 8px; flex-wrap: nowrap; align-content: normal; }
    .crumbbar { position: sticky; top: var(--header-h); padding: 2px 14px; }
  }
  @media (max-width: 599px) { .more-label { display: none; } .more-button { padding: 6px 9px; } }
  @media (max-width: 400px) {
    /* Keeps the brand mark and gives the Sources button the room; the heading stays for screen readers. */
    .brand-lockup h1 { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  }
</style>
