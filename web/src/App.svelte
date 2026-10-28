<script lang="ts">
  import { comparisonLabel, STARTUP_TITLE } from './lib/copy/startupCopy';
  import { scopeProblem } from './lib/autorun';
  import { VALIDATION_MESSAGE } from './lib/api';
  import { app, backToPreviousWindow, closeCoverage, dropRecord, evidence, initialize, investigateWindow, navigation, openComparison, openWhere, closeGlossary, closeSidePanel, onWindowKey, openSidePanel, panels, resetInvestigation, restore, results, scope, startApp } from './lib/appState.svelte';
  import AppHeader from './components/AppHeader.svelte';
  import SubBar from './components/SubBar.svelte';
  import Landing from './components/Landing.svelte';
  import CaseReconnect from './components/CaseReconnect.svelte';
  import ConnectionsDrawer from './components/ConnectionsDrawer.svelte';
  import CoverageDrawer from './components/CoverageDrawer.svelte';
  import Glossary from './components/Glossary.svelte';
  import PatternsPanel from './components/PatternsPanel.svelte';
  import LedgerPanel from './components/LedgerPanel.svelte';
  import ExportPanel from './components/ExportPanel.svelte';
  import ImportPanel from './lib/components/ImportPanel.svelte';
  import BlackboxPanel from './components/BlackboxPanel.svelte';
  import CaseHome from './lib/components/CaseHome.svelte';
  import Pulse from './components/Pulse.svelte';
  import RollupView from './components/RollupView.svelte';
  import WhereView from './components/WhereView.svelte';
  import EvidenceView from './components/EvidenceView.svelte';
  import RecordDetailPanel from './components/RecordDetail.svelte';
  import ZoomStrip from './components/ZoomStrip.svelte';
  import ProvenanceNote from './components/ProvenanceNote.svelte';
  import { zoomView } from './lib/zoomView.svelte';
  import { timeZone } from './lib/timeZone.svelte';
  import { backLabel, investigateState } from './lib/promote';
  import { requestDiscover } from './lib/discoverIntent';

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
  // The time charts follow a view window inside the queried one. A new analysis on screen is a new top of the zoom steps.
  $effect(() => { zoomView.follow(analysis ?? null); });
  const zoomModel = $derived(zoomView.model);
  const zoomRead = $derived(zoomView.viewAnalysis);
  // "Investigate this window" is offered once zoomed. "Back to ..." shows while the queried window is the one the last promotion made.
  const investigate = $derived(investigateState({ isRoot: zoomModel?.isRoot ?? true, runLoading: loading, stale: results.displayedRunIsStale }));
  const windowBackLabel = $derived(results.windowBack ? backLabel(results.windowBack) : null);
  // Opens the Import drawer with the "This computer" scan showing, so server data can be added without leaving the analysis.
  function addData() { requestDiscover(); openSidePanel('import'); }
</script>

<svelte:head><title>Unravis</title></svelte:head>
<svelte:window onkeydown={onWindowKey} />

