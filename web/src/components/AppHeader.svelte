<script lang="ts">
  import { theme } from '../lib/theme.svelte';
  import { app, conn, deleteSource, goTo, navigation, onScopeChange, openConnections, openGlossary, openSidePanel, panels, scope, startNewConnection } from '../lib/appState.svelte';
  import Breadcrumb from './Breadcrumb.svelte';
  import SourcePopover from './SourcePopover.svelte';

  // The top bar: brand, the depth breadcrumb, the mode badge, the Sources popover, the drawer buttons and the theme toggle.
  const info = $derived(app.info);
  const crumbs = $derived(navigation.crumbs);
</script>

<header class="topbar">
  <div class="brand-lockup"><div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div><h1>Unravis</h1></div>
  {#if crumbs.length}<Breadcrumb {crumbs} onNavigate={(crumb) => goTo(crumb.depth)} />{:else}<div class="crumbs-spacer"></div>{/if}
  <div class="topbar-meta">
    {#if info}<span class:demo-badge={info.demo} class="mode-badge">{info.demo ? 'Demo data' : 'Live mode'}</span>{/if}
    {#if info?.demo}<span class="updated-label">Data reference time <time>{new Date(info.now).toLocaleString()}</time></span>{/if}
    {#if info && !app.firstRun}
      <SourcePopover groups={app.connectionGroups} selectedIds={scope.query.sourceIds} total={app.sources.length} demo={info.demo} busyDelete={conn.busyDelete} sourceError={conn.sourceError} sourceSuccess={conn.sourceSuccess} savedCount={conn.recentConnections.length} coverageWindow={{ start: scope.query.start, end: scope.query.end }}
        onToggle={(id, checked) => { scope.query.sourceIds = checked ? [...scope.query.sourceIds.filter(item => item !== id), id] : scope.query.sourceIds.filter(item => item !== id); onScopeChange(); }}
        onDelete={deleteSource} onManage={() => openConnections('saved')} onAdd={startNewConnection} />
    {/if}
    <button type="button" class="header-button" aria-haspopup="dialog" aria-expanded={panels.sidePanel === 'patterns'} onclick={() => openSidePanel('patterns')}>Patterns</button>
    <button type="button" class="header-button" aria-haspopup="dialog" aria-expanded={panels.sidePanel === 'ledger'} onclick={() => openSidePanel('ledger')}>Ledger</button>
    <button type="button" class="header-button" aria-haspopup="dialog" aria-expanded={panels.sidePanel === 'export'} onclick={() => openSidePanel('export')}>Export</button>
    <button type="button" class="header-button" aria-label="Help and glossary" aria-haspopup="dialog" aria-expanded={panels.glossaryOpen} onclick={() => openGlossary()}>Help</button>
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
