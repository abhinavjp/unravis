<script lang="ts">
  import { app, closeCoverage, dropRecord, evidence, initialize, navigation, openComparison, openWhere, closeGlossary, closeSidePanel, onWindowKey, panels, resetInvestigation, results, scope, startApp } from './lib/appState.svelte';
  import AppHeader from './components/AppHeader.svelte';
  import SubBar from './components/SubBar.svelte';
  import Landing from './components/Landing.svelte';
  import ConnectionsDrawer from './components/ConnectionsDrawer.svelte';
  import CoverageDrawer from './components/CoverageDrawer.svelte';
  import Glossary from './components/Glossary.svelte';
  import PatternsPanel from './components/PatternsPanel.svelte';
  import LedgerPanel from './components/LedgerPanel.svelte';
  import ExportPanel from './components/ExportPanel.svelte';
  import Pulse from './components/Pulse.svelte';
  import WhereView from './components/WhereView.svelte';
  import EvidenceView from './components/EvidenceView.svelte';
  import RecordDetailPanel from './components/RecordDetail.svelte';

  // The page shell. The state and the logic live in lib/appState.svelte.ts; each region of the page is its own component.
  // startApp registers the effects and the mount work (theme, first load, the glossary event) for this component.
  startApp();

  const info = $derived(app.info);
  const analysis = $derived(results.analysis);
  const loading = $derived(results.loading);
  const depth = $derived(navigation.depth);
  const zone = $derived(scope.zone);
  const sourceLabels = $derived(app.sourceLabels);
  const failingSources = $derived(results.failingSources);
  const record = $derived(evidence.record);
</script>

<svelte:head><title>Unravis</title></svelte:head>
<svelte:window onkeydown={onWindowKey} />

<div class="app-shell">
  <AppHeader />

  {#if app.initError}<div class="banner error-banner" role="alert"><strong>Explorer unavailable</strong><span>{app.initError}</span><button class="quiet-button" onclick={() => { app.initError = ''; void initialize(); }}>Retry</button></div>{/if}

  {#if info && !app.firstRun}<SubBar />{/if}

    {#if panels.connectionsOpen}<ConnectionsDrawer />{/if}

    {#if panels.coverage}<CoverageDrawer sourceId={panels.coverage.id} sourceLabel={panels.coverage.label} start={scope.query.start} end={scope.query.end} onClose={closeCoverage} />{/if}

    {#if panels.glossaryOpen}<Glossary onClose={closeGlossary} focusTerm={panels.glossaryTerm} />{/if}
    {#if panels.sidePanel === 'patterns'}<PatternsPanel query={scope.query} {zone} {sourceLabels} onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'ledger'}<LedgerPanel onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'export'}<ExportPanel query={scope.query} {zone} {sourceLabels} onClose={closeSidePanel} />{/if}

    <main class="stage" class:with-drawer={depth === 2 && analysis != null} aria-busy={loading} inert={panels.modalOpen}>
      <div class="stage-inner">
        {#if !info && !app.initError}
          <Landing mode="loading" />
        {:else if app.firstRun}
          <Landing mode="connect" />
        {:else if info}
          {#if results.error}<div class="banner error-banner" role="alert"><strong>Comparison not run</strong><span>{results.error}</span></div>{/if}
          {#if analysis}
            {#if results.displayedRunIsStale && !loading}<div class="notice frozen-run-note" role="status">The figures below are from the last run. Press <strong>Run analysis</strong> to apply your changes.</div>{/if}
            {#if failingSources.length}
              <div class="banner source-failure" role="alert"><strong>{failingSources.length === analysis.sources.length ? 'No sources returned data' : 'Some sources failed'}</strong>
                {#each failingSources as failed}<span><b>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}:</b> {failed.error || failed.indexWarning || 'Source unavailable'}</span>{/each}
              </div>
            {/if}
            <div class="stage-views" inert={loading}>
              <!-- D0 Pulse -->
              <div class="view" class:off={depth > 0}>
                <Pulse {analysis} {zone} onOpen={openWhere} />
                <div class="pulse-actions"><button type="button" class="primary-button" onclick={() => openWhere(navigation.lens)}>Where is it?</button><button type="button" class="secondary-button" onclick={openComparison}>All operations</button><p>Tap a figure, or open the ranked list of operations.</p></div>
              </div>

              <!-- D1 Where -->
              <WhereView {analysis} />

              <!-- D3 Record -->
              <div class="view record-view" class:off={depth !== 3}>
                <button type="button" class="secondary-button back-button" onclick={() => dropRecord()}>&larr; Back to the evidence</button>
                {#if evidence.recordError}<div class="inline-error" role="alert">{evidence.recordError}</div>{/if}
                {#if record}<RecordDetailPanel detail={record} sourceLabel={sourceLabels.get(record.sourceId) ?? record.sourceId} onClose={() => dropRecord()} />{/if}
              </div>
            </div>

            <!-- D2 Evidence -->
            <EvidenceView {analysis} />
          {:else if loading}
            <Landing mode="running" />
          {:else}
            <Landing mode="start" />
          {/if}
          <div class="notice frozen-run-note" role="status">{scope.restoredInTab ? 'Restored this tab’s investigation filters. ' : ''}{scope.investigationNotice} <button class="quiet-button" onclick={resetInvestigation}>Reset investigation</button></div>
        {/if}
      </div>
    </main>
</div>

<style>
  /* Back is a real button (B8): it sits at the left of the record rather than stretching across it. */
  .record-view > .back-button { justify-self: start; align-self: flex-start; }
</style>