<div class="app-shell">
  <AppHeader />

  {#if app.initError}<div class="banner error-banner" role="alert"><strong>{STARTUP_TITLE}</strong><span>{app.initError}</span>{#if app.initCanRetry}<button class="quiet-button" onclick={() => { app.initError = ''; void initialize(); }}>Retry</button>{/if}</div>{/if}

  {#if app.lockError}<div class="banner error-banner" role="alert"><span>{app.lockError}</span><button class="quiet-button" onclick={() => { app.lockError = ''; }}>Dismiss</button></div>{/if}

  {#if info && !app.firstRun && app.screen === 'running'}<SubBar />{/if}

    {#if panels.connectionsOpen}<ConnectionsDrawer />{/if}

    {#if panels.coverage}<CoverageDrawer sourceId={panels.coverage.id} sourceLabel={panels.coverage.label} start={scope.query.start} end={scope.query.end} onClose={closeCoverage} />{/if}

    {#if panels.glossaryOpen}<Glossary onClose={closeGlossary} focusTerm={panels.glossaryTerm} />{/if}
    {#if panels.sidePanel === 'patterns'}<PatternsPanel query={scope.query} {zone} {sourceLabels} onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'ledger'}<LedgerPanel onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'export'}<ExportPanel query={scope.query} {zone} {sourceLabels} onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'import'}<ImportPanel query={scope.query} {zone} onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'blackbox'}<BlackboxPanel onClose={closeSidePanel} />{/if}

    <main class="stage" class:with-drawer={depth === 2 && analysis != null} aria-busy={loading} inert={panels.modalOpen}>
      <div class="stage-inner">
        {#if !app.initError && (!info || app.screen === 'loading')}
          <Landing mode="loading" />
        {:else if app.screen === 'unlock'}
          <Landing mode="unlock" />
        {:else if app.screen === 'setup'}
          <Landing mode="setup" />
        {:else if app.screen === 'start'}
          <Landing mode="cases" />
        {:else if app.screen === 'create'}
          <!-- The guided flow. createRoute() leads here only once WIZARD_READY is on (end of phase 05). -->
          <Landing mode="create" />
        {:else if app.screen === 'case'}
          <CaseHome />
        {:else if app.firstRun && restore.active}
          <!-- A case that used MongoDB is being reopened: reconnecting quietly, or asking for the sign-in, never the first-run connect step. -->
          {#if restore.working}<Landing mode="restoring" />{:else}<CaseReconnect />{/if}
        {:else if app.firstRun}
          <Landing mode="connect" />
        {:else if info}
          {#if restore.needsAttention || restore.working}<CaseReconnect compact />{/if}
          {#if results.error}<div class="banner error-banner" role="alert"><strong>{comparisonLabel(results.error, [VALIDATION_MESSAGE, scopeProblem(scope.query, scope.dateInputsValid)])}</strong><span>{results.error}</span></div>{/if}
          {#if analysis}
            {#if results.displayedRunIsStale && !loading}<div class="notice frozen-run-note" role="status">The figures below are from the last run. Press <strong>Run analysis</strong> to apply your changes.</div>{/if}
            {#if failingSources.length}
              <div class="banner source-failure" role="alert"><strong>{failingSources.length === analysis.sources.length ? 'No sources returned data' : 'Some sources failed'}</strong>
                {#each failingSources as failed}<span><b>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}:</b> {failed.error || failed.indexWarning || 'Source unavailable'}</span>{/each}
              </div>
            {/if}
            {#if zoomModel && depth !== 3}
              <ZoomStrip model={zoomModel} zone={timeZone.zone} announcement={zoomView.announcement} {investigate} backLabel={windowBackLabel} onInvestigate={investigateWindow} onWindowBack={backToPreviousWindow} onBrush={win => zoomView.setWindow(win)} onBack={() => zoomView.back()} onEarlier={() => zoomView.earlier()} onLater={() => zoomView.later()} onZoomIn={() => zoomView.zoomIn()} onZoomOut={() => zoomView.zoomOut()} onReset={() => zoomView.reset()} onJump={index => zoomView.jumpTo(index)} />
              {#if zoomRead}<ProvenanceNote provenance={zoomRead.provenance} {sourceLabels} />{/if}
            {/if}
            <div class="stage-views" inert={loading}>
              <!-- D0 Pulse -->
              <div class="view" class:off={depth > 0}>
                <Pulse {analysis} {zone} onOpen={openWhere} />
                <div class="pulse-actions"><button type="button" class="primary-button" onclick={() => openWhere(navigation.lens)}>Where is it?</button><button type="button" class="secondary-button" onclick={openComparison}>All operations</button><button type="button" class="secondary-button" aria-haspopup="dialog" onclick={addData}>Add more data</button><p>Tap a figure, or open the ranked list of operations. Add more data to bring in SQL, Redis, IIS or server logs for the same period.</p></div>
                <RollupView />
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
